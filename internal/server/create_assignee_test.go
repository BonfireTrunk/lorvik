package server

import (
	"net/http"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func TestCreateCardAssigneeIsStoredAndValidated(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := apitypes.Actor{Kind: "user", ID: e.user.ID.String()}
	got := call[apitypes.Card](e, agentSecret, http.MethodPost, "/projects/lorvik/cards",
		apitypes.CreateCard{Title: "assigned at creation", Status: "ready", Assignee: &owner}, http.StatusCreated)
	if got.Assignee == nil || *got.Assignee != owner {
		t.Errorf("assignee = %+v, want %+v", got.Assignee, owner)
	}
	if shown := e.show(userSecret, got.Number); shown.Assignee == nil || *shown.Assignee != owner {
		t.Errorf("shown assignee = %+v, want %+v", shown.Assignee, owner)
	}

	// An actor that is not in the org is rejected, nothing is created.
	e.problem(agentSecret, http.MethodPost, "/projects/lorvik/cards",
		apitypes.CreateCard{Title: "bad assignee", Assignee: &apitypes.Actor{Kind: "user", ID: "999"}}, http.StatusUnprocessableEntity)
}
