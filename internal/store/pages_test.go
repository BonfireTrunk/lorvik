//go:build integration

package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

const (
	zeppelinPage = "<html><head><title>Airship</title></head><body><p>The zeppelin docks at dawn.</p></body></html>"
	harborPage   = "<p>The harbor opens at noon.</p>"
)

// putCardPage stores html on c the way the service does: the card's
// page_bytes through UpdateCard, the page through PutCardPage.
func (e *env) putCardPage(t *testing.T, c domain.Card, html string) domain.Card {
	t.Helper()
	c.PageBytes = len(html)
	updated, err := e.st.UpdateCard(t.Context(), c, c.Version)
	if err != nil {
		t.Fatalf("update card: %v", err)
	}
	if err := e.st.PutCardPage(t.Context(), c.ID, html, domain.PageText(html)); err != nil {
		t.Fatalf("put card page: %v", err)
	}
	return updated
}

func TestCardPageIsStoredReplacedAndRemoved(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	c := e.card(t, func(c *domain.Card) { c.PageBytes = len(zeppelinPage) })
	if got, _ := e.st.GetCard(ctx, c.ID); got.PageBytes != len(zeppelinPage) {
		t.Errorf("page_bytes on insert = %d, want %d", got.PageBytes, len(zeppelinPage))
	}
	if _, err := e.st.GetCardPage(ctx, c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetCardPage before any write = %v, want ErrNotFound", err)
	}
	if err := e.st.PutCardPage(ctx, c.ID, zeppelinPage, domain.PageText(zeppelinPage)); err != nil {
		t.Fatal(err)
	}
	if err := e.st.PutCardPage(ctx, c.ID, harborPage, domain.PageText(harborPage)); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if got, err := e.st.GetCardPage(ctx, c.ID); err != nil || got != harborPage {
		t.Errorf("page = %q, %v; want the replacement", got, err)
	}
	if err := e.st.DeleteCardPage(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.GetCardPage(ctx, c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetCardPage after delete = %v, want ErrNotFound", err)
	}
	if err := e.st.DeleteCardPage(ctx, c.ID); err != nil {
		t.Errorf("deleting a missing page = %v, want nil", err)
	}
}

// editCard mimics the service: a text edit snapshots first, keeping the old
// page only when page is non-nil, then updates, then writes the new page.
func (e *env) editCard(t *testing.T, c domain.Card, body string, page *string) domain.Card {
	t.Helper()
	ctx := t.Context()
	if err := e.st.SnapshotCardRevision(ctx, c.ID, c.Version, gen.New(), e.user, t0, page != nil); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	c.Body = body
	if page != nil {
		c.PageBytes = len(*page)
	}
	updated, err := e.st.UpdateCard(ctx, c, c.Version)
	if err != nil {
		t.Fatalf("update card: %v", err)
	}
	switch {
	case page == nil:
	case *page == "":
		err = e.st.DeleteCardPage(ctx, c.ID)
	default:
		err = e.st.PutCardPage(ctx, c.ID, *page, domain.PageText(*page))
	}
	if err != nil {
		t.Fatalf("write page: %v", err)
	}
	return updated
}

func (e *env) editNote(t *testing.T, n domain.Note, body string, status domain.RecordStatus, page *string) domain.Note {
	t.Helper()
	ctx := t.Context()
	n.Body, n.Status, n.UpdatedAt = body, status, n.UpdatedAt.Add(time.Second)
	if page != nil {
		n.PageBytes = len(*page)
	}
	updated, err := e.st.UpdateNote(ctx, n, n.Version, gen.New(), page != nil)
	if err != nil {
		t.Fatalf("update note: %v", err)
	}
	switch {
	case page == nil:
	case *page == "":
		err = e.st.DeleteNotePage(ctx, n.ID)
	default:
		err = e.st.PutNotePage(ctx, n.ID, *page, domain.PageText(*page))
	}
	if err != nil {
		t.Fatalf("write page: %v", err)
	}
	return updated
}

// storedRevisionPage reads the raw page column: nil is NULL.
func (e *env) storedRevisionPage(t *testing.T, table, idCol string, id domain.ID, version int64) *string {
	t.Helper()
	var p *string
	sql := "SELECT page FROM " + table + " WHERE " + idCol + " = $1 AND version = $2"
	if err := e.st.db.QueryRow(t.Context(), sql, int64(id), version).Scan(&p); err != nil {
		t.Fatalf("read stored page of %s v%d: %v", table, version, err)
	}
	return p
}

func eqPage(got *string, want *string) bool {
	return (got == nil) == (want == nil) && (got == nil || *got == *want)
}

func TestCardRevisionStoresAPageOnlyWhenTheEditChangesIt(t *testing.T) {
	e := newEnv(t)
	c := e.card(t, nil)
	null, empty, pa, pb := (*string)(nil), new(""), new(zeppelinPage), new(harborPage)
	c = e.editCard(t, c, "b2", nil) // v1 -> v2, body only, no page
	c = e.editCard(t, c, "b3", pa)  // v2 -> v3, page added: v2 stores "" (had none)
	c = e.editCard(t, c, "b4", nil) // v3 -> v4, body only
	c = e.editCard(t, c, "b5", pb)  // v4 -> v5, page changed: v4 stores A
	for v, want := range map[int64]*string{1: null, 2: empty, 3: null, 4: pa} {
		if got := e.storedRevisionPage(t, "card_revisions", "card_id", c.ID, v); !eqPage(got, want) {
			t.Errorf("stored page of revision %d = %v, want %v", v, deref2(got), deref2(want))
		}
	}
}

func deref2(p *string) string {
	if p == nil {
		return "<NULL>"
	}
	return "\"" + *p + "\""
}

func TestCardRevisionReadResolvesThePageAtThatVersion(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	c := e.card(t, func(c *domain.Card) { c.PageBytes = len(zeppelinPage) })
	if err := e.st.PutCardPage(ctx, c.ID, zeppelinPage, domain.PageText(zeppelinPage)); err != nil {
		t.Fatal(err)
	}
	c = e.editCard(t, c, "b2", nil)               // v1 -> v2
	c = e.editCard(t, c, "b3", new(harborPage))   // v2 -> v3: page B
	c = e.editCard(t, c, "b4", nil)               // v3 -> v4
	c = e.editCard(t, c, "b5", new(""))           // v4 -> v5: page removed
	c = e.editCard(t, c, "b6", nil)               // v5 -> v6
	c = e.editCard(t, c, "b7", new(zeppelinPage)) // v6 -> v7: page re-added
	c = e.editCard(t, c, "b8", nil)               // v7 -> v8, current page A
	want := map[int64]string{1: zeppelinPage, 2: zeppelinPage, 3: harborPage, 4: harborPage, 5: "", 6: "", 7: zeppelinPage}
	for v, w := range want {
		r, err := e.st.GetCardRevision(ctx, c.ID, v)
		if err != nil || r.Page != w {
			t.Errorf("revision %d page = %q, %v; want %q", v, r.Page, err, w)
		}
	}
	// With no later kept page and no current page, the page is empty.
	c = e.editCard(t, c, "b9", new(""))
	if r, err := e.st.GetCardRevision(ctx, c.ID, 8); err != nil || r.Page != zeppelinPage {
		t.Errorf("revision 8 page = %q, %v; want the page it had", r.Page, err)
	}
	c = e.editCard(t, c, "b10", nil)
	if r, err := e.st.GetCardRevision(ctx, c.ID, 10); err == nil {
		t.Errorf("revision 10 = %+v, want not found (current version)", r)
	}
	if r, err := e.st.GetCardRevision(ctx, c.ID, 9); err != nil || r.Page != "" {
		t.Errorf("revision 9 page = %q, %v; want none (current has none)", r.Page, err)
	}
}

func TestNoteRevisionStoresAPageOnlyWhenTheEditChangesIt(t *testing.T) {
	e := newEnv(t)
	n := e.note(t, e.project.ID, "pg", "")
	null, empty, pa := (*string)(nil), new(""), new(zeppelinPage)
	n = e.editNote(t, n, "b2", domain.RecordActive, nil)             // v1 -> v2
	n = e.editNote(t, n, "b2", domain.RecordArchived, nil)           // v2 -> v3 archive toggle
	n = e.editNote(t, n, "b2", domain.RecordActive, pa)              // v3 -> v4 page added
	n = e.editNote(t, n, "b2", domain.RecordArchived, nil)           // v4 -> v5 archive toggle with a page
	n = e.editNote(t, n, "b2", domain.RecordActive, new(harborPage)) // v5 -> v6 page changed
	for v, want := range map[int64]*string{1: null, 2: null, 3: empty, 4: null, 5: pa} {
		if got := e.storedRevisionPage(t, "note_revisions", "note_id", n.ID, v); !eqPage(got, want) {
			t.Errorf("stored page of revision %d = %v, want %v", v, deref2(got), deref2(want))
		}
	}
}

func TestNoteRevisionReadResolvesThePageAtThatVersion(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	n := e.note(t, e.project.ID, "pg", "")
	n = e.editNote(t, n, "b", domain.RecordActive, new(zeppelinPage)) // v1 -> v2
	n = e.editNote(t, n, "b", domain.RecordArchived, nil)             // v2 -> v3
	n = e.editNote(t, n, "b", domain.RecordActive, new(harborPage))   // v3 -> v4
	n = e.editNote(t, n, "b", domain.RecordArchived, nil)             // v4 -> v5
	n = e.editNote(t, n, "b", domain.RecordActive, new(""))           // v5 -> v6 removed
	n = e.editNote(t, n, "b", domain.RecordArchived, nil)             // v6 -> v7
	for v, w := range map[int64]string{1: "", 2: zeppelinPage, 3: zeppelinPage, 4: harborPage, 5: harborPage, 6: ""} {
		if r, err := e.st.GetNoteRevision(ctx, n.ID, v); err != nil || r.Page != w {
			t.Errorf("revision %d page = %q, %v; want %q", v, r.Page, err, w)
		}
	}
}

func TestNotePageIsStoredReplacedAndRemoved(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	n := e.note(t, e.project.ID, "pg", "")
	if _, err := e.st.GetNotePage(ctx, n.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetNotePage before any write = %v, want ErrNotFound", err)
	}
	// Edit 1 adds a page to a note that had none.
	n.PageBytes, n.UpdatedAt = len(zeppelinPage), t0.Add(1)
	n, err := e.st.UpdateNote(ctx, n, n.Version, gen.New(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.PutNotePage(ctx, n.ID, zeppelinPage, domain.PageText(zeppelinPage)); err != nil {
		t.Fatal(err)
	}
	// Edit 2 replaces it.
	n.PageBytes, n.UpdatedAt = len(harborPage), t0.Add(2)
	n, err = e.st.UpdateNote(ctx, n, n.Version, gen.New(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.PutNotePage(ctx, n.ID, harborPage, domain.PageText(harborPage)); err != nil {
		t.Fatal(err)
	}

	if got, err := e.st.GetNotePage(ctx, n.ID); err != nil || got != harborPage {
		t.Errorf("page = %q, %v; want the replacement", got, err)
	}
	if err := e.st.DeleteNotePage(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.GetNotePage(ctx, n.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetNotePage after delete = %v, want ErrNotFound", err)
	}
}

func TestSearchFindsCardsAndNotesByTextOnlyTheirPageHas(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	c := e.card(t, func(c *domain.Card) { c.Title = "Dock schedule" })
	e.putCardPage(t, c, zeppelinPage)
	other := e.card(t, func(c *domain.Card) { c.Title = "Unrelated" })
	e.putCardPage(t, other, harborPage)
	n := e.note(t, e.project.ID, "airfield", "Where the machines land.")
	n.PageBytes, n.UpdatedAt = len(zeppelinPage), t0.Add(1)
	if _, err := e.st.UpdateNote(ctx, n, n.Version, gen.New(), true); err != nil {
		t.Fatal(err)
	}
	if err := e.st.PutNotePage(ctx, n.ID, zeppelinPage, domain.PageText(zeppelinPage)); err != nil {
		t.Fatal(err)
	}

	hits, _, err := e.st.Search(ctx, e.org.ID, nil, "zeppelin", 10, nil)
	if err != nil || len(hits) != 2 {
		t.Fatalf("hits = %+v, %v; want the card and the note", hits, err)
	}
	for _, h := range hits {
		if h.Rank <= 0 || !strings.Contains(strings.ToLower(h.Snippet), "zeppelin") {
			t.Errorf("hit %s %s has rank %v snippet %q, want a snippet from the page text", h.Type, h.ID, h.Rank, h.Snippet)
		}
	}
	// Attribute text is searchable: the page title is in the visible text.
	if hits, _, err := e.st.Search(ctx, e.org.ID, nil, "airship", 10, nil); err != nil || len(hits) != 2 {
		t.Errorf("hits for the page title = %d, %v; want 2", len(hits), err)
	}

	cards, _, err := e.st.ListCards(ctx, e.project.ID, query.CardFilter{Query: "zeppelin", Page: query.Page{Limit: 10}})
	if err != nil || len(cards) != 1 || cards[0].ID != c.ID || cards[0].PageBytes != len(zeppelinPage) {
		t.Errorf("ListCards by page text = %+v, %v; want only the card with that page", cards, err)
	}
	notes, _, err := e.st.ListNotes(ctx, e.org.ID, query.NoteFilter{Query: "zeppelin", Page: query.Page{Limit: 10}})
	if err != nil || len(notes) != 1 || notes[0].ID != n.ID {
		t.Errorf("ListNotes by page text = %+v, %v; want the note", notes, err)
	}
	if rank, err := e.st.SearchRank(ctx, domain.SubjectCard, c.ID, "zeppelin"); err != nil || rank <= 0 {
		t.Errorf("SearchRank = %v, %v; want a positive rank from the page", rank, err)
	}

	// Removing the page removes the match.
	if err := e.st.DeleteCardPage(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if hits, _, err := e.st.Search(ctx, e.org.ID, nil, "zeppelin", 10, nil); err != nil || len(hits) != 1 {
		t.Errorf("hits after the card's page is removed = %d, %v; want only the note", len(hits), err)
	}
}
