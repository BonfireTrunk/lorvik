package server

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// errGitHubBreakerOpen is what a call fails with while the breaker is open.
// It is wrapped as a noAnswerError so callers treat it as GitHub not answering.
var errGitHubBreakerOpen = errors.New("GitHub calls are paused after repeated failures")

// githubBreaker stops calls to GitHub while it has not been answering. After
// githubBreakerFailures calls in a row without an answer it opens for
// githubBreakerOpenFor; then one call goes through as a probe while the rest
// still fail at once. An answer closes it, no answer opens it again.
type githubBreaker struct {
	now    func() time.Time
	logger *slog.Logger

	mu        sync.Mutex
	failures  int
	open      bool
	openUntil time.Time
	probing   bool
}

func newGitHubBreaker(now func() time.Time, logger *slog.Logger) *githubBreaker {
	return &githubBreaker{now: now, logger: logger}
}

// allow reports whether a call may go out, and whether it is the probe.
func (b *githubBreaker) allow() (ok, probe bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.open {
		return true, false
	}
	if b.probing || b.now().Before(b.openUntil) {
		return false, false
	}
	b.probing = true
	return true, true
}

// record takes the end of a call that allow let through. Calls that began
// before the breaker opened and end while it is open say nothing about
// whether GitHub is back, so only the probe counts then.
func (b *githubBreaker) record(ctx context.Context, probe, noAnswer bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if probe {
		b.probing = false
	} else if b.open {
		return
	}
	if !noAnswer {
		b.failures = 0
		if b.open {
			b.open = false
			b.logger.InfoContext(ctx, "github circuit closed: GitHub answered")
		}
		return
	}
	b.failures++
	if b.open || b.failures >= githubBreakerFailures {
		b.open = true
		b.openUntil = b.now().Add(githubBreakerOpenFor)
		b.logger.WarnContext(ctx, "github circuit open: GitHub is not answering",
			"failures", b.failures, "open_for", githubBreakerOpenFor.String())
	}
}

// abandon ends a call that says nothing about GitHub, such as one its own
// caller cancelled.
func (b *githubBreaker) abandon(probe bool) {
	if !probe {
		return
	}
	b.mu.Lock()
	b.probing = false
	b.mu.Unlock()
}
