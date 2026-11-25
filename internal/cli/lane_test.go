package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func TestNextSaysWhichLanesAreHoldingCardsBack(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		problem    apitypes.Problem
		wantStderr string
	}{
		{"several lanes", apitypes.Problem{Title: "Not Found", WaitingCards: 3, BusyLanes: []string{"schema", "web"}},
			"lorvik: 3 cards waiting on busy lanes: schema, web\n"},
		{"one card", apitypes.Problem{Title: "Not Found", WaitingCards: 1, BusyLanes: []string{"generated"}},
			"lorvik: 1 card waiting on busy lanes: generated\n"},
		{"nothing held back says nothing extra", apitypes.Problem{Title: "Not Found"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, serve(t, map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/next": problem(404, tt.problem),
			}))
			err := h.run(t, "-p", "demo", "next")
			if cmdutil.ExitCode(err) != 3 {
				t.Fatalf("exit = %d (%v), want 3", cmdutil.ExitCode(err), err)
			}
			if h.out.String() != "no ready cards\n" {
				t.Errorf("stdout = %q", h.out.String())
			}
			if h.errOut.String() != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", h.errOut.String(), tt.wantStderr)
			}
		})
	}
}

func TestShowPrintsBranchAndLaneOnTheMetaLine(t *testing.T) {
	t.Parallel()
	card := fixtureCard()
	card.Branch, card.Lane = "card-42-fix", "schema"
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42": reply(200, card),
	}))
	if err := h.run(t, "-p", "demo", "show", "42"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "  branch card-42-fix  lane schema\n") {
		t.Errorf("show output lacks branch and lane:\n%s", h.out.String())
	}
}

func TestShowNamesTheLengthAChatStatedForItsClaim(t *testing.T) {
	t.Parallel()
	card := fixtureCard()
	card.Claim.Lease = "1h0m0s"
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42": reply(200, card),
	}))
	if err := h.run(t, "-p", "demo", "show", "42"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "  lease in 28m of 1h0m0s") {
		t.Errorf("show output lacks the stated length:\n%s", h.out.String())
	}
}

func TestStartBranchIsSentAfterTheClaimWithTheClaimedVersion(t *testing.T) {
	t.Parallel()
	var got apitypes.UpdateCard
	claimed := fixtureCard()
	claimed.Version = 8
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"POST /api/v1/projects/demo/cards/42/claim": reply(200, claimed),
		"PATCH /api/v1/projects/demo/cards/42": func(w http.ResponseWriter, r *http.Request) {
			got = readJSON[apitypes.UpdateCard](t, r)
			reply(200, claimed)(w, r)
		},
	}))
	if err := h.run(t, "-p", "demo", "start", "42", "--branch", "card-42-fix"); err != nil {
		t.Fatal(err)
	}
	if got.Version != 8 || got.Branch == nil || *got.Branch != "card-42-fix" || got.Lane != nil {
		t.Errorf("update = %+v, want version 8 and only the branch", got)
	}
}

func TestForStatesTheLengthOfAClaim(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		path string
		want string
	}{
		{"start", []string{"start", "42", "--for", "1h"}, "claim", "1h"},
		{"claim", []string{"claim", "42", "--for", "90m"}, "claim", "90m"},
		{"heartbeat", []string{"heartbeat", "42", "--for", "2h"}, "renew", "2h"},
		{"heartbeat without a length", []string{"heartbeat", "42"}, "renew", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got apitypes.ClaimCard
			h := newHarness(t, serve(t, map[string]http.HandlerFunc{
				"POST /api/v1/projects/demo/cards/42/" + tt.path: func(w http.ResponseWriter, r *http.Request) {
					got = readJSON[apitypes.ClaimCard](t, r)
					reply(200, fixtureCard())(w, r)
				},
				"GET /api/v1/actors": reply(200, fixtureActors()),
			}))
			if err := h.run(t, append([]string{"-p", "demo"}, tt.args...)...); err != nil {
				t.Fatal(err)
			}
			if got.Lease != tt.want {
				t.Errorf("lease sent = %q, want %q", got.Lease, tt.want)
			}
		})
	}
	if err := newHarness(t, serve(t, nil)).run(t, "-p", "demo", "release", "42", "--for", "1h"); cmdutil.ExitCode(err) != 2 {
		t.Errorf("release --for: err = %v, want a usage error", err)
	}
}

func TestEditSendsBranchAndLaneAndEmptyClears(t *testing.T) {
	t.Parallel()
	var got apitypes.UpdateCard
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42": reply(200, fixtureCard()),
		"PATCH /api/v1/projects/demo/cards/42": func(w http.ResponseWriter, r *http.Request) {
			got = readJSON[apitypes.UpdateCard](t, r)
			reply(200, fixtureCard())(w, r)
		},
	}))
	if err := h.run(t, "-p", "demo", "edit", "42", "--branch", "", "--lane", "web"); err != nil {
		t.Fatal(err)
	}
	if got.Branch == nil || *got.Branch != "" || got.Lane == nil || *got.Lane != "web" || got.Title != nil {
		t.Errorf("update = %+v, want empty branch, lane web, nothing else", got)
	}
}

func TestProjectSetConcurrencySendsOnlyThatFieldAndRejectsOtherValues(t *testing.T) {
	t.Parallel()
	var got apitypes.UpdateProject
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"PATCH /api/v1/projects/demo": func(w http.ResponseWriter, r *http.Request) {
			got = readJSON[apitypes.UpdateProject](t, r)
			reply(200, apitypes.Project{Slug: "demo", Concurrency: "serial"})(w, r)
		},
	}))
	if err := h.run(t, "project", "set", "demo", "--concurrency", "serial"); err != nil {
		t.Fatal(err)
	}
	if got.Concurrency == nil || *got.Concurrency != "serial" || got.Name != nil || got.Lease != nil {
		t.Errorf("update = %+v, want only concurrency serial", got)
	}
	err := h.run(t, "project", "set", "demo", "--concurrency", "sideways")
	if cmdutil.ExitCode(err) != 2 {
		t.Errorf("exit = %d (%v), want usage error 2", cmdutil.ExitCode(err), err)
	}
}

func TestAddSendsTheLane(t *testing.T) {
	t.Parallel()
	var got apitypes.CreateCard
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"POST /api/v1/projects/demo/cards": func(w http.ResponseWriter, r *http.Request) {
			got = readJSON[apitypes.CreateCard](t, r)
			reply(201, fixtureCard())(w, r)
		},
	}))
	if err := h.run(t, "-p", "demo", "add", "Add a column", "--lane", "schema"); err != nil {
		t.Fatal(err)
	}
	if got.Lane != "schema" {
		t.Errorf("create = %+v, want lane schema", got)
	}
}
