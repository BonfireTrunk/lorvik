package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

const (
	reviewerSecret = "lvk_u_reviewer-secret"
	viewerSecret   = "lvk_u_viewer-secret"
)

func (e *testEnv) lockUsers() {
	e.t.Helper()
	reviewer := e.seedUser("rita", domain.RoleReviewer)
	e.seedToken(reviewerSecret, reviewer, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
	viewer := e.seedUser("vera", domain.RoleViewer)
	e.seedToken(viewerSecret, viewer, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
}

func (e *testEnv) acquire(secret, name, session string, in apitypes.AcquireLock) *httpResult {
	e.t.Helper()
	var hdr []string
	if session != "" {
		hdr = []string{apitypes.HeaderSession, session}
	}
	rec := e.do(secret, http.MethodPut, "/locks/"+url.PathEscape(name), in, hdr...)
	return &httpResult{code: rec.Code, body: rec.Body.Bytes()}
}

type httpResult struct {
	code int
	body []byte
}

func (r *httpResult) created(t *testing.T) apitypes.Lock {
	t.Helper()
	if r.code != http.StatusCreated {
		t.Fatalf("status %d, want 201: %s", r.code, r.body)
	}
	var l apitypes.Lock
	if err := json.Unmarshal(r.body, &l); err != nil {
		t.Fatalf("decode lock: %v: %s", err, r.body)
	}
	return l
}

func (r *httpResult) problem(t *testing.T, want int) apitypes.Problem {
	t.Helper()
	if r.code != want {
		t.Fatalf("status %d, want %d: %s", r.code, want, r.body)
	}
	var p apitypes.Problem
	if err := json.Unmarshal(r.body, &p); err != nil {
		t.Fatalf("decode problem: %v: %s", err, r.body)
	}
	return p
}

func (e *testEnv) lockEvents() []domain.Event {
	e.fs.mu.Lock()
	defer e.fs.mu.Unlock()
	var out []domain.Event
	for _, ev := range e.fs.events {
		if ev.Subject.Type == domain.SubjectLock {
			out = append(out, ev)
		}
	}
	return out
}

func eventPayload(t *testing.T, ev domain.Event) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(ev.Payload, &m); err != nil {
		t.Fatalf("decode payload %s: %v", ev.Payload, err)
	}
	return m
}

func lockEventTypes(evs []domain.Event) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.Type.String()
	}
	return out
}

func TestAcquireThenAnotherActorGetsAConflictNamingTheHolder(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	got := e.acquire(agentSecret, "db/schema", "brisk-otter", apitypes.AcquireLock{Purpose: "regenerate sqlc"}).created(t)
	if got.Name != "db/schema" || got.Holder.ID != e.agent.ID.String() || got.Session != "brisk-otter" ||
		!got.ExpiresAt.Equal(t0.Add(10*time.Minute)) || !got.AcquiredAt.Equal(t0) {
		t.Fatalf("lock = %+v, want db/schema held by the agent for 10 minutes", got)
	}

	e.advance(90 * time.Second)
	p := e.acquire(agent2Secret, "db/schema", "calm-heron", apitypes.AcquireLock{Purpose: "other"}).problem(t, http.StatusConflict)
	wantDetail := fmt.Sprintf(`lock held: db/schema is held by agent %s (session "brisk-otter") for "regenerate sqlc", 510s left`, e.agent.ID)
	if p.Detail != wantDetail || p.Hint != "wait, or pick other work" {
		t.Errorf("conflict = %q / %q, want %q / the wait hint", p.Detail, p.Hint, wantDetail)
	}
	if got := lockEventTypes(e.lockEvents()); !slices.Equal(got, []string{"lock.acquired"}) {
		t.Errorf("events = %v, want only the first acquire", got)
	}
}

func TestAcquireAgainFromTheSameSessionRenewsWithTheNewPurpose(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	first := e.acquire(agentSecret, "gen", "brisk-otter", apitypes.AcquireLock{Purpose: "one"}).created(t)

	e.advance(100 * time.Second)
	again := e.acquire(agentSecret, "gen", "brisk-otter", apitypes.AcquireLock{Purpose: "two", TTLSeconds: 300}).created(t)
	if again.ID != first.ID || again.Purpose != "two" || !again.AcquiredAt.Equal(t0) || !again.ExpiresAt.Equal(t0.Add(400*time.Second)) {
		t.Errorf("re-acquire = %+v, want the same lock, new purpose, acquired_at kept, expiry now+300s", again)
	}
	if got := lockEventTypes(e.lockEvents()); !slices.Equal(got, []string{"lock.acquired"}) {
		t.Errorf("events = %v, want no event for an idempotent re-acquire", got)
	}

	other := e.acquire(agentSecret, "gen", "other-session", apitypes.AcquireLock{Purpose: "three"})
	other.problem(t, http.StatusConflict)
}

func TestExpiredLockIsTakenOverWithExpiredThenAcquiredEvents(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	first := e.acquire(agentSecret, "gen", "brisk-otter", apitypes.AcquireLock{Purpose: "old work", TTLSeconds: 60}).created(t)

	e.advance(61 * time.Second)
	got := e.acquire(agent2Secret, "gen", "calm-heron", apitypes.AcquireLock{Purpose: "new work"}).created(t)
	if got.ID != first.ID || got.Holder.ID != e.agent2.ID.String() || got.Purpose != "new work" || !got.AcquiredAt.Equal(t0.Add(61*time.Second)) {
		t.Errorf("takeover = %+v, want the row now held by the second agent", got)
	}
	evs := e.lockEvents()
	if types := lockEventTypes(evs); !slices.Equal(types, []string{"lock.acquired", "lock.expired", "lock.acquired"}) {
		t.Fatalf("events = %v, want acquired, expired, acquired", types)
	}
	if evs[1].Actor != e.agent || evs[2].Actor != e.agent2 {
		t.Errorf("actors = %v then %v, want expired for the old holder and acquired by the new", evs[1].Actor, evs[2].Actor)
	}
	if pl := eventPayload(t, evs[1]); pl["name"] != "gen" || pl["purpose"] != "old work" {
		t.Errorf("expired payload = %v, want name and the old purpose", pl)
	}
}

func TestRenewIsForTheHoldingSessionOnly(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.acquire(agentSecret, "gen", "brisk-otter", apitypes.AcquireLock{Purpose: "work"}).created(t)
	e.advance(5 * time.Minute)

	tests := []struct {
		name    string
		secret  string
		session string
		want    int
	}{
		{"same actor, other session", agentSecret, "other-session", http.StatusForbidden},
		{"same actor, no session", agentSecret, "", http.StatusForbidden},
		{"other actor", agent2Secret, "brisk-otter", http.StatusForbidden},
		{"holder session", agentSecret, "brisk-otter", http.StatusOK},
	}
	for _, tt := range tests {
		var hdr []string
		if tt.session != "" {
			hdr = []string{apitypes.HeaderSession, tt.session}
		}
		rec := e.do(tt.secret, http.MethodPost, "/locks/gen/renew", apitypes.RenewLock{TTLSeconds: 1200}, hdr...)
		if rec.Code != tt.want {
			t.Errorf("%s: status %d, want %d: %s", tt.name, rec.Code, tt.want, rec.Body)
		}
	}
	l := call[apitypes.LockList](e, agentSecret, http.MethodGet, "/locks", nil, http.StatusOK).Items
	if len(l) != 1 || !l[0].ExpiresAt.Equal(t0.Add(5*time.Minute+20*time.Minute)) {
		t.Errorf("locks = %+v, want the one lock expiring 20 minutes after the renewal", l)
	}
	if got := lockEventTypes(e.lockEvents()); len(got) != 1 {
		t.Errorf("events = %v, want renewal to write none", got)
	}

	e.advance(time.Hour)
	if rec := e.do(agentSecret, http.MethodPost, "/locks/gen/renew", apitypes.RenewLock{}, apitypes.HeaderSession, "brisk-otter"); rec.Code != http.StatusNotFound {
		t.Errorf("renew after expiry: status %d, want 404: %s", rec.Code, rec.Body)
	}
}

func TestReleaseRules(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.lockUsers()
	tests := []struct {
		name   string
		secret string
		force  bool
		want   int
		forced bool
	}{
		{"another agent", agent2Secret, false, http.StatusForbidden, false},
		{"another agent forcing", agent2Secret, true, http.StatusForbidden, false},
		{"member user forcing", memberSecret, true, http.StatusForbidden, false},
		{"reviewer without force", reviewerSecret, false, http.StatusForbidden, false},
		{"reviewer forcing", reviewerSecret, true, http.StatusNoContent, true},
		{"holder from another session", agentSecret, false, http.StatusNoContent, false},
	}
	for _, tt := range tests {
		e.acquire(agentSecret, "gen", "brisk-otter", apitypes.AcquireLock{Purpose: "work"}).created(t)
		before := len(e.lockEvents())
		path := "/locks/gen"
		if tt.force {
			path += "?force=true"
		}
		hdr := []string{}
		if tt.secret == agentSecret {
			hdr = []string{apitypes.HeaderSession, "different-session"}
		}
		if rec := e.do(tt.secret, http.MethodDelete, path, nil, hdr...); rec.Code != tt.want {
			t.Fatalf("%s: status %d, want %d: %s", tt.name, rec.Code, tt.want, rec.Body)
		}
		evs := e.lockEvents()[before:]
		if tt.want != http.StatusNoContent {
			if len(evs) != 0 {
				t.Errorf("%s: events %v, want none for a refusal", tt.name, lockEventTypes(evs))
			}
			continue
		}
		if len(evs) != 1 || evs[0].Type != domain.EventLockReleased {
			t.Fatalf("%s: events %v, want one lock.released", tt.name, lockEventTypes(evs))
		}
		pl := eventPayload(t, evs[0])
		holder, hasHolder := pl["holder"]
		if pl["name"] != "gen" || pl["forced"] != tt.forced || hasHolder != tt.forced {
			t.Errorf("%s: payload %v, want forced=%v and holder only when forced", tt.name, pl, tt.forced)
		}
		if tt.forced && holder.(map[string]any)["id"] != e.agent.ID.String() {
			t.Errorf("%s: holder %v, want the agent", tt.name, holder)
		}
	}
	if l := call[apitypes.LockList](e, userSecret, http.MethodGet, "/locks", nil, http.StatusOK).Items; len(l) != 0 {
		t.Errorf("locks = %+v, want none after the releases", l)
	}
}

func TestInboxCarriesLiveLocksOfTheOrg(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.acquire(agentSecret, "b-lock", "brisk-otter", apitypes.AcquireLock{Purpose: "b", TTLSeconds: 60}).created(t)
	e.acquire(agent2Secret, "a-lock", "calm-heron", apitypes.AcquireLock{Purpose: "a", TTLSeconds: 3600}).created(t)

	inbox := call[apitypes.Inbox](e, memberSecret, http.MethodGet, "/inbox", nil, http.StatusOK)
	var names []string
	for _, l := range inbox.Locks {
		names = append(names, l.Name)
	}
	if !slices.Equal(names, []string{"a-lock", "b-lock"}) {
		t.Fatalf("inbox locks = %v, want both by name, including the ones held by others", names)
	}

	e.advance(2 * time.Minute)
	inbox = call[apitypes.Inbox](e, memberSecret, http.MethodGet, "/inbox", nil, http.StatusOK)
	if len(inbox.Locks) != 1 || inbox.Locks[0].Name != "a-lock" {
		t.Errorf("inbox locks = %+v, want the lapsed lock dropped", inbox.Locks)
	}
}

func TestNextWithholdsCardsInALockedLane(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.newCard(agentSecret, apitypes.CreateCard{Title: "touch schema", Status: "ready", Lane: "db"})
	e.acquire(agent2Secret, "db", "calm-heron", apitypes.AcquireLock{Purpose: "migrating", TTLSeconds: 60}).created(t)

	p := e.problem(agentSecret, http.MethodGet, "/projects/lorvik/cards/next", nil, http.StatusNotFound)
	if p.WaitingCards != 1 || !slices.Equal(p.BusyLanes, []string{"db"}) {
		t.Errorf("next = %+v, want one waiting card and busy lane db", p)
	}

	e.advance(61 * time.Second)
	if c := call[apitypes.Card](e, agentSecret, http.MethodGet, "/projects/lorvik/cards/next", nil, http.StatusOK); c.Title != "touch schema" {
		t.Errorf("next after expiry = %q, want the card back", c.Title)
	}

	e.acquire(agent2Secret, "db", "calm-heron", apitypes.AcquireLock{Purpose: "again"}).created(t)
	e.problem(agentSecret, http.MethodGet, "/projects/lorvik/cards/next", nil, http.StatusNotFound)
	if rec := e.do(agent2Secret, http.MethodDelete, "/locks/db", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("release: %d %s", rec.Code, rec.Body)
	}
	call[apitypes.Card](e, agentSecret, http.MethodGet, "/projects/lorvik/cards/next", nil, http.StatusOK)
}

func TestSweeperDeletesExpiredLocksAndEmitsExpired(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.acquire(agentSecret, "short", "brisk-otter", apitypes.AcquireLock{Purpose: "s", TTLSeconds: 60}).created(t)
	e.acquire(agent2Secret, "long", "calm-heron", apitypes.AcquireLock{Purpose: "l", TTLSeconds: 3600}).created(t)

	if n, err := e.svc.sweepLocks(context.Background(), e.clock()); err != nil || n != 0 {
		t.Fatalf("sweep before expiry = %d, %v, want nothing", n, err)
	}
	e.advance(61 * time.Second)
	n, err := e.svc.sweepLocks(context.Background(), e.clock())
	if err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v, want 1", n, err)
	}
	evs := e.lockEvents()
	last := evs[len(evs)-1]
	if last.Type != domain.EventLockExpired || last.Actor != e.agent {
		t.Fatalf("last event = %s by %v, want lock.expired by the former holder", last.Type, last.Actor)
	}
	if pl := eventPayload(t, last); pl["name"] != "short" || pl["purpose"] != "s" || pl["holder"].(map[string]any)["id"] != e.agent.ID.String() {
		t.Errorf("payload = %v, want name, holder and purpose", pl)
	}
	if n, err := e.svc.sweepLocks(context.Background(), e.clock()); err != nil || n != 0 {
		t.Errorf("second sweep = %d, %v, want nothing left", n, err)
	}
	if l := call[apitypes.LockList](e, userSecret, http.MethodGet, "/locks", nil, http.StatusOK).Items; len(l) != 1 || l[0].Name != "long" {
		t.Errorf("locks = %+v, want only the long one", l)
	}
}

func TestAcquireValidationAndAccess(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.lockUsers()
	card := e.readyCard(agentSecret, "work")
	tests := []struct {
		name   string
		secret string
		lock   string
		in     apitypes.AcquireLock
		want   int
		field  string
	}{
		{"uppercase name", agentSecret, "Gen", apitypes.AcquireLock{Purpose: "p"}, http.StatusUnprocessableEntity, "name"},
		{"underscore name", agentSecret, "a_b", apitypes.AcquireLock{Purpose: "p"}, http.StatusUnprocessableEntity, "name"},
		{"name too long", agentSecret, "a123456789a123456789a123456789a1234567890", apitypes.AcquireLock{Purpose: "p"}, http.StatusUnprocessableEntity, "name"},
		{"blank purpose", agentSecret, "gen", apitypes.AcquireLock{Purpose: "  "}, http.StatusUnprocessableEntity, "purpose"},
		{"purpose too long", agentSecret, "gen", apitypes.AcquireLock{Purpose: string(make([]byte, 201))}, http.StatusUnprocessableEntity, "purpose"},
		{"ttl too short", agentSecret, "gen", apitypes.AcquireLock{Purpose: "p", TTLSeconds: 59}, http.StatusUnprocessableEntity, "ttl_seconds"},
		{"ttl too long", agentSecret, "gen", apitypes.AcquireLock{Purpose: "p", TTLSeconds: 3601}, http.StatusUnprocessableEntity, "ttl_seconds"},
		{"card without project", agentSecret, "gen", apitypes.AcquireLock{Purpose: "p", Card: &card.Number}, http.StatusUnprocessableEntity, "project"},
		{"unknown card", agentSecret, "gen", apitypes.AcquireLock{Purpose: "p", Card: new(int64), Project: "lorvik"}, http.StatusUnprocessableEntity, "card"},
		{"viewer", viewerSecret, "gen", apitypes.AcquireLock{Purpose: "p"}, http.StatusForbidden, ""},
		{"project-bound agent", agent2Secret, "gen", apitypes.AcquireLock{Purpose: "p"}, http.StatusCreated, ""},
		{"nested name with the card", memberSecret, "web/build-1", apitypes.AcquireLock{Purpose: "p", Card: &card.Number, Project: "lorvik"}, http.StatusCreated, ""},
	}
	for _, tt := range tests {
		res := e.acquire(tt.secret, tt.lock, "", tt.in)
		if res.code != tt.want {
			t.Errorf("%s: status %d, want %d: %s", tt.name, res.code, tt.want, res.body)
			continue
		}
		if tt.field != "" && !hasField(res.problem(t, tt.want), tt.field) {
			t.Errorf("%s: problem %s lacks field %s", tt.name, res.body, tt.field)
		}
	}
	list := call[apitypes.LockList](e, userSecret, http.MethodGet, "/locks", nil, http.StatusOK).Items
	var withCard *apitypes.Lock
	for i := range list {
		if list[i].Name == "web/build-1" {
			withCard = &list[i]
		}
	}
	if withCard == nil || withCard.Card == nil || *withCard.Card != card.Number || withCard.Project != "lorvik" {
		t.Errorf("lock with card = %+v, want card number and project resolved", withCard)
	}
}
