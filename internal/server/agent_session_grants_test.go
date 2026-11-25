package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// A grant is never hidden by the list's cap: a chat older than the twenty
// most recently seen, and one never seen at all, are listed with theirs.
func TestAgentDetailListsEveryGrantedChat(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := t.Context()
	id := e.agent.ID
	if err := e.fs.TouchAgentSession(ctx, id, "chat-old", e.clock()); err != nil {
		t.Fatal(err)
	}
	for i := range agentSessionLimit + 3 {
		if err := e.fs.TouchAgentSession(ctx, id, fmt.Sprintf("chat-%02d", i), e.clock().Add(time.Hour+time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	body := apitypes.AgentSessionGrantBody{Rights: []string{"ready"}}
	call[apitypes.Agent](e, userSecret, http.MethodPut, "/agents/"+id.String()+"/sessions/chat-old/projects/lorvik", body, http.StatusOK)
	got := call[apitypes.Agent](e, userSecret, http.MethodPut, "/agents/"+id.String()+"/sessions/chat-new/projects/lorvik", body, http.StatusOK)

	if len(got.Sessions) != agentSessionLimit+2 {
		t.Fatalf("sessions = %d, want the %d newest plus the two granted", len(got.Sessions), agentSessionLimit)
	}
	old, never := got.Sessions[agentSessionLimit], got.Sessions[agentSessionLimit+1]
	if old.Label != "chat-old" || old.LastSeenAt == nil || len(old.Grants) != 1 {
		t.Errorf("older granted chat = %+v, want chat-old with its last seen time and grant", old)
	}
	if never.Label != "chat-new" || never.LastSeenAt != nil || len(never.Grants) != 1 {
		t.Errorf("never-seen granted chat = %+v, want chat-new last, without a last seen time, with its grant", never)
	}
	if got.Sessions[0].Label != "chat-22" {
		t.Errorf("first chat = %q, want the newest seen, chat-22", got.Sessions[0].Label)
	}
}

// A chat's grant rides on the agent's place in the project: taking the place
// away ends the grant, with one event per grant, so rebinding starts clean.
func TestChatGrantsEndWithTheAgentsPlace(t *testing.T) {
	t.Parallel()
	id := func(e *testEnv) string { return e.agent2.ID.String() }
	tests := []struct {
		name   string
		agent  func(e *testEnv) domain.ActorRef
		method string
		path   func(e *testEnv) string
	}{
		{"unbound from the project", func(e *testEnv) domain.ActorRef { return e.agent2 }, http.MethodDelete,
			func(e *testEnv) string { return "/agents/" + id(e) + "/projects/lorvik" }},
		{"removed from the project", func(e *testEnv) domain.ActorRef { return e.agent2 }, http.MethodDelete,
			func(e *testEnv) string { return "/projects/lorvik/members/agent/" + id(e) }},
		{"removed from the org", func(e *testEnv) domain.ActorRef { return e.agent }, http.MethodDelete,
			func(e *testEnv) string { return "/members/agent/" + e.agent.ID.String() }},
		{"revoked", func(e *testEnv) domain.ActorRef { return e.agent2 }, http.MethodDelete,
			func(e *testEnv) string { return "/agents/" + id(e) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			agent := tt.agent(e)
			grant := "/agents/" + agent.ID.String() + "/sessions/chat-a/projects/lorvik"
			call[apitypes.Agent](e, userSecret, http.MethodPut, grant, apitypes.AgentSessionGrantBody{Rights: []string{"ready"}}, http.StatusOK)
			if rec := e.do(userSecret, tt.method, tt.path(e), nil); rec.Code != http.StatusNoContent {
				t.Fatalf("%s %s = %d: %s", tt.method, tt.path(e), rec.Code, rec.Body)
			}
			g, err := e.fs.GetAgentSessionGrant(t.Context(), e.proj.ID, agent.ID, "chat-a")
			if err != nil || g.Status.Alive() {
				t.Fatalf("grant = %+v, %v; want it removed", g, err)
			}
			if n := e.countEvents(domain.EventAgentSessionRights); n != 2 {
				t.Errorf("agent.session_rights events = %d, want the grant and its end", n)
			}
			var ended domain.Event
			for _, ev := range e.fs.events {
				if ev.Type == domain.EventAgentSessionRights {
					ended = ev
				}
			}
			if !strings.Contains(string(ended.Payload), `"rights":[]`) || !strings.Contains(string(ended.Payload), `"session":"chat-a"`) {
				t.Errorf("last session_rights payload = %s, want chat-a with empty rights", ended.Payload)
			}
		})
	}
}

func TestRebindingAnAgentLeavesItsChatsWithNoExtraRights(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	id := e.agent2.ID.String()
	inChat[apitypes.Me](e, agent2Secret, "chat-a", http.MethodGet, "/me", nil, http.StatusOK)
	call[apitypes.Agent](e, userSecret, http.MethodPut, "/agents/"+id+"/sessions/chat-a/projects/lorvik", apitypes.AgentSessionGrantBody{Rights: []string{"ready"}}, http.StatusOK)
	if rec := e.do(userSecret, http.MethodDelete, "/agents/"+id+"/projects/lorvik", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unbind = %d: %s", rec.Code, rec.Body)
	}
	got := call[apitypes.Agent](e, userSecret, http.MethodPut, "/agents/"+id+"/projects/lorvik", apitypes.PutMembership{Rights: []string{"work"}}, http.StatusOK)
	if len(got.Sessions) != 1 || len(got.Sessions[0].Grants) != 0 {
		t.Errorf("sessions = %+v, want chat-a without a grant", got.Sessions)
	}
	if code := e.readyAs(agent2Secret, "chat-a", e.backlog(memberSecret, "backlog").Number); code != http.StatusForbidden {
		t.Errorf("rebound chat readies = %d, want 403", code)
	}
}

// chatAnswers has a member ask a question and chat-a of the project-bound
// agent answer it, and returns the answer's status: that agent holds no
// answer right of its own, so only the chat's grant lets it.
func (e *testEnv) chatAnswers(title string) int {
	e.t.Helper()
	q := e.newCard(memberSecret, apitypes.CreateCard{Title: title, Kind: "question"})
	in := apitypes.AnswerCard{Version: q.Version, Answer: "yes", Note: "docs/plan.md says so"}
	return e.do(agent2Secret, http.MethodPost, cardPathOf(q.Number, "/answer"), in, apitypes.HeaderSession, "chat-a").Code
}

// A grant with an end gives its rights until that moment and nothing from
// it on, before any sweep has run; the sweep then takes the row back once
// and writes one event that names the end.
func TestAChatGrantEndsByItself(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	id := e.agent2.ID.String()
	end := e.clock().Add(8 * time.Hour)
	grant := "/agents/" + id + "/sessions/chat-a/projects/lorvik"
	call[apitypes.Agent](e, userSecret, http.MethodPut, grant, apitypes.AgentSessionGrantBody{Rights: []string{"answer"}, EndsAt: &end}, http.StatusOK)

	e.advance(8*time.Hour - time.Second)
	if code := e.chatAnswers("before the end"); code != http.StatusOK {
		t.Fatalf("answer a second before the end = %d, want 200", code)
	}
	e.advance(time.Second)
	if code := e.chatAnswers("at the end"); code != http.StatusForbidden {
		t.Errorf("answer at the end, unswept = %d, want 403", code)
	}
	if got := call[apitypes.Agent](e, userSecret, http.MethodGet, "/agents/"+id, nil, http.StatusOK); len(got.Sessions) != 1 || len(got.Sessions[0].Grants) != 0 {
		t.Errorf("sessions at the end = %+v, want chat-a without a grant", got.Sessions)
	}

	for pass, want := range []int{1, 0} {
		if n, err := e.svc.endGrants(t.Context(), e.clock()); err != nil || n != want {
			t.Fatalf("sweep %d ended %d grants, %v; want %d", pass+1, n, err, want)
		}
	}
	g, err := e.fs.GetAgentSessionGrant(t.Context(), e.proj.ID, e.agent2.ID, "chat-a")
	if err != nil || g.Status.Alive() {
		t.Fatalf("grant after the sweep = %+v, %v; want it removed", g, err)
	}
	if n := e.countEvents(domain.EventAgentSessionRights); n != 2 {
		t.Fatalf("agent.session_rights events = %d, want the grant and its end", n)
	}
	last := e.fs.events[len(e.fs.events)-1]
	var pl struct {
		Session string    `json:"session"`
		Rights  []string  `json:"rights"`
		EndedAt time.Time `json:"ended_at"`
	}
	if err := json.Unmarshal(last.Payload, &pl); err != nil {
		t.Fatal(err)
	}
	if last.Actor != e.agent2 || last.Session != domain.UnpromptedSession || pl.Session != "chat-a" || len(pl.Rights) != 0 || !pl.EndedAt.Equal(end) {
		t.Errorf("end event = actor %v session %q payload %s; want the agent, unprompted, chat-a with no rights and ended_at %s", last.Actor, last.Session, last.Payload, end)
	}
}

// The sweep ends only what is still past its end when it gets there: a
// grant its owner extended or took back since it was listed stays as it is.
func TestTheSweepLeavesAGrantExtendedOrTakenBackMeanwhile(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	end := e.clock().Add(time.Hour)
	grant := "/agents/" + e.agent2.ID.String() + "/sessions/chat-a/projects/lorvik"
	body := apitypes.AgentSessionGrantBody{Rights: []string{"answer"}, EndsAt: &end}
	call[apitypes.Agent](e, userSecret, http.MethodPut, grant, body, http.StatusOK)
	e.advance(2 * time.Hour)
	later := e.clock().Add(time.Hour)
	body.EndsAt = &later
	call[apitypes.Agent](e, userSecret, http.MethodPut, grant, body, http.StatusOK)

	if _, err := e.fs.EndAgentSessionGrant(t.Context(), e.proj.ID, e.agent2.ID, "chat-a", e.clock()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ending the extended grant = %v, want not found", err)
	}
	if code := e.chatAnswers("extended"); code != http.StatusOK {
		t.Errorf("answer on the extended grant = %d, want 200", code)
	}
}

func TestAChatGrantRefusesAnEndThatIsNotAhead(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	grant := "/agents/" + e.agent2.ID.String() + "/sessions/chat-a/projects/lorvik"
	for name, end := range map[string]time.Time{"now": e.clock(), "past": e.clock().Add(-time.Minute)} {
		rec := e.do(userSecret, http.MethodPut, grant, apitypes.AgentSessionGrantBody{Rights: []string{"ready"}, EndsAt: &end})
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "ends_at") {
			t.Errorf("end %s = %d %s, want 422 naming ends_at", name, rec.Code, rec.Body)
		}
	}
}

// The chat reads what it was given from the project: the rights, the end
// and its owner's instructions. Another chat of the agent reads none of it.
func TestAProjectShowsTheCallingChatItsGrant(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	end := e.clock().Add(8 * time.Hour)
	grant := "/agents/" + e.agent2.ID.String() + "/sessions/chat-a/projects/lorvik"
	call[apitypes.Agent](e, userSecret, http.MethodPut, grant,
		apitypes.AgentSessionGrantBody{Rights: []string{"ready"}, EndsAt: &end, Instructions: "  lanes server and cli only\n"}, http.StatusOK)

	got := inChat[apitypes.Project](e, agent2Secret, "chat-a-c1", http.MethodGet, "/projects/lorvik", nil, http.StatusOK).ChatGrant
	if got == nil || !slices.Equal(got.Rights, []string{"ready"}) || got.EndsAt == nil || !got.EndsAt.Equal(end) || got.Instructions != "lanes server and cli only" {
		t.Errorf("chat-a's subagent reads %+v, want ready until %s with the trimmed instructions", got, end)
	}
	if other := inChat[apitypes.Project](e, agent2Secret, "chat-b", http.MethodGet, "/projects/lorvik", nil, http.StatusOK).ChatGrant; other != nil {
		t.Errorf("chat-b reads %+v, want no grant", other)
	}
	if mine := call[apitypes.Project](e, userSecret, http.MethodGet, "/projects/lorvik", nil, http.StatusOK).ChatGrant; mine != nil {
		t.Errorf("a person reads %+v, want no grant", mine)
	}
	e.advance(8 * time.Hour)
	if after := inChat[apitypes.Project](e, agent2Secret, "chat-a", http.MethodGet, "/projects/lorvik", nil, http.StatusOK).ChatGrant; after != nil {
		t.Errorf("chat-a reads %+v at the end, want no grant", after)
	}
}

// A change to what a chat was given wakes that chat and no other: the grant,
// its taking back, and its end.
func TestAChangeToAChatsGrantWakesThatChat(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	end := e.clock().Add(time.Hour)
	grant := "/agents/" + e.agent2.ID.String() + "/sessions/chat-a/projects/lorvik"
	wakes := func(chat string) (n int) {
		for _, ev := range inChat[apitypes.Inbox](e, agent2Secret, chat, http.MethodGet, "/inbox", nil, http.StatusOK).Events {
			if ev.Type == "agent.session_rights" && ev.Wake && ev.Mine {
				n++
			}
		}
		return n
	}
	call[apitypes.Agent](e, userSecret, http.MethodPut, grant, apitypes.AgentSessionGrantBody{Rights: []string{"ready"}, EndsAt: &end}, http.StatusOK)
	e.advance(time.Hour)
	if n, err := e.svc.endGrants(t.Context(), e.clock()); err != nil || n != 1 {
		t.Fatalf("sweep ended %d grants, %v; want 1", n, err)
	}
	if n := wakes("chat-a-c1"); n != 2 {
		t.Errorf("chat-a's subagent is woken by %d grant events, want the grant and its end", n)
	}
	if n := wakes("chat-b"); n != 0 {
		t.Errorf("chat-b is woken by %d grant events, want none", n)
	}
}

// chatGrant is chat-a's one grant in an agent's detail, nil when it has none.
func chatGrant(a apitypes.Agent) *apitypes.SessionGrant {
	for _, s := range a.Sessions {
		if s.Label == "chat-a" && len(s.Grants) == 1 {
			return &s.Grants[0]
		}
	}
	return nil
}

// The instructions are their giver's words to one chat. The people who may
// manage the agent read them in its detail; everyone else reads the rights
// and the end there, and no event carries the text.
func TestOnlyWhoManagesAnAgentReadsWhatItsChatWasTold(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	const told, adminSecret, viewerSecret = "nothing about pricing", "lvk_u_alex-secret", "lvk_u_ann-secret"
	e.seedToken(adminSecret, e.seedUser("alex", domain.RoleAdmin), domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
	e.seedToken(viewerSecret, e.seedUser("ann", domain.RoleViewer), domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
	end := e.clock().Add(8 * time.Hour)
	path := "/agents/" + e.agent2.ID.String()
	call[apitypes.Agent](e, userSecret, http.MethodPut, path+"/sessions/chat-a/projects/lorvik",
		apitypes.AgentSessionGrantBody{Rights: []string{"answer"}, EndsAt: &end, Instructions: told}, http.StatusOK)

	tests := []struct{ name, secret, session, want string }{
		{"its owner", userSecret, "", told},
		{"an admin", adminSecret, "", told},
		{"a member", memberSecret, "", ""},
		{"a viewer", viewerSecret, "", ""},
		{"another agent", agentSecret, "chat-x", ""},
		{"another chat of the agent", agent2Secret, "chat-b", ""},
	}
	for _, tt := range tests {
		g := chatGrant(inChat[apitypes.Agent](e, tt.secret, tt.session, http.MethodGet, path, nil, http.StatusOK))
		if g == nil || !slices.Equal(g.Rights, []string{"answer"}) || g.EndsAt == nil || !g.EndsAt.Equal(end) {
			t.Errorf("%s reads grant %+v, want answer until %s", tt.name, g, end)
			continue
		}
		if g.Instructions != tt.want {
			t.Errorf("%s reads instructions %q, want %q", tt.name, g.Instructions, tt.want)
		}
	}
	for _, ev := range e.fs.events {
		if strings.Contains(string(ev.Payload), told) {
			t.Errorf("event %s carries the instructions: %s", ev.Type, ev.Payload)
		}
	}
}

// An agent's detail names a private project only to who may see it or may
// manage the agent: everyone else reads neither its role there nor a chat's
// grant in it.
func TestAnAgentsDetailLeavesOutProjectsTheReaderCannotSee(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	const adminSecret, viewerSecret = "lvk_u_alex-secret", "lvk_u_ann-secret"
	e.seedToken(adminSecret, e.seedUser("alex", domain.RoleAdmin), domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
	e.seedToken(viewerSecret, e.seedUser("ann", domain.RoleViewer), domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
	vault := e.newProject("vault", domain.VisibilityPrivate)
	for _, actor := range []domain.ActorRef{e.user, e.agent2} {
		if _, err := e.fs.PutProjectMember(context.Background(), domain.ProjectMember{ProjectID: vault.ID, Actor: actor, Role: domain.RoleMember,
			Rights: domain.RightWork, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0}); err != nil {
			t.Fatal(err)
		}
	}
	path := "/agents/" + e.agent2.ID.String()
	for _, slug := range []string{"lorvik", "vault"} {
		call[apitypes.Agent](e, userSecret, http.MethodPut, path+"/sessions/chat-a/projects/"+slug, apitypes.AgentSessionGrantBody{Rights: []string{"answer"}}, http.StatusOK)
	}

	tests := []struct {
		name, secret, session string
		want                  []string
	}{
		{"its owner, in vault", userSecret, "", []string{"lorvik", "vault"}},
		{"an admin, who may manage it", adminSecret, "", []string{"lorvik", "vault"}},
		{"a member outside vault", memberSecret, "", []string{"lorvik"}},
		{"a viewer outside vault", viewerSecret, "", []string{"lorvik"}},
		{"another agent outside vault", agentSecret, "chat-x", []string{"lorvik"}},
	}
	for _, tt := range tests {
		got := inChat[apitypes.Agent](e, tt.secret, tt.session, http.MethodGet, path, nil, http.StatusOK)
		var projects, granted []string
		for _, r := range got.Projects {
			projects = append(projects, r.Project)
		}
		for _, s := range got.Sessions {
			for _, g := range s.Grants {
				granted = append(granted, g.Project)
			}
		}
		slices.Sort(projects)
		slices.Sort(granted)
		if !slices.Equal(projects, tt.want) || !slices.Equal(granted, tt.want) {
			t.Errorf("%s reads projects %v and grants in %v, want %v for both", tt.name, projects, granted, tt.want)
		}
	}
}

// The event of a registration reaches every reader of the org, so it names
// only the projects every member can see.
func TestAnAgentsRegistrationEventLeavesOutPrivateProjects(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.newProject("vault", domain.VisibilityPrivate)
	call[apitypes.AgentCreated](e, userSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "hand", Projects: []string{"lorvik", "vault"}}, http.StatusCreated)
	ev := e.lastEvent()
	var body struct{ Projects []string }
	if err := json.Unmarshal(ev.Payload, &body); err != nil || ev.Type != domain.EventAgentRegistered || !slices.Equal(body.Projects, []string{"lorvik"}) {
		t.Errorf("event %s %s (%v), want agent.registered naming only lorvik", ev.Type, ev.Payload, err)
	}
}

// A grant is saved whole: giving it again without an end or instructions
// leaves it with neither, so it lasts until taken back.
func TestGivingAChatsGrantAgainReplacesItsEndAndInstructions(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	end := e.clock().Add(time.Hour)
	path := "/agents/" + e.agent2.ID.String()
	grant := path + "/sessions/chat-a/projects/lorvik"
	call[apitypes.Agent](e, userSecret, http.MethodPut, grant, apitypes.AgentSessionGrantBody{Rights: []string{"answer"}, EndsAt: &end, Instructions: "lanes server and cli only"}, http.StatusOK)
	got := chatGrant(call[apitypes.Agent](e, userSecret, http.MethodPut, grant, apitypes.AgentSessionGrantBody{Rights: []string{"answer"}}, http.StatusOK))
	if got == nil || got.EndsAt != nil || got.Instructions != "" {
		t.Fatalf("grant given again = %+v, want no end and no instructions", got)
	}
	e.advance(2 * time.Hour)
	if n, err := e.svc.endGrants(t.Context(), e.clock()); err != nil || n != 0 {
		t.Errorf("sweep past the first end ended %d grants, %v; want none", n, err)
	}
	if code := e.chatAnswers("past the first end"); code != http.StatusOK {
		t.Errorf("answer past the first end = %d, want 200", code)
	}
}

// What a chat's grant adds shows in the agent's project list while the grant
// lives and not from its end on, swept or not.
func TestAnEndedGrantLeavesTheChatsProjectList(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	end := e.clock().Add(time.Hour)
	call[apitypes.Agent](e, userSecret, http.MethodPut, "/agents/"+e.agent2.ID.String()+"/sessions/chat-a/projects/lorvik",
		apitypes.AgentSessionGrantBody{Rights: []string{"answer"}, EndsAt: &end}, http.StatusOK)
	answers := func() bool {
		for _, p := range inChat[apitypes.ProjectList](e, agent2Secret, "chat-a", http.MethodGet, "/projects", nil, http.StatusOK).Items {
			if p.Slug == "lorvik" {
				return slices.Contains(p.Rights, "answer")
			}
		}
		t.Fatal("lorvik is not in the agent's project list")
		return false
	}
	if !answers() {
		t.Fatal("before the end: the list shows no answer right for chat-a")
	}
	e.advance(time.Hour)
	if answers() {
		t.Error("at the end: the list still shows the answer right")
	}
}
