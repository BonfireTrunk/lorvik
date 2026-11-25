package server

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// Every agent token lives at most an hour, whoever mints it and however it
// is asked for, and one issued for longer before the limit never works.
func TestAgentTokensLiveAtMostAnHour(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	lifetime := func(secret string) time.Duration {
		t.Helper()
		tok, err := e.fs.GetTokenByHash(ctx, domain.HashToken(secret))
		if err != nil {
			t.Fatal(err)
		}
		return tok.ExpiresAt.Sub(tok.CreatedAt)
	}
	created := call[apitypes.AgentCreated](e, userSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "hand", Projects: []string{"lorvik"}}, http.StatusCreated)
	if got := lifetime(created.Token); got != time.Hour {
		t.Errorf("registration token lives %v, want 1h", got)
	}
	minted := call[apitypes.TokenCreated](e, userSecret, http.MethodPost, "/tokens", apitypes.CreateToken{Agent: created.Agent.ID}, http.StatusCreated)
	if got := lifetime(minted.Secret); got != time.Hour {
		t.Errorf("minted agent token lives %v, want 1h", got)
	}
	if p := e.problem(userSecret, http.MethodPost, "/tokens", apitypes.CreateToken{Agent: created.Agent.ID, ExpiresIn: "2h"}, http.StatusUnprocessableEntity); !hasField(p, "expires_in") {
		t.Errorf("errors = %+v, want expires_in", p.Errors)
	}
	if p := e.problem(userSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "long", TokenExpiresIn: "720h"}, http.StatusUnprocessableEntity); !hasField(p, "token_expires_in") {
		t.Errorf("errors = %+v, want token_expires_in", p.Errors)
	}
	if mine := call[apitypes.TokenCreated](e, userSecret, http.MethodPost, "/tokens", apitypes.CreateToken{}, http.StatusCreated); lifetime(mine.Secret) != domain.DefaultTokenTTL {
		t.Errorf("a user's own token lives %v, want the org default", lifetime(mine.Secret))
	}

	const old = "lvk_a_issued-before-the-limit"
	e.seedToken(old, e.agent2, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
	if p := e.problem(old, http.MethodGet, "/projects", nil, http.StatusUnauthorized); p.Detail != "agent token outlives the one-hour limit" {
		t.Errorf("detail = %q", p.Detail)
	}
	e.advance(time.Hour)
	if p := e.problem(minted.Secret, http.MethodGet, "/projects", nil, http.StatusUnauthorized); p.Detail != "token expired" {
		t.Errorf("detail after an hour = %q, want token expired", p.Detail)
	}
}

// The CLI's own registration for a harness takes no projects, never brings
// back an agent its owner revoked (a hand registration may), and approves
// nothing even when an admin's login made it.
func TestAutoRegistration(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	auto := apitypes.CreateAgent{Name: "claude-code@box", Harness: "claude-code", Machine: "box", Auto: true}
	first := call[apitypes.AgentCreated](e, userSecret, http.MethodPost, "/agents", auto, http.StatusCreated)
	if first.Agent.Status != "active" || len(first.Agent.Projects) != 0 || first.Agent.Role != "" {
		t.Errorf("auto agent = %+v, want active with no role anywhere", first.Agent)
	}
	withProjects := auto
	withProjects.Name, withProjects.Projects = "codex@box", []string{"lorvik"}
	if p := e.problem(userSecret, http.MethodPost, "/agents", withProjects, http.StatusUnprocessableEntity); !hasField(p, "auto") {
		t.Errorf("errors = %+v, want auto", p.Errors)
	}

	if rec := e.do(userSecret, http.MethodDelete, "/agents/"+first.Agent.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d: %s", rec.Code, rec.Body)
	}
	if p := e.problem(userSecret, http.MethodPost, "/agents", auto, http.StatusConflict); !strings.Contains(p.Hint, "lorvik agent restore "+first.Agent.ID) {
		t.Errorf("hint = %q, want the way to restore that agent", p.Hint)
	}
	byHand := auto
	byHand.Auto = false
	call[apitypes.AgentCreated](e, userSecret, http.MethodPost, "/agents", byHand, http.StatusCreated)

	// A name another user's revoked agent once held is free.
	theirs := call[apitypes.AgentCreated](e, memberSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "goose@laptop"}, http.StatusCreated)
	if rec := e.do(memberSecret, http.MethodDelete, "/agents/"+theirs.Agent.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d: %s", rec.Code, rec.Body)
	}
	call[apitypes.AgentCreated](e, userSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "goose@laptop", Auto: true}, http.StatusCreated)

	call[apitypes.Org](e, userSecret, http.MethodPatch, "/org", apitypes.UpdateOrg{Settings: &apitypes.OrgSettings{AgentApproval: "admin"}}, http.StatusOK)
	if got := call[apitypes.AgentCreated](e, userSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "pi@box", Auto: true}, http.StatusCreated); got.Agent.Status != "pending" {
		t.Errorf("auto agent by the owner under admin approval = %q, want pending", got.Agent.Status)
	}
	if got := call[apitypes.AgentCreated](e, userSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "pi-by-hand"}, http.StatusCreated); got.Agent.Status != "active" {
		t.Errorf("hand agent by the owner under admin approval = %q, want active", got.Agent.Status)
	}
}

func (e *testEnv) newProject(slug string, vis domain.Visibility) domain.Project {
	e.t.Helper()
	proj, err := e.fs.CreateProject(context.Background(), domain.Project{ID: e.ids.New(), OrgID: e.org.ID, Slug: slug, Name: slug, Visibility: vis, Concurrency: domain.ConcurrencyParallel,
		Scope: domain.ProjectScopeProject, Lease: 30 * time.Minute, CreatedBy: e.user, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		e.t.Fatal(err)
	}
	return proj
}

func (e *testEnv) countEvents(typ domain.EventType) int {
	n := 0
	for _, ev := range e.fs.events {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

// With no say set, an agent asking to join waits for its owner, once: asking
// again neither duplicates the request nor lets it in. The owner sees the
// request, allows it, and the agent is in at member.
func TestAgentJoinWaitsForItsOwnerAndIsAllowed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.newProject("web", domain.VisibilityOrg)
	for range 2 {
		got := call[apitypes.JoinResult](e, agent2Secret, http.MethodPost, "/projects/web/join", nil, http.StatusOK)
		if got.Status != "pending" || got.Message != "waiting for ada to allow codex in web on the dashboard" {
			t.Errorf("join = %+v, want pending with the owner named", got)
		}
	}
	if n := e.countEvents(domain.EventAgentJoinRequested); n != 1 {
		t.Errorf("join_requested events = %d, want 1", n)
	}
	e.problem(agent2Secret, http.MethodGet, "/projects/web", nil, http.StatusNotFound)
	if got := call[apitypes.AgentRequestList](e, memberSecret, http.MethodGet, "/agent-requests", nil, http.StatusOK); len(got.Items) != 0 {
		t.Errorf("another user's requests = %+v, want none", got.Items)
	}
	reqs := call[apitypes.AgentRequestList](e, userSecret, http.MethodGet, "/agent-requests", nil, http.StatusOK)
	if len(reqs.Items) != 1 || reqs.Items[0].Agent.Name != "codex" || reqs.Items[0].Project != "web" || !slices.Equal(reqs.Items[0].Rights, []string{"work"}) {
		t.Fatalf("requests = %+v, want codex asking for web", reqs.Items)
	}
	path := "/agent-requests/" + e.agent2.ID.String() + "/web"
	e.problem(memberSecret, http.MethodPut, path, nil, http.StatusNotFound)
	call[apitypes.Agent](e, userSecret, http.MethodPut, path, nil, http.StatusOK)
	if p := call[apitypes.Project](e, agent2Secret, http.MethodGet, "/projects/web", nil, http.StatusOK); !slices.Equal(p.Rights, []string{"work"}) {
		t.Errorf("rights after allow = %v, want work", p.Rights)
	}
	if got := call[apitypes.AgentRequestList](e, userSecret, http.MethodGet, "/agent-requests", nil, http.StatusOK); len(got.Items) != 0 {
		t.Errorf("requests after allow = %+v, want none", got.Items)
	}
	if got := call[apitypes.JoinResult](e, agent2Secret, http.MethodPost, "/projects/web/join", nil, http.StatusOK); got.Status != "member" {
		t.Errorf("join after allow = %+v, want member", got)
	}
	// Nothing is left to allow.
	e.problem(userSecret, http.MethodPut, path, nil, http.StatusNotFound)
}

// A denied agent is refused when it asks again, until its owner adds it.
func TestAgentJoinDeniedStaysDenied(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.newProject("web", domain.VisibilityOrg)
	call[apitypes.JoinResult](e, agent2Secret, http.MethodPost, "/projects/web/join", nil, http.StatusOK)
	if rec := e.do(userSecret, http.MethodDelete, "/agent-requests/"+e.agent2.ID.String()+"/web", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("deny = %d: %s", rec.Code, rec.Body)
	}
	if p := e.problem(agent2Secret, http.MethodPost, "/projects/web/join", nil, http.StatusForbidden); !strings.Contains(p.Detail, "denied or removed") {
		t.Errorf("detail = %q", p.Detail)
	}
	if got := call[apitypes.AgentRequestList](e, userSecret, http.MethodGet, "/agent-requests", nil, http.StatusOK); len(got.Items) != 0 {
		t.Errorf("requests after deny = %+v, want none", got.Items)
	}
	call[apitypes.Agent](e, userSecret, http.MethodPut, "/agents/"+e.agent2.ID.String()+"/projects/web", apitypes.PutMembership{Rights: []string{"work"}}, http.StatusOK)
	if got := call[apitypes.JoinResult](e, agent2Secret, http.MethodPost, "/projects/web/join", nil, http.StatusOK); got.Status != "member" {
		t.Errorf("join after the owner added it = %+v, want member", got)
	}
}

// Always ask from any say that applies wins, then allow from any.
func TestAgentJoinFollowsTheSays(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                        string
		account, agent, rule, admin domain.AgentJoin
		want                        string
	}{
		{name: "account switch on", account: domain.AgentJoinAllow, want: "joined"},
		{name: "agent allows everywhere", agent: domain.AgentJoinAllow, want: "joined"},
		{name: "owner allows this project", rule: domain.AgentJoinAllow, want: "joined"},
		{name: "project admin says always ask", account: domain.AgentJoinAllow, agent: domain.AgentJoinAllow, admin: domain.AgentJoinAsk, want: "pending"},
		{name: "agent says always ask", account: domain.AgentJoinAllow, agent: domain.AgentJoinAsk, want: "pending"},
		{name: "owner says always ask here", agent: domain.AgentJoinAllow, rule: domain.AgentJoinAsk, want: "pending"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			ctx := context.Background()
			web := e.newProject("web", domain.VisibilityOrg)
			web.AgentJoin = tc.admin
			_, _ = e.fs.UpdateProject(ctx, web)
			u, _ := e.fs.GetUser(ctx, e.user.ID)
			u.AgentJoin = tc.account
			_, _ = e.fs.UpdateUser(ctx, u)
			ag, _ := e.fs.GetAgent(ctx, e.agent2.ID)
			ag.AgentJoin = tc.agent
			_, _ = e.fs.UpdateAgent(ctx, ag)
			if tc.rule != domain.AgentJoinFollow {
				_, _ = e.fs.PutAgentJoinRule(ctx, domain.AgentJoinRule{ProjectID: web.ID, UserID: e.user.ID, AgentJoin: tc.rule, UpdatedAt: t0})
			}
			got := call[apitypes.JoinResult](e, agent2Secret, http.MethodPost, "/projects/web/join", nil, http.StatusOK)
			if got.Status != tc.want {
				t.Errorf("join = %+v, want %s", got, tc.want)
			}
			if tc.want == "joined" {
				call[apitypes.Project](e, agent2Secret, http.MethodGet, "/projects/web", nil, http.StatusOK)
			}
		})
	}
}

// An agent joins with work at most, within its owner's role, cannot ask into
// a project its owner cannot see, and only an agent asks at all.
func TestAgentJoinIsBoundByItsOwner(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	e.newProject("web", domain.VisibilityOrg)
	e.newProject("vault", domain.VisibilityPrivate)
	sam, _ := e.fs.GetUser(ctx, e.member.ID)
	sam.AgentJoin = domain.AgentJoinAllow
	_, _ = e.fs.UpdateUser(ctx, sam)
	created := call[apitypes.AgentCreated](e, memberSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "goose@sam", Auto: true}, http.StatusCreated)
	e.problem(created.Token, http.MethodPost, "/projects/vault/join", nil, http.StatusNotFound)
	e.setOrgRole(e.member, domain.RoleViewer, domain.RecordActive)
	if got := call[apitypes.JoinResult](e, created.Token, http.MethodPost, "/projects/web/join", nil, http.StatusOK); got.Status != "joined" || len(got.Rights) != 0 {
		t.Errorf("join = %+v, want joined with no rights: the owner is a viewer", got)
	}
	e.problem(userSecret, http.MethodPost, "/projects/web/join", nil, http.StatusForbidden)
}

// Each say is set where it belongs and only to what that level may say:
// the account switch on /me (follow or allow), the agent's setting by a user
// who manages it, the project admin's setting (follow or ask) and a user's
// own rule for one project (follow removes it).
func TestAgentJoinSettings(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.newProject("web", domain.VisibilityOrg)
	if u := call[apitypes.User](e, userSecret, http.MethodPatch, "/me", apitypes.UpdateUser{AgentJoin: new("allow")}, http.StatusOK); u.AgentJoin != "allow" {
		t.Errorf("PATCH /me agent_join = %q, want allow", u.AgentJoin)
	}
	if me := call[apitypes.Me](e, userSecret, http.MethodGet, "/me", nil, http.StatusOK); me.User == nil || me.User.AgentJoin != "allow" {
		t.Errorf("GET /me user = %+v, want agent_join allow", me.User)
	}
	e.problem(userSecret, http.MethodPatch, "/me", apitypes.UpdateUser{AgentJoin: new("ask")}, http.StatusUnprocessableEntity)

	agentPath := "/agents/" + e.agent2.ID.String()
	if a := call[apitypes.Agent](e, userSecret, http.MethodPatch, agentPath, apitypes.UpdateAgent{AgentJoin: new("ask")}, http.StatusOK); a.AgentJoin != "ask" {
		t.Errorf("agent agent_join = %q, want ask", a.AgentJoin)
	}
	e.problem(memberSecret, http.MethodPatch, agentPath, apitypes.UpdateAgent{AgentJoin: new("allow")}, http.StatusForbidden)
	e.problem(agent2Secret, http.MethodPatch, agentPath, apitypes.UpdateAgent{AgentJoin: new("allow")}, http.StatusForbidden)

	if p := call[apitypes.Project](e, userSecret, http.MethodPatch, "/projects/web", apitypes.UpdateProject{AgentJoin: new("ask")}, http.StatusOK); p.AgentJoin != "ask" {
		t.Errorf("project agent_join = %q, want ask", p.AgentJoin)
	}
	e.problem(userSecret, http.MethodPatch, "/projects/web", apitypes.UpdateProject{AgentJoin: new("allow")}, http.StatusUnprocessableEntity)
	e.problem(memberSecret, http.MethodPatch, "/projects/web", apitypes.UpdateProject{AgentJoin: new("follow")}, http.StatusForbidden)

	for _, want := range []string{"allow", "ask", "follow"} {
		if p := call[apitypes.Project](e, memberSecret, http.MethodPut, "/projects/web/agent-join", apitypes.PutAgentJoin{AgentJoin: want}, http.StatusOK); p.MyAgentJoin != want {
			t.Errorf("PUT agent-join = %q, want %s", p.MyAgentJoin, want)
		}
		if p := call[apitypes.Project](e, memberSecret, http.MethodGet, "/projects/web", nil, http.StatusOK); p.MyAgentJoin != want {
			t.Errorf("GET my_agent_join = %q, want %s", p.MyAgentJoin, want)
		}
	}
	if p := call[apitypes.Project](e, userSecret, http.MethodGet, "/projects/web", nil, http.StatusOK); p.MyAgentJoin != "follow" {
		t.Errorf("another user's rule = %q, want their own follow", p.MyAgentJoin)
	}
	e.problem(agent2Secret, http.MethodPut, "/projects/lorvik/agent-join", apitypes.PutAgentJoin{AgentJoin: "allow"}, http.StatusForbidden)
}

// A revoked agent gets no new token, so renewing from a login never brings
// it back.
func TestRevokedAgentGetsNoNewToken(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if rec := e.do(userSecret, http.MethodDelete, "/agents/"+e.agent2.ID.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d: %s", rec.Code, rec.Body)
	}
	if p := e.problem(userSecret, http.MethodPost, "/tokens", apitypes.CreateToken{Agent: e.agent2.ID.String()}, http.StatusConflict); !strings.Contains(p.Detail, "revoked") {
		t.Errorf("detail = %q", p.Detail)
	}
}

// Revoking an agent revokes its tokens; the agent hears that it was revoked,
// not only that its token was, so it does not go looking for another token.
func TestARevokedAgentsTokenSaysTheAgentWasRevoked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.do(userSecret, http.MethodDelete, "/agents/"+e.agent2.ID.String(), nil)
	if p := e.problem(agent2Secret, http.MethodGet, "/me", nil, http.StatusUnauthorized); p.Detail != "agent revoked" {
		t.Errorf("detail = %q, want agent revoked", p.Detail)
	}
	if p := e.problem("lvk_u_revoked-secret", http.MethodGet, "/me", nil, http.StatusUnauthorized); p.Detail == "agent revoked" {
		t.Errorf("an unknown user token says %q", p.Detail)
	}
}

// Every change to a user's say over their agents joining is on the record:
// the account switch and the rule in one project each write an event; a
// profile edit that leaves the switch alone writes none.
func TestAgentJoinSettingChangesWriteEvents(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	count := func() int {
		n := 0
		for _, ty := range e.eventTypes() {
			if ty == "agent.join_setting" {
				n++
			}
		}
		return n
	}
	allow, name := "allow", "Ada"
	call[apitypes.User](e, userSecret, http.MethodPatch, "/me", apitypes.UpdateUser{AgentJoin: &allow}, http.StatusOK)
	if got := count(); got != 1 {
		t.Fatalf("after the account switch: %d events, want 1", got)
	}
	if ev := e.lastEvent(); ev.ProjectID != nil || !strings.Contains(string(ev.Payload), `"agent_join":"allow"`) {
		t.Errorf("account switch event = project %v payload %s", ev.ProjectID, ev.Payload)
	}
	call[apitypes.User](e, userSecret, http.MethodPatch, "/me", apitypes.UpdateUser{Name: &name}, http.StatusOK)
	if got := count(); got != 1 {
		t.Errorf("after a name change: %d events, want still 1", got)
	}
	call[apitypes.Project](e, userSecret, http.MethodPut, "/projects/lorvik/agent-join", apitypes.PutAgentJoin{AgentJoin: "ask"}, http.StatusOK)
	if ev := e.lastEvent(); count() != 2 || ev.ProjectID == nil || *ev.ProjectID != e.proj.ID || !strings.Contains(string(ev.Payload), `"project":"lorvik"`) {
		t.Errorf("project rule event = %d events, project %v payload %s", count(), ev.ProjectID, ev.Payload)
	}
}

// A request shows the rights Allow would grant now: an owner demoted since
// the agent asked sees none, not the work right asked for.
func TestAgentRequestShowsTheRightsAllowGrantsNow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	secret, _ := e.memberAgentInPrivateProject("priv")
	e.newProject("web", domain.VisibilityOrg)
	call[apitypes.JoinResult](e, secret, http.MethodPost, "/projects/web/join", nil, http.StatusOK)
	if _, err := e.fs.PutMembership(context.Background(), domain.Membership{OrgID: e.org.ID, Actor: e.member, Role: domain.RoleViewer,
		Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	reqs := call[apitypes.AgentRequestList](e, memberSecret, http.MethodGet, "/agent-requests", nil, http.StatusOK)
	if len(reqs.Items) != 1 || len(reqs.Items[0].Rights) != 0 {
		t.Errorf("requests = %+v, want one with no rights", reqs.Items)
	}
}

// Revoking an agent removes its waiting requests to join projects, so no row
// is left that nobody can act on.
func TestRevokingAnAgentDropsItsJoinRequests(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	secret, ref := e.memberAgentInPrivateProject("priv")
	web := e.newProject("web", domain.VisibilityOrg)
	if got := call[apitypes.JoinResult](e, secret, http.MethodPost, "/projects/web/join", nil, http.StatusOK); got.Status != "pending" {
		t.Fatalf("join = %q, want pending", got.Status)
	}
	call[struct{}](e, memberSecret, http.MethodDelete, "/agents/"+ref.ID.String(), nil, http.StatusNoContent)
	pm, err := e.fs.GetProjectMember(context.Background(), web.ID, ref)
	if err != nil {
		t.Fatal(err)
	}
	if pm.Status != domain.RecordRemoved {
		t.Errorf("request status = %v, want removed", pm.Status)
	}
}

// /me names the GitHub account linked to the caller, and nothing when none is.
func TestMeNamesTheLinkedGitHubAccount(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if _, err := e.fs.CreateIdentity(context.Background(), domain.Identity{ID: e.ids.New(), UserID: e.user.ID, Provider: domain.ProviderGitHub,
		ProviderID: "gh-1", Login: "octo", CreatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, secret, want string
	}{
		{"linked", userSecret, "octo"},
		{"not linked", memberSecret, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			me := call[apitypes.Me](e, tt.secret, http.MethodGet, "/me", nil, http.StatusOK)
			if me.User == nil || me.User.GitHubLogin != tt.want {
				t.Errorf("github_login = %+v, want %q", me.User, tt.want)
			}
		})
	}
}
