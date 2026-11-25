package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func TestSubscribeAndUnsubscribeCallTheSubscriptionEndpointAndSayWhatTheyDid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		wantCall string
		wantOut  string
		wantJSON map[string]any
	}{
		{"subscribe", []string{"subscribe", "42"}, "PUT /api/v1/projects/demo/cards/42/subscription", "subscribed to #42 Fix login\n", nil},
		{"subscribe json", []string{"subscribe", "42", "--json"}, "PUT /api/v1/projects/demo/cards/42/subscription", "",
			map[string]any{"project": "demo", "number": float64(42), "title": "Fix login", "subscribed": true}},
		{"unsubscribe", []string{"unsubscribe", "42"}, "DELETE /api/v1/projects/demo/cards/42/subscription", "unsubscribed from #42\n", nil},
		{"unsubscribe json", []string{"unsubscribe", "42", "--json"}, "DELETE /api/v1/projects/demo/cards/42/subscription", "",
			map[string]any{"project": "demo", "number": float64(42), "subscribed": false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{}
			srv := serve(t, rec.wrapAll(map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/42":                 reply(200, fixtureCard()),
				"PUT /api/v1/projects/demo/cards/42/subscription":    reply(204, nil),
				"DELETE /api/v1/projects/demo/cards/42/subscription": reply(204, nil),
			}))
			h := newHarness(t, srv)
			if err := h.run(t, append([]string{"-p", "demo"}, tt.args...)...); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(rec.calls(), "\n"), tt.wantCall) {
				t.Errorf("calls = %v, want %q", rec.calls(), tt.wantCall)
			}
			if tt.wantJSON == nil {
				if h.out.String() != tt.wantOut {
					t.Errorf("output = %q, want %q", h.out.String(), tt.wantOut)
				}
				return
			}
			var got map[string]any
			if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
				t.Fatalf("output is not JSON: %v\n%s", err, h.out.String())
			}
			for k, v := range tt.wantJSON {
				if got[k] != v {
					t.Errorf("json %s = %v, want %v (all: %v)", k, got[k], v, got)
				}
			}
			if len(got) != len(tt.wantJSON) {
				t.Errorf("json fields = %v, want exactly %v", got, tt.wantJSON)
			}
		})
	}
}

func TestSubscribeRefusalFromTheServerIsNotHidden(t *testing.T) {
	t.Parallel()
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42":              reply(200, fixtureCard()),
		"PUT /api/v1/projects/demo/cards/42/subscription": problem(409, apitypes.Problem{Detail: "this chat is subscribed to 50 cards; unsubscribe from one first"}),
	})
	h := newHarness(t, srv)
	err := h.run(t, "-p", "demo", "subscribe", "42")
	if err == nil || !strings.Contains(err.Error(), "this chat is subscribed to 50 cards") {
		t.Fatalf("err = %v, want the server's 409 detail", err)
	}
}

func statusServer(t *testing.T, subs http.HandlerFunc) *httptest.Server {
	t.Helper()
	return serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo":       reply(200, apitypes.Project{Slug: "demo", Name: "Demo"}),
		"GET /api/v1/projects/demo/cards": reply(200, apitypes.CardList{Items: []apitypes.CardBrief{}}),
		"GET /api/v1/actors":              reply(200, fixtureActors()),
		"GET /api/v1/me/subscriptions":    subs,
	})
}

func TestStatusListsTheCardsThisChatIsSubscribedTo(t *testing.T) {
	t.Parallel()
	list := apitypes.SubscriptionList{Items: []apitypes.Subscription{
		{Project: "demo", Number: 42, Title: "Fix login", Status: "waiting", SubscribedAt: fixedNow.Add(-2 * time.Hour)},
	}}
	srv := statusServer(t, reply(200, list))
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "status"); err != nil {
		t.Fatal(err)
	}
	if want := "subscribed (this chat):\n  #42  demo  Fix login  since 2h ago\n"; !strings.Contains(h.out.String(), want) {
		t.Errorf("status output:\n%s\nwant it to contain %q", h.out.String(), want)
	}
	j := newHarness(t, srv)
	if err := j.run(t, "-p", "demo", "status", "--json"); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Subscribed []apitypes.Subscription `json:"subscribed"`
	}
	if err := json.Unmarshal(j.out.Bytes(), &got); err != nil || len(got.Subscribed) != 1 || got.Subscribed[0].Number != 42 {
		t.Errorf("json subscribed = %+v (err %v), want the one subscription", got.Subscribed, err)
	}
}

func TestStatusForACallerThatCannotSubscribeShowsNoSectionAndAnEmptyJSONList(t *testing.T) {
	t.Parallel()
	srv := statusServer(t, problem(422, apitypes.Problem{Detail: "subscribing is for an agent's chat; people see replies on the dashboard"}))
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "status"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.out.String(), "subscribed") {
		t.Errorf("status output has a subscribed section for a caller that cannot subscribe:\n%s", h.out.String())
	}
	j := newHarness(t, srv)
	if err := j.run(t, "-p", "demo", "status", "--json"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(j.out.String(), `"subscribed": []`) {
		t.Errorf("json lacks an empty subscribed list: %s", j.out.String())
	}
}
