package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// The sweeper reports only the claims it settled; a lapsed claim whose card
// is gone is deleted but not counted, so the log does not overstate.
func TestSweepCountsOnlySettledClaims(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	live := e.readyCard(memberSecret, "held")
	e.claim(memberSecret, live.Number)
	gone := domain.Claim{CardID: e.ids.New(), OrgID: e.org.ID, ProjectID: e.proj.ID, Actor: e.member, ExpiresAt: t0.Add(time.Minute), CreatedAt: t0}
	if err := e.fs.PutClaim(context.Background(), gone); err != nil {
		t.Fatal(err)
	}
	e.advance(31 * time.Minute)
	n, err := e.svc.sweepOnce(context.Background(), e.clock())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("sweepOnce = %d, want 1: the claim on the missing card is not settled", n)
	}
	if _, err := e.fs.GetClaim(context.Background(), gone.CardID); err == nil {
		t.Error("the claim on the missing card was not deleted")
	}
	if got := e.show(memberSecret, live.Number); got.Status != "ready" {
		t.Errorf("held card status = %s, want ready", got.Status)
	}
}

// A claim listed as lapsed but retaken before the sweeper settles it is left
// alone: the card stays working under the new holder and no expiry is told.
func TestSweepSkipsAClaimRetakenAfterItWasListed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(memberSecret, "contested")
	e.claim(memberSecret, c.Number)
	e.advance(31 * time.Minute)
	listed, err := e.fs.ListExpiredClaims(context.Background(), e.clock(), sweepBatch)
	if err != nil || len(listed) != 1 {
		t.Fatalf("expired claims = %+v, %v; want the member's", listed, err)
	}
	e.claim(agentSecret, c.Number)
	settled, err := e.svc.expireClaim(context.Background(), listed[0].CardID, e.clock())
	if err != nil || settled {
		t.Fatalf("expireClaim = %v, %v; want nothing settled", settled, err)
	}
	if got := e.show(agentSecret, c.Number); got.Status != "working" || got.Claim == nil || got.Claim.Actor.ID != e.agent.ID.String() {
		t.Errorf("card = %s held by %+v, want working under the agent", got.Status, got.Claim)
	}
	if ev := e.eventsOf("card.claim_expired"); len(ev) != 0 {
		t.Errorf("expiry events = %v, want none", ev)
	}
}

// listenerInbox reads the inbox as a background listener does, which renews
// no claim, and returns the last calls in it by card number.
func listenerInbox(t *testing.T, e *testEnv, secret, session string) map[int64]apitypes.Event {
	t.Helper()
	hdr := []string{apitypes.HeaderListener, "1"}
	if session != "" {
		hdr = append(hdr, apitypes.HeaderSession, session)
	}
	rec := e.do(secret, http.MethodGet, "/inbox", nil, hdr...)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /inbox = %d: %s", rec.Code, rec.Body)
	}
	var in apitypes.Inbox
	if err := json.Unmarshal(rec.Body.Bytes(), &in); err != nil {
		t.Fatal(err)
	}
	calls := map[int64]apitypes.Event{}
	for _, ev := range in.Events {
		if ev.Type == "card.claim_ending" {
			calls[ev.Subject.Number] = ev
		}
	}
	return calls
}

// The sweeper writes one last call per expiry, five minutes ahead. It wakes
// the chat that holds the card and no other, renews nothing, and a renewal
// arms the next call.
func TestTheSweeperCallsOnceBeforeAClaimEnds(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(agentSecret, "held by a chat in a long step")
	if rec := e.do(agentSecret, http.MethodPost, cardPathOf(c.Number, "/claim"), apitypes.ClaimCard{}, apitypes.HeaderSession, "brisk-otter"); rec.Code != http.StatusOK {
		t.Fatalf("claim = %d: %s", rec.Code, rec.Body)
	}
	announce := func(want int) {
		t.Helper()
		if n, err := e.svc.announceEnding(context.Background(), e.clock()); err != nil || n != want {
			t.Fatalf("announceEnding at %s = %d, %v; want %d", e.clock().Sub(t0), n, err, want)
		}
	}
	e.advance(24 * time.Minute)
	announce(0)
	e.advance(time.Minute)
	announce(1)
	e.advance(time.Minute)
	announce(0)

	evs := e.eventsOf("card.claim_ending")
	if len(evs) != 1 || evs[0]["session"] != "brisk-otter" || evs[0]["expires_at"] != t0.Add(30*time.Minute).Format(time.RFC3339Nano) {
		t.Fatalf("last calls = %v, want one naming the session and the expiry", evs)
	}
	if ev, ok := listenerInbox(t, e, agentSecret, "brisk-otter")[c.Number]; !ok || !ev.Wake || ev.Actor.ID != e.agent.ID.String() {
		t.Errorf("holding chat's last call = %+v (present %v), want one that wakes it with the holder as actor", ev, ok)
	}
	if ev, ok := listenerInbox(t, e, agentSecret, "calm-heron")[c.Number]; !ok || ev.Wake {
		t.Errorf("another chat's copy = %+v (present %v), want it listed and not waking", ev, ok)
	}
	if got := e.show(userSecret, c.Number).Claim; got == nil || !got.ExpiresAt.Equal(t0.Add(30*time.Minute)) {
		t.Fatalf("claim after the call was delivered = %+v, want its original expiry", got)
	}

	if rec := e.do(agentSecret, http.MethodPost, cardPathOf(c.Number, "/renew"), apitypes.ClaimCard{}, apitypes.HeaderSession, "brisk-otter"); rec.Code != http.StatusOK {
		t.Fatalf("renew = %d: %s", rec.Code, rec.Body)
	}
	e.advance(24 * time.Minute)
	announce(0)
	e.advance(time.Minute)
	announce(1)

	e.advance(5 * time.Minute)
	announce(0)
	if n, err := e.svc.sweepOnce(context.Background(), e.clock()); err != nil || n != 1 {
		t.Fatalf("sweepOnce = %d, %v; want the unanswered claim settled", n, err)
	}
	if got := e.show(userSecret, c.Number); got.Status != "ready" || got.Claim != nil {
		t.Errorf("card after no answer = %s, claim %+v; want ready and free", got.Status, got.Claim)
	}
}

// beatenStore lists the ending claims as another server's sweeper marks them.
type beatenStore struct {
	Store
	fs *fakeStore
}

func (b beatenStore) ListEndingClaims(ctx context.Context, now time.Time, limit int) ([]domain.Claim, error) {
	claims, err := b.fs.ListEndingClaims(ctx, now, limit)
	for _, cl := range claims {
		if _, err := b.fs.MarkClaimEnding(ctx, cl.CardID, cl.ExpiresAt); err != nil {
			return nil, err
		}
	}
	return claims, err
}

// Two servers on one database both list a claim; only the one that marks it
// writes the call.
func TestASweeperBeatenToAClaimWritesNoSecondLastCall(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(memberSecret, "seen by two sweepers")
	e.claim(memberSecret, c.Number)
	e.advance(25 * time.Minute)
	e.svc.store = beatenStore{Store: e.fs, fs: e.fs}
	if n, err := e.svc.announceEnding(context.Background(), e.clock()); err != nil || n != 0 {
		t.Fatalf("announceEnding = %d, %v; want 0: the other sweeper marked the claim", n, err)
	}
	if evs := e.eventsOf("card.claim_ending"); len(evs) != 0 {
		t.Errorf("last calls = %v, want none from the sweeper that lost", evs)
	}
}

// A person on the dashboard sends no session label, and the inbox leaves out
// what the reader did in their own session: the call is written in a session
// no request can carry, so it still reaches them.
func TestTheLastCallReachesAPersonWithNoSessionLabel(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(memberSecret, "held from the dashboard")
	e.claim(memberSecret, c.Number)
	e.advance(25 * time.Minute)
	if n, err := e.svc.announceEnding(context.Background(), e.clock()); err != nil || n != 1 {
		t.Fatalf("announceEnding = %d, %v; want 1", n, err)
	}
	if evs := e.eventsOf("card.claim_ending"); len(evs) != 1 || evs[0]["session"] != nil {
		t.Fatalf("last calls = %v, want one with no session", evs)
	}
	if ev, ok := listenerInbox(t, e, memberSecret, "")[c.Number]; !ok || !ev.Wake {
		t.Errorf("the holder's last call = %+v (present %v), want it in their inbox", ev, ok)
	}
}
