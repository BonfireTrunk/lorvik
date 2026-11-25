package server

import (
	"sync"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// pendingLink is a GitHub link a signed-in user started: who gets the
// identity, and the token and session that asked for it. The callback checks
// that token again, so revoking it withdraws a link still in flight.
type pendingLink struct {
	actor   domain.ActorRef
	tokenID domain.ID
	session string
	expires time.Time
}

// linkStates holds the links in flight, keyed by their OAuth state. Who a
// link is for is kept here and never in the browser: the callback is not
// authenticated, so anything it read back from a cookie could be written by
// the caller. The map lives in this process, so a link started before a
// restart, or on another instance, ends as expired and is started again.
type linkStates struct {
	mu      sync.Mutex
	byState map[string]pendingLink
}

// put records a link under state. It drops expired links and any earlier
// link of the same user, so the map never holds more than one per user.
func (l *linkStates) put(state string, p pendingLink, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byState == nil {
		l.byState = map[string]pendingLink{}
	}
	for k, v := range l.byState {
		if !v.expires.After(now) || v.actor == p.actor {
			delete(l.byState, k)
		}
	}
	l.byState[state] = p
}

// take returns the link recorded under state and forgets it, so a state
// works once. It reports false for an unknown or expired state.
func (l *linkStates) take(state string, now time.Time) (pendingLink, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.byState[state]
	delete(l.byState, state)
	if !ok || !p.expires.After(now) {
		return pendingLink{}, false
	}
	return p, true
}
