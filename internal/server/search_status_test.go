package server

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func TestSearchHitsCarryTheStatusAndKindOfCards(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(agentSecret, "Rotate quartz keys")
	e.claim(agentSecret, c.Number)
	call[apitypes.Card](e, agentSecret, http.MethodPost, cardPathOf(c.Number, "/transition"),
		apitypes.TransitionCard{Version: e.show(agentSecret, c.Number).Version, Status: "done"}, http.StatusOK)
	d := e.newCard(userSecret, apitypes.CreateCard{Kind: "decision", Title: "Quartz keys live in the vault", Status: "done"})
	call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(d.Number, "/transition"),
		apitypes.TransitionCard{Version: d.Version, Status: "dropped", DropReason: "revoked"}, http.StatusOK)

	got := call[apitypes.SearchResult](e, userSecret, http.MethodGet, "/search?q=quartz", nil, http.StatusOK)
	type seen struct{ kind, status string }
	hits := map[int64]seen{}
	for _, h := range got.Items {
		if h.Type != "card" {
			t.Errorf("hit %+v, want only cards", h)
		}
		hits[h.Number] = seen{h.Kind, h.Status}
	}
	if len(got.Items) != 2 || hits[c.Number] != (seen{"task", "done"}) || hits[d.Number] != (seen{"decision", "dropped"}) {
		t.Errorf("hits = %v from %+v, want the task done and the decision dropped, each with its kind", hits, got.Items)
	}
}

func TestSearchPagesWithCursor(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, title := range []string{"feldspar one", "feldspar two", "feldspar three"} {
		e.newCard(userSecret, apitypes.CreateCard{Title: title})
	}
	first := call[apitypes.SearchResult](e, userSecret, http.MethodGet, "/search?q=feldspar&limit=2", nil, http.StatusOK)
	if len(first.Items) != 2 || first.Remaining != 1 || first.Next == "" || first.Prev != "" {
		t.Fatalf("first page = %d items, remaining %d, next %q, prev %q; want 2, 1 left, a next and no prev", len(first.Items), first.Remaining, first.Next, first.Prev)
	}
	rest := call[apitypes.SearchResult](e, userSecret, http.MethodGet, "/search?q=feldspar&limit=2&cursor="+first.Next, nil, http.StatusOK)
	if len(rest.Items) != 1 || rest.Remaining != 0 {
		t.Fatalf("second page = %d items, remaining %d; want the last one", len(rest.Items), rest.Remaining)
	}
	for _, h := range first.Items {
		if h.ID == rest.Items[0].ID {
			t.Errorf("hit %s came back on the second page", h.ID)
		}
	}
	e.problem(userSecret, http.MethodGet, "/search?q=feldspar&cursor=not-a-cursor", nil, http.StatusBadRequest)
}

func TestFullTextListsContinueWithCursor(t *testing.T) {
	t.Parallel()
	type list struct {
		next      string
		remaining int
		prev      string
		ids       []string
	}
	cards := func(e *testEnv, path string) list {
		l := call[apitypes.CardList](e, userSecret, http.MethodGet, path, nil, http.StatusOK)
		out := list{next: l.Next, remaining: l.Remaining, prev: l.Prev}
		for _, c := range l.Items {
			out.ids = append(out.ids, c.ID)
		}
		return out
	}
	tests := []struct {
		name, path string
		seed       func(e *testEnv, title string)
		read       func(e *testEnv, path string) list
	}{
		{"project cards", "/projects/lorvik/cards", func(e *testEnv, title string) { e.newCard(userSecret, apitypes.CreateCard{Title: title}) }, cards},
		{"all cards", "/cards", func(e *testEnv, title string) { e.newCard(userSecret, apitypes.CreateCard{Title: title}) }, cards},
		{"notes", "/notes", func(e *testEnv, title string) {
			call[apitypes.Note](e, userSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "lorvik", Slug: strings.ReplaceAll(title, " ", "-"), Title: title, Body: title}, http.StatusCreated)
		}, func(e *testEnv, path string) list {
			l := call[apitypes.NoteList](e, userSecret, http.MethodGet, path, nil, http.StatusOK)
			out := list{next: l.Next, remaining: l.Remaining, prev: l.Prev}
			for _, n := range l.Items {
				out.ids = append(out.ids, n.ID)
			}
			return out
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			for _, title := range []string{"feldspar one", "feldspar two", "feldspar three"} {
				tc.seed(e, title)
			}
			first := tc.read(e, tc.path+"?q=feldspar&limit=2")
			if len(first.ids) != 2 || first.remaining != 1 || first.next == "" || first.prev != "" {
				t.Fatalf("first page = %+v; want 2 rows, 1 left, a next and no prev", first)
			}
			rest := tc.read(e, tc.path+"?q=feldspar&limit=2&cursor="+first.next)
			if len(rest.ids) != 1 || rest.remaining != 0 || rest.next != "" || slices.Contains(first.ids, rest.ids[0]) {
				t.Errorf("second page = %+v after %v; want the one other row and no next", rest, first.ids)
			}
			plain := tc.read(e, tc.path+"?limit=1")
			e.problem(userSecret, http.MethodGet, tc.path+"?q=feldspar&cursor="+plain.next, nil, http.StatusBadRequest)
			e.problem(userSecret, http.MethodGet, tc.path+"?cursor="+first.next, nil, http.StatusBadRequest)
		})
	}
}
