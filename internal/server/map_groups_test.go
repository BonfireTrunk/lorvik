package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func walkGroups(e *testEnv, secret, slug string, limit int) []apitypes.MapGroup {
	e.t.Helper()
	var out []apitypes.MapGroup
	for _, raw := range walkList(e, secret, "/projects/"+slug+"/map/groups", limit) {
		var g apitypes.MapGroup
		if err := json.Unmarshal(raw, &g); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, g)
	}
	return out
}

func walkGroupLinks(e *testEnv, secret, slug string, limit int) []apitypes.MapGroupLink {
	e.t.Helper()
	var out []apitypes.MapGroupLink
	for _, raw := range walkList(e, secret, "/projects/"+slug+"/map/group-links", limit) {
		var l apitypes.MapGroupLink
		if err := json.Unmarshal(raw, &l); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, l)
	}
	return out
}

// mapSeed is a project with two stories (one empty), lane cards, cards with
// no lane, and links inside, across and out of the groups.
type mapSeed struct {
	s1, s2, s3                  int64
	s1a, s1b, s1q, s2a          int64
	laneA1, laneA2, laneB, bare int64
	bareQ                       int64
}

func seedMap(e *testEnv) mapSeed {
	e.t.Helper()
	story := func(title string) int64 {
		return e.newCard(userSecret, apitypes.CreateCard{Kind: "story", Title: title}).Number
	}
	var m mapSeed
	m.s1, m.s2, m.s3 = story("one"), story("two"), story("empty")
	child := func(parent int64, c apitypes.CreateCard) int64 {
		c.Parent = &parent
		return e.newCard(userSecret, c).Number
	}
	m.s1a = child(m.s1, apitypes.CreateCard{Title: "s1a", Status: "ready"})
	m.s1b = child(m.s1, apitypes.CreateCard{Title: "s1b", Status: "backlog"})
	m.s1q = child(m.s1, apitypes.CreateCard{Kind: "question", Title: "s1q"})
	m.s2a = child(m.s2, apitypes.CreateCard{Title: "s2a", Status: "ready", Lane: "ignored"})
	e.claim(userSecret, m.s2a)
	m.laneA1 = e.newCard(userSecret, apitypes.CreateCard{Title: "a1", Status: "ready", Lane: "a"}).Number
	m.laneA2 = e.newCard(userSecret, apitypes.CreateCard{Title: "a2", Status: "backlog", Lane: "a"}).Number
	m.laneB = e.newCard(userSecret, apitypes.CreateCard{Title: "b", Status: "ready", Lane: "b"}).Number
	m.bare = e.newCard(userSecret, apitypes.CreateCard{Title: "bare", Status: "ready"}).Number
	m.bareQ = e.newCard(userSecret, apitypes.CreateCard{Kind: "question", Title: "bareq"}).Number

	foreign := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/global/cards", apitypes.CreateCard{Title: "far", Status: "ready"}, http.StatusCreated)
	e.linkCards("lorvik", m.s1a, "blocks", m.s2a, "")
	e.linkCards("lorvik", m.s1b, "blocks", m.s2a, "")
	e.linkCards("lorvik", m.s1a, "relates", m.s2a, "")
	e.linkCards("lorvik", m.s1a, "blocks", m.s1b, "")
	e.linkCards("lorvik", m.laneA1, "blocks", m.laneA2, "")
	e.linkCards("lorvik", m.laneA1, "blocks", m.s1a, "")
	e.linkCards("lorvik", m.laneB, "relates", m.bare, "")
	e.linkCards("lorvik", m.s1, "relates", m.s2, "")
	e.linkCards("lorvik", m.bare, "blocks", foreign.Number, "global")
	return m
}

// expectedGroups tallies the project's cards from the card list, the other
// way round from the store: by parent and lane, status by status.
func expectedGroups(e *testEnv) map[string]apitypes.StatusCounts {
	e.t.Helper()
	out := map[string]apitypes.StatusCounts{}
	for _, raw := range walkList(e, userSecret, "/projects/lorvik/cards", 200) {
		var c apitypes.CardBrief
		if err := json.Unmarshal(raw, &c); err != nil {
			e.t.Fatal(err)
		}
		var key string
		switch {
		case c.Parent != nil:
			key = fmt.Sprintf("story:%d", *c.Parent)
		case c.Kind == "story":
			key = fmt.Sprintf("story:%d", c.Number)
			if _, ok := out[key]; !ok {
				out[key] = apitypes.StatusCounts{}
			}
			continue
		default:
			key = "lane:" + c.Lane
		}
		n := out[key]
		switch c.Status {
		case "backlog":
			n.Backlog++
		case "ready":
			n.Ready++
		case "working":
			n.Working++
		case "waiting":
			n.Waiting++
		case "done":
			n.Done++
		case "dropped":
			n.Dropped++
		}
		if c.Kind == "question" && c.Status != "done" && c.Status != "dropped" {
			n.OpenQuestions++
		}
		out[key] = n
	}
	return out
}

func TestMapGroupCountsMatchTheCardList(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	m := seedMap(e)
	want := expectedGroups(e)

	for _, limit := range []int{200, 1, 2} {
		got := walkGroups(e, userSecret, "lorvik", limit)
		if len(got) != len(want) {
			t.Fatalf("limit %d: %d groups %+v, want %d", limit, len(got), got, len(want))
		}
		seen := map[string]bool{}
		for _, g := range got {
			if seen[g.Key] {
				t.Errorf("limit %d: group %q listed twice", limit, g.Key)
			}
			seen[g.Key] = true
			if g.Counts != want[g.Key] {
				t.Errorf("limit %d: %s counts = %+v, want %+v", limit, g.Key, g.Counts, want[g.Key])
			}
		}
	}

	byKey := map[string]apitypes.MapGroup{}
	var keys []string
	for _, g := range walkGroups(e, userSecret, "lorvik", 200) {
		byKey[g.Key] = g
		keys = append(keys, g.Key)
	}
	wantKeys := []string{
		fmt.Sprintf("story:%d", m.s1), fmt.Sprintf("story:%d", m.s2), fmt.Sprintf("story:%d", m.s3),
		"lane:", "lane:a", "lane:b",
	}
	if fmt.Sprint(keys) != fmt.Sprint(wantKeys) {
		t.Errorf("group order = %v, want stories by number then lanes by name: %v", keys, wantKeys)
	}
	one := byKey[fmt.Sprintf("story:%d", m.s1)]
	if one.Kind != "story" || one.Story != m.s1 || one.Title != "one" || one.Status != "backlog" || one.Lane != "" {
		t.Errorf("story group = %+v", one)
	}
	if c := one.Counts; c.Ready != 1 || c.Backlog != 1 || c.OpenQuestions != 1 {
		t.Errorf("story one counts = %+v, want a ready task, a backlog task and an open question", c)
	}
	if c := byKey[fmt.Sprintf("story:%d", m.s2)].Counts; c.Waiting != 1 || c.Ready != 0 {
		t.Errorf("story two counts = %+v, want its one child, held back by two blockers; its lane is not a group", c)
	}
	if c := byKey[fmt.Sprintf("story:%d", m.s3)].Counts; c != (apitypes.StatusCounts{}) {
		t.Errorf("empty story counts = %+v, want zeros", c)
	}
	if g := byKey["lane:"]; g.Kind != "lane" || g.Lane != "" || g.Counts.Ready != 2 || g.Counts.OpenQuestions != 1 {
		t.Errorf("no-lane group = %+v, want a ready task and a ready open question", g)
	}
	if g := byKey["lane:a"]; g.Lane != "a" || g.Counts.Ready != 1 || g.Counts.Backlog != 1 {
		t.Errorf("lane a group = %+v", g)
	}
}

func TestMapGroupLinksCountLinksBetweenGroupsByKind(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	m := seedMap(e)
	s1, s2 := fmt.Sprintf("story:%d", m.s1), fmt.Sprintf("story:%d", m.s2)
	want := []apitypes.MapGroupLink{
		{From: s1, To: s2, Kind: "blocks", Count: 2},
		{From: s1, To: s2, Kind: "relates", Count: 2},
		{From: "lane:a", To: s1, Kind: "blocks", Count: 1},
		{From: "lane:b", To: "lane:", Kind: "relates", Count: 1},
	}
	for _, limit := range []int{200, 1, 3} {
		got := walkGroupLinks(e, userSecret, "lorvik", limit)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("limit %d: group links = %+v, want %+v (none inside a group, none to another project)", limit, got, want)
		}
	}
}

func TestMapGroupListsPageForwardAndRefuseForeignCursors(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	seedMap(e)
	first := call[apitypes.MapGroupList](e, userSecret, http.MethodGet, "/projects/lorvik/map/groups?limit=2", nil, http.StatusOK)
	if len(first.Items) != 2 || first.Remaining != 4 || first.Next == "" || first.Prev != "" {
		t.Errorf("first page = %+v, want two groups, remaining 4, next set, no prev", first)
	}
	last := call[apitypes.MapGroupList](e, userSecret, http.MethodGet, "/projects/lorvik/map/groups?limit=4&cursor="+first.Next, nil, http.StatusOK)
	if len(last.Items) != 4 || last.Remaining != 0 || last.Next != "" {
		t.Errorf("last page = %+v, want four groups and no next", last)
	}
	links := call[apitypes.MapGroupLinkList](e, userSecret, http.MethodGet, "/projects/lorvik/map/group-links?limit=1", nil, http.StatusOK)
	for _, path := range []string{
		"/projects/lorvik/map/groups?cursor=" + links.Next,
		"/projects/lorvik/map/group-links?cursor=" + first.Next,
		"/projects/lorvik/map/groups?cursor=" + first.Next + "x",
	} {
		if rec := e.do(userSecret, http.MethodGet, path, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, rec.Code)
		}
	}
}

func TestMapGroupListsHideProjectsTheReaderCannotSee(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "secret", Name: "Secret", Visibility: "private"}, http.StatusCreated)
	g1 := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/global/cards", apitypes.CreateCard{Title: "g1", Status: "ready", Lane: "x"}, http.StatusCreated).Number
	hidden := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/secret/cards", apitypes.CreateCard{Title: "h", Status: "ready", Lane: "y"}, http.StatusCreated).Number
	e.linkCards("global", g1, "blocks", hidden, "secret")

	for _, tail := range []string{"/map/groups", "/map/group-links"} {
		for _, slug := range []string{"secret", "nope"} {
			if rec := e.do(agentSecret, http.MethodGet, "/projects/"+slug+tail, nil); rec.Code != http.StatusNotFound {
				t.Errorf("agent GET %s%s = %d, want 404 as for an unknown project", slug, tail, rec.Code)
			}
		}
		if rec := e.do(userSecret, http.MethodGet, "/projects/secret"+tail, nil); rec.Code != http.StatusOK {
			t.Errorf("owner GET secret%s = %d, want 200", tail, rec.Code)
		}
	}
	groups := walkGroups(e, agentSecret, "global", 200)
	if len(groups) != 1 || groups[0].Key != "lane:x" || groups[0].Counts.Ready != 1 {
		t.Errorf("global groups = %+v, want only lane x", groups)
	}
	if links := walkGroupLinks(e, agentSecret, "global", 200); len(links) != 0 {
		t.Errorf("global group links = %+v, want none: the link ends in another project", links)
	}
}
