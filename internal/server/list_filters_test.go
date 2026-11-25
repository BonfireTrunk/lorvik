package server

import (
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func ts(d time.Duration) string { return url.QueryEscape(t0.Add(d).Format(time.RFC3339)) }

func cardNumbers(l apitypes.CardList) []int64 {
	out := []int64{}
	for _, c := range l.Items {
		out = append(out, c.Number)
	}
	return out
}

// filterEnv seeds four cards over three hours:
//
//	#1 low     created 0h, claimed by the agent at 2h
//	#2 urgent  created 0h, untouched
//	#3 high    created 1h, claimed by the member at 2h
//	#4 normal  created 1h, claimed by codex at 2h, done at 3h
func filterEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newEnv(t)
	e.newCard(agentSecret, apitypes.CreateCard{Title: "one", Status: "ready", Priority: "low"})
	e.newCard(agentSecret, apitypes.CreateCard{Title: "two", Status: "ready", Priority: "urgent"})
	e.advance(time.Hour)
	e.newCard(agentSecret, apitypes.CreateCard{Title: "three", Status: "ready", Priority: "high"})
	e.newCard(agentSecret, apitypes.CreateCard{Title: "four", Status: "ready", Priority: "normal"})
	e.advance(time.Hour)
	e.claim(agentSecret, 1)
	e.claim(memberSecret, 3)
	e.claim(agent2Secret, 4)
	e.advance(time.Hour)
	call[apitypes.Card](e, agent2Secret, http.MethodPost, cardPathOf(4, "/transition"),
		apitypes.TransitionCard{Version: e.show(agent2Secret, 4).Version, Status: "done"}, http.StatusOK)
	return e
}

func TestListCardsFilters(t *testing.T) {
	t.Parallel()
	e := filterEnv(t)
	tests := []struct {
		name  string
		query string
		want  []int64
	}{
		{"priority single", "priority=urgent", []int64{2}},
		{"priority several", "priority=low,high&sort=number", []int64{1, 3}},
		{"created after is inclusive", "created_after=" + ts(time.Hour) + "&sort=number", []int64{3, 4}},
		{"created before is exclusive", "created_before=" + ts(time.Hour) + "&sort=number", []int64{1, 2}},
		{"created window", "created_after=" + ts(-time.Hour) + "&created_before=" + ts(time.Hour) + "&sort=number", []int64{1, 2}},
		{"updated after", "updated_after=" + ts(2*time.Hour) + "&sort=number", []int64{1, 3, 4}},
		{"updated before", "updated_before=" + ts(2*time.Hour), []int64{2}},
		{"updated window", "updated_after=" + ts(2*time.Hour) + "&updated_before=" + ts(3*time.Hour) + "&sort=number", []int64{1, 3}},
		{"closed after skips never closed", "closed_after=" + ts(0), []int64{4}},
		{"closed before the close", "closed_before=" + ts(3*time.Hour), []int64{}},
		{"closed combined with open is empty", "closed_after=" + ts(0) + "&open=true", []int64{}},
		{"assignee me", "assignee=me", []int64{1}},
		{"assignee id", "assignee=" + e.member.ID.String(), []int64{3}},
		{"assignee none", "assignee=none" + "&sort=number", []int64{2, 4}},
		{"assignee with priority", "assignee=none&priority=urgent", []int64{2}},
		{"sort created", "sort=created", []int64{1, 2, 3, 4}},
		{"sort created desc", "sort=created&desc=true", []int64{4, 3, 2, 1}},
		{"sort updated", "sort=updated", []int64{2, 1, 3, 4}},
		{"sort updated desc", "sort=updated&desc=true", []int64{4, 3, 1, 2}},
		{"sort priority", "sort=priority", []int64{1, 4, 3, 2}},
		{"sort priority desc", "sort=priority&desc=true", []int64{2, 3, 4, 1}},
		{"sort number desc", "sort=number&desc=true", []int64{4, 3, 2, 1}},
		{"filter and sort", "created_after=" + ts(time.Hour) + "&sort=priority&desc=true", []int64{3, 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := call[apitypes.CardList](e, agentSecret, http.MethodGet, "/projects/lorvik/cards?"+tt.query, nil, http.StatusOK)
			if !slices.Equal(cardNumbers(got), tt.want) {
				t.Errorf("?%s = %v, want %v", tt.query, cardNumbers(got), tt.want)
			}
		})
	}
}

func TestListCardsPagesInTheChosenSort(t *testing.T) {
	t.Parallel()
	e := filterEnv(t)
	for _, tt := range []struct {
		query string
		pages [][]int64
	}{
		{"sort=updated&desc=true", [][]int64{{4, 3}, {1, 2}}},
		{"sort=updated", [][]int64{{2, 1}, {3, 4}}},
		{"sort=created&desc=true", [][]int64{{4, 3}, {2, 1}}},
		{"sort=priority", [][]int64{{1, 4}, {3, 2}}},
		{"sort=number", [][]int64{{1, 2}, {3, 4}}},
	} {
		first := call[apitypes.CardList](e, agentSecret, http.MethodGet, "/projects/lorvik/cards?limit=2&"+tt.query, nil, http.StatusOK)
		if !slices.Equal(cardNumbers(first), tt.pages[0]) || first.Remaining != 2 {
			t.Fatalf("?%s page 1 = %v remaining=%d, want %v", tt.query, cardNumbers(first), first.Remaining, tt.pages[0])
		}
		second := call[apitypes.CardList](e, agentSecret, http.MethodGet, "/projects/lorvik/cards?limit=2&"+tt.query+"&cursor="+p2cursor(first.Next), nil, http.StatusOK)
		if !slices.Equal(cardNumbers(second), tt.pages[1]) || second.Remaining != 0 {
			t.Fatalf("?%s page 2 = %v remaining=%d, want %v", tt.query, cardNumbers(second), second.Remaining, tt.pages[1])
		}
		back := call[apitypes.CardList](e, agentSecret, http.MethodGet, "/projects/lorvik/cards?limit=2&"+tt.query+"&cursor="+p2cursor(second.Prev), nil, http.StatusOK)
		if !slices.Equal(cardNumbers(back), tt.pages[0]) {
			t.Errorf("?%s back = %v, want %v", tt.query, cardNumbers(back), tt.pages[0])
		}
	}
}

func TestListCardsRejectsACursorFromAnotherSort(t *testing.T) {
	t.Parallel()
	e := filterEnv(t)
	token := func(query string) string {
		return call[apitypes.CardList](e, agentSecret, http.MethodGet, "/projects/lorvik/cards?limit=1&"+query, nil, http.StatusOK).Next
	}
	tests := []struct{ name, issuedFor, usedWith string }{
		{"board cursor on a sort", "", "sort=created"},
		{"sort cursor on board order", "sort=created", ""},
		{"other sort", "sort=created", "sort=updated"},
		{"other direction", "sort=created", "sort=created&desc=true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := e.problem(agentSecret, http.MethodGet, "/projects/lorvik/cards?"+tt.usedWith+"&cursor="+p2cursor(token(tt.issuedFor)), nil, http.StatusBadRequest)
			if p.Detail != "invalid cursor" {
				t.Errorf("detail = %q, want invalid cursor", p.Detail)
			}
		})
	}
}

func TestListCardsRejectsBadFilters(t *testing.T) {
	t.Parallel()
	e := filterEnv(t)
	tests := []struct{ name, query, field string }{
		{"date not RFC 3339", "created_after=yesterday", "created_after"},
		{"empty window", "updated_after=" + ts(time.Hour) + "&updated_before=" + ts(time.Hour), "updated_before"},
		{"assignee not an id", "assignee=bob", "assignee"},
		{"desc without sort", "desc=true", "desc"},
		{"sort with text query", "sort=created&q=one", "sort"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := e.problem(agentSecret, http.MethodGet, "/projects/lorvik/cards?"+tt.query, nil, http.StatusUnprocessableEntity)
			if !hasField(p, tt.field) {
				t.Errorf("errors = %+v, want field %s", p.Errors, tt.field)
			}
		})
	}
}

func TestListNotesFilters(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	post := func(secret, slug string) {
		call[apitypes.Note](e, secret, http.MethodPost, "/notes",
			apitypes.CreateNote{Project: "lorvik", Slug: slug, Title: slug, Body: "x"}, http.StatusCreated)
	}
	post(agentSecret, "alpha")
	e.advance(time.Hour)
	post(userSecret, "beta")
	e.advance(time.Hour)
	post(agentSecret, "gamma")
	slugs := func(query string) []string {
		l := call[apitypes.NoteList](e, agentSecret, http.MethodGet, "/notes?"+query, nil, http.StatusOK)
		out := []string{}
		for _, n := range l.Items {
			out = append(out, n.Slug)
		}
		return out
	}
	tests := []struct {
		name, query string
		want        []string
	}{
		{"author me", "author=me", []string{"gamma", "alpha"}},
		{"author id", "author=" + e.user.ID.String(), []string{"beta"}},
		{"created after", "created_after=" + ts(time.Hour), []string{"gamma", "beta"}},
		{"created before", "created_before=" + ts(time.Hour), []string{"alpha"}},
		{"updated window", "updated_after=" + ts(time.Hour) + "&updated_before=" + ts(2*time.Hour), []string{"beta"}},
		{"author and date", "author=me&created_before=" + ts(time.Hour), []string{"alpha"}},
	}
	for _, tt := range tests {
		if got := slugs(tt.query); !slices.Equal(got, tt.want) {
			t.Errorf("%s: ?%s = %v, want %v", tt.name, tt.query, got, tt.want)
		}
	}
	p := e.problem(agentSecret, http.MethodGet, "/notes?created_after=soon", nil, http.StatusUnprocessableEntity)
	if !hasField(p, "created_after") {
		t.Errorf("errors = %+v, want created_after", p.Errors)
	}
}
