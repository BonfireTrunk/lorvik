//go:build integration

package store

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// decision creates a decision card in the env's project: ready when
// proposed, done with an acceptor when accepted, dropped with the given
// reason otherwise.
func (e *env) decision(t *testing.T, title string, status domain.Status, reason domain.DropReason) domain.Card {
	t.Helper()
	return e.card(t, func(c *domain.Card) {
		c.Kind = domain.KindDecision
		c.Title = title
		c.Status = status
		c.DropReason = reason
		c.CreatedBy = e.agent
		if status != domain.StatusReady {
			closed := t0
			c.ClosedAt = &closed
			c.Acceptance = &domain.Acceptance{By: e.user, At: t0}
		}
	})
}

func (e *env) note(t *testing.T, projectID domain.ID, slug, body string) domain.Note {
	t.Helper()
	n, err := e.st.CreateNote(t.Context(), domain.Note{ID: gen.New(), OrgID: e.org.ID, ProjectID: projectID, Status: domain.RecordActive, Slug: slug,
		Title: slug, Body: body, CreatedBy: e.user, UpdatedBy: e.user, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		t.Fatalf("create note %s: %v", slug, err)
	}
	return n
}

func TestNotesResolveBySlugPerProjectAndKeepRevisions(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	proj := e.note(t, e.project.ID, "arch", "project body")
	glob := e.note(t, e.global.ID, "arch", "global body")
	got, err := e.st.GetNoteBySlug(ctx, e.project.ID, "arch")
	if err != nil || got.ID != proj.ID {
		t.Errorf("project arch = %v, %v", got.ID, err)
	}
	got, err = e.st.GetNoteBySlug(ctx, e.global.ID, "arch")
	if err != nil || got.ID != glob.ID {
		t.Errorf("global arch = %v, %v", got.ID, err)
	}
	proj.Body, proj.UpdatedBy, proj.UpdatedAt = "edited", e.agent, t0.Add(time.Hour)
	rev := gen.New()
	updated, err := e.st.UpdateNote(ctx, proj, 1, rev, false)
	if err != nil || updated.Version != 2 || updated.Body != "edited" || updated.UpdatedBy != e.agent {
		t.Fatalf("updated = %+v, %v", updated, err)
	}
	if _, err := e.st.UpdateNote(ctx, proj, 1, gen.New(), false); !errors.Is(err, domain.ErrVersionConflict) {
		t.Errorf("stale update err = %v, want ErrVersionConflict", err)
	}
	revs, err := e.st.ListNoteRevisions(ctx, proj.ID, query.HistoryPage{})
	if err != nil || len(revs) != 1 || revs[0].ID != rev || revs[0].Version != 1 || revs[0].Title != "arch" || revs[0].Body != "" || revs[0].UpdatedBy != e.user {
		t.Errorf("revisions = %+v, %v; want version 1 with its title and no body", revs, err)
	}
	r, err := e.st.GetNoteRevision(ctx, proj.ID, 1)
	if err != nil || r.Body != "project body" {
		t.Errorf("revision 1 = %+v, %v", r, err)
	}
	if _, err := e.st.GetNoteRevision(ctx, proj.ID, 2); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing revision err = %v", err)
	}
}

func TestListNotesPagesByUpdatedAtAndHidesArchived(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	old := e.note(t, e.project.ID, "old", "x")
	mid := e.note(t, e.project.ID, "mid", "x")
	mid.UpdatedAt = t0.Add(time.Minute)
	if _, err := e.st.UpdateNote(ctx, mid, 1, gen.New(), false); err != nil {
		t.Fatal(err)
	}
	newest := e.note(t, e.global.ID, "newest", "x")
	newest.UpdatedAt = t0.Add(2 * time.Minute)
	if _, err := e.st.UpdateNote(ctx, newest, 1, gen.New(), false); err != nil {
		t.Fatal(err)
	}
	archived := e.note(t, e.project.ID, "gone", "x")
	archived.Status = domain.RecordArchived
	if _, err := e.st.UpdateNote(ctx, archived, 1, gen.New(), false); err != nil {
		t.Fatal(err)
	}
	p1, remaining, err := e.st.ListNotes(ctx, e.org.ID, query.NoteFilter{Page: query.Page{Limit: 2}})
	if err != nil || len(p1) != 2 || p1[0].ID != newest.ID || p1[1].ID != mid.ID || remaining != 1 {
		t.Fatalf("page 1 = %d remaining %d, %v", len(p1), remaining, err)
	}
	cur := &query.NoteCursor{UpdatedAt: p1[1].UpdatedAt, ID: p1[1].ID}
	p2, remaining, err := e.st.ListNotes(ctx, e.org.ID, query.NoteFilter{Page: query.Page{Limit: 2}, Cursor: cur})
	if err != nil || len(p2) != 1 || p2[0].ID != old.ID || remaining != 0 {
		t.Fatalf("page 2 = %d remaining %d, %v", len(p2), remaining, err)
	}
	withArchived, _, err := e.st.ListNotes(ctx, e.org.ID, query.NoteFilter{ProjectID: &e.project.ID, Archived: true, Page: query.Page{Limit: 10}})
	if err != nil || len(withArchived) != 3 {
		t.Errorf("project notes with archived = %d, %v; want 3", len(withArchived), err)
	}
	inGlobal, _, err := e.st.ListNotes(ctx, e.org.ID, query.NoteFilter{ProjectID: &e.global.ID, Page: query.Page{Limit: 10}})
	if err != nil || len(inGlobal) != 1 || inGlobal[0].ID != newest.ID {
		t.Errorf("global notes = %d, %v", len(inGlobal), err)
	}
}

func TestRefsReplaceAndBacklink(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	src := e.note(t, e.project.ID, "src", "x")
	c := e.card(t, nil)
	n2 := e.note(t, e.project.ID, "other", "y")
	links := []domain.RefLink{
		{SourceType: domain.SubjectNote, SourceID: src.ID, TargetType: domain.SubjectCard, TargetID: c.ID},
		{SourceType: domain.SubjectNote, SourceID: src.ID, TargetType: domain.SubjectNote, TargetID: n2.ID},
	}
	if err := e.st.ReplaceRefs(ctx, src.ID, links); err != nil {
		t.Fatal(err)
	}
	got, err := e.st.Backlinks(ctx, c.ID)
	if err != nil || len(got) != 1 || got[0].SourceID != src.ID || got[0].SourceType != domain.SubjectNote {
		t.Errorf("backlinks of card = %+v, %v", got, err)
	}
	if err := e.st.ReplaceRefs(ctx, src.ID, links[1:]); err != nil {
		t.Fatal(err)
	}
	if got, err := e.st.Backlinks(ctx, c.ID); err != nil || len(got) != 0 {
		t.Errorf("backlinks after replace = %+v, %v; want none", got, err)
	}
	if got, err := e.st.Backlinks(ctx, n2.ID); err != nil || len(got) != 1 {
		t.Errorf("note backlinks = %+v, %v; want one", got, err)
	}
	bad := []domain.RefLink{{SourceType: domain.SubjectNote, SourceID: c.ID, TargetType: domain.SubjectCard, TargetID: n2.ID}}
	if err := e.st.ReplaceRefs(ctx, src.ID, bad); err == nil {
		t.Error("ReplaceRefs with a foreign source id = nil, want an error")
	}
}

func TestSearchRanksAcrossEntities(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	c := e.card(t, func(c *domain.Card) { c.Title = "Rotate signing keys"; c.Body = "The keys expire yearly." })
	e.decision(t, "Signing keys live in the vault", domain.StatusDone, domain.DropNone)
	e.note(t, e.global.ID, "keys", "How we rotate signing keys.")
	e.card(t, func(c *domain.Card) { c.Title = "Unrelated" })
	hits, _, err := e.st.Search(ctx, e.org.ID, nil, "signing keys", 10, nil)
	if err != nil || len(hits) != 3 {
		t.Fatalf("hits = %+v, %v; want 3", hits, err)
	}
	for _, h := range hits {
		if h.Snippet == "" || h.Rank <= 0 {
			t.Errorf("hit %s %s has snippet %q rank %v", h.Type, h.ID, h.Snippet, h.Rank)
		}
	}
	scoped, _, err := e.st.Search(ctx, e.org.ID, []domain.ID{e.project.ID}, "signing keys", 10, nil)
	if err != nil || len(scoped) != 2 {
		t.Errorf("project-scoped hits = %d, %v; want 2 (global note excluded)", len(scoped), err)
	}
	none, _, err := e.st.Search(ctx, e.org.ID, []domain.ID{}, "signing keys", 10, nil)
	if err != nil || len(none) != 0 {
		t.Errorf("empty scope hits = %d, %v; want none (no readable project is not every project)", len(none), err)
	}
	one, _, err := e.st.Search(ctx, e.org.ID, nil, "expire yearly", 10, nil)
	if err != nil || len(one) != 1 || one[0].ID != c.ID || one[0].Number != c.Number {
		t.Errorf("body hit = %+v, %v", one, err)
	}
}

func TestSearchHitsCarryStatusAndKindForCardsButNotNotes(t *testing.T) {
	e := newEnv(t)
	e.card(t, func(c *domain.Card) { c.Title = "Rotate quartz keys"; c.Status = domain.StatusDone })
	e.decision(t, "Quartz keys live in the vault", domain.StatusDropped, domain.DropRevoked)
	e.note(t, e.global.ID, "quartz", "How we rotate quartz keys.")
	hits, _, err := e.st.Search(t.Context(), e.org.ID, nil, "quartz", 10, nil)
	if err != nil || len(hits) != 3 {
		t.Fatalf("hits = %+v, %v; want 3", hits, err)
	}
	type seen struct {
		status string
		kind   domain.CardKind
	}
	var cards []seen
	for _, h := range hits {
		switch h.Type {
		case domain.SubjectCard:
			cards = append(cards, seen{h.Status, h.Kind})
		case domain.SubjectNote:
			if h.Status != "" || h.Kind != 0 {
				t.Errorf("note hit = %+v, want no status or kind", h)
			}
		}
	}
	slices.SortFunc(cards, func(a, b seen) int { return int(a.kind) - int(b.kind) })
	if want := []seen{{"done", domain.KindTask}, {"dropped", domain.KindDecision}}; !slices.Equal(cards, want) {
		t.Errorf("card hits = %v, want %v", cards, want)
	}
}

// Equal ranks are the common case for short documents, so the walk must rely on
// the type and id tiebreak to neither repeat nor drop a hit.
func TestSearchPagesWithAfterAndCountsWhatRemains(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	want := map[domain.ID]bool{}
	for range 3 {
		want[e.card(t, func(c *domain.Card) { c.Title = "feldspar"; c.Body = "" }).ID] = true
	}
	n := e.note(t, e.project.ID, "feldspar", "feldspar")
	want[n.ID] = true

	var after *query.SearchCursor
	seen := map[domain.ID]bool{}
	for page, wantRemaining := range []int{2, 0} {
		hits, remaining, err := e.st.Search(ctx, e.org.ID, nil, "feldspar", 2, after)
		if err != nil || len(hits) != 2 || remaining != wantRemaining {
			t.Fatalf("page %d = %+v, remaining %d, %v; want 2 hits, remaining %d", page, hits, remaining, err, wantRemaining)
		}
		for _, h := range hits {
			if seen[h.ID] || !want[h.ID] {
				t.Errorf("page %d returned %s again or out of the set", page, h.ID)
			}
			seen[h.ID] = true
		}
		last := hits[len(hits)-1]
		after = &query.SearchCursor{Rank: last.Rank, Type: last.Type, ID: last.ID}
	}
	if len(seen) != len(want) {
		t.Errorf("walk saw %d hits, want %d", len(seen), len(want))
	}
	if hits, remaining, err := e.st.Search(ctx, e.org.ID, nil, "feldspar", 2, after); err != nil || len(hits) != 0 || remaining != 0 {
		t.Errorf("past the end = %+v, remaining %d, %v; want nothing", hits, remaining, err)
	}
}

// Identical documents rank equally, so only the tiebreak keeps a walk from
// repeating or dropping rows. Card numbers repeat across projects, which the
// org-wide walk has to survive.
func TestFullTextCardListsWalkEqualRanksWithoutLoss(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	want := map[domain.ID]bool{}
	for _, project := range []domain.ID{e.project.ID, e.project.ID, e.global.ID, e.global.ID} {
		c := e.card(t, func(c *domain.Card) { c.ProjectID = project; c.Title = "feldspar"; c.Body = "" })
		want[c.ID] = true
	}
	var cur *query.CardCursor
	seen := map[domain.ID]bool{}
	for range 4 {
		rows, remaining, err := e.st.ListCardsIn(ctx, []domain.ID{e.project.ID, e.global.ID}, query.CardFilter{Query: "feldspar", Cursor: cur, Page: query.Page{Limit: 1}})
		if err != nil || len(rows) != 1 {
			t.Fatalf("page = %+v, %v; want one row", rows, err)
		}
		if seen[rows[0].ID] || !want[rows[0].ID] {
			t.Fatalf("row %s repeated or unknown after %v", rows[0].ID, seen)
		}
		seen[rows[0].ID] = true
		rank, err := e.st.SearchRank(ctx, domain.SubjectCard, rows[0].ID, "feldspar")
		if err != nil || rank <= 0 {
			t.Fatalf("rank = %v, %v", rank, err)
		}
		if wantLeft := 4 - len(seen); remaining != wantLeft {
			t.Errorf("after %d rows remaining = %d, want %d", len(seen), remaining, wantLeft)
		}
		cur = &query.CardCursor{Rank: rank, Number: rows[0].Number, ID: rows[0].ID}
	}
	if rows, _, err := e.st.ListCardsIn(ctx, []domain.ID{e.project.ID, e.global.ID}, query.CardFilter{Query: "feldspar", Cursor: cur, Page: query.Page{Limit: 1}}); err != nil || len(rows) != 0 {
		t.Errorf("past the end = %+v, %v", rows, err)
	}
}

func TestFullTextNoteListWalksEqualRanksWithoutLoss(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	for _, slug := range []string{"a", "b", "c"} {
		e.note(t, e.project.ID, slug, "feldspar")
	}
	var cur *query.NoteCursor
	seen := map[domain.ID]bool{}
	for range 3 {
		rows, _, err := e.st.ListNotes(ctx, e.org.ID, query.NoteFilter{Query: "feldspar", Cursor: cur, Page: query.Page{Limit: 1}})
		if err != nil || len(rows) != 1 || seen[rows[0].ID] {
			t.Fatalf("page = %+v, %v; want one new row after %v", rows, err, seen)
		}
		seen[rows[0].ID] = true
		rank, err := e.st.SearchRank(ctx, domain.SubjectNote, rows[0].ID, "feldspar")
		if err != nil {
			t.Fatal(err)
		}
		cur = &query.NoteCursor{Rank: rank, ID: rows[0].ID}
	}
	if rows, _, err := e.st.ListNotes(ctx, e.org.ID, query.NoteFilter{Query: "feldspar", Cursor: cur, Page: query.Page{Limit: 1}}); err != nil || len(rows) != 0 {
		t.Errorf("past the end = %+v, %v", rows, err)
	}
}
