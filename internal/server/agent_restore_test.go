package server

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// A revoked agent comes back as it was: its project access holds, its old
// token stays dead but no longer says the agent is revoked, so the CLI
// renews it, and its owner's login mints it a new one. Only its owner or an
// agent manager restores it, never an agent.
func TestRestoringARevokedAgent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := "/agents/" + e.agent2.ID.String()
	if p := e.problem(userSecret, http.MethodPost, path+"/restore", nil, http.StatusConflict); !strings.Contains(p.Detail, "agent is active") {
		t.Errorf("restoring an active agent = %q, want agent is active", p.Detail)
	}
	call[struct{}](e, userSecret, http.MethodDelete, path, nil, http.StatusNoContent)
	e.problem(memberSecret, http.MethodPost, path+"/restore", nil, http.StatusForbidden)
	e.problem(agentSecret, http.MethodPost, path+"/restore", nil, http.StatusForbidden)

	got := call[apitypes.Agent](e, userSecret, http.MethodPost, path+"/restore", nil, http.StatusOK)
	if got.Status != "active" {
		t.Errorf("status = %q, want active", got.Status)
	}
	if len(got.Projects) != 1 || got.Projects[0].Project != "lorvik" || !slices.Equal(got.Projects[0].Rights, []string{"work", "ready"}) {
		t.Errorf("projects = %+v, want lorvik with work and ready as before the revoke", got.Projects)
	}
	if ev := e.lastEvent(); ev.Type != domain.EventAgentRestored || !strings.Contains(string(ev.Payload), `"status":"active"`) {
		t.Errorf("last event = %s %s, want agent.restored as active", ev.Type, ev.Payload)
	}
	if p := e.problem(agent2Secret, http.MethodGet, "/me", nil, http.StatusUnauthorized); p.Detail != "token revoked" {
		t.Errorf("the old token says %q, want token revoked", p.Detail)
	}
	tok := call[apitypes.TokenCreated](e, userSecret, http.MethodPost, "/tokens", apitypes.CreateToken{Agent: e.agent2.ID.String()}, http.StatusCreated)
	if me := call[apitypes.Me](e, tok.Secret, http.MethodGet, "/me", nil, http.StatusOK); me.Actor.ID != e.agent2.ID.String() {
		t.Errorf("the new token acts as %s, want the restored agent", me.Actor.ID)
	}
}

// Where the org wants agents approved, a restore by someone who cannot
// approve leaves the agent pending, like a registration; an agent manager's
// restore approves it.
func TestRestoringWaitsForApprovalWhereAgentsNeedIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	theirs := call[apitypes.AgentCreated](e, memberSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "goose@laptop"}, http.StatusCreated)
	path := "/agents/" + theirs.Agent.ID
	call[struct{}](e, memberSecret, http.MethodDelete, path, nil, http.StatusNoContent)
	call[apitypes.Org](e, userSecret, http.MethodPatch, "/org", apitypes.UpdateOrg{Settings: &apitypes.OrgSettings{AgentApproval: "admin"}}, http.StatusOK)

	if got := call[apitypes.Agent](e, memberSecret, http.MethodPost, path+"/restore", nil, http.StatusOK); got.Status != "pending" {
		t.Errorf("restored by its owner, a member = %q, want pending", got.Status)
	}
	if ev := e.lastEvent(); ev.Type != domain.EventAgentRestored || !strings.Contains(string(ev.Payload), `"status":"pending"`) {
		t.Errorf("last event = %s %s, want agent.restored as pending", ev.Type, ev.Payload)
	}
	tok := call[apitypes.TokenCreated](e, memberSecret, http.MethodPost, "/tokens", apitypes.CreateToken{Agent: theirs.Agent.ID}, http.StatusCreated)
	if p := e.problem(tok.Secret, http.MethodGet, "/me", nil, http.StatusForbidden); p.Detail != "agent pending approval" {
		t.Errorf("a pending restored agent hears %q, want agent pending approval", p.Detail)
	}
	call[struct{}](e, memberSecret, http.MethodDelete, path, nil, http.StatusNoContent)
	if got := call[apitypes.Agent](e, userSecret, http.MethodPost, path+"/restore", nil, http.StatusOK); got.Status != "active" {
		t.Errorf("restored by an agent manager = %q, want active", got.Status)
	}
}

// A revoked agent frees its name, so another may hold it by the time it is
// restored; two live agents never share one.
func TestRestoringIsRefusedWhenTheNameWasTaken(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := "/agents/" + e.agent2.ID.String()
	call[struct{}](e, userSecret, http.MethodDelete, path, nil, http.StatusNoContent)
	call[apitypes.AgentCreated](e, userSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "codex"}, http.StatusCreated)
	if p := e.problem(userSecret, http.MethodPost, path+"/restore", nil, http.StatusConflict); !strings.Contains(p.Hint, "took this name") {
		t.Errorf("hint = %q, want it to say the name was taken", p.Hint)
	}
}

// Removing a revoked agent lets its harness start over: the registration
// the revoke refused now makes a new agent, and the old one is gone from
// every list and to every change, though still named for its past work.
func TestRemovingARevokedAgentLetsItsHarnessRegisterAgain(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	auto := apitypes.CreateAgent{Name: "claude-code@box", Harness: "claude-code", Machine: "box", Auto: true}
	first := call[apitypes.AgentCreated](e, userSecret, http.MethodPost, "/agents", auto, http.StatusCreated)
	path := "/agents/" + first.Agent.ID
	call[struct{}](e, userSecret, http.MethodDelete, path, nil, http.StatusNoContent)
	e.problem(userSecret, http.MethodPost, "/agents", auto, http.StatusConflict)

	e.problem(memberSecret, http.MethodPost, path+"/remove", nil, http.StatusForbidden)
	call[struct{}](e, userSecret, http.MethodPost, path+"/remove", nil, http.StatusNoContent)
	if ev := e.lastEvent(); ev.Type != domain.EventAgentRemoved {
		t.Errorf("last event = %s, want agent.removed", ev.Type)
	}
	// The CLI renews with the id in the agent's file, hears 404, and registers.
	e.problem(userSecret, http.MethodPost, "/tokens", apitypes.CreateToken{Agent: first.Agent.ID}, http.StatusNotFound)
	second := call[apitypes.AgentCreated](e, userSecret, http.MethodPost, "/agents", auto, http.StatusCreated)
	if second.Agent.ID == first.Agent.ID {
		t.Error("the new registration reused the removed agent")
	}

	all := call[apitypes.AgentList](e, userSecret, http.MethodGet, "/agents?all=true", nil, http.StatusOK)
	if slices.ContainsFunc(all.Items, func(a apitypes.Agent) bool { return a.ID == first.Agent.ID }) {
		t.Error("a removed agent is listed with all=true")
	}
	if got := call[apitypes.Agent](e, userSecret, http.MethodGet, path, nil, http.StatusOK); got.Status != "removed" {
		t.Errorf("status = %q, want removed", got.Status)
	}
	e.problem(userSecret, http.MethodPost, path+"/restore", nil, http.StatusNotFound)
	e.problem(userSecret, http.MethodPost, path+"/remove", nil, http.StatusNotFound)
}

// Removing an active agent stops it like a revoke and takes its access with
// it. Its token says token revoked, not agent revoked, so its chat goes on
// to register a new agent.
func TestRemovingAnActiveAgentStopsItAndTakesItsAccess(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	for _, a := range []domain.ActorRef{e.agent, e.agent2} {
		call[struct{}](e, userSecret, http.MethodPost, "/agents/"+a.ID.String()+"/remove", nil, http.StatusNoContent)
	}
	for _, secret := range []string{agentSecret, agent2Secret} {
		if p := e.problem(secret, http.MethodGet, "/me", nil, http.StatusUnauthorized); p.Detail != "token revoked" {
			t.Errorf("a removed agent's token says %q, want token revoked", p.Detail)
		}
	}
	if m, err := e.fs.GetMembership(ctx, e.org.ID, e.agent); err != nil || m.Status != domain.RecordRemoved {
		t.Errorf("org membership = %v, %v, want removed", m.Status, err)
	}
	if pm, err := e.fs.GetProjectMember(ctx, e.proj.ID, e.agent2); err != nil || pm.Status != domain.RecordRemoved {
		t.Errorf("project membership = %v, %v, want removed", pm.Status, err)
	}
	if n := e.countEvents(domain.EventAgentRevoked); n != 0 {
		t.Errorf("agent.revoked events = %d, want none: a removal is not a revoke", n)
	}
}

// An org-wide agent keeps its org rights through a revoke and a restore.
func TestRestoringBringsBackOrgWideRights(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := "/agents/" + e.agent.ID.String()
	call[struct{}](e, userSecret, http.MethodDelete, path, nil, http.StatusNoContent)
	got := call[apitypes.Agent](e, userSecret, http.MethodPost, path+"/restore", nil, http.StatusOK)
	if got.Role != "viewer" || !slices.Equal(got.Rights, []string{"work", "ready"}) {
		t.Errorf("restored org-wide agent = role %q rights %v, want viewer with work and ready", got.Role, got.Rights)
	}
}

// A removed agent is gone to every route that changes an agent or gives it
// a place: none of them may bring a row of it back to life.
func TestARemovedAgentIsGoneToEveryChange(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	id := e.agent2.ID.String()
	path := "/agents/" + id
	e.problem(agentSecret, http.MethodPost, path+"/remove", nil, http.StatusForbidden)
	call[struct{}](e, userSecret, http.MethodPost, path+"/remove", nil, http.StatusNoContent)

	rights := apitypes.PutMembership{Rights: []string{"work"}}
	tests := []struct {
		name, method, path string
		body               any
		want               int
	}{
		{"update", http.MethodPatch, path, apitypes.UpdateAgent{AgentJoin: new("allow")}, http.StatusNotFound},
		{"approve", http.MethodPost, path + "/approve", nil, http.StatusNotFound},
		{"revoke", http.MethodDelete, path, nil, http.StatusNotFound},
		{"bind", http.MethodPut, path + "/projects/lorvik", rights, http.StatusNotFound},
		{"unbind", http.MethodDelete, path + "/projects/lorvik", nil, http.StatusNotFound},
		{"chat grant", http.MethodPut, path + "/sessions/chat-a/projects/lorvik", apitypes.AgentSessionGrantBody{Rights: []string{"ready"}}, http.StatusNotFound},
		{"org member", http.MethodPut, "/members/agent/" + id, rights, http.StatusUnprocessableEntity},
		{"project member", http.MethodPut, "/projects/lorvik/members/agent/" + id, rights, http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		e.problem(userSecret, tt.method, tt.path, tt.body, tt.want)
	}
	ctx := context.Background()
	if pm, err := e.fs.GetProjectMember(ctx, e.proj.ID, e.agent2); err != nil || pm.Status != domain.RecordRemoved {
		t.Errorf("project membership = %v, %v, want still removed", pm.Status, err)
	}
	if _, err := e.fs.GetMembership(ctx, e.org.ID, e.agent2); err == nil {
		t.Error("a removed agent was given an org membership")
	}
}

// A member removes their own agent, and with it go its chat's grant, its
// waiting request to join a project and its place on a card.
func TestRemovingTakesGrantsRequestsAndAssignments(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	secret, ref := e.memberAgentInPrivateProject("priv")
	web := e.newProject("web", domain.VisibilityOrg)
	if got := call[apitypes.JoinResult](e, secret, http.MethodPost, "/projects/web/join", nil, http.StatusOK); got.Status != "pending" {
		t.Fatalf("join = %q, want pending", got.Status)
	}
	if _, err := e.fs.PutProjectMember(ctx, domain.ProjectMember{ProjectID: e.proj.ID, Actor: ref, Role: domain.RoleViewer, Rights: domain.RightWork, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	path := "/agents/" + ref.ID.String()
	call[apitypes.Agent](e, memberSecret, http.MethodPut, path+"/sessions/chat-a/projects/lorvik", apitypes.AgentSessionGrantBody{Rights: []string{"work"}}, http.StatusOK)
	card := e.newCard(userSecret, apitypes.CreateCard{Title: "theirs", Status: "ready", Assignee: &apitypes.Actor{Kind: "agent", ID: ref.ID.String()}})

	call[struct{}](e, memberSecret, http.MethodPost, path+"/remove", nil, http.StatusNoContent)

	if pm, err := e.fs.GetProjectMember(ctx, web.ID, ref); err != nil || pm.Status != domain.RecordRemoved {
		t.Errorf("join request = %v, %v, want removed", pm.Status, err)
	}
	if grants, err := e.fs.ListAgentSessionGrants(ctx, ref.ID); err != nil || len(grants) != 0 {
		t.Errorf("chat grants = %+v, %v, want none alive", grants, err)
	}
	if got := e.show(userSecret, card.Number); got.Assignee != nil {
		t.Errorf("assignee = %+v, want cleared", got.Assignee)
	}
}

// Should a token of a removed agent ever be alive, the agent's own status
// still refuses it.
func TestALiveTokenOfARemovedAgentIsRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	gone, err := e.fs.CreateAgent(context.Background(), domain.Agent{ID: e.ids.New(), OrgID: e.org.ID, Name: "gone", Harness: domain.HarnessOther, CreatedBy: e.user.ID,
		Status: domain.RecordForgotten, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	const secret = "lvk_a_gone-agent-secret"
	e.seedToken(secret, gone.Ref(), domain.ScopeWrite, t0.Add(domain.AgentTokenTTL))
	if p := e.problem(secret, http.MethodGet, "/me", nil, http.StatusUnauthorized); p.Detail != "unknown agent" {
		t.Errorf("detail = %q, want unknown agent", p.Detail)
	}
}
