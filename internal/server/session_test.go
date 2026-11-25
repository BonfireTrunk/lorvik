package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func TestBadSessionLabelIsRejectedWith422(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, label := range []string{"Upper", "has space", "under_score", "ünï", "a-label-that-is-way-longer-than-forty-chars"} {
		rec := e.do(agentSecret, http.MethodGet, "/projects/lorvik", nil, apitypes.HeaderSession, label)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("session %q = %d, want 422: %s", label, rec.Code, rec.Body)
		}
	}
	for _, label := range []string{"", "brisk-otter", "a1-b2"} {
		if rec := e.do(agentSecret, http.MethodGet, "/projects/lorvik", nil, apitypes.HeaderSession, label); rec.Code != http.StatusOK {
			t.Errorf("session %q = %d, want 200: %s", label, rec.Code, rec.Body)
		}
	}
}

func TestClaimRecordsTheSessionAndRenewalsKeepIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	post := func(n int64, suffix, session string) apitypes.Card {
		t.Helper()
		var hdr []string
		if session != "" {
			hdr = []string{apitypes.HeaderSession, session}
		}
		rec := e.do(agentSecret, http.MethodPost, cardPathOf(n, suffix), apitypes.ClaimCard{}, hdr...)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s = %d: %s", suffix, rec.Code, rec.Body)
		}
		var c apitypes.Card
		if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	withSession := e.readyCard(agentSecret, "with session")
	if c := post(withSession.Number, "/claim", "brisk-otter"); c.Claim == nil || c.Claim.Session != "brisk-otter" {
		t.Fatalf("claim = %+v, want session brisk-otter", c.Claim)
	}
	if c := post(withSession.Number, "/renew", "brisk-otter"); c.Claim == nil || c.Claim.Session != "brisk-otter" {
		t.Errorf("renewed claim = %+v, want the original session kept", c.Claim)
	}
	if c := e.show(agentSecret, withSession.Number); c.Claim == nil || c.Claim.Session != "brisk-otter" {
		t.Errorf("shown claim = %+v, want session brisk-otter", c.Claim)
	}
	none := e.readyCard(agentSecret, "no session")
	if c := post(none.Number, "/claim", ""); c.Claim == nil || c.Claim.Session != "" {
		t.Errorf("claim without header = %+v, want empty session", c.Claim)
	}
}

func TestAgentClaimBelongsToTheSessionThatTookIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(agentSecret, "one chat's card")
	if rec := e.do(agentSecret, http.MethodPost, cardPathOf(c.Number, "/claim"), apitypes.ClaimCard{}, apitypes.HeaderSession, "brisk-otter"); rec.Code != http.StatusOK {
		t.Fatalf("claim = %d: %s", rec.Code, rec.Body)
	}
	tests := []struct {
		name, suffix string
		body         any
		want         int
	}{
		{"claim", "/claim", apitypes.ClaimCard{}, http.StatusConflict},
		{"renew", "/renew", apitypes.ClaimCard{}, http.StatusForbidden},
		{"release", "/release", apitypes.ClaimCard{}, http.StatusForbidden},
		{"transition", "/transition", apitypes.TransitionCard{Version: 2, Status: "dropped", DropReason: "obsolete"}, http.StatusConflict},
	}
	for _, session := range []string{"other-otter", ""} {
		for _, tt := range tests {
			var hdr []string
			if session != "" {
				hdr = []string{apitypes.HeaderSession, session}
			}
			rec := e.do(agentSecret, http.MethodPost, cardPathOf(c.Number, tt.suffix), tt.body, hdr...)
			if rec.Code != tt.want {
				t.Errorf("%s from session %q = %d, want %d: %s", tt.name, session, rec.Code, tt.want, rec.Body)
			}
			if tt.want == http.StatusConflict && !strings.Contains(rec.Body.String(), "in session brisk-otter") {
				t.Errorf("%s from session %q: body %s, want the holding session named", tt.name, session, rec.Body)
			}
		}
	}
	if got := e.show(agentSecret, c.Number); got.Status != "working" || got.Claim == nil || got.Claim.Session != "brisk-otter" {
		t.Fatalf("after refusals = %+v, want still working under brisk-otter", got)
	}

	e.advance(31 * time.Minute)
	rec := e.do(agentSecret, http.MethodPost, cardPathOf(c.Number, "/claim"), apitypes.ClaimCard{}, apitypes.HeaderSession, "other-otter")
	if rec.Code != http.StatusOK {
		t.Fatalf("claim after expiry = %d: %s", rec.Code, rec.Body)
	}
	var pl map[string]any
	if err := json.Unmarshal(e.lastEvent().Payload, &pl); err != nil {
		t.Fatal(err)
	}
	if pl["taken_from"] == nil || pl["taken_from_session"] != "brisk-otter" {
		t.Errorf("claimed payload = %v, want taken_from with session brisk-otter", pl)
	}
}

func TestHumanClaimIsTheHumansInEverySession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(memberSecret, "laptop then browser")
	if rec := e.do(memberSecret, http.MethodPost, cardPathOf(c.Number, "/claim"), apitypes.ClaimCard{}, apitypes.HeaderSession, "laptop"); rec.Code != http.StatusOK {
		t.Fatalf("claim = %d: %s", rec.Code, rec.Body)
	}
	if rec := e.do(memberSecret, http.MethodPost, cardPathOf(c.Number, "/release"), apitypes.ClaimCard{}); rec.Code != http.StatusOK {
		t.Fatalf("release without a session = %d: %s", rec.Code, rec.Body)
	}
	var pl map[string]any
	if err := json.Unmarshal(e.lastEvent().Payload, &pl); err != nil {
		t.Fatal(err)
	}
	if _, ok := pl["released_from"]; ok {
		t.Errorf("released payload = %v, want no released_from for the human's own claim", pl)
	}
}

func TestAChatStatesHowLongItsClaimLasts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	hdr := []string{apitypes.HeaderSession, "brisk-otter"}
	post := func(n int64, op string, body apitypes.ClaimCard, want int) apitypes.Card {
		t.Helper()
		rec := e.do(agentSecret, http.MethodPost, cardPathOf(n, op), body, hdr...)
		if rec.Code != want {
			t.Fatalf("POST %s %+v = %d, want %d: %s", op, body, rec.Code, want, rec.Body)
		}
		var c apitypes.Card
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		return c
	}
	card := e.readyCard(agentSecret, "a long build")
	n := card.Number
	for _, bad := range []string{"30s", "25h", "soon"} {
		post(n, "/claim", apitypes.ClaimCard{Lease: bad}, http.StatusUnprocessableEntity)
	}

	got := post(n, "/claim", apitypes.ClaimCard{Lease: "1h"}, http.StatusOK).Claim
	if got == nil || got.Lease != "1h0m0s" || !got.ExpiresAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("claim for 1h = %+v, want the stated hour", got)
	}

	// Twenty minutes in, a project lease of 30m would be renewed; an hour's is not yet.
	e.advance(20 * time.Minute)
	e.do(agentSecret, http.MethodGet, "/me", nil, hdr...)
	if got := e.show(userSecret, n).Claim; got == nil || !got.ExpiresAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("claim at 20m = %+v, want the first expiry: half of the stated hour has not passed", got)
	}
	e.advance(20 * time.Minute)
	e.do(agentSecret, http.MethodGet, "/me", nil, hdr...)
	if got := e.show(userSecret, n).Claim; got == nil || !got.ExpiresAt.Equal(t0.Add(100*time.Minute)) {
		t.Errorf("claim at 40m = %+v, want renewed by the stated hour", got)
	}

	// The holder claiming its own card again keeps the length it stated.
	if got := post(n, "/claim", apitypes.ClaimCard{}, http.StatusOK).Claim; got == nil || got.Lease != "1h0m0s" || !got.ExpiresAt.Equal(t0.Add(100*time.Minute)) {
		t.Errorf("claim after the holder claimed again = %+v, want the stated hour kept", got)
	}

	if got := post(n, "/renew", apitypes.ClaimCard{Lease: "10m"}, http.StatusOK).Claim; got == nil || got.Lease != "10m0s" || !got.ExpiresAt.Equal(t0.Add(50*time.Minute)) {
		t.Errorf("renew for 10m at 40m = %+v, want it to end at 50m", got)
	}
	// A claim keeps whole seconds: the fraction is cut before the first expiry.
	if got := post(n, "/renew", apitypes.ClaimCard{Lease: "10m0.4s"}, http.StatusOK).Claim; got == nil || got.Lease != "10m0s" || !got.ExpiresAt.Equal(t0.Add(50*time.Minute)) {
		t.Errorf("renew for 10m0.4s = %+v, want ten minutes exactly", got)
	}
	post(n, "/renew", apitypes.ClaimCard{Lease: "2d"}, http.StatusUnprocessableEntity)
	post(n, "/release", apitypes.ClaimCard{Lease: "1h"}, http.StatusUnprocessableEntity)
	if got := post(n, "/release", apitypes.ClaimCard{}, http.StatusOK).Claim; got != nil {
		t.Errorf("claim after release = %+v, want none", got)
	}
	if got := post(n, "/claim", apitypes.ClaimCard{}, http.StatusOK).Claim; got == nil || got.Lease != "" || !got.ExpiresAt.Equal(t0.Add(70*time.Minute)) {
		t.Errorf("claim without a stated length = %+v, want the project's 30m", got)
	}
}

func TestARequestRenewsOnlyItsOwnSessionsClaims(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	claimIn := func(title, session string) int64 {
		t.Helper()
		c := e.readyCard(agentSecret, title)
		if rec := e.do(agentSecret, http.MethodPost, cardPathOf(c.Number, "/claim"), apitypes.ClaimCard{}, apitypes.HeaderSession, session); rec.Code != http.StatusOK {
			t.Fatalf("claim = %d: %s", rec.Code, rec.Body)
		}
		return c.Number
	}
	mine := claimIn("active chat", "brisk-otter")
	abandoned := claimIn("closed chat", "quiet-heron")
	e.advance(20 * time.Minute)
	if rec := e.do(agentSecret, http.MethodGet, "/me", nil, apitypes.HeaderSession, "brisk-otter"); rec.Code != http.StatusOK {
		t.Fatalf("GET /me = %d: %s", rec.Code, rec.Body)
	}
	if got := e.show(agentSecret, mine).Claim; got == nil || !got.ExpiresAt.Equal(t0.Add(50*time.Minute)) {
		t.Errorf("active chat's claim = %+v, want renewed to now+30m", got)
	}
	if got := e.show(agentSecret, abandoned).Claim; got == nil || !got.ExpiresAt.Equal(t0.Add(30*time.Minute)) {
		t.Errorf("closed chat's claim = %+v, want its original expiry", got)
	}
}

func TestTheEventStreamAndAListenerRenewNoClaims(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		path     string
		listener bool
		want     bool
	}{
		{"/events/stream", false, false},
		{"/inbox", false, true},
		{"/me", false, true},
		{"/inbox", true, false},
		{"/actors", true, false},
	} {
		r := httptest.NewRequest(http.MethodGet, apiPrefix+tt.path, nil)
		if tt.listener {
			r.Header.Set(apitypes.HeaderListener, "1")
		}
		if got := renewsClaims(r); got != tt.want {
			t.Errorf("renewsClaims(%s, listener %v) = %v, want %v", tt.path, tt.listener, got, tt.want)
		}
	}
}

func TestAListenersInboxReadLeavesTheChatsClaimToLapse(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(agentSecret, "held by a chat that went quiet")
	if rec := e.do(agentSecret, http.MethodPost, cardPathOf(c.Number, "/claim"), apitypes.ClaimCard{}, apitypes.HeaderSession, "brisk-otter"); rec.Code != http.StatusOK {
		t.Fatalf("claim = %d: %s", rec.Code, rec.Body)
	}
	e.advance(20 * time.Minute)
	if rec := e.do(agentSecret, http.MethodGet, "/inbox", nil, apitypes.HeaderSession, "brisk-otter", apitypes.HeaderListener, "1"); rec.Code != http.StatusOK {
		t.Fatalf("listener GET /inbox = %d: %s", rec.Code, rec.Body)
	}
	if got := e.show(userSecret, c.Number).Claim; got == nil || !got.ExpiresAt.Equal(t0.Add(30*time.Minute)) {
		t.Errorf("claim after the listener's read = %+v, want its original expiry", got)
	}
	if rec := e.do(agentSecret, http.MethodGet, "/inbox", nil, apitypes.HeaderSession, "brisk-otter"); rec.Code != http.StatusOK {
		t.Fatalf("GET /inbox = %d: %s", rec.Code, rec.Body)
	}
	if got := e.show(userSecret, c.Number).Claim; got == nil || !got.ExpiresAt.Equal(t0.Add(50*time.Minute)) {
		t.Errorf("claim after the chat's own read = %+v, want renewed to now+30m", got)
	}
}

func TestClaimMineIsSetOnlyForTheClaimantsOwnSession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	get := func(secret string, n int64, hdr ...string) apitypes.Card {
		t.Helper()
		rec := e.do(secret, http.MethodGet, cardPathOf(n, ""), nil, hdr...)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET #%d = %d: %s", n, rec.Code, rec.Body)
		}
		var c apitypes.Card
		if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	agentCard := e.readyCard(agentSecret, "agent's card")
	if rec := e.do(agentSecret, http.MethodPost, cardPathOf(agentCard.Number, "/claim"), apitypes.ClaimCard{}, apitypes.HeaderSession, "brisk-otter"); rec.Code != http.StatusOK {
		t.Fatalf("agent claim = %d: %s", rec.Code, rec.Body)
	}
	humanCard := e.readyCard(memberSecret, "member's card")
	if rec := e.do(memberSecret, http.MethodPost, cardPathOf(humanCard.Number, "/claim"), apitypes.ClaimCard{}, apitypes.HeaderSession, "laptop"); rec.Code != http.StatusOK {
		t.Fatalf("member claim = %d: %s", rec.Code, rec.Body)
	}
	tests := []struct {
		name   string
		secret string
		card   int64
		hdr    []string
		want   bool
	}{
		{"agent in the claiming session", agentSecret, agentCard.Number, []string{apitypes.HeaderSession, "brisk-otter"}, true},
		{"agent in another session", agentSecret, agentCard.Number, []string{apitypes.HeaderSession, "calm-heron"}, false},
		{"agent without a session", agentSecret, agentCard.Number, nil, false},
		{"another actor", memberSecret, agentCard.Number, nil, false},
		{"human in the claiming session", memberSecret, humanCard.Number, []string{apitypes.HeaderSession, "laptop"}, true},
		{"human in another session", memberSecret, humanCard.Number, []string{apitypes.HeaderSession, "browser"}, true},
		{"another actor than the human", userSecret, humanCard.Number, nil, false},
		{"agent on a human's claim", agentSecret, humanCard.Number, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := get(tt.secret, tt.card, tt.hdr...)
			if c.Claim == nil || c.Claim.Mine != tt.want {
				t.Errorf("claim = %+v, want mine=%v", c.Claim, tt.want)
			}
			if c.Claim != nil && c.Claim.Owned != tt.want {
				t.Errorf("claim = %+v, want owned=%v while the lease is live", c.Claim, tt.want)
			}
		})
	}
	if rec := e.do(memberSecret, http.MethodGet, "/projects/lorvik/cards", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"mine":true`) {
		t.Errorf("card list = %d, want the member's own claim marked mine: %s", rec.Code, rec.Body)
	}

	e.advance(48 * time.Hour)
	for _, tt := range []struct {
		name   string
		secret string
		card   int64
		hdr    []string
		want   bool
	}{
		{"agent in the claiming session", agentSecret, agentCard.Number, []string{apitypes.HeaderSession, "brisk-otter"}, true},
		{"agent in another session", agentSecret, agentCard.Number, []string{apitypes.HeaderSession, "calm-heron"}, false},
		{"human in another session", memberSecret, humanCard.Number, []string{apitypes.HeaderSession, "browser"}, true},
		{"another actor than the human", userSecret, humanCard.Number, nil, false},
	} {
		t.Run("lapsed lease, "+tt.name, func(t *testing.T) {
			c := get(tt.secret, tt.card, tt.hdr...)
			if c.Claim == nil || c.Claim.Mine || c.Claim.Owned != tt.want {
				t.Errorf("claim = %+v, want mine=false and owned=%v once the lease has lapsed", c.Claim, tt.want)
			}
		})
	}
}
