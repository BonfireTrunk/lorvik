package server

import (
	"net/http"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func TestMemberEditsTextOfOthersCardButNotTriageFields(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(agentSecret, "agent's card")

	// Title and body of an open card are open to any member.
	got := call[apitypes.Card](e, memberSecret, http.MethodPatch, cardPathOf(c.Number, ""),
		apitypes.UpdateCard{Version: c.Version, Body: new("found something")}, http.StatusOK)
	if got.Body != "found something" {
		t.Errorf("body = %q, want the member's text", got.Body)
	}
	// Any triage field in the same request is refused.
	p := e.problem(memberSecret, http.MethodPatch, cardPathOf(c.Number, ""),
		apitypes.UpdateCard{Version: got.Version, Body: new("more"), Priority: new("high")}, http.StatusForbidden)
	if p.Detail == "" {
		t.Errorf("forbidden problem has no detail")
	}
	// A closed card is not open to text edits by non-owners.
	done := call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(c.Number, "/transition"),
		apitypes.TransitionCard{Version: got.Version, Status: "dropped", DropReason: "obsolete"}, http.StatusOK)
	e.problem(memberSecret, http.MethodPatch, cardPathOf(c.Number, ""), apitypes.UpdateCard{Version: done.Version, Body: new("late")}, http.StatusForbidden)
}

func TestMemberEditsPriorityOfOwnCard(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := call[apitypes.Card](e, memberSecret, http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Title: "mine"}, http.StatusCreated)
	got := call[apitypes.Card](e, memberSecret, http.MethodPatch, cardPathOf(c.Number, ""),
		apitypes.UpdateCard{Version: c.Version, Priority: new("high")}, http.StatusOK)
	if got.Priority != "high" {
		t.Errorf("priority = %q, want high", got.Priority)
	}
}

func TestCardTextEditsKeepRevisions(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := call[apitypes.Card](e, agentSecret, http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Title: "first", Body: "one"}, http.StatusCreated)
	c = call[apitypes.Card](e, memberSecret, http.MethodPatch, cardPathOf(c.Number, ""), apitypes.UpdateCard{Version: c.Version, Body: new("two")}, http.StatusOK)
	// A triage-only edit changes no text, so it keeps no revision.
	c = call[apitypes.Card](e, agentSecret, http.MethodPatch, cardPathOf(c.Number, ""), apitypes.UpdateCard{Version: c.Version, Priority: new("high")}, http.StatusOK)
	call[apitypes.Card](e, agentSecret, http.MethodPatch, cardPathOf(c.Number, ""), apitypes.UpdateCard{Version: c.Version, Title: new("second")}, http.StatusOK)

	revs := call[[]apitypes.CardRevision](e, agentSecret, http.MethodGet, cardPathOf(c.Number, "/revisions"), nil, http.StatusOK)
	if len(revs) != 2 || revs[0].Version != 3 || revs[1].Version != 1 || revs[0].Body != "" || revs[0].Title != "first" {
		t.Fatalf("revisions = %+v, want versions 3 then 1 without bodies", revs)
	}
	if revs[1].UpdatedBy.ID != e.member.ID.String() {
		t.Errorf("revision 1 by %+v, want the member who replaced it", revs[1].UpdatedBy)
	}
	r := call[apitypes.CardRevision](e, agentSecret, http.MethodGet, cardPathOf(c.Number, "/revisions/1"), nil, http.StatusOK)
	if r.Title != "first" || r.Body != "one" {
		t.Errorf("revision 1 = %+v, want the original text", r)
	}
	e.problem(agentSecret, http.MethodGet, cardPathOf(c.Number, "/revisions/2"), nil, http.StatusNotFound)
}
