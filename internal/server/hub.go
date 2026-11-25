package server

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// hub fans "something changed in org X" signals out to event streams. Local
// writes wake subscribers directly; writes on other server instances arrive
// through the store's Notifier.
type hub struct {
	mu   sync.Mutex
	subs map[domain.ID]map[chan struct{}]struct{}
}

func newHub() *hub { return &hub{subs: map[domain.ID]map[chan struct{}]struct{}{}} }

// subscribe returns a channel that receives a signal after every change in
// the org, and a function to leave.
func (h *hub) subscribe(orgID domain.ID) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	if h.subs[orgID] == nil {
		h.subs[orgID] = map[chan struct{}]struct{}{}
	}
	h.subs[orgID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[orgID], ch)
		if len(h.subs[orgID]) == 0 {
			delete(h.subs, orgID)
		}
		h.mu.Unlock()
	}
}

// wake signals every subscriber of the org without blocking.
func (h *hub) wake(orgID domain.ID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[orgID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// runNotifier feeds cross-instance notifications into the hub until ctx
// ends, reconnecting with backoff when the listener fails.
func (h *hub) runNotifier(ctx context.Context, n Notifier, logger *slog.Logger) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := n.Listen(ctx, h.wake)
		if ctx.Err() != nil {
			return
		}
		logger.WarnContext(ctx, "event listener stopped; reconnecting", "error", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}
