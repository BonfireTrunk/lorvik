package server

import (
	"log/slog"
	"maps"
	"slices"
	"testing"
	"time"
)

// The touch memory forgets a token or chat with no request for a day, so
// labels sent once do not stay in the server's memory for good.
func TestTheTouchMemoryForgetsWhatHadNoRequestForADay(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a := newAuthenticator(e.svc, slog.New(slog.DiscardHandler))
	ctx := t.Context()
	a.touch(ctx, e.agent.ID, t0)
	a.touchSession(ctx, e.agent.ID, "calm-fox", "Release notes", t0)
	a.touchSession(ctx, e.agent.ID, "old-owl", "Sign-in", t0.Add(23*time.Hour))
	if len(a.seen) != 2 || len(a.touched) != 1 {
		t.Fatalf("after 23 hours: %d chats and %d tokens kept, want 2 and 1", len(a.seen), len(a.touched))
	}
	a.touchSession(ctx, e.agent.ID, "old-owl", "Sign-in", t0.Add(24*time.Hour+time.Minute))
	chats := slices.Collect(maps.Keys(a.seen))
	if len(chats) != 1 || chats[0].label != "old-owl" || len(a.named) != 1 || a.named[chats[0]] != "Sign-in" || len(a.touched) != 0 {
		t.Errorf("after a day: chats %v, names %v, %d tokens; want only old-owl and no token", chats, a.named, len(a.touched))
	}
}

// The agent's chats are trimmed to chatsKept when a chat the memory does not
// hold makes a request: a new one, or one forgotten after a day. A chat
// already held does not trim again.
func TestAChatNewToTheMemoryTrimsTheAgentsChats(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a := newAuthenticator(e.svc, slog.New(slog.DiscardHandler))
	ctx := t.Context()
	steps := []struct {
		label string
		at    time.Duration
		want  int
	}{
		{"calm-fox", 0, 1},
		{"calm-fox", 2 * time.Minute, 1},
		{"old-owl", 3 * time.Minute, 2},
		{"calm-fox", 25 * time.Hour, 3},
	}
	for _, s := range steps {
		a.touchSession(ctx, e.agent.ID, s.label, "", t0.Add(s.at))
		e.fs.mu.Lock()
		trims := slices.Clone(e.fs.trims)
		e.fs.mu.Unlock()
		if len(trims) != s.want || trims[len(trims)-1] != chatsKept {
			t.Fatalf("%s at %v: trims %v, want %d of them, each keeping %d", s.label, s.at, trims, s.want, chatsKept)
		}
	}
}
