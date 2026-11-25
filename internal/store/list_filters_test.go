//go:build integration

package store

import (
	"slices"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

func at(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }

// filterCards seeds four cards:
//
//	#1 low     created 0h, updated 2h, assigned to the agent
//	#2 urgent  created 0h, updated 0h, nobody
//	#3 high    created 1h, updated 1h, claimed by the user
//	#4 normal  created 1h, updated 3h, done and closed at 3h
func filterCards(t *testing.T, e *env) {
	t.Helper()
	touch := func(c domain.Card, h int) {
		c.UpdatedAt = at(h)
		if _, err := e.st.UpdateCard(t.Context(), c, c.Version); err != nil {
			t.Fatalf("touch #%d: %v", c.Number, err)
		}
	}
	one := e.card(t, func(c *domain.Card) { c.Priority = domain.PriorityLow; c.Assignee = &e.agent })
	touch(one, 2)
	e.card(t, func(c *domain.Card) { c.Priority = domain.PriorityUrgent })
	three := e.card(t, func(c *domain.Card) { c.Priority = domain.PriorityHigh; c.CreatedAt, c.UpdatedAt = at(1), at(1) })
	e.claim(t, three, e.user, at(5))
	four := e.card(t, func(c *domain.Card) {
		c.CreatedAt, c.UpdatedAt = at(1), at(1)
		c.Status = domain.StatusDone
		c.ClosedAt = new(at(3))
	})
	touch(four, 3)
}

func TestListCardsFilters(t *testing.T) {
	e := newEnv(t)
	filterCards(t, e)
	tests := []struct {
		name string
		f    query.CardFilter
		want []int64
	}{
		{"priorities", query.CardFilter{Priorities: []domain.Priority{domain.PriorityLow, domain.PriorityHigh}}, []int64{1, 3}},
		{"created after is inclusive", query.CardFilter{Created: query.TimeRange{After: at(1)}}, []int64{3, 4}},
		{"created before is exclusive", query.CardFilter{Created: query.TimeRange{Before: at(1)}}, []int64{1, 2}},
		{"created window", query.CardFilter{Created: query.TimeRange{After: at(-1), Before: at(1)}}, []int64{1, 2}},
		{"updated after", query.CardFilter{Updated: query.TimeRange{After: at(2)}}, []int64{1, 4}},
		{"updated before", query.CardFilter{Updated: query.TimeRange{Before: at(2)}}, []int64{2, 3}},
		{"updated window", query.CardFilter{Updated: query.TimeRange{After: at(1), Before: at(3)}}, []int64{1, 3}},
		{"closed after skips cards never closed", query.CardFilter{Closed: query.TimeRange{After: at(0)}}, []int64{4}},
		{"closed before the close", query.CardFilter{Closed: query.TimeRange{Before: at(3)}}, []int64{}},
		{"assigned", query.CardFilter{Assignee: &e.agent.ID}, []int64{1}},
		{"claimed", query.CardFilter{Assignee: &e.user.ID}, []int64{3}},
		{"unassigned", query.CardFilter{Unassigned: true}, []int64{2, 4}},
		{"combined", query.CardFilter{Unassigned: true, Priorities: []domain.Priority{domain.PriorityUrgent}, Created: query.TimeRange{Before: at(1)}}, []int64{2}},
		{"filters with a text query", query.CardFilter{Query: "task", Priorities: []domain.Priority{domain.PriorityHigh}}, []int64{3}},
	}
	for _, tt := range tests {
		tt.f.Sort = query.SortNumber
		tt.f.Page = query.Page{Limit: 10}
		got, _, err := e.st.ListCards(t.Context(), e.project.ID, tt.f)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if !equalInts(numbers(got), tt.want) {
			t.Errorf("%s = %v, want %v", tt.name, numbers(got), tt.want)
		}
	}
}

// A map box lists its cards with the lane and no-story filters: a lane is the
// cards outside any story that carry it, and a story card is in no lane.
func TestListCardsFiltersByLaneAndStory(t *testing.T) {
	e := newEnv(t)
	story := e.card(t, func(c *domain.Card) { c.Kind = domain.KindStory; c.Lane = "a" })
	e.card(t, func(c *domain.Card) { c.Lane = "a" })
	e.card(t, func(c *domain.Card) { c.Lane = "a"; c.ParentID = &story.ID })
	e.card(t, func(c *domain.Card) { c.Lane = "b" })
	e.card(t, func(c *domain.Card) { c.ParentID = &story.ID })
	e.card(t, nil)
	tests := []struct {
		name string
		f    query.CardFilter
		want []int64
	}{
		{"lane", query.CardFilter{Lane: "a"}, []int64{1, 2, 3}},
		{"lane outside stories", query.CardFilter{Lane: "a", NoStory: true}, []int64{2}},
		{"no lane outside stories", query.CardFilter{NoLane: true, NoStory: true}, []int64{6}},
		{"no lane", query.CardFilter{NoLane: true}, []int64{5, 6}},
		{"outside stories leaves out the story card", query.CardFilter{NoStory: true}, []int64{2, 4, 6}},
		{"lane with a status", query.CardFilter{Lane: "b", Statuses: []domain.Status{domain.StatusReady}}, []int64{4}},
		{"lane with another status", query.CardFilter{Lane: "b", Statuses: []domain.Status{domain.StatusDone}}, []int64{}},
	}
	for _, tt := range tests {
		tt.f.Sort = query.SortNumber
		tt.f.Page = query.Page{Limit: 10}
		got, _, err := e.st.ListCards(t.Context(), e.project.ID, tt.f)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if !equalInts(numbers(got), tt.want) {
			t.Errorf("%s = %v, want %v", tt.name, numbers(got), tt.want)
		}
	}
}

func TestListCardsSortsAndPagesBothWays(t *testing.T) {
	e := newEnv(t)
	filterCards(t, e)
	ctx := t.Context()
	cursorOf := func(c domain.Card) *query.CardCursor {
		return &query.CardCursor{Status: c.Status, Priority: c.Priority, Number: c.Number, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
	}
	tests := []struct {
		name string
		sort query.CardSort
		desc bool
		want []int64
	}{
		{"created", query.SortCreated, false, []int64{1, 2, 3, 4}},
		{"created desc", query.SortCreated, true, []int64{4, 3, 2, 1}},
		{"updated", query.SortUpdated, false, []int64{2, 3, 1, 4}},
		{"updated desc", query.SortUpdated, true, []int64{4, 1, 3, 2}},
		{"priority", query.SortPriority, false, []int64{1, 4, 3, 2}},
		{"priority desc", query.SortPriority, true, []int64{2, 3, 4, 1}},
		{"number", query.SortNumber, false, []int64{1, 2, 3, 4}},
		{"number desc", query.SortNumber, true, []int64{4, 3, 2, 1}},
	}
	for _, tt := range tests {
		f := query.CardFilter{Sort: tt.sort, Desc: tt.desc, Page: query.Page{Limit: 2}}
		p1, remaining, err := e.st.ListCards(ctx, e.project.ID, f)
		if err != nil || !equalInts(numbers(p1), tt.want[:2]) || remaining != 2 {
			t.Errorf("%s page 1 = %v remaining %d, %v; want %v 2", tt.name, numbers(p1), remaining, err, tt.want[:2])
			continue
		}
		f.Cursor = cursorOf(p1[1])
		p2, remaining, err := e.st.ListCards(ctx, e.project.ID, f)
		if err != nil || !equalInts(numbers(p2), tt.want[2:]) || remaining != 0 {
			t.Errorf("%s page 2 = %v remaining %d, %v; want %v 0", tt.name, numbers(p2), remaining, err, tt.want[2:])
			continue
		}
		f.Cursor, f.Page.Backward = cursorOf(p2[0]), true
		back, remaining, err := e.st.ListCards(ctx, e.project.ID, f)
		if err != nil || !equalInts(numbers(back), tt.want[:2]) || remaining != 0 {
			t.Errorf("%s back = %v remaining %d, %v; want %v 0", tt.name, numbers(back), remaining, err, tt.want[:2])
		}
	}
}

func TestListNotesFiltersByAuthorAndDate(t *testing.T) {
	e := newEnv(t)
	mk := func(slug string, by domain.ActorRef, h int) domain.Note {
		n, err := e.st.CreateNote(t.Context(), domain.Note{ID: gen.New(), OrgID: e.org.ID, ProjectID: e.project.ID, Status: domain.RecordActive,
			Slug: slug, Title: slug, Body: "x", CreatedBy: by, UpdatedBy: by, CreatedAt: at(h), UpdatedAt: at(h)})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	a := mk("a", e.user, 0)
	mk("b", e.agent, 1)
	mk("c", e.user, 2)
	// Editing a moves its updated time but not its created time.
	a.UpdatedAt = at(3)
	if _, err := e.st.UpdateNote(t.Context(), a, 1, gen.New(), false); err != nil {
		t.Fatal(err)
	}
	slugs := func(f query.NoteFilter) []string {
		f.Page = query.Page{Limit: 10}
		got, _, err := e.st.ListNotes(t.Context(), e.org.ID, f)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, n := range got {
			out = append(out, n.Slug)
		}
		return out
	}
	tests := []struct {
		name string
		f    query.NoteFilter
		want []string
	}{
		{"author", query.NoteFilter{Author: &e.user.ID}, []string{"a", "c"}},
		{"created after is inclusive", query.NoteFilter{Created: query.TimeRange{After: at(1)}}, []string{"c", "b"}},
		{"created before is exclusive", query.NoteFilter{Created: query.TimeRange{Before: at(1)}}, []string{"a"}},
		{"updated after sees the edit", query.NoteFilter{Updated: query.TimeRange{After: at(3)}}, []string{"a"}},
		{"updated before hides the edit", query.NoteFilter{Updated: query.TimeRange{Before: at(3)}}, []string{"c", "b"}},
		{"author and date", query.NoteFilter{Author: &e.user.ID, Created: query.TimeRange{After: at(1)}}, []string{"c"}},
	}
	for _, tt := range tests {
		if got := slugs(tt.f); !slices.Equal(got, tt.want) {
			t.Errorf("%s = %v, want %v", tt.name, got, tt.want)
		}
	}
}
