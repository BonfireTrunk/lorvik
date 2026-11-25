package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// giveRights sets the rights of the org-wide agent, keeping its viewer role.
func (e *testEnv) giveRights(r domain.Rights) {
	e.t.Helper()
	if _, err := e.fs.PutMembership(context.Background(), domain.Membership{OrgID: e.org.ID, Actor: e.agent, Role: domain.RoleViewer, Rights: r,
		Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		e.t.Fatal(err)
	}
}

// giveProjectRights sets the rights of the project-bound agent.
func (e *testEnv) giveProjectRights(r domain.Rights) {
	e.t.Helper()
	if _, err := e.fs.PutProjectMember(context.Background(), domain.ProjectMember{ProjectID: e.proj.ID, Actor: e.agent2, Role: domain.RoleViewer, Rights: r,
		Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		e.t.Fatal(err)
	}
}

// membersAgent seeds an org-wide agent owned by the member sam with rights r
// and returns its secret.
func (e *testEnv) membersAgent(r domain.Rights) string {
	e.t.Helper()
	ctx := context.Background()
	a, err := e.fs.CreateAgent(ctx, domain.Agent{ID: e.ids.New(), OrgID: e.org.ID, Name: "sam-bot", Harness: domain.HarnessCodex, CreatedBy: e.member.ID,
		Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.fs.PutMembership(ctx, domain.Membership{OrgID: e.org.ID, Actor: a.Ref(), Role: domain.RoleViewer, Rights: r,
		Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		e.t.Fatal(err)
	}
	const secret = "lvk_a_sam-bot-rights"
	e.seedToken(secret, a.Ref(), domain.ScopeWrite, t0.Add(domain.AgentTokenTTL))
	return secret
}

// inChat is call for a request carrying a session label.
func inChat[T any](e *testEnv, secret, session, method, path string, body any, want int) T {
	e.t.Helper()
	rec := e.do(secret, method, path, body, apitypes.HeaderSession, session)
	if rec.Code != want {
		e.t.Fatalf("%s %s in %s = %d, want %d: %s", method, path, session, rec.Code, want, rec.Body)
	}
	var v T
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			e.t.Fatalf("decode %s %s: %v: %s", method, path, err, rec.Body)
		}
	}
	return v
}

// readyAs moves a backlog card to ready as an agent in a chat and returns the status.
func (e *testEnv) readyAs(secret, session string, n int64) int {
	e.t.Helper()
	in := apitypes.TransitionCard{Status: "ready", Version: e.show(secret, n).Version}
	return e.do(secret, http.MethodPost, cardPathOf(n, "/transition"), in, apitypes.HeaderSession, session).Code
}

func (e *testEnv) backlog(secret, title string) apitypes.Card {
	e.t.Helper()
	c := e.newCard(secret, apitypes.CreateCard{Title: title})
	if c.Status != "backlog" {
		e.t.Fatalf("card %q = %s, want backlog", title, c.Status)
	}
	return c
}

func TestAgentWithWorkAndReadyReadiesAnyBacklogCardAndRunsIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightWork | domain.RightReady)
	theirs := e.backlog(memberSecret, "filed by sam")
	own := e.backlog(agentSecret, "filed by the agent")
	for _, n := range []int64{theirs.Number, own.Number} {
		if got := e.move(agentSecret, n, apitypes.TransitionCard{Status: "ready"}); got.Status != "ready" {
			t.Fatalf("#%d = %s, want ready", n, got.Status)
		}
		e.claim(agentSecret, n)
		if got := e.move(agentSecret, n, apitypes.TransitionCard{Status: "done"}); got.Status != "done" {
			t.Errorf("#%d = %s, want done", n, got.Status)
		}
	}
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "which db?"})
	p := e.problem(agentSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: q.Version, Answer: "pg"}, http.StatusForbidden)
	if !strings.Contains(p.Detail, "lacks the answer right in lorvik") || !strings.Contains(p.Hint, "--relayed") {
		t.Errorf("problem = %q / %q, want the answer right named with the relay way out", p.Detail, p.Hint)
	}
}

func TestAgentWithWorkOnlyCannotReadyButCanUnpark(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightWork)
	own := e.backlog(agentSecret, "mine")
	in := apitypes.TransitionCard{Status: "ready", Version: own.Version}
	rec := e.do(agentSecret, http.MethodPost, cardPathOf(own.Number, "/transition"), in, apitypes.HeaderSession, "swift-heron-1a2b-c4")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("backlog to ready = %d, want 403: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{"agent claude-code lacks the ready right in lorvik", "this chat (swift-heron-1a2b)"} {
		if !strings.Contains(body, want) {
			t.Errorf("refusal = %s, want %q", body, want)
		}
	}
	// Un-parking is an ordinary edit of its own card.
	ready := e.backlog(agentSecret, "parked")
	e.move(userSecret, ready.Number, apitypes.TransitionCard{Status: "ready"})
	e.move(agentSecret, ready.Number, apitypes.TransitionCard{Status: "waiting"})
	if got := e.move(agentSecret, ready.Number, apitypes.TransitionCard{Status: "ready"}); got.Status != "ready" {
		t.Errorf("waiting to ready = %s, want ready", got.Status)
	}
}

func TestAgentFilesWorkStraightIntoReadyOnlyWithTheReadyRightOrInsideAReadiedStory(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightWork)
	parked := e.newCard(memberSecret, apitypes.CreateCard{Kind: "story", Title: "not yet readied"})
	readied := e.newCard(memberSecret, apitypes.CreateCard{Kind: "story", Title: "readied", Status: "ready"})
	cards := "/projects/lorvik/cards"
	for name, in := range map[string]apitypes.CreateCard{
		"task":                     {Title: "found work", Status: "ready"},
		"story":                    {Kind: "story", Title: "found story", Status: "ready"},
		"child of a backlog story": {Title: "child", Status: "ready", Parent: &parked.Number},
	} {
		p := e.problem(agentSecret, http.MethodPost, cards, in, http.StatusForbidden)
		if !strings.Contains(p.Detail, "lacks the ready right in lorvik") || !strings.Contains(p.Hint, "file it in backlog") {
			t.Errorf("%s: problem = %q / %q, want the ready right named with backlog as the way out", name, p.Detail, p.Hint)
		}
	}
	for name, in := range map[string]apitypes.CreateCard{
		"child of a readied story": {Title: "child", Status: "ready", Parent: &readied.Number},
		"question":                 {Kind: "question", Title: "which db?"},
		"proposed decision":        {Kind: "decision", Title: "use pg"},
		"backlog task":             {Title: "found work"},
	} {
		if c := e.newCard(agentSecret, in); c.Status == "" {
			t.Errorf("%s: created %+v", name, c)
		}
	}
	started := e.newCard(memberSecret, apitypes.CreateCard{Title: "started child", Status: "ready", Parent: &readied.Number})
	e.claim(agentSecret, started.Number)
	if st := e.show(memberSecret, readied.Number).Status; st != "working" {
		t.Fatalf("story = %s, want working once a child started", st)
	}
	if c := e.newCard(agentSecret, apitypes.CreateCard{Title: "child", Status: "ready", Parent: &readied.Number}); c.Status != "ready" {
		t.Errorf("child of a working story = %s, want ready", c.Status)
	}
	call[apitypes.Agent](e, userSecret, http.MethodPut, "/agents/"+e.agent.ID.String()+"/sessions/chat-a/projects/lorvik", apitypes.AgentSessionGrantBody{Rights: []string{"ready"}}, http.StatusOK)
	if c := inChat[apitypes.Card](e, agentSecret, "chat-a-c1", http.MethodPost, cards, apitypes.CreateCard{Title: "found work", Status: "ready"}, http.StatusCreated); c.Status != "ready" {
		t.Errorf("in a chat granted ready = %s, want ready", c.Status)
	}
	e.giveRights(domain.RightWork | domain.RightReady)
	if c := e.newCard(agentSecret, apitypes.CreateCard{Title: "found work", Status: "ready"}); c.Status != "ready" {
		t.Errorf("with the ready right = %s, want ready", c.Status)
	}
}

func TestAQuestionFromAnAgentWithoutTheReadyRightLeavesABacklogCardInBacklog(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightWork)
	asked := e.backlog(agentSecret, "blocked when asked")
	linked := e.backlog(agentSecret, "blocked by a link")
	active := e.readyCard(memberSecret, "ready work")
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "which db?", Blocks: []int64{asked.Number, active.Number}})
	call[apitypes.Card](e, agentSecret, http.MethodPost, cardPathOf(q.Number, "/links"), apitypes.LinkCards{Kind: "blocks", To: linked.Number}, http.StatusOK)
	for _, c := range []apitypes.Card{asked, linked} {
		if st := e.show(memberSecret, c.Number).Status; st != "backlog" {
			t.Errorf("#%d = %s, want backlog: parking would ready it", c.Number, st)
		}
	}
	if st := e.show(memberSecret, active.Number).Status; st != "waiting" {
		t.Errorf("ready card = %s, want waiting", st)
	}
	if rec := e.do(agentSecret, http.MethodDelete, cardPathOf(q.Number, "/links/blocks/"+strconv.FormatInt(linked.Number, 10)), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink = %d: %s", rec.Code, rec.Body)
	}
	call[apitypes.Card](e, agentSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: e.show(agentSecret, q.Number).Version, Answer: "pg", Relayed: true, Note: "the owner in chat"}, http.StatusOK)
	for _, c := range []apitypes.Card{asked, linked} {
		if st := e.show(memberSecret, c.Number).Status; st != "backlog" {
			t.Errorf("#%d after unlink and answer = %s, want backlog", c.Number, st)
		}
	}
	if st := e.show(memberSecret, active.Number).Status; st != "ready" {
		t.Errorf("ready card after the answer = %s, want ready", st)
	}
}

func TestAChatCannotAnswerAQuestionItOrItsSubagentsAsked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightWork | domain.RightAnswer)
	ask := func(session string) apitypes.Card {
		return inChat[apitypes.Card](e, agentSecret, session, http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Kind: "question", Title: "asked in " + session}, http.StatusCreated)
	}
	answer := func(session string, q apitypes.Card, relayed bool) *httptest.ResponseRecorder {
		in := apitypes.AnswerCard{Version: q.Version, Answer: "pg", Relayed: relayed, Note: "docs/plan.md names Postgres"}
		if relayed {
			in.Note = "the owner said so in chat"
		}
		return e.do(agentSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), in, apitypes.HeaderSession, session)
	}
	for _, tt := range []struct{ asked, answers, refusal string }{
		{"chat-a", "chat-a", "chat chat-a asked"},
		{"chat-a", "chat-a-c2", "chat chat-a asked"},
		{"chat-a-c3", "chat-a", "chat chat-a asked"},
		{"", "", "a chat without a session label asked"},
		{"chat-a", "chat-b", ""},
		{"chat-a", "", ""},
	} {
		rec := answer(tt.answers, ask(tt.asked), false)
		switch {
		case tt.refusal == "" && rec.Code != http.StatusOK:
			t.Errorf("asked in %q, %q answers = %d, want 200: %s", tt.asked, tt.answers, rec.Code, rec.Body)
		case tt.refusal != "" && (rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), tt.refusal) || !strings.Contains(rec.Body.String(), "--relayed")):
			t.Errorf("asked in %q, %q answers = %d %s, want 403 naming %q and the relay", tt.asked, tt.answers, rec.Code, rec.Body, tt.refusal)
		}
	}
	if rec := answer("chat-a", e.newCard(memberSecret, apitypes.CreateCard{Kind: "question", Title: "sam asks"}), false); rec.Code != http.StatusOK {
		t.Errorf("answering a person's question = %d, want 200: %s", rec.Code, rec.Body)
	}
	if rec := answer("chat-a", ask("chat-a"), true); rec.Code != http.StatusOK {
		t.Errorf("relaying the human's answer to its own question = %d, want 200: %s", rec.Code, rec.Body)
	}
}

func TestAgentWithAnswerOnlyAnswersLikeAReviewer(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightAnswer)
	task := e.readyCard(memberSecret, "choose storage")
	q := e.newCard(memberSecret, apitypes.CreateCard{Kind: "question", Title: "Postgres or SQLite?", Blocks: []int64{task.Number}})
	if e.show(memberSecret, task.Number).Status != "waiting" {
		t.Fatal("the task should wait for the question")
	}
	got := call[apitypes.Card](e, agentSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: q.Version, Answer: "Postgres", Note: "docs/plan.md names Postgres"}, http.StatusOK)
	if got.Status != "done" || got.Question.AnsweredBy == nil || got.Question.AnsweredBy.ID != e.agent.ID.String() {
		t.Errorf("answered = %+v, want done, answered by the agent", got)
	}
	if ans := e.eventsOf("card.answered"); len(ans) != 1 || ans[0]["relayed"] != nil {
		t.Errorf("answered events = %+v, want one that is not a relay", ans)
	}
	if t2 := e.show(memberSecret, task.Number); t2.Status != "ready" {
		t.Errorf("blocked task = %s, want ready", t2.Status)
	}
	if n := len(call[[]apitypes.Comment](e, agentSecret, http.MethodGet, cardPathOf(q.Number, "/comments"), nil, http.StatusOK)); n != 0 {
		t.Errorf("comments = %d, want none: the reason is kept on the card, not posted", n)
	}
	for name, p := range map[string]apitypes.Problem{
		"claim":  e.problem(agentSecret, http.MethodPost, cardPathOf(task.Number, "/claim"), apitypes.ClaimCard{}, http.StatusForbidden),
		"create": e.problem(agentSecret, http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Title: "x"}, http.StatusForbidden),
	} {
		if !strings.Contains(p.Detail, "lacks the work right in lorvik") {
			t.Errorf("%s: detail = %q, want the work right named", name, p.Detail)
		}
	}
	q2 := e.newCard(memberSecret, apitypes.CreateCard{Kind: "question", Title: "again?"})
	p := e.problem(agentSecret, http.MethodPost, cardPathOf(q2.Number, "/answer"), apitypes.AnswerCard{Version: q2.Version, Answer: "yes", Note: "docs/plan.md", Decide: true}, http.StatusUnprocessableEntity)
	if !hasField(p, "decide") {
		t.Errorf("errors = %+v, want decide: a decision stays a human's", p.Errors)
	}
	if e.show(memberSecret, q2.Number).Status != "ready" {
		t.Error("the refused decide left the question open")
	}
}

func TestTriageEditsAndDropsOthersCardsButNeverTakesTheirClaim(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	theirs := e.readyCard(memberSecret, "sam's card")
	edit := apitypes.UpdateCard{Version: theirs.Version, Priority: new("urgent")}
	e.problem(agentSecret, http.MethodPatch, cardPathOf(theirs.Number, ""), edit, http.StatusForbidden)
	p := e.problem(agentSecret, http.MethodPost, cardPathOf(theirs.Number, "/transition"), apitypes.TransitionCard{Version: theirs.Version, Status: "dropped", DropReason: "obsolete"}, http.StatusForbidden)
	if !strings.Contains(p.Detail, "lacks the triage right in lorvik") {
		t.Errorf("detail = %q, want the triage right named", p.Detail)
	}
	e.giveRights(domain.RightWork | domain.RightTriage)
	if got := call[apitypes.Card](e, agentSecret, http.MethodPatch, cardPathOf(theirs.Number, ""), edit, http.StatusOK); got.Priority != "urgent" {
		t.Errorf("priority = %s, want urgent", got.Priority)
	}
	e.claim(memberSecret, theirs.Number)
	e.problem(agentSecret, http.MethodPost, cardPathOf(theirs.Number, "/claim"), apitypes.ClaimCard{}, http.StatusConflict)
	e.moveFails(agentSecret, theirs.Number, apitypes.TransitionCard{Status: "done"}, http.StatusConflict)
	other := e.readyCard(memberSecret, "another")
	if got := e.move(agentSecret, other.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "obsolete"}); got.Status != "dropped" {
		t.Errorf("dropped = %s", got.Status)
	}
}

func TestAgentRightsAreCappedByItsOwnersRole(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	secret := e.membersAgent(domain.RightWork | domain.RightReady)
	c := e.backlog(memberSecret, "sam's backlog card")
	in := apitypes.TransitionCard{Status: "ready", Version: c.Version}
	p := e.problem(secret, http.MethodPost, cardPathOf(c.Number, "/transition"), in, http.StatusForbidden)
	if !strings.Contains(p.Detail, "lacks the ready right") {
		t.Errorf("detail = %q, want the ready right named: sam is a member and cannot ready", p.Detail)
	}
	e.setOrgRole(e.member, domain.RoleReviewer, domain.RecordActive)
	call[apitypes.Card](e, secret, http.MethodPost, cardPathOf(c.Number, "/transition"), in, http.StatusOK)
}

func TestSessionGrantGivesOneChatAndItsSubagentsMoreRights(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightWork)
	grant := "/agents/" + e.agent.ID.String() + "/sessions/chat-a/projects/lorvik"
	body := apitypes.AgentSessionGrantBody{Rights: []string{"ready"}}
	cards := []apitypes.Card{}
	for range 6 {
		cards = append(cards, e.backlog(memberSecret, "backlog"))
	}
	if code := e.readyAs(agentSecret, "chat-a", cards[0].Number); code != http.StatusForbidden {
		t.Fatalf("before the grant = %d, want 403", code)
	}
	agent := call[apitypes.Agent](e, userSecret, http.MethodPut, grant, body, http.StatusOK)
	if ev := e.lastEvent(); ev.Type != domain.EventAgentSessionRights || !strings.Contains(string(ev.Payload), `"session":"chat-a"`) || !strings.Contains(string(ev.Payload), `"rights":["ready"]`) {
		t.Errorf("last event = %s %s, want agent.session_rights for chat-a with ready", ev.Type, ev.Payload)
	}
	if len(agent.Sessions) != 1 || agent.Sessions[0].Label != "chat-a" || len(agent.Sessions[0].Grants) != 1 || agent.Sessions[0].Grants[0].Project != "lorvik" {
		t.Errorf("sessions = %+v, want chat-a seen and granted in lorvik", agent.Sessions)
	}
	for _, tt := range []struct {
		chat string
		card int
		want int
	}{
		{"chat-a", 0, http.StatusOK},
		{"chat-b", 1, http.StatusForbidden},
		{"chat-a-c5", 2, http.StatusOK},
		{"chat-a-c5-c2", 3, http.StatusOK},
	} {
		if code := e.readyAs(agentSecret, tt.chat, cards[tt.card].Number); code != tt.want {
			t.Errorf("%s readies = %d, want %d", tt.chat, code, tt.want)
		}
	}
	if code := e.readyAs(agentSecret, "", cards[1].Number); code != http.StatusForbidden {
		t.Errorf("no session readies = %d, want 403: a grant needs its chat", code)
	}
	if rec := e.do(userSecret, http.MethodDelete, grant, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d: %s", rec.Code, rec.Body)
	}
	if ev := e.lastEvent(); ev.Type != domain.EventAgentSessionRights || !strings.Contains(string(ev.Payload), `"rights":[]`) {
		t.Errorf("last event = %s %s, want the grant taken back", ev.Type, ev.Payload)
	}
	if code := e.readyAs(agentSecret, "chat-a", cards[4].Number); code != http.StatusForbidden {
		t.Errorf("after the delete = %d, want 403", code)
	}
	e.problem(userSecret, http.MethodDelete, grant, nil, http.StatusNotFound)
}

func TestSessionGrantRefusals(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.newProject("vault", domain.VisibilityPrivate)
	base := "/agents/" + e.agent.ID.String() + "/sessions/"
	body := apitypes.AgentSessionGrantBody{Rights: []string{"ready"}}

	if p := e.problem(userSecret, http.MethodPut, base+"chat-a/projects/vault", body, http.StatusUnprocessableEntity); !hasField(p, "project") || !strings.Contains(p.Errors[0].Message, "agent claude-code is not in vault") {
		t.Errorf("project the agent cannot see: %+v", p.Errors)
	}
	p := e.problem(userSecret, http.MethodPut, base+"chat-a-c5/projects/lorvik", body, http.StatusUnprocessableEntity)
	if !hasField(p, "label") || !strings.Contains(p.Hint, "-c<n>") {
		t.Errorf("subagent label: %+v hint %q", p.Errors, p.Hint)
	}
	e.problem(memberSecret, http.MethodPut, base+"chat-a/projects/lorvik", body, http.StatusNotFound)
	e.problem(agentSecret, http.MethodPut, base+"chat-a/projects/lorvik", body, http.StatusForbidden)
	e.problem(userSecret, http.MethodPut, base+"chat-a/projects/lorvik", apitypes.AgentSessionGrantBody{}, http.StatusUnprocessableEntity)

	// The granter's own role caps what they hand on: sam, a member, cannot give ready.
	e.membersAgent(domain.RightWork)
	mine := e.agentNamed("sam-bot")
	p = e.problem(memberSecret, http.MethodPut, "/agents/"+mine.String()+"/sessions/chat-a/projects/lorvik", body, http.StatusUnprocessableEntity)
	if !hasField(p, "rights") {
		t.Errorf("rights beyond the granter: %+v", p.Errors)
	}
	call[apitypes.Agent](e, memberSecret, http.MethodPut, "/agents/"+mine.String()+"/sessions/chat-a/projects/lorvik", apitypes.AgentSessionGrantBody{Rights: []string{"work"}}, http.StatusOK)
	if e.countEvents(domain.EventAgentSessionRights) != 1 {
		t.Errorf("refusals wrote events: %v", e.eventTypes())
	}
}

func (e *testEnv) agentNamed(name string) domain.ID {
	e.t.Helper()
	ags, err := e.fs.ListAgents(context.Background(), e.org.ID, true)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, a := range ags {
		if a.Name == name {
			return a.ID
		}
	}
	e.t.Fatalf("no agent %q", name)
	return 0
}

func TestSessionRecordingReachesTheStoreOncePerMinute(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	touches := func() int {
		e.fs.mu.Lock()
		defer e.fs.mu.Unlock()
		return e.fs.sessionTouches
	}
	inChat[apitypes.Me](e, agentSecret, "chat-a", http.MethodGet, "/me", nil, http.StatusOK)
	e.advance(30 * time.Second)
	inChat[apitypes.Me](e, agentSecret, "chat-a-c1", http.MethodGet, "/me", nil, http.StatusOK)
	if n := touches(); n != 1 {
		t.Errorf("store calls within the minute = %d, want 1: subagents share their chat", n)
	}
	inChat[apitypes.Me](e, agentSecret, "chat-b", http.MethodGet, "/me", nil, http.StatusOK)
	if n := touches(); n != 2 {
		t.Errorf("store calls after another chat = %d, want 2", n)
	}
	e.advance(31 * time.Second)
	inChat[apitypes.Me](e, agentSecret, "chat-a", http.MethodGet, "/me", nil, http.StatusOK)
	if n := touches(); n != 3 {
		t.Errorf("store calls after the minute = %d, want 3", n)
	}
}

func TestRequestsWithASessionRecordItsBaseLabel(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	inChat[apitypes.Me](e, agentSecret, "chat-a-c3-c1", http.MethodGet, "/me", nil, http.StatusOK)
	call[apitypes.Me](e, agentSecret, http.MethodGet, "/me", nil, http.StatusOK)
	call[apitypes.Me](e, userSecret, http.MethodGet, "/me", nil, http.StatusOK)
	e.advance(5 * time.Minute)
	inChat[apitypes.Me](e, agentSecret, "chat-b", http.MethodGet, "/me", nil, http.StatusOK)
	call[apitypes.Agent](e, userSecret, http.MethodPut, "/agents/"+e.agent.ID.String()+"/sessions/chat-a/projects/lorvik", apitypes.AgentSessionGrantBody{Rights: []string{"ready", "triage"}}, http.StatusOK)

	got := call[apitypes.Agent](e, userSecret, http.MethodGet, "/agents/"+e.agent.ID.String(), nil, http.StatusOK)
	if len(got.Sessions) != 2 || got.Sessions[0].Label != "chat-b" || got.Sessions[1].Label != "chat-a" {
		t.Fatalf("sessions = %+v, want chat-b then chat-a, base labels, newest first", got.Sessions)
	}
	if g := got.Sessions[1].Grants; len(g) != 1 || g[0].Project != "lorvik" || !slices.Equal(g[0].Rights, []string{"ready", "triage"}) || len(got.Sessions[0].Grants) != 0 {
		t.Errorf("grants = %+v / %+v, want chat-a granted ready and triage in lorvik", got.Sessions[1].Grants, got.Sessions[0].Grants)
	}
	if len(e.fs.sessions) != 2 {
		t.Errorf("stored sessions = %d, want 2: the person and the plain request leave none", len(e.fs.sessions))
	}
}

func TestJoinGivesWorkOnlyWithinTheOwnersRole(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		owner domain.Role
		want  []string
	}{
		{"member owner", domain.RoleMember, []string{"work"}},
		{"reviewer owner still only work", domain.RoleReviewer, []string{"work"}},
		{"viewer owner", domain.RoleViewer, []string{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.newProject("web", domain.VisibilityOrg)
			sam, _ := e.fs.GetUser(context.Background(), e.member.ID)
			sam.AgentJoin = domain.AgentJoinAllow
			_, _ = e.fs.UpdateUser(context.Background(), sam)
			created := call[apitypes.AgentCreated](e, memberSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "goose@sam", Auto: true}, http.StatusCreated)
			e.setOrgRole(e.member, tt.owner, domain.RecordActive)
			got := call[apitypes.JoinResult](e, created.Token, http.MethodPost, "/projects/web/join", nil, http.StatusOK)
			if got.Status != "joined" || !slices.Equal(got.Rights, tt.want) {
				t.Errorf("join = %+v, want joined with %v", got, tt.want)
			}
			id, _ := domain.ParseID(created.Agent.ID)
			pm, err := e.fs.GetProjectMember(context.Background(), e.projectID("web"), domain.ActorRef{Kind: domain.ActorAgent, ID: id})
			if err != nil || pm.Role != domain.RoleViewer || pm.Rights != domain.RightWork {
				t.Errorf("stored row = %+v, %v, want a viewer row with work: the owner cap applies at use time", pm, err)
			}
		})
	}
}

func TestAgentThatJoinedUnderAViewerOwnerWorksOnceTheOwnerIsPromoted(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	sam, _ := e.fs.GetUser(context.Background(), e.member.ID)
	sam.AgentJoin = domain.AgentJoinAllow
	_, _ = e.fs.UpdateUser(context.Background(), sam)
	created := call[apitypes.AgentCreated](e, memberSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "goose@sam", Auto: true}, http.StatusCreated)
	e.setOrgRole(e.member, domain.RoleViewer, domain.RecordActive)
	got := call[apitypes.JoinResult](e, created.Token, http.MethodPost, "/projects/lorvik/join", nil, http.StatusOK)
	if got.Status != "joined" || len(got.Rights) != 0 || !strings.Contains(got.Message, "no rights beyond reading") {
		t.Fatalf("join = %+v, want joined with no rights while the owner is a viewer", got)
	}
	card := e.readyCard(userSecret, "ready for the goose")
	e.problem(created.Token, http.MethodPost, cardPathOf(card.Number, "/claim"), apitypes.ClaimCard{}, http.StatusForbidden)
	e.setOrgRole(e.member, domain.RoleMember, domain.RecordActive)
	call[apitypes.Card](e, created.Token, http.MethodPost, cardPathOf(card.Number, "/claim"), apitypes.ClaimCard{}, http.StatusOK)
}

func (e *testEnv) projectID(slug string) domain.ID {
	e.t.Helper()
	p, err := e.fs.GetProjectBySlug(context.Background(), e.org.ID, slug)
	if err != nil {
		e.t.Fatal(err)
	}
	return p.ID
}

func TestMeShowsTheRightsOfThisRequestsSession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveProjectRights(domain.RightWork)
	call[apitypes.Agent](e, userSecret, http.MethodPut, "/agents/"+e.agent2.ID.String()+"/sessions/chat-a/projects/lorvik", apitypes.AgentSessionGrantBody{Rights: []string{"ready"}}, http.StatusOK)
	rightsIn := func(session string) []string {
		var me apitypes.Me
		if session == "" {
			me = call[apitypes.Me](e, agent2Secret, http.MethodGet, "/me", nil, http.StatusOK)
		} else {
			me = inChat[apitypes.Me](e, agent2Secret, session, http.MethodGet, "/me", nil, http.StatusOK)
		}
		if len(me.Projects) != 1 {
			t.Fatalf("projects = %+v, want one", me.Projects)
		}
		return me.Projects[0].Rights
	}
	if got := rightsIn("chat-a-c1"); !slices.Equal(got, []string{"work", "ready"}) {
		t.Errorf("granted chat's subagent sees %v, want work, ready", got)
	}
	for _, session := range []string{"chat-b", ""} {
		if got := rightsIn(session); !slices.Equal(got, []string{"work"}) {
			t.Errorf("session %q sees %v, want work only", session, got)
		}
	}
	if p := inChat[apitypes.Project](e, agent2Secret, "chat-a", http.MethodGet, "/projects/lorvik", nil, http.StatusOK); !slices.Equal(p.Rights, []string{"work", "ready"}) {
		t.Errorf("project rights = %v, want work, ready", p.Rights)
	}
}
