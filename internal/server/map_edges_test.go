package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func walkLinks(e *testEnv, secret, slug string, limit int) []apitypes.ProjectLink {
	e.t.Helper()
	var out []apitypes.ProjectLink
	for _, raw := range walkList(e, secret, "/projects/"+slug+"/links", limit) {
		var l apitypes.ProjectLink
		if err := json.Unmarshal(raw, &l); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, l)
	}
	return out
}

func walkMentions(e *testEnv, secret, slug string, limit int) []apitypes.ProjectMention {
	e.t.Helper()
	var out []apitypes.ProjectMention
	for _, raw := range walkList(e, secret, "/projects/"+slug+"/mentions", limit) {
		var m apitypes.ProjectMention
		if err := json.Unmarshal(raw, &m); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func (e *testEnv) linkCards(slug string, from int64, kind string, to int64, toProject string) {
	e.t.Helper()
	call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/"+slug+"/cards/"+itoa(from)+"/links",
		apitypes.LinkCards{Kind: kind, To: to, Project: toProject}, http.StatusOK)
}

func TestProjectLinksPageEveryEdgeOnceInBothDirections(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	var own []int64
	for i := range 5 {
		own = append(own, e.newCard(userSecret, apitypes.CreateCard{Title: fmt.Sprintf("c%d", i), Status: "ready"}).Number)
	}
	foreign := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/global/cards", apitypes.CreateCard{Title: "far", Status: "ready"}, http.StatusCreated)
	e.linkCards("lorvik", own[0], "blocks", own[1], "")
	e.linkCards("lorvik", own[0], "relates", own[1], "")
	e.linkCards("lorvik", own[1], "blocks", own[2], "")
	e.linkCards("lorvik", own[3], "relates", own[4], "")
	e.linkCards("lorvik", own[2], "blocks", foreign.Number, "global")
	e.linkCards("global", foreign.Number, "relates", own[4], "lorvik")
	const edges = 6

	all := walkLinks(e, userSecret, "lorvik", 200)
	if len(all) != edges {
		t.Fatalf("links = %+v, want %d", all, edges)
	}
	for _, limit := range []int{1, 2, 4} {
		got := walkLinks(e, userSecret, "lorvik", limit)
		if fmt.Sprint(got) != fmt.Sprint(all) {
			t.Errorf("limit %d walk = %+v, want %+v (each edge once, same order)", limit, got, all)
		}
	}

	var outbound, inbound int
	for _, l := range all {
		switch {
		case l.To.Project == "global" && l.From.Project == "":
			outbound++
		case l.From.Project == "global" && l.To.Project == "":
			inbound++
		}
	}
	if outbound != 1 || inbound != 1 {
		t.Errorf("cross-project edges: %d outbound, %d inbound, want 1 each in %+v", outbound, inbound, all)
	}
	if theirs := walkLinks(e, userSecret, "global", 200); len(theirs) != 2 {
		t.Errorf("global's links = %+v, want the two cross-project edges", theirs)
	}
}

func TestProjectLinksFirstPageReportsRemainingAndStopsNext(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a := e.newCard(userSecret, apitypes.CreateCard{Title: "a", Status: "ready"}).Number
	b := e.newCard(userSecret, apitypes.CreateCard{Title: "b", Status: "ready"}).Number
	e.linkCards("lorvik", a, "blocks", b, "")
	e.linkCards("lorvik", a, "relates", b, "")
	first := call[apitypes.ProjectLinkList](e, userSecret, http.MethodGet, "/projects/lorvik/links?limit=1", nil, http.StatusOK)
	if len(first.Items) != 1 || first.Remaining != 1 || first.Next == "" || first.Prev != "" {
		t.Errorf("first page = %+v, want one item, remaining 1, next set, no prev", first)
	}
	last := call[apitypes.ProjectLinkList](e, userSecret, http.MethodGet, "/projects/lorvik/links?limit=1&cursor="+first.Next, nil, http.StatusOK)
	if len(last.Items) != 1 || last.Remaining != 0 || last.Next != "" {
		t.Errorf("last page = %+v, want one item and no next", last)
	}
	if rec := e.do(userSecret, http.MethodGet, "/projects/lorvik/links?cursor="+first.Next+"x", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad cursor = %d, want 400", rec.Code)
	}
	if rec := e.do(userSecret, http.MethodGet, "/projects/lorvik/mentions?cursor="+first.Next, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("link cursor on mentions = %d, want 400", rec.Code)
	}
	if rec := e.do(userSecret, http.MethodGet, "/projects/nope/links", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project = %d, want 404", rec.Code)
	}
}

func TestProjectLinksLeaveOutCardsInProjectsTheReaderCannotSee(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "secret", Name: "Secret", Visibility: "private"}, http.StatusCreated)
	g1 := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/global/cards", apitypes.CreateCard{Title: "g1", Status: "ready"}, http.StatusCreated).Number
	g2 := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/global/cards", apitypes.CreateCard{Title: "g2", Status: "ready"}, http.StatusCreated).Number
	hidden := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/secret/cards", apitypes.CreateCard{Title: "h", Status: "ready"}, http.StatusCreated).Number
	e.linkCards("global", g1, "blocks", g2, "")
	e.linkCards("global", g1, "relates", hidden, "secret")
	e.linkCards("secret", hidden, "blocks", g2, "global")

	if got := walkLinks(e, userSecret, "global", 2); len(got) != 3 {
		t.Errorf("owner links = %+v, want 3", got)
	}
	got := walkLinks(e, agentSecret, "global", 2)
	if len(got) != 1 || got[0].From.Number != g1 || got[0].To.Number != g2 || got[0].From.Project != "" || got[0].To.Project != "" {
		t.Errorf("agent links = %+v, want only g1 blocks g2", got)
	}
}

func TestProjectMentionsPageAndHideNotesTheReaderCannotSee(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "secret", Name: "Secret", Visibility: "private"}, http.StatusCreated)
	for _, n := range []apitypes.CreateNote{
		{Project: "lorvik", Slug: "arch", Title: "Arch", Body: "x"},
		{Project: "lorvik", Slug: "style", Title: "Style", Body: "x"},
		{Project: "global", Slug: "handbook", Title: "Handbook", Body: "x"},
		{Project: "secret", Slug: "vault", Title: "Vault", Body: "x"},
	} {
		call[apitypes.Note](e, userSecret, http.MethodPost, "/notes", n, http.StatusCreated)
	}
	one := e.newCard(userSecret, apitypes.CreateCard{Title: "one", Body: "[[note:arch]] [[note:style]] [[note:secret/vault]]"}).Number
	two := e.newCard(userSecret, apitypes.CreateCard{Title: "two", Body: "[[note:handbook]] [[card:1]]"}).Number
	call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/secret/cards", apitypes.CreateCard{Title: "elsewhere", Body: "[[note:arch]]"}, http.StatusCreated)

	owner := walkMentions(e, userSecret, "lorvik", 200)
	if len(owner) != 4 {
		t.Fatalf("owner mentions = %+v, want 4", owner)
	}
	for _, limit := range []int{1, 3} {
		if got := walkMentions(e, userSecret, "lorvik", limit); fmt.Sprint(got) != fmt.Sprint(owner) {
			t.Errorf("limit %d walk = %+v, want %+v", limit, got, owner)
		}
	}
	agent := walkMentions(e, agentSecret, "lorvik", 1)
	slugs := map[int64][]string{}
	for _, m := range agent {
		if m.Note.ID == "" {
			t.Errorf("mention %+v has no note id", m)
		}
		slugs[m.Card] = append(slugs[m.Card], m.Note.Slug)
	}
	if len(agent) != 3 || len(slugs[one]) != 2 || len(slugs[two]) != 1 || slugs[two][0] != "handbook" {
		t.Errorf("agent mentions = %+v, want arch and style on #%d, handbook on #%d, nothing from the private note", agent, one, two)
	}
}
