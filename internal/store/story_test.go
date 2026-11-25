//go:build integration

package store

import (
	"slices"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

func TestFoldAndAcceptanceSurviveAnUpdateRoundTrip(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	d := e.decision(t, "Use pgx", domain.StatusDone, domain.DropNone)
	got, err := e.st.GetCard(ctx, d.ID)
	if err != nil || got.Acceptance == nil || got.Acceptance.By != e.user || !got.Acceptance.At.Equal(t0) {
		t.Fatalf("accepted decision = %+v, %v; want the acceptor read back", got.Acceptance, err)
	}
	revoked := got
	revoked.Status, revoked.DropReason = domain.StatusDropped, domain.DropRevoked
	revoked.UpdatedAt = at(1)
	revoked, err = e.st.UpdateCard(ctx, revoked, got.Version)
	if err != nil || revoked.DropReason != domain.DropRevoked || revoked.Acceptance == nil || revoked.Acceptance.By != e.user {
		t.Errorf("revoked = %+v, %v; want reason revoked and the acceptor kept", revoked, err)
	}

	task := e.card(t, func(c *domain.Card) { c.Status = domain.StatusDone; c.ClosedAt = new(t0) })
	task.Fold = &domain.Fold{Into: "docs/x.md", At: at(2), By: e.agent}
	folded, err := e.st.UpdateCard(ctx, task, task.Version)
	if err != nil || folded.Fold == nil || folded.Fold.Into != "docs/x.md" || folded.Fold.By != e.agent || !folded.Fold.At.Equal(at(2)) {
		t.Errorf("folded = %+v, %v", folded.Fold, err)
	}
	none := e.card(t, func(c *domain.Card) { c.Status = domain.StatusDone; c.ClosedAt = new(t0) })
	none.Fold = &domain.Fold{At: at(2), By: e.agent}
	folded, err = e.st.UpdateCard(ctx, none, none.Version)
	if err != nil || folded.Fold == nil || folded.Fold.Into != "" {
		t.Errorf("fold into nothing = %+v, %v; want a mark with no document", folded.Fold, err)
	}
}

func TestListUnfoldedOrdersByClosedAtAndSkipsFoldedAndChildren(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	done := func(h int, mut func(*domain.Card)) domain.Card {
		return e.card(t, func(c *domain.Card) {
			c.Status = domain.StatusDone
			c.ClosedAt = new(at(h))
			if mut != nil {
				mut(c)
			}
		})
	}
	old := done(1, nil)
	newer := done(3, nil)
	tie1 := done(2, nil)
	tie2 := done(2, nil)
	folded := done(4, nil)
	folded.Fold = &domain.Fold{At: at(5), By: e.agent}
	if _, err := e.st.UpdateCard(ctx, folded, folded.Version); err != nil {
		t.Fatal(err)
	}
	story := e.card(t, func(c *domain.Card) { c.Kind = domain.KindStory; c.Status = domain.StatusBacklog })
	done(5, func(c *domain.Card) { c.ParentID = &story.ID })
	e.card(t, nil)
	e.card(t, func(c *domain.Card) {
		c.Status = domain.StatusDropped
		c.DropReason = domain.DropObsolete
		c.ClosedAt = new(at(6))
	})

	want := []int64{newer.Number, tie2.Number, tie1.Number, old.Number}
	p1, remaining, err := e.st.ListUnfolded(ctx, e.project.ID, query.Page{Limit: 2}, nil)
	if err != nil || !slices.Equal(numbers(p1), want[:2]) || remaining != 2 {
		t.Fatalf("page 1 = %v remaining %d, %v; want %v 2", numbers(p1), remaining, err, want[:2])
	}
	last := p1[1]
	p2, remaining, err := e.st.ListUnfolded(ctx, e.project.ID, query.Page{Limit: 2}, &query.UnfoldedCursor{ClosedAt: *last.ClosedAt, ID: last.ID})
	if err != nil || !slices.Equal(numbers(p2), want[2:]) || remaining != 0 {
		t.Errorf("page 2 = %v remaining %d, %v; want %v 0", numbers(p2), remaining, err, want[2:])
	}
}

func TestListCardsInSpansProjectsNewestIDFirst(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	other := e.newProject(t, "web")
	third := e.newProject(t, "ops")
	a := e.card(t, nil)
	b := e.card(t, func(c *domain.Card) { c.ProjectID = other.ID })
	c := e.card(t, nil)
	e.card(t, func(c *domain.Card) { c.ProjectID = third.ID })

	ids := []domain.ID{e.project.ID, other.ID}
	got, remaining, err := e.st.ListCardsIn(ctx, ids, query.CardFilter{Page: query.Page{Limit: 2}})
	if err != nil || len(got) != 2 || got[0].ID != c.ID || got[1].ID != b.ID || remaining != 1 {
		t.Fatalf("page 1 = %v remaining %d, %v; want %s then %s with 1 left", idsOfCards(got), remaining, err, c.ID, b.ID)
	}
	next, remaining, err := e.st.ListCardsIn(ctx, ids, query.CardFilter{Page: query.Page{Limit: 2}, Cursor: &query.CardCursor{ID: got[1].ID}})
	if err != nil || len(next) != 1 || next[0].ID != a.ID || remaining != 0 {
		t.Errorf("page 2 = %v remaining %d, %v; want %s", idsOfCards(next), remaining, err, a.ID)
	}
	if none, _, err := e.st.ListCardsIn(ctx, nil, query.CardFilter{Page: query.Page{Limit: 5}}); err != nil || len(none) != 0 {
		t.Errorf("no projects = %v, %v; want empty", idsOfCards(none), err)
	}
	story := e.card(t, func(c *domain.Card) {
		c.Kind = domain.KindStory
		c.Status = domain.StatusBacklog
		c.ProjectID = other.ID
	})
	onlyStories, _, err := e.st.ListCardsIn(ctx, ids, query.CardFilter{Kind: domain.KindStory, Page: query.Page{Limit: 5}})
	if err != nil || len(onlyStories) != 1 || onlyStories[0].ID != story.ID {
		t.Errorf("kind filter = %v, %v; want the story", idsOfCards(onlyStories), err)
	}
}

func idsOfCards(cs []domain.Card) []domain.ID {
	out := make([]domain.ID, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func TestArchivedProjectIsReportedSoTheAllProjectsListCanSkipIt(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	old := e.newProject(t, "old")
	live := e.card(t, nil)
	e.card(t, func(c *domain.Card) { c.ProjectID = old.ID })
	old.Status, old.UpdatedAt = domain.RecordArchived, at(1)
	if _, err := e.st.UpdateProject(ctx, old); err != nil {
		t.Fatal(err)
	}
	projects, err := e.st.ListProjects(ctx, e.org.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []domain.ID
	for _, p := range projects {
		if p.Status.Alive() {
			ids = append(ids, p.ID)
		}
	}
	if slices.Contains(ids, old.ID) || !slices.Contains(ids, e.project.ID) {
		t.Fatalf("alive projects = %v, want the archived one dropped and the live one kept", ids)
	}
	got, _, err := e.st.ListCardsIn(ctx, ids, query.CardFilter{Page: query.Page{Limit: 10}})
	if err != nil || len(got) != 1 || got[0].ID != live.ID {
		t.Errorf("cards = %v, %v; want only the live project's card", idsOfCards(got), err)
	}
}

func TestListChildrenOrdersByPriorityDescendingThenNumber(t *testing.T) {
	e := newEnv(t)
	story := e.card(t, func(c *domain.Card) { c.Kind = domain.KindStory; c.Status = domain.StatusBacklog })
	child := func(p domain.Priority, mut func(*domain.Card)) domain.Card {
		return e.card(t, func(c *domain.Card) {
			c.ParentID = &story.ID
			c.Priority = p
			if mut != nil {
				mut(c)
			}
		})
	}
	normal1 := child(domain.PriorityNormal, nil)
	low := child(domain.PriorityLow, nil)
	urgent := child(domain.PriorityUrgent, nil)
	normal2 := child(domain.PriorityNormal, func(c *domain.Card) {
		c.Kind = domain.KindDecision
		c.Status = domain.StatusDone
		c.ClosedAt = new(t0)
	})
	e.card(t, nil)

	got, err := e.st.ListChildren(t.Context(), story.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{urgent.Number, normal1.Number, normal2.Number, low.Number}
	if !slices.Equal(numbers(got), want) {
		t.Errorf("children = %v, want %v: priority descending, then number, whatever the status", numbers(got), want)
	}
}
