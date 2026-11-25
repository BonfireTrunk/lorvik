package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

const (
	pageOne = "<!doctype html><title>One</title><p>first page</p>"
	pageTwo = "<!doctype html><title>Two</title><p>second page</p>"
)

func tooBigPage() string { return strings.Repeat("a", domain.MaxPageBytes+1) }

func TestCardPageCreateReplaceRemove(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.newCard(agentSecret, apitypes.CreateCard{Title: "with page", Page: pageOne})
	if c.PageBytes != len(pageOne) || c.Body != "" {
		t.Fatalf("created card page_bytes = %d, body = %q; want %d and an empty body", c.PageBytes, c.Body, len(pageOne))
	}
	got := call[apitypes.HTMLPage](e, agentSecret, http.MethodGet, cardPathOf(c.Number, "/page"), nil, http.StatusOK)
	if got.HTML != pageOne || got.Bytes != len(pageOne) || got.Version != c.Version {
		t.Errorf("page = %+v, want the created html at version %d", got, c.Version)
	}
	list := call[apitypes.CardList](e, agentSecret, http.MethodGet, "/projects/lorvik/cards", nil, http.StatusOK)
	if len(list.Items) != 1 || list.Items[0].PageBytes != len(pageOne) {
		t.Errorf("list items = %+v, want the brief to carry page_bytes", list.Items)
	}

	c = call[apitypes.Card](e, agentSecret, http.MethodPatch, cardPathOf(c.Number, ""), apitypes.UpdateCard{Version: c.Version, Page: new(pageTwo)}, http.StatusOK)
	if c.PageBytes != len(pageTwo) {
		t.Errorf("page_bytes after replace = %d, want %d", c.PageBytes, len(pageTwo))
	}
	got = call[apitypes.HTMLPage](e, agentSecret, http.MethodGet, cardPathOf(c.Number, "/page"), nil, http.StatusOK)
	if got.HTML != pageTwo || got.Version != c.Version {
		t.Errorf("page after replace = %+v, want the new html at version %d", got, c.Version)
	}

	c = call[apitypes.Card](e, agentSecret, http.MethodPatch, cardPathOf(c.Number, ""), apitypes.UpdateCard{Version: c.Version, Page: new("")}, http.StatusOK)
	if c.PageBytes != 0 {
		t.Errorf("page_bytes after removal = %d, want 0", c.PageBytes)
	}
	e.problem(agentSecret, http.MethodGet, cardPathOf(c.Number, "/page"), nil, http.StatusNotFound)
}

func TestCardPageRouteIs404WithoutAPageAndOnlyJSON(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	plain := e.newCard(agentSecret, apitypes.CreateCard{Title: "no page", Body: "text"})
	e.problem(agentSecret, http.MethodGet, cardPathOf(plain.Number, "/page"), nil, http.StatusNotFound)

	withPage := e.newCard(agentSecret, apitypes.CreateCard{Title: "page", Page: "<script>alert(1)</script>"})
	rec := e.do(agentSecret, http.MethodGet, cardPathOf(withPage.Number, "/page"), nil)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("page content type = %q, want JSON and never text/html", ct)
	}
}

func TestPageAboveTheLimitIsRejected(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	card := e.newCard(agentSecret, apitypes.CreateCard{Title: "c"})
	note := call[apitypes.Note](e, agentSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "lorvik", Slug: "n", Title: "n", Body: "b"}, http.StatusCreated)
	tests := []struct {
		name, method, path string
		body               any
	}{
		{"create card", http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Title: "x", Page: tooBigPage()}},
		{"update card", http.MethodPatch, cardPathOf(card.Number, ""), apitypes.UpdateCard{Version: card.Version, Page: new(tooBigPage())}},
		{"create note", http.MethodPost, "/notes", apitypes.CreateNote{Project: "lorvik", Slug: "big", Title: "x", Body: "b", Page: tooBigPage()}},
		{"update note", http.MethodPatch, "/notes/n?project=lorvik", apitypes.UpdateNote{Version: note.Version, Page: new(tooBigPage())}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := e.problem(agentSecret, tt.method, tt.path, tt.body, http.StatusUnprocessableEntity)
			if !hasField(p, "page") {
				t.Errorf("errors = %+v, want one on page", p.Errors)
			}
		})
	}
}

func TestPageOfInvalidUTF8IsRejectedByTheService(t *testing.T) {
	t.Parallel()
	// JSON decoding repairs invalid UTF-8 before the service sees it, so the
	// check is exercised on the validation the service calls.
	if err := domain.ValidatePage("<p>\xff</p>"); err == nil {
		t.Fatal("ValidatePage accepted invalid UTF-8")
	}
}

func TestFrozenDecisionPageCannotBeEdited(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	d := e.newCard(userSecret, apitypes.CreateCard{Kind: "decision", Title: "Use Go", Status: "done", Page: pageOne})
	p := e.problem(userSecret, http.MethodPatch, cardPathOf(d.Number, ""), apitypes.UpdateCard{Version: d.Version, Page: new(pageTwo)}, http.StatusConflict)
	if p.Hint != "accepted text is frozen; record a superseding decision" {
		t.Errorf("hint = %q", p.Hint)
	}
	got := call[apitypes.HTMLPage](e, userSecret, http.MethodGet, cardPathOf(d.Number, "/page"), nil, http.StatusOK)
	if got.HTML != pageOne {
		t.Errorf("page = %q, want it unchanged", got.HTML)
	}
}

func TestPageEditsKeepRevisionsWithTheOldPage(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.newCard(agentSecret, apitypes.CreateCard{Title: "t", Body: "b"})
	// Adding a page to a card that had none is a text change; version 1 had no page.
	c = call[apitypes.Card](e, agentSecret, http.MethodPatch, cardPathOf(c.Number, ""), apitypes.UpdateCard{Version: c.Version, Page: new(pageOne)}, http.StatusOK)
	// A page-only replacement snapshots the old page.
	c = call[apitypes.Card](e, agentSecret, http.MethodPatch, cardPathOf(c.Number, ""), apitypes.UpdateCard{Version: c.Version, Page: new(pageTwo)}, http.StatusOK)
	// The same page again changes nothing and keeps no revision.
	call[apitypes.Card](e, agentSecret, http.MethodPatch, cardPathOf(c.Number, ""), apitypes.UpdateCard{Version: c.Version, Page: new(pageTwo)}, http.StatusOK)

	revs := call[[]apitypes.CardRevision](e, agentSecret, http.MethodGet, cardPathOf(c.Number, "/revisions"), nil, http.StatusOK)
	if len(revs) != 2 || revs[0].Version != 2 || revs[1].Version != 1 {
		t.Fatalf("revisions = %+v, want versions 2 then 1", revs)
	}
	if revs[0].Page != "" || revs[1].Page != "" {
		t.Errorf("revision list carries pages: %+v", revs)
	}
	if r := call[apitypes.CardRevision](e, agentSecret, http.MethodGet, cardPathOf(c.Number, "/revisions/2"), nil, http.StatusOK); r.Page != pageOne || r.Body != "b" {
		t.Errorf("revision 2 = %+v, want the first page and the body", r)
	}
	if r := call[apitypes.CardRevision](e, agentSecret, http.MethodGet, cardPathOf(c.Number, "/revisions/1"), nil, http.StatusOK); r.Page != "" {
		t.Errorf("revision 1 page = %q, want none", r.Page)
	}
}

func TestCardUpdatedEventNamesPageOnlyWhenItChanged(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.newCard(agentSecret, apitypes.CreateCard{Title: "t", Page: pageOne})
	tests := []struct {
		name string
		in   func(version int64) apitypes.UpdateCard
		want []string
	}{
		{"page and title", func(v int64) apitypes.UpdateCard {
			return apitypes.UpdateCard{Version: v, Title: new("t2"), Page: new(pageTwo)}
		}, []string{"title", "page"}},
		{"same page", func(v int64) apitypes.UpdateCard {
			return apitypes.UpdateCard{Version: v, Page: new(pageTwo), Priority: new("high")}
		}, []string{"priority"}},
		{"page removed", func(v int64) apitypes.UpdateCard { return apitypes.UpdateCard{Version: v, Page: new("")} }, []string{"page"}},
	}
	for _, tt := range tests {
		c = call[apitypes.Card](e, agentSecret, http.MethodPatch, cardPathOf(c.Number, ""), tt.in(c.Version), http.StatusOK)
		var pl struct {
			Fields []string `json:"fields"`
		}
		if err := json.Unmarshal(e.lastEvent().Payload, &pl); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(pl.Fields, tt.want) {
			t.Errorf("%s: fields = %v, want %v", tt.name, pl.Fields, tt.want)
		}
	}
}

func TestDecisionFromAnswerCopiesThePage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, body, wantBody string
	}{
		{"empty body reads see the page", "", "## Context\n\nSee the page.\n\n## Decision\n\nPostgres\n"},
		{"body is kept as the context", "Need multi-writer.", "## Context\n\nNeed multi-writer.\n\n## Decision\n\nPostgres\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "Postgres or SQLite?", Body: tt.body, Page: pageOne})
			call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: q.Version, Answer: "Postgres", Decide: true}, http.StatusOK)
			d := e.show(agentSecret, 2)
			if d.Kind != "decision" || d.Body != tt.wantBody || d.PageBytes != len(pageOne) {
				t.Errorf("decision = kind %q body %q page_bytes %d, want body %q and page_bytes %d", d.Kind, d.Body, d.PageBytes, tt.wantBody, len(pageOne))
			}
			if got := call[apitypes.HTMLPage](e, agentSecret, http.MethodGet, cardPathOf(d.Number, "/page"), nil, http.StatusOK); got.HTML != pageOne {
				t.Errorf("decision page = %q, want the question's", got.HTML)
			}
		})
	}
}

func TestNotePageNeedsBodyOrPage(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	tests := []struct {
		name string
		in   apitypes.CreateNote
		want int
	}{
		{"body only", apitypes.CreateNote{Project: "lorvik", Slug: "a", Title: "a", Body: "text"}, http.StatusCreated},
		{"page only", apitypes.CreateNote{Project: "lorvik", Slug: "b", Title: "b", Page: pageOne}, http.StatusCreated},
		{"neither", apitypes.CreateNote{Project: "lorvik", Slug: "c", Title: "c"}, http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		rec := e.do(agentSecret, http.MethodPost, "/notes", tt.in)
		if rec.Code != tt.want {
			t.Errorf("%s: status = %d, want %d: %s", tt.name, rec.Code, tt.want, rec.Body)
		}
	}
}

func TestNotePageLifecycleAndRevisions(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	n := call[apitypes.Note](e, agentSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "lorvik", Slug: "pg", Title: "pg", Page: pageOne}, http.StatusCreated)
	if n.PageBytes != len(pageOne) {
		t.Fatalf("page_bytes = %d, want %d", n.PageBytes, len(pageOne))
	}
	got := call[apitypes.HTMLPage](e, agentSecret, http.MethodGet, "/notes/pg/page?project=lorvik", nil, http.StatusOK)
	if got.HTML != pageOne || got.Version != n.Version {
		t.Errorf("page = %+v, want the html at version %d", got, n.Version)
	}

	n = call[apitypes.Note](e, agentSecret, http.MethodPatch, "/notes/pg?project=lorvik", apitypes.UpdateNote{Version: n.Version, Page: new(pageTwo)}, http.StatusOK)
	if n.PageBytes != len(pageTwo) {
		t.Errorf("page_bytes after replace = %d, want %d", n.PageBytes, len(pageTwo))
	}
	rev := call[apitypes.NoteRevision](e, agentSecret, http.MethodGet, "/notes/pg/revisions/1?project=lorvik", nil, http.StatusOK)
	if rev.Page != pageOne {
		t.Errorf("revision page = %q, want the page before the edit", rev.Page)
	}
	revs := call[[]apitypes.NoteRevision](e, agentSecret, http.MethodGet, "/notes/pg/revisions?project=lorvik", nil, http.StatusOK)
	if len(revs) != 1 || revs[0].Page != "" {
		t.Errorf("revision list = %+v, want no page in a list", revs)
	}

	// A page-only note cannot lose its page: it would be left with nothing.
	e.problem(agentSecret, http.MethodPatch, "/notes/pg?project=lorvik", apitypes.UpdateNote{Version: n.Version, Page: new("")}, http.StatusUnprocessableEntity)

	n = call[apitypes.Note](e, agentSecret, http.MethodPatch, "/notes/pg?project=lorvik", apitypes.UpdateNote{Version: n.Version, Body: new("now text"), Page: new("")}, http.StatusOK)
	if n.PageBytes != 0 {
		t.Errorf("page_bytes after removal = %d, want 0", n.PageBytes)
	}
	e.problem(agentSecret, http.MethodGet, "/notes/pg/page?project=lorvik", nil, http.StatusNotFound)
}

// doRaw sends body as is; e.do marshals with HTML escaping, which is the very
// thing the routes' size cap must not depend on.
func (e *testEnv) doRaw(secret, method, path string, body []byte) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, "/api/v1"+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func jsonNoEscape(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPageRoutesTakeALargerBodyThanOtherRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	full := strings.Repeat("<", domain.MaxPageBytes)
	tests := []struct {
		name, path string
		body       any
		want       int
	}{
		{"1 MiB of angle brackets on create card", "/projects/lorvik/cards", apitypes.CreateCard{Title: "big", Page: full}, http.StatusCreated},
		{"1 MiB of angle brackets on create note", "/notes", apitypes.CreateNote{Project: "lorvik", Slug: "big", Title: "big", Page: full}, http.StatusCreated},
		{"a 4 MiB body is refused", "/projects/lorvik/cards", apitypes.CreateCard{Title: "huge", Page: strings.Repeat("a", 4<<20)}, http.StatusRequestEntityTooLarge},
		{"other routes keep the 1 MiB cap", "/projects/lorvik/cards/1/comments", apitypes.CreateComment{Body: strings.Repeat("a", 1<<20+1)}, http.StatusRequestEntityTooLarge},
	}
	e.readyCard(agentSecret, "first")
	for _, tt := range tests {
		rec := e.doRaw(agentSecret, http.MethodPost, tt.path, jsonNoEscape(t, tt.body))
		if rec.Code != tt.want {
			t.Errorf("%s: status = %d, want %d: %.200s", tt.name, rec.Code, tt.want, rec.Body)
		}
	}
}
