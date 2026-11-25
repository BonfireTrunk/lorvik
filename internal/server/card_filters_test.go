package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func sumCounts(c apitypes.StatusCounts) int {
	return c.Backlog + c.Ready + c.Working + c.Waiting + c.Done + c.Dropped
}

func listNumbers(e *testEnv, path string, limit int) []int64 {
	e.t.Helper()
	var out []int64
	for _, raw := range walkList(e, userSecret, path, limit) {
		var c apitypes.CardBrief
		if err := json.Unmarshal(raw, &c); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, c.Number)
	}
	return out
}

// A map box lists its cards with these filters, so each one must hold what
// its group counts: a lane is the cards of no story that carry it, and a
// story card is in no lane.
func TestCardListFiltersListTheCardsOfEachMapGroup(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	m := seedMap(e)

	for _, g := range walkGroups(e, userSecret, "lorvik", 200) {
		if g.Kind != "lane" {
			continue
		}
		path := "/projects/lorvik/cards?no_story=true&lane=" + g.Lane
		if g.Lane == "" {
			path = "/projects/lorvik/cards?no_story=true&no_lane=true"
		}
		for _, limit := range []int{200, 1} {
			if got := listNumbers(e, path, limit); len(got) != sumCounts(g.Counts) {
				t.Errorf("limit %d: %s lists %v, want %d cards like the group", limit, path, got, sumCounts(g.Counts))
			}
		}
	}

	got := listNumbers(e, "/projects/lorvik/cards?no_story=true&lane=a&status=ready", 200)
	if fmt.Sprint(got) != fmt.Sprint([]int64{m.laneA1}) {
		t.Errorf("lane a, ready = %v, want only #%d", got, m.laneA1)
	}
	for _, n := range listNumbers(e, "/projects/lorvik/cards?no_lane=true", 200) {
		if n == m.laneA1 || n == m.laneB {
			t.Errorf("no_lane listed #%d, which has a lane", n)
		}
	}
}

func TestCardListFilterConflictsAreRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	m := seedMap(e)
	for _, path := range []string{
		fmt.Sprintf("/projects/lorvik/cards?no_story=true&parent=%d", m.s1),
		"/projects/lorvik/cards?lane=a&no_lane=true",
	} {
		p := call[apitypes.Problem](e, userSecret, http.MethodGet, path, nil, http.StatusUnprocessableEntity)
		if len(p.Errors) == 0 {
			t.Errorf("%s: problem %+v names no field", path, p)
		}
	}
}
