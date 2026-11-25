package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func (e *testEnv) claimAs(secret, slug string, number int64, session string) {
	e.t.Helper()
	rec := e.do(secret, http.MethodPost, fmt.Sprintf("/projects/%s/cards/%d/claim", slug, number), apitypes.ClaimCard{}, apitypes.HeaderSession, session)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("claim %s #%d = %d: %s", slug, number, rec.Code, rec.Body)
	}
}

func walkLiveClaims(e *testEnv, secret, path string, limit int) []apitypes.LiveClaim {
	e.t.Helper()
	var out []apitypes.LiveClaim
	for _, raw := range walkList(e, secret, path, limit) {
		var c apitypes.LiveClaim
		if err := json.Unmarshal(raw, &c); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

func claimKeys(cs []apitypes.LiveClaim) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = fmt.Sprintf("%s/%d", c.Card.Project, c.Card.Number)
	}
	return out
}

func TestLiveClaimsCarryCardHolderAndLeaseAndMarkOnlyTheCallersOwn(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task := e.readyCard(userSecret, "ship the panel")
	e.claimAs(agentSecret, "lorvik", task.Number, "brisk-otter")

	for _, tt := range []struct {
		name, secret, session string
		mine                  bool
	}{
		{"same agent and session", agentSecret, "brisk-otter", true},
		{"same agent, another session", agentSecret, "quiet-heron", false},
		{"another actor", userSecret, "", false},
	} {
		rec := e.do(tt.secret, http.MethodGet, "/claims", nil, apitypes.HeaderSession, tt.session)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: GET /claims = %d: %s", tt.name, rec.Code, rec.Body)
		}
		var list apitypes.LiveClaimList
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		if len(list.Items) != 1 {
			t.Fatalf("%s: claims = %+v, want one", tt.name, list.Items)
		}
		got := list.Items[0]
		want := apitypes.LiveClaim{
			Card:      apitypes.LiveClaimCard{Project: "lorvik", Number: task.Number, Title: "ship the panel", Kind: "task", Status: "working"},
			Holder:    apitypes.Actor{Kind: "agent", ID: e.agent.ID.String()},
			Session:   "brisk-otter",
			ClaimedAt: t0,
			ExpiresAt: t0.Add(30 * time.Minute),
			Mine:      tt.mine,
		}
		if got != want {
			t.Errorf("%s: claim = %+v, want %+v", tt.name, got, want)
		}
	}
}

func TestLiveClaimsLeaveOutExpiredLeasesTheSweeperHasNotDeleted(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	old := e.readyCard(userSecret, "old lease")
	fresh := e.readyCard(userSecret, "fresh lease")
	e.claimAs(agent2Secret, "lorvik", old.Number, "s1")
	e.advance(20 * time.Minute)
	e.claimAs(agentSecret, "lorvik", fresh.Number, "s1")
	e.advance(11 * time.Minute)

	got := call[apitypes.LiveClaimList](e, userSecret, http.MethodGet, "/claims", nil, http.StatusOK).Items
	if len(got) != 1 || got[0].Card.Number != fresh.Number {
		t.Errorf("claims = %+v, want only #%d; #%d's lease ended a minute ago", claimKeys(got), fresh.Number, old.Number)
	}
	if n := len(e.fs.claims); n != 2 {
		t.Fatalf("store holds %d claim rows, want both, so the test covers an undeleted expired row", n)
	}
}

func TestLiveClaimsDoNotLeakProjectsTheCallerCannotSee(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "secret", Name: "Secret", Visibility: "private"}, http.StatusCreated)
	open := e.readyCard(userSecret, "open work")
	hidden := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/secret/cards", apitypes.CreateCard{Title: "hidden work", Status: "ready"}, http.StatusCreated)
	e.claimAs(userSecret, "lorvik", open.Number, "")
	e.claimAs(userSecret, "secret", hidden.Number, "")

	if got := claimKeys(call[apitypes.LiveClaimList](e, userSecret, http.MethodGet, "/claims", nil, http.StatusOK).Items); len(got) != 2 {
		t.Errorf("owner sees %v, want both claims", got)
	}
	got := call[apitypes.LiveClaimList](e, agentSecret, http.MethodGet, "/claims", nil, http.StatusOK).Items
	if len(got) != 1 || got[0].Card.Project != "lorvik" {
		t.Errorf("agent sees %v, want only lorvik's claim", claimKeys(got))
	}
	e.problem(agentSecret, http.MethodGet, "/claims?project=secret", nil, http.StatusNotFound)
	if rec := e.do(agentSecret, http.MethodGet, "/claims?project=nope", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project = %d, want 404, the same as a hidden one", rec.Code)
	}
}

func TestLiveClaimsProjectFilterNarrowsToOneProject(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a := e.readyCard(userSecret, "in lorvik")
	g := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/global/cards", apitypes.CreateCard{Title: "in global", Status: "ready"}, http.StatusCreated)
	e.claimAs(userSecret, "lorvik", a.Number, "")
	e.claimAs(userSecret, "global", g.Number, "")

	if got := claimKeys(call[apitypes.LiveClaimList](e, userSecret, http.MethodGet, "/claims", nil, http.StatusOK).Items); len(got) != 2 {
		t.Fatalf("org-wide = %v, want both", got)
	}
	got := call[apitypes.LiveClaimList](e, userSecret, http.MethodGet, "/claims?project=global", nil, http.StatusOK).Items
	if len(got) != 1 || got[0].Card.Project != "global" || got[0].Card.Number != g.Number {
		t.Errorf("project=global = %v, want only global's claim", claimKeys(got))
	}
}

func TestLiveClaimsPageEveryRowOnceInHolderSessionCardOrder(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	type held struct {
		secret, session string
		cards           int
	}
	for _, h := range []held{{agent2Secret, "b", 2}, {agent2Secret, "a", 2}, {agentSecret, "z", 2}, {userSecret, "", 2}, {memberSecret, "", 1}} {
		for i := range h.cards {
			c := e.readyCard(userSecret, fmt.Sprintf("%s-%s-%d", h.secret, h.session, i))
			e.claimAs(h.secret, "lorvik", c.Number, h.session)
		}
	}
	all := walkLiveClaims(e, userSecret, "/claims", 200)
	if len(all) != 9 {
		t.Fatalf("claims = %v, want 9", claimKeys(all))
	}
	var numbers []int64
	for _, c := range all {
		numbers = append(numbers, c.Card.Number)
	}
	if want := []int64{7, 8, 9, 5, 6, 3, 4, 1, 2}; !slices.Equal(numbers, want) {
		t.Errorf("claim order = %v, want %v: users before agents, then holder id, session, card", numbers, want)
	}
	for _, limit := range []int{1, 2, 4} {
		got := walkLiveClaims(e, userSecret, "/claims", limit)
		if fmt.Sprint(claimKeys(got)) != fmt.Sprint(claimKeys(all)) {
			t.Errorf("limit %d walk = %v, want %v (each claim once, same order)", limit, claimKeys(got), claimKeys(all))
		}
	}
	first := call[apitypes.LiveClaimList](e, userSecret, http.MethodGet, "/claims?limit=4", nil, http.StatusOK)
	if len(first.Items) != 4 || first.Remaining != 5 || first.Next == "" || first.Prev != "" {
		t.Errorf("first page = %d items, remaining %d, next %q, prev %q; want 4, 5, set, empty", len(first.Items), first.Remaining, first.Next, first.Prev)
	}
	e.problem(userSecret, http.MethodGet, "/claims?cursor=garbage", nil, http.StatusBadRequest)
	e.problem(userSecret, http.MethodGet, "/claims?cursor="+cursor{List: linkList}.encode(), nil, http.StatusBadRequest)
}
