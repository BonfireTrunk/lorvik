package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func liveClaim(holder apitypes.Actor, session, project string, number int64, title string, claimedAgo, left time.Duration) apitypes.LiveClaim {
	return apitypes.LiveClaim{
		Card:      apitypes.LiveClaimCard{Project: project, Number: number, Title: title, Kind: "task", Status: "working"},
		Holder:    holder,
		Session:   session,
		ClaimedAt: ago(claimedAgo),
		ExpiresAt: fixedNow.Add(left),
	}
}

func fixtureLiveClaims() []apitypes.LiveClaim {
	return []apitypes.LiveClaim{
		liveClaim(ana, "", "web", 17, "Pick a queue", 2*time.Hour, 20*time.Minute),
		liveClaim(claude, "brisk-otter", "demo", 42, "Fix login", 10*time.Minute, 28*time.Minute+30*time.Second),
		liveClaim(claude, "brisk-otter", "web", 7, "Index the events", 40*time.Second, 29*time.Minute),
		liveClaim(claude, "quiet-comet", "demo", 44, "Add rate limiting", 3*time.Hour, 5*time.Minute),
	}
}

func TestWhoGroupsByHolderAndSessionAndSummarises(t *testing.T) {
	t.Parallel()
	claims := fixtureLiveClaims()
	claims[3].SessionName = "Rate limits"
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/claims": reply(200, apitypes.LiveClaimList{Items: claims}),
		"GET /api/v1/actors": reply(200, fixtureActors()),
	}))
	if err := h.run(t, "who"); err != nil {
		t.Fatal(err)
	}
	want := "Ana Souza  person\n" +
		"  #17  web  Pick a queue  claimed 2h ago  20m left\n" +
		"claude-code  agent  session brisk-otter\n" +
		"  #42  demo  Fix login  claimed 10m ago  29m left\n" +
		"  #7  web  Index the events  claimed now  29m left\n" +
		"claude-code  agent  session Rate limits (quiet-comet)\n" +
		"  #44  demo  Add rate limiting  claimed 3h ago  5m left\n" +
		"3 chats of 1 agent and 1 person working in 2 projects\n"
	if got := h.out.String(); got != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", got, want)
	}
	if h.errOut.Len() != 0 {
		t.Errorf("stderr = %q, want nothing", h.errOut.String())
	}
}

func TestWhoSaysSoWhenNothingIsLive(t *testing.T) {
	t.Parallel()
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{"GET /api/v1/claims": reply(200, apitypes.LiveClaimList{})}))
	if err := h.run(t, "who"); err != nil {
		t.Fatal(err)
	}
	if got, want := h.out.String(), "nobody is working on anything\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestWhoIsOrgWideAndFollowsEveryPageForTextAndJSON(t *testing.T) {
	t.Parallel()
	all := fixtureLiveClaims()
	var rec recorder
	routes := rec.wrapAll(map[string]http.HandlerFunc{
		"GET /api/v1/claims": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("cursor") == "" {
				reply(200, apitypes.LiveClaimList{Items: all[:3], Next: "tok2", Remaining: 1})(w, r)
				return
			}
			reply(200, apitypes.LiveClaimList{Items: all[3:]})(w, r)
		},
		"GET /api/v1/actors": reply(200, fixtureActors()),
	})
	text := newHarness(t, serve(t, routes))
	if err := text.run(t, "-p", "demo", "who"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(text.out.String(), "3 chats of 1 agent and 1 person working in 2 projects\n") {
		t.Errorf("text summary over two pages = %q, want all 4 cards counted", text.out.String())
	}
	for _, c := range rec.calls() {
		if strings.Contains(c, "project=") {
			t.Errorf("request %q narrows to a project, want the org-wide list", c)
		}
	}

	js := newHarness(t, serve(t, routes))
	if err := js.run(t, "--json", "who"); err != nil {
		t.Fatal(err)
	}
	var got apitypes.LiveClaimList
	if err := json.Unmarshal(js.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not the list JSON: %v\n%s", err, js.out)
	}
	if len(got.Items) != 4 || got.Next != "" || got.Remaining != 0 {
		t.Errorf("json = %+v, want the 4 claims of both pages and no more to read", got)
	}
	if got.Items[1] != all[1] {
		t.Errorf("claim = %+v, want the server's row %+v", got.Items[1], all[1])
	}
}

func TestWhoJSONIsAnEmptyArrayWhenNothingIsLive(t *testing.T) {
	t.Parallel()
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{"GET /api/v1/claims": reply(200, apitypes.LiveClaimList{})}))
	if err := h.run(t, "--json", "who"); err != nil {
		t.Fatal(err)
	}
	var got apitypes.LiveClaimList
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Items == nil || len(got.Items) != 0 || !strings.Contains(h.out.String(), `"items": []`) {
		t.Errorf("json = %s, want an items array that is empty, not null", h.out)
	}
}

func TestWhoSummaryWordsChatsAgentsPeopleAndProjects(t *testing.T) {
	t.Parallel()
	bo := apitypes.Actor{Kind: "user", ID: "u-bo"}
	cases := []struct {
		name   string
		claims []apitypes.LiveClaim
		want   string
	}{
		{"one chat, singular", []apitypes.LiveClaim{liveClaim(claude, "a", "web", 1, "t", 0, time.Hour)}, "1 chat of 1 agent working in 1 project"},
		{"five chats of one agent in two projects", []apitypes.LiveClaim{
			liveClaim(claude, "a", "web", 1, "t", 0, time.Hour),
			liveClaim(claude, "b", "web", 2, "t", 0, time.Hour),
			liveClaim(claude, "c", "demo", 3, "t", 0, time.Hour),
			liveClaim(claude, "d", "demo", 4, "t", 0, time.Hour),
			liveClaim(claude, "e", "web", 5, "t", 0, time.Hour),
		}, "5 chats of 1 agent working in 2 projects"},
		{"two people", []apitypes.LiveClaim{
			liveClaim(ana, "", "web", 1, "t", 0, time.Hour),
			liveClaim(bo, "", "web", 2, "t", 0, time.Hour),
		}, "2 chats of 2 people working in 1 project"},
		{"agents and a person", []apitypes.LiveClaim{
			liveClaim(ana, "", "web", 1, "t", 0, time.Hour),
			liveClaim(claude, "a", "web", 2, "t", 0, time.Hour),
			liveClaim(apitypes.Actor{Kind: "agent", ID: "other"}, "b", "demo", 3, "t", 0, time.Hour),
		}, "3 chats of 2 agents and 1 person working in 2 projects"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := whoSummary(tc.claims); got != tc.want {
				t.Errorf("whoSummary = %q, want %q", got, tc.want)
			}
		})
	}
}
