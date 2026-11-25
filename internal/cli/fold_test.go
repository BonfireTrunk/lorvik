package cli

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func TestFoldBriefSummaryComesFromClosingNote(t *testing.T) {
	t.Parallel()
	story := apitypes.Card{
		ID: "6020", Number: 20, Project: "demo", Kind: "story", Status: "working", Title: "Webhooks", Priority: "normal", Version: 7,
		Children: []apitypes.CardBrief{
			{ID: "6021", Number: 21, Kind: "task", Status: "done", Title: "Busy child", Priority: "normal"},
			{ID: "6022", Number: 22, Kind: "task", Status: "done", Title: "Quiet child", Priority: "normal"},
		},
	}
	busy := child(21, "Busy child", "")
	busy.ClosingNote = "  signing\nand retries  "
	for range 10 {
		busy.Events = append(busy.Events, storyEvent("card.commented", 21, `{"title":"Busy child"}`))
	}
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/actors":                          reply(200, fixtureActors()),
		"GET /api/v1/projects/demo/cards/20":          reply(200, story),
		"GET /api/v1/projects/demo/cards/21":          reply(200, busy),
		"GET /api/v1/projects/demo/cards/22":          reply(200, child(22, "Quiet child", "")),
		"GET /api/v1/projects/demo/cards/21/comments": reply(200, []apitypes.Comment{}),
		"GET /api/v1/projects/demo/cards/22/comments": reply(200, []apitypes.Comment{}),
	})
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "fold", "20", "--json"); err != nil {
		t.Fatal(err)
	}
	var got foldBrief
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(got.Children))
	}
	if want := "signing and retries"; got.Children[0].Summary != want {
		t.Errorf("busy summary = %q, want %q", got.Children[0].Summary, want)
	}
	if got.Children[1].Summary != "" || got.Children[1].Done != "" {
		t.Errorf("quiet child = %+v, want no summary or done", got.Children[1])
	}
	if strings.Contains(h.out.String(), `"summary":""`) {
		t.Errorf("empty summary serialised: %s", h.out.String())
	}
}

func TestFoldBriefFindsDoneCommentOnAnOlderPage(t *testing.T) {
	t.Parallel()
	story := apitypes.Card{
		ID: "6020", Number: 20, Project: "demo", Kind: "story", Status: "working", Title: "Webhooks", Priority: "normal", Version: 7,
		Children: []apitypes.CardBrief{{ID: "6021", Number: 21, Kind: "task", Status: "done", Title: "Busy child", Priority: "normal"}},
	}
	const oldestPageStart = apitypes.MaxPageSize + 5
	comments := func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("limit"); got != strconv.Itoa(apitypes.MaxPageSize) {
			t.Errorf("limit = %q, want %d", got, apitypes.MaxPageSize)
		}
		top := 2 * apitypes.MaxPageSize
		if before := r.URL.Query().Get("before"); before != "" {
			n, err := strconv.Atoi(before)
			if err != nil {
				t.Errorf("before = %q: %v", before, err)
			}
			top = n - 1
		}
		page := []apitypes.Comment{}
		for id := top; id > 0 && len(page) < apitypes.MaxPageSize; id-- {
			body := "chatter"
			switch id {
			case oldestPageStart:
				body = "Done: the newest\none"
			case oldestPageStart - 1:
				body = "Done: an earlier one"
			}
			page = append(page, apitypes.Comment{ID: strconv.Itoa(id), Body: body})
		}
		reply(200, page)(w, r)
	}
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/actors":                          reply(200, fixtureActors()),
		"GET /api/v1/projects/demo/cards/20":          reply(200, story),
		"GET /api/v1/projects/demo/cards/21":          reply(200, child(21, "Busy child", "")),
		"GET /api/v1/projects/demo/cards/21/comments": comments,
	})
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "fold", "20", "--json"); err != nil {
		t.Fatal(err)
	}
	var got foldBrief
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if want := "Done: the newest one"; len(got.Children) != 1 || got.Children[0].Done != want {
		t.Errorf("children = %+v, want done %q", got.Children, want)
	}
}

func TestFoldBriefCoversChildrenBeyondTheEmbeddedOnes(t *testing.T) {
	t.Parallel()
	brief := func(n int64, title string) apitypes.CardBrief {
		return apitypes.CardBrief{ID: strconv.FormatInt(n, 10), Number: n, Kind: "task", Status: "done", Title: title, Priority: "normal"}
	}
	story := apitypes.Card{
		ID: "6020", Number: 20, Project: "demo", Kind: "story", Status: "working", Title: "Webhooks", Priority: "normal", Version: 7,
		Children: []apitypes.CardBrief{brief(21, "First")}, ChildrenRemaining: 1,
	}
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/actors":                          reply(200, fixtureActors()),
		"GET /api/v1/projects/demo/cards/20":          reply(200, story),
		"GET /api/v1/projects/demo/cards":             reply(200, apitypes.CardList{Items: []apitypes.CardBrief{brief(21, "First"), brief(22, "Second")}}),
		"GET /api/v1/projects/demo/cards/21":          reply(200, child(21, "First", "")),
		"GET /api/v1/projects/demo/cards/22":          reply(200, child(22, "Second", "")),
		"GET /api/v1/projects/demo/cards/21/comments": reply(200, []apitypes.Comment{}),
		"GET /api/v1/projects/demo/cards/22/comments": reply(200, []apitypes.Comment{}),
	})
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "fold", "20", "--json"); err != nil {
		t.Fatal(err)
	}
	var got foldBrief
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Children) != 2 || got.Children[1].Card.Title != "Second" {
		t.Errorf("children = %+v, want First and Second", got.Children)
	}
}
