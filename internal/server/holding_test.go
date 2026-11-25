package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

// heldOf decodes X-Lorvik-Holding; ok is false when the response has none.
func heldOf(t *testing.T, rec *httptest.ResponseRecorder) (apitypes.Holding, bool) {
	t.Helper()
	v := rec.Header().Get(apitypes.HeaderHolding)
	if v == "" {
		return apitypes.Holding{}, false
	}
	raw, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		t.Fatalf("%s = %q: %v", apitypes.HeaderHolding, v, err)
	}
	var h apitypes.Holding
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("%s decodes to %q: %v", apitypes.HeaderHolding, raw, err)
	}
	return h, true
}

func heldNumbers(h apitypes.Holding) []int64 {
	out := []int64{}
	for _, c := range h.Cards {
		out = append(out, c.Number)
	}
	return out
}

// A response says what the chat holds once the request has done its work:
// the claim that takes a card names it, the release that frees it does not.
// An agent's list is its session's; a person's covers every session.
func TestAResponseSaysWhatTheChatHoldsAfterTheRequest(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ask := func(secret, session, method, path string, body any) apitypes.Holding {
		t.Helper()
		hdr := []string{apitypes.HeaderHolding, "1"}
		if session != "" {
			hdr = append(hdr, apitypes.HeaderSession, session)
		}
		rec := e.do(secret, method, path, body, hdr...)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s = %d: %s", method, path, rec.Code, rec.Body)
		}
		h, ok := heldOf(t, rec)
		if !ok {
			t.Fatalf("%s %s: no %s header", method, path, apitypes.HeaderHolding)
		}
		return h
	}
	first := e.readyCard(userSecret, "first")
	second := e.readyCard(userSecret, strings.Repeat("ü", 70))
	theirs := e.readyCard(userSecret, "held by another chat")

	if rec := e.do(agentSecret, http.MethodGet, "/inbox", nil, apitypes.HeaderSession, "lead"); rec.Header().Get(apitypes.HeaderHolding) != "" {
		t.Errorf("a request that did not ask got %s", apitypes.HeaderHolding)
	}
	if h := ask(agentSecret, "lead", http.MethodGet, "/inbox", nil); h.Cards == nil || len(h.Cards) != 0 || h.More != 0 {
		t.Errorf("holding nothing = %+v, want an empty list", h)
	}

	if h := ask(agentSecret, "lead", http.MethodPost, cardPathOf(first.Number, "/claim"), apitypes.ClaimCard{}); !slices.Equal(heldNumbers(h), []int64{first.Number}) {
		t.Errorf("after the claim = %+v, want the card just claimed", h)
	}
	e.advance(time.Minute)
	ask(agentSecret, "other", http.MethodPost, cardPathOf(theirs.Number, "/claim"), apitypes.ClaimCard{})
	h := ask(agentSecret, "lead", http.MethodPost, cardPathOf(second.Number, "/claim"), apitypes.ClaimCard{Lease: "10m"})
	if !slices.Equal(heldNumbers(h), []int64{second.Number, first.Number}) {
		t.Fatalf("lead holds %v, want its two cards, the soonest to end first, and not the other chat's", heldNumbers(h))
	}
	if got := h.Cards[0]; got.Project != "lorvik" || got.Title != strings.Repeat("ü", apitypes.HoldingTitle)+"..." || !got.ExpiresAt.Equal(e.clock().Add(10*time.Minute)) {
		t.Errorf("held card = %+v, want project lorvik, the title cut to %d characters and the stated expiry", got, apitypes.HoldingTitle)
	}
	// A listener is told too, and telling it renews nothing.
	rec := e.do(agentSecret, http.MethodGet, "/inbox", nil, apitypes.HeaderSession, "lead", apitypes.HeaderHolding, "1", apitypes.HeaderListener, "1")
	if h, ok := heldOf(t, rec); !ok || len(h.Cards) != 2 {
		t.Errorf("listener's holding = %+v (sent %v), want both cards", h, ok)
	}

	if h := ask(agentSecret, "lead", http.MethodPost, cardPathOf(second.Number, "/release"), apitypes.ClaimCard{}); !slices.Equal(heldNumbers(h), []int64{first.Number}) {
		t.Errorf("after the release = %v, want only the first card", heldNumbers(h))
	}
	e.advance(31 * time.Minute)
	// The sweeper has not run: the lapsed claim is still on its card.
	if h := ask(agentSecret, "lead", http.MethodGet, "/inbox", nil); len(h.Cards) != 0 {
		t.Errorf("after the leases ran out = %v, want none", heldNumbers(h))
	}

	mine := e.readyCard(userSecret, "a person's card")
	ask(memberSecret, "", http.MethodPost, cardPathOf(mine.Number, "/claim"), apitypes.ClaimCard{})
	if h := ask(memberSecret, "laptop", http.MethodGet, "/inbox", nil); !slices.Equal(heldNumbers(h), []int64{mine.Number}) {
		t.Errorf("a person in another session holds %v, want their card", heldNumbers(h))
	}
}

func TestHoldingNamesAtMostTheCapAndCountsTheRest(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	var want []int64
	for i := range apitypes.HoldingCap + 2 {
		c := e.readyCard(userSecret, "card")
		e.claimAs(agentSecret, "lorvik", c.Number, "lead")
		e.advance(time.Second)
		if i < apitypes.HoldingCap {
			want = append(want, c.Number)
		}
	}
	rec := e.do(agentSecret, http.MethodGet, "/inbox", nil, apitypes.HeaderSession, "lead", apitypes.HeaderHolding, "1")
	h, ok := heldOf(t, rec)
	if !ok || !slices.Equal(heldNumbers(h), want) || h.More != 2 {
		t.Errorf("holding = %v and %d more (sent %v), want %v and 2 more", heldNumbers(h), h.More, ok, want)
	}
}

// Titles outside ASCII take up to four bytes a character: the header names
// fewer cards rather than grow past what a proxy's header buffer takes.
func TestTheHoldingHeaderStaysUnderItsLimit(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	const held = apitypes.HoldingCap
	for range held {
		c := e.readyCard(userSecret, strings.Repeat("🂡", 50))
		e.claimAs(agentSecret, "lorvik", c.Number, "lead")
	}
	rec := e.do(agentSecret, http.MethodGet, "/inbox", nil, apitypes.HeaderSession, "lead", apitypes.HeaderHolding, "1")
	h, ok := heldOf(t, rec)
	if n := len(rec.Header().Get(apitypes.HeaderHolding)); n > holdingHeaderMax {
		t.Errorf("header is %d bytes, want at most %d", n, holdingHeaderMax)
	}
	if !ok || len(h.Cards) == 0 || len(h.Cards) == held || len(h.Cards)+h.More != held {
		t.Errorf("holding names %d and counts %d more (sent %v), want some of the %d named and the rest counted", len(h.Cards), h.More, ok, held)
	}
}

func TestTheEventStreamIsNotAskedWhatTheChatHolds(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{"/inbox": true, "/events/stream": false, "/projects/lorvik/cards/1": true} {
		r := httptest.NewRequest(http.MethodGet, apiPrefix+path, nil)
		if wantsHolding(r) {
			t.Errorf("%s without the header: wantsHolding = true", path)
		}
		r.Header.Set(apitypes.HeaderHolding, "1")
		if got := wantsHolding(r); got != want {
			t.Errorf("%s: wantsHolding = %v, want %v", path, got, want)
		}
	}
}
