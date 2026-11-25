//go:build integration

package store

import (
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

func (e *env) link(t *testing.T, from, to domain.Card, kind domain.CardLinkKind) {
	t.Helper()
	if err := e.st.AddCardLink(t.Context(), domain.CardLink{FromCardID: from.ID, ToCardID: to.ID, Kind: kind, CreatedBy: e.agent, CreatedAt: t0}); err != nil {
		t.Fatal(err)
	}
}

func TestListProjectLinksOrdersByKeyBothDirectionsAndAnchorsOnTheProject(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	web := e.newProject(t, "web")
	a := e.card(t, nil)
	b := e.card(t, nil)
	g := e.card(t, func(c *domain.Card) { c.ProjectID = e.global.ID })
	w := e.card(t, func(c *domain.Card) { c.ProjectID = web.ID })
	w2 := e.card(t, func(c *domain.Card) { c.ProjectID = web.ID })
	e.link(t, a, b, domain.LinkRelates)
	e.link(t, a, b, domain.LinkBlocks)
	e.link(t, g, a, domain.LinkApplies)
	e.link(t, b, g, domain.LinkBlocks)
	e.link(t, w, w2, domain.LinkBlocks)

	want := []query.LinkEdge{
		{FromID: a.ID, ToID: b.ID, FromNumber: a.Number, ToNumber: b.Number, FromProject: e.project.ID, ToProject: e.project.ID, Kind: domain.LinkBlocks},
		{FromID: a.ID, ToID: b.ID, FromNumber: a.Number, ToNumber: b.Number, FromProject: e.project.ID, ToProject: e.project.ID, Kind: domain.LinkRelates},
		{FromID: b.ID, ToID: g.ID, FromNumber: b.Number, ToNumber: g.Number, FromProject: e.project.ID, ToProject: e.global.ID, Kind: domain.LinkBlocks},
		{FromID: g.ID, ToID: a.ID, FromNumber: g.Number, ToNumber: a.Number, FromProject: e.global.ID, ToProject: e.project.ID, Kind: domain.LinkApplies},
	}
	var got []query.LinkEdge
	var after *query.LinkCursor
	for range 10 {
		page, remaining, err := e.st.ListProjectLinks(ctx, e.project.ID, nil, 3, after)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 && (len(page) != 3 || remaining != 1) {
			t.Fatalf("page 1 = %d remaining %d, want 3 and 1", len(page), remaining)
		}
		got = append(got, page...)
		if remaining == 0 {
			break
		}
		last := page[len(page)-1]
		after = &query.LinkCursor{From: last.FromID, To: last.ToID, Kind: last.Kind}
	}
	if len(got) != len(want) {
		t.Fatalf("links = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("link %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	visible, _, err := e.st.ListProjectLinks(ctx, e.project.ID, []domain.ID{e.project.ID}, 10, nil)
	if err != nil || len(visible) != 2 {
		t.Errorf("links visible only in the project = %+v, %v; want the two inside it", visible, err)
	}
	none, remaining, err := e.st.ListProjectLinks(ctx, e.project.ID, []domain.ID{}, 10, nil)
	if err != nil || len(none) != 0 || remaining != 0 {
		t.Errorf("links with nothing visible = %+v, %v; want none", none, err)
	}
}

func TestListProjectMentionsOrdersByCardThenNoteAndFiltersNotes(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	web := e.newProject(t, "web")
	c1 := e.card(t, nil)
	c2 := e.card(t, nil)
	webCard := e.card(t, func(c *domain.Card) { c.ProjectID = web.ID })
	local := e.note(t, e.project.ID, "arch", "x")
	shared := e.note(t, e.global.ID, "handbook", "x")
	hidden := e.note(t, web.ID, "vault", "x")
	ref := func(src domain.Card, target domain.Note) domain.RefLink {
		return domain.RefLink{SourceType: domain.SubjectCard, SourceID: src.ID, TargetType: domain.SubjectNote, TargetID: target.ID}
	}
	if err := e.st.ReplaceRefs(ctx, c1.ID, []domain.RefLink{ref(c1, hidden), ref(c1, local), {SourceType: domain.SubjectCard, SourceID: c1.ID, TargetType: domain.SubjectCard, TargetID: c2.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.ReplaceRefs(ctx, c2.ID, []domain.RefLink{ref(c2, shared)}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.ReplaceRefs(ctx, webCard.ID, []domain.RefLink{ref(webCard, local)}); err != nil {
		t.Fatal(err)
	}

	first, remaining, err := e.st.ListProjectMentions(ctx, e.project.ID, nil, 2, nil)
	if err != nil || len(first) != 2 || remaining != 1 {
		t.Fatalf("page 1 = %+v remaining %d, %v; want 2 and 1", first, remaining, err)
	}
	if first[0].CardID != c1.ID || first[1].CardID != c1.ID || first[0].NoteID >= first[1].NoteID {
		t.Errorf("page 1 = %+v, want card 1's notes in id order", first)
	}
	rest, remaining, err := e.st.ListProjectMentions(ctx, e.project.ID, nil, 2, &query.MentionCursor{Card: first[1].CardID, Note: first[1].NoteID})
	if err != nil || len(rest) != 1 || remaining != 0 || rest[0] != (query.MentionEdge{CardID: c2.ID, CardNumber: c2.Number, NoteID: shared.ID, NoteSlug: "handbook"}) {
		t.Errorf("page 2 = %+v remaining %d, %v; want card 2's mention of handbook", rest, remaining, err)
	}

	seen, _, err := e.st.ListProjectMentions(ctx, e.project.ID, []domain.ID{e.project.ID, e.global.ID}, 10, nil)
	if err != nil || len(seen) != 2 {
		t.Errorf("mentions with web hidden = %+v, %v; want the two notes outside web", seen, err)
	}
	for _, m := range seen {
		if m.NoteID == hidden.ID {
			t.Errorf("mention of the hidden note returned: %+v", m)
		}
	}
}
