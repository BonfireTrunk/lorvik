//go:build integration

package store

import (
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

func (e *env) mapSeed(t *testing.T) (s1, s2, empty domain.Card, cards map[string]domain.Card) {
	t.Helper()
	story := func(title string) domain.Card {
		return e.card(t, func(c *domain.Card) { c.Kind = domain.KindStory; c.Status = domain.StatusBacklog; c.Title = title })
	}
	s1, s2, empty = story("one"), story("two"), story("empty")
	child := func(parent domain.Card, status domain.Status, kind domain.CardKind) domain.Card {
		return e.card(t, func(c *domain.Card) { c.ParentID = &parent.ID; c.Status = status; c.Kind = kind })
	}
	lane := func(name string, status domain.Status) domain.Card {
		return e.card(t, func(c *domain.Card) { c.Lane = name; c.Status = status })
	}
	cards = map[string]domain.Card{
		"s1ready": child(s1, domain.StatusReady, domain.KindTask),
		"s1done":  child(s1, domain.StatusDone, domain.KindTask),
		"s1q":     child(s1, domain.StatusReady, domain.KindQuestion),
		"s2back":  child(s2, domain.StatusBacklog, domain.KindTask),
		"upper":   lane("B", domain.StatusReady),
		"a1":      lane("a", domain.StatusReady),
		"a2":      lane("a", domain.StatusWorking),
		"bare":    lane("", domain.StatusBacklog),
	}
	return s1, s2, empty, cards
}

func TestListMapGroupsCountsStoriesAndLanesAndPagesInOrder(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s1, s2, empty, _ := e.mapSeed(t)
	web := e.newProject(t, "web")
	e.card(t, func(c *domain.Card) { c.ProjectID = web.ID; c.Lane = "elsewhere" })

	var got []query.MapGroup
	var after *query.MapGroupRef
	for range 10 {
		page, remaining, err := e.st.ListMapGroups(ctx, e.project.ID, 2, after)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 && (len(page) != 2 || remaining != 4) {
			t.Fatalf("page 1 = %d remaining %d, want 2 and 4", len(page), remaining)
		}
		got = append(got, page...)
		if remaining == 0 {
			break
		}
		after = &page[len(page)-1].Ref
	}
	want := []query.MapGroupRef{
		{Kind: query.GroupStory, Number: s1.Number}, {Kind: query.GroupStory, Number: s2.Number}, {Kind: query.GroupStory, Number: empty.Number},
		{Kind: query.GroupLane, Lane: ""}, {Kind: query.GroupLane, Lane: "B"}, {Kind: query.GroupLane, Lane: "a"},
	}
	if len(got) != len(want) {
		t.Fatalf("groups = %+v, want %d", got, len(want))
	}
	for i := range want {
		if got[i].Ref != want[i] {
			t.Errorf("group %d = %+v, want %+v (lanes in byte order)", i, got[i].Ref, want[i])
		}
	}

	for i, story := range []domain.Card{s1, s2, empty} {
		children, err := e.st.CountChildrenByStatus(ctx, story.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range []domain.Status{domain.StatusBacklog, domain.StatusReady, domain.StatusWorking, domain.StatusWaiting, domain.StatusDone, domain.StatusDropped} {
			if got[i].Counts.ByStatus[st] != children[st] {
				t.Errorf("story #%d %v = %d, CountChildrenByStatus says %d", story.Number, st, got[i].Counts.ByStatus[st], children[st])
			}
		}
	}
	if got[0].Title != "one" || got[0].StoryStatus != domain.StatusBacklog || got[0].Counts.OpenQuestions != 1 {
		t.Errorf("story one = %+v, want its title, status and one open question", got[0])
	}
	if a := got[5].Counts; a.ByStatus[domain.StatusReady] != 1 || a.ByStatus[domain.StatusWorking] != 1 {
		t.Errorf("lane a counts = %+v, want one ready and one working", a)
	}
	if n := got[3].Counts.ByStatus[domain.StatusBacklog]; n != 1 {
		t.Errorf("no-lane backlog = %d, want 1", n)
	}
}

func TestListMapGroupLinksCountsPerPairAndKindAndSkipsInsideAndOutside(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s1, s2, _, c := e.mapSeed(t)
	far := e.card(t, func(c *domain.Card) { c.ProjectID = e.global.ID })
	e.link(t, c["s1ready"], c["s2back"], domain.LinkBlocks)
	e.link(t, c["s1done"], c["s2back"], domain.LinkBlocks)
	e.link(t, c["s1ready"], c["s2back"], domain.LinkRelates)
	e.link(t, c["s1ready"], c["s1done"], domain.LinkBlocks)
	e.link(t, c["a1"], c["a2"], domain.LinkBlocks)
	e.link(t, c["a1"], c["s1ready"], domain.LinkBlocks)
	e.link(t, s1, s2, domain.LinkRelates)
	e.link(t, c["bare"], c["upper"], domain.LinkApplies)
	e.link(t, c["bare"], far, domain.LinkBlocks)

	story := func(n int64) query.MapGroupRef { return query.MapGroupRef{Kind: query.GroupStory, Number: n} }
	lane := func(n string) query.MapGroupRef { return query.MapGroupRef{Kind: query.GroupLane, Lane: n} }
	want := []query.MapGroupLink{
		{From: story(s1.Number), To: story(s2.Number), Kind: domain.LinkBlocks, Count: 2},
		{From: story(s1.Number), To: story(s2.Number), Kind: domain.LinkRelates, Count: 2},
		{From: lane(""), To: lane("B"), Kind: domain.LinkApplies, Count: 1},
		{From: lane("a"), To: story(s1.Number), Kind: domain.LinkBlocks, Count: 1},
	}
	var got []query.MapGroupLink
	var after *query.MapGroupLinkCursor
	for range 10 {
		page, remaining, err := e.st.ListMapGroupLinks(ctx, e.project.ID, 3, after)
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
		after = &query.MapGroupLinkCursor{From: last.From, To: last.To, Kind: last.Kind}
	}
	if len(got) != len(want) {
		t.Fatalf("group links = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("group link %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
