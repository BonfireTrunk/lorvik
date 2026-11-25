package server

import (
	"net/http"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

func TestGlobalCardHidesLinksIntoAProjectTheReaderCannotSee(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "secret", Name: "Secret", Visibility: "private"}, http.StatusCreated)
	g := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/global/cards", apitypes.CreateCard{Title: "org-wide", Status: "ready"}, http.StatusCreated)
	hidden := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/secret/cards", apitypes.CreateCard{Title: "hidden", Status: "ready"}, http.StatusCreated)
	call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/secret/cards/"+itoa(hidden.Number)+"/links",
		apitypes.LinkCards{Kind: "blocks", To: g.Number, Project: "global"}, http.StatusOK)

	asOwner := call[apitypes.Card](e, userSecret, http.MethodGet, "/projects/global/cards/"+itoa(g.Number), nil, http.StatusOK)
	if len(asOwner.Links) != 1 || asOwner.Links[0].Project != "secret" || len(asOwner.BlockedBy) != 1 {
		t.Errorf("owner sees links %+v, blocked by %+v; want the secret card in both", asOwner.Links, asOwner.BlockedBy)
	}
	asAgent := call[apitypes.Card](e, agentSecret, http.MethodGet, "/projects/global/cards/"+itoa(g.Number), nil, http.StatusOK)
	if len(asAgent.Links) != 0 || len(asAgent.BlockedBy) != 0 {
		t.Errorf("agent sees links %+v, blocked by %+v; want neither (secret is private)", asAgent.Links, asAgent.BlockedBy)
	}
	for _, ev := range asAgent.Events {
		if ev.Type == domain.EventCardLinked.String() || ev.Type == domain.EventCardStatusChanged.String() {
			t.Errorf("agent sees %s in the global card's history; it names the secret card", ev.Type)
		}
	}
}

func TestCrossProjectLinkEventsLandInTheNonGlobalProject(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "secret", Name: "Secret", Visibility: "private"}, http.StatusCreated)
	g := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/global/cards", apitypes.CreateCard{Title: "org-wide", Status: "ready"}, http.StatusCreated)
	hidden := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/secret/cards", apitypes.CreateCard{Title: "hidden", Status: "ready"}, http.StatusCreated)
	from := e.latestPosition()
	// Linked from the global side, so the project of the acting card is not what decides.
	call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/global/cards/"+itoa(g.Number)+"/links",
		apitypes.LinkCards{Kind: "blocks", To: hidden.Number, Project: "secret"}, http.StatusOK)
	if rec := e.do(userSecret, http.MethodDelete, "/projects/global/cards/"+itoa(g.Number)+"/links/blocks/"+itoa(hidden.Number)+"?project=secret", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink = %d: %s", rec.Code, rec.Body)
	}

	types := func(secret, query string) map[string]string {
		out := map[string]string{}
		for _, ev := range call[apitypes.EventList](e, secret, http.MethodGet, "/events?after="+from+query, nil, http.StatusOK).Items {
			p := ""
			if ev.Project != nil {
				p = *ev.Project
			}
			out[ev.Type] = p
		}
		return out
	}
	owner := types(userSecret, "")
	for _, typ := range []domain.EventType{domain.EventCardLinked, domain.EventCardUnlinked, domain.EventCardStatusChanged} {
		if owner[typ.String()] != "secret" {
			t.Errorf("owner feed %v: %s in %q, want secret", owner, typ, owner[typ.String()])
		}
	}
	if agent := types(agentSecret, ""); len(agent) != 0 {
		t.Errorf("agent feed = %v, want nothing from the secret project", agent)
	}
	if global := types(userSecret, "&project=global"); len(global) != 0 {
		t.Errorf("global feed = %v, want the link events kept out of it", global)
	}
}

func TestSearchAndNoteListsNeverCountHiddenProjects(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "secret", Name: "Secret", Visibility: "private"}, http.StatusCreated)
	for _, slug := range []string{"s1", "s2", "s3"} {
		call[apitypes.Note](e, userSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "secret", Slug: slug, Title: "quartz " + slug, Body: "hidden"}, http.StatusCreated)
	}
	call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/secret/cards", apitypes.CreateCard{Title: "quartz hidden"}, http.StatusCreated)
	shown := call[apitypes.Note](e, userSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "lorvik", Slug: "open", Title: "quartz open", Body: "visible"}, http.StatusCreated)
	card := e.newCard(agentSecret, apitypes.CreateCard{Title: "quartz shown"})

	notes := call[apitypes.NoteList](e, agentSecret, http.MethodGet, "/notes?limit=1", nil, http.StatusOK)
	if len(notes.Items) != 1 || notes.Items[0].ID != shown.ID || notes.Remaining != 0 {
		t.Errorf("agent notes = %+v, want only the lorvik note with nothing remaining", notes)
	}
	hits := call[apitypes.SearchResult](e, agentSecret, http.MethodGet, "/search?q=quartz&limit=2", nil, http.StatusOK).Items
	if len(hits) != 2 {
		t.Fatalf("agent hits = %+v, want the lorvik note and card (hidden rows must not take the limit)", hits)
	}
	for _, h := range hits {
		if h.Project == nil || *h.Project != "lorvik" {
			t.Errorf("agent hit %+v, want lorvik only", h)
		}
		if h.Type == "card" && h.Number != card.Number {
			t.Errorf("agent card hit #%d, want #%d", h.Number, card.Number)
		}
	}
	if owner := call[apitypes.SearchResult](e, userSecret, http.MethodGet, "/search?q=quartz&limit=10", nil, http.StatusOK).Items; len(owner) != 6 {
		t.Errorf("owner hits = %d, want 6", len(owner))
	}
}
