package server

import (
	"net/http"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func TestARevokedAgentCannotBeAssignedAndLosesOpenAssignments(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	to := &apitypes.Actor{Kind: "agent", ID: e.agent.ID.String()}
	open := e.newCard(userSecret, apitypes.CreateCard{Title: "open", Status: "ready", Assignee: to})
	closed := e.newCard(userSecret, apitypes.CreateCard{Title: "closed", Status: "ready", Assignee: to})
	e.move(userSecret, closed.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "wontfix"})

	if rec := e.do(userSecret, http.MethodDelete, "/agents/"+e.agent.ID.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d: %s", rec.Code, rec.Body)
	}
	if got := e.show(userSecret, open.Number); got.Assignee != nil {
		t.Errorf("open card assignee = %+v, want cleared so next offers it again", got.Assignee)
	}
	if got := e.show(userSecret, closed.Number); got.Assignee == nil {
		t.Error("dropped card lost its assignee; closed work stays as it was")
	}
	updates := e.eventsOf("card.updated")
	if len(updates) != 1 {
		t.Errorf("card.updated events = %v, want one for the open card", updates)
	}
	p := e.problem(userSecret, http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Title: "late", Assignee: to}, http.StatusUnprocessableEntity)
	if !hasField(p, "assignee") {
		t.Errorf("assigning a revoked agent: errors = %+v, want assignee", p.Errors)
	}
}

func TestLosingAProjectRoleClearsOnlyAssignmentsTheActorCanNoLongerHold(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	bound := e.newCard(userSecret, apitypes.CreateCard{Title: "for codex", Status: "ready", Assignee: &apitypes.Actor{Kind: "agent", ID: e.agent2.ID.String()}})
	call[apitypes.ProjectMember](e, userSecret, http.MethodPut, "/projects/lorvik/members/user/"+e.member.ID.String(), apitypes.PutMembership{Role: "member"}, http.StatusOK)
	viaOrg := e.newCard(userSecret, apitypes.CreateCard{Title: "for sam", Status: "ready", Assignee: &apitypes.Actor{Kind: "user", ID: e.member.ID.String()}})

	if rec := e.do(userSecret, http.MethodDelete, "/agents/"+e.agent2.ID.String()+"/projects/lorvik", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unbind = %d: %s", rec.Code, rec.Body)
	}
	if got := e.show(userSecret, bound.Number); got.Assignee != nil {
		t.Errorf("project-bound agent's card assignee = %+v, want cleared: it has no role left in lorvik", got.Assignee)
	}
	if rec := e.do(userSecret, http.MethodDelete, "/projects/lorvik/members/user/"+e.member.ID.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("remove project member = %d: %s", rec.Code, rec.Body)
	}
	if got := e.show(userSecret, viaOrg.Number); got.Assignee == nil || got.Assignee.ID != e.member.ID.String() {
		t.Errorf("org member's card assignee = %+v, want kept: the org role still reaches lorvik", got.Assignee)
	}
	if rec := e.do(userSecret, http.MethodDelete, "/members/user/"+e.member.ID.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("remove member = %d: %s", rec.Code, rec.Body)
	}
	if got := e.show(userSecret, viaOrg.Number); got.Assignee != nil {
		t.Errorf("removed member's card assignee = %+v, want cleared", got.Assignee)
	}
}
