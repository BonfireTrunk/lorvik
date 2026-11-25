package server

import (
	"context"
	"sync"
	"time"
)

// Counters counts recent events per key, for the sign-in limits. The server
// keeps them in memory; a deployment with several instances passes a shared
// implementation in Deps so every instance counts against the same numbers.
type Counters interface {
	// Take records one event under key unless limit events already lie inside
	// window. It reports whether it recorded one and, if not, how long until
	// the oldest leaves the window.
	Take(ctx context.Context, key string, limit int, window time.Duration) (retry time.Duration, ok bool)
	// Full reports whether limit events lie inside window, recording nothing.
	Full(ctx context.Context, key string, limit int, window time.Duration) (retry time.Duration, full bool)
	// Forget drops what is counted under key.
	Forget(ctx context.Context, key string)
}

// maxCounterKeys bounds one memCounters. A key holds at most its limit in
// timestamps, so a full map of the largest limit (30) is about 20 MB.
const maxCounterKeys = 20_000

// evictSample is how many keys a full map looks at to pick the one to drop.
const evictSample = 8

// memCounters is Counters in this process's memory: a restart clears it, and
// each instance of the server counts on its own.
type memCounters struct {
	now func() time.Time
	max int

	mu    sync.Mutex
	byKey map[string]*recent
	swept time.Time
}

// recent holds the events still inside one key's window, oldest first.
type recent struct {
	at     []time.Time
	window time.Duration
}

func newMemCounters(now func() time.Time, limit int) *memCounters {
	return &memCounters{now: now, max: limit, byKey: map[string]*recent{}}
}

// live drops the events that have left the window and returns the entry, or
// nil when nothing is left under key.
func (m *memCounters) live(key string, now time.Time) *recent {
	e := m.byKey[key]
	if e == nil {
		return nil
	}
	old := 0
	for old < len(e.at) && !e.at[old].Add(e.window).After(now) {
		old++
	}
	e.at = e.at[old:]
	if len(e.at) == 0 {
		delete(m.byKey, key)
		return nil
	}
	return e
}

// Take implements Counters.
func (m *memCounters) Take(_ context.Context, key string, limit int, window time.Duration) (time.Duration, bool) {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.live(key, now)
	if e == nil {
		m.makeRoom(now)
		e = &recent{window: window}
		m.byKey[key] = e
	}
	if len(e.at) >= limit {
		return e.at[0].Add(window).Sub(now), false
	}
	e.at = append(e.at, now)
	return 0, true
}

// Full implements Counters.
func (m *memCounters) Full(_ context.Context, key string, limit int, window time.Duration) (time.Duration, bool) {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.live(key, now)
	if e == nil || len(e.at) < limit {
		return 0, false
	}
	return e.at[0].Add(window).Sub(now), true
}

// Forget implements Counters.
func (m *memCounters) Forget(_ context.Context, key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.byKey, key)
}

// makeRoom keeps the map under its bound before a key is added. Once a
// minute it drops the keys with nothing left in their window. A full map
// drops one key: refusing new keys instead would let a flood of made-up
// logins or addresses shut everyone else out of sign-in.
//
// The key to drop comes from a few keys in the map's random order: the one
// holding the fewest events, and among those the one whose window ends first.
// Looking at every key would make each request of a flood cost a pass over
// the whole map under the lock. Fewest events first, because a flood's own
// keys hold one event each while a key at its limit, the one a flood would
// want gone, holds many.
func (m *memCounters) makeRoom(now time.Time) {
	if now.Sub(m.swept) >= time.Minute {
		m.swept = now
		for k := range m.byKey {
			m.live(k, now)
		}
	}
	if len(m.byKey) < m.max {
		return
	}
	var drop string
	var fewest *recent
	seen := 0
	for k := range m.byKey {
		e := m.live(k, now)
		if e == nil {
			return
		}
		if fewest == nil || len(e.at) < len(fewest.at) || (len(e.at) == len(fewest.at) && e.end().Before(fewest.end())) {
			drop, fewest = k, e
		}
		if seen++; seen == evictSample {
			break
		}
	}
	delete(m.byKey, drop)
}

// end is when the newest event leaves the window.
func (e *recent) end() time.Time { return e.at[len(e.at)-1].Add(e.window) }
