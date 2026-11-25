package server

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *stepClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *stepClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

const (
	gitHubDrops   = "drops the connection"
	gitHubNoUser  = "answers 404"
	gitHubAnswers = "answers the user"
)

// breakerGitHub is a client against a fake GitHub whose behaviour the test
// switches, counting the requests that reach it.
type breakerGitHub struct {
	g        *githubClient
	clock    *stepClock
	requests atomic.Int32
	mode     atomic.Value
	logs     *bytes.Buffer
	logsMu   sync.Mutex
}

func (b *breakerGitHub) Write(p []byte) (int, error) {
	b.logsMu.Lock()
	defer b.logsMu.Unlock()
	return b.logs.Write(p)
}

func (b *breakerGitHub) logged() string {
	b.logsMu.Lock()
	defer b.logsMu.Unlock()
	return b.logs.String()
}

func newBreakerGitHub(t *testing.T) *breakerGitHub {
	t.Helper()
	b := &breakerGitHub{clock: &stepClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, logs: &bytes.Buffer{}}
	b.mode.Store(gitHubDrops)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.requests.Add(1)
		switch b.mode.Load() {
		case gitHubDrops:
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
		case gitHubNoUser:
			http.NotFound(w, r)
		default:
			_, _ = w.Write([]byte(`{"id":7,"login":"ada"}`))
		}
	}))
	t.Cleanup(srv.Close)
	logger := slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelInfo}))
	b.g = newGitHubClient(ghClientID, ghClientSecret, srv.URL, srv.URL, githubHTTPClient(), b.clock.now, logger)
	return b
}

func (b *breakerGitHub) lookup(t *testing.T) error {
	t.Helper()
	_, err := b.g.lookup(context.Background(), "ada")
	return err
}

func (b *breakerGitHub) failTimes(t *testing.T, n int) {
	t.Helper()
	b.mode.Store(gitHubDrops)
	for range n {
		if err := b.lookup(t); !gotNoAnswer(err) {
			t.Fatalf("lookup error = %v, want a call without an answer", err)
		}
	}
}

func TestGitHubBreakerOpensAfterThreeCallsWithoutAnAnswer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		failures int
		reaches  bool
	}{
		{"two failures leave it closed", 2, true},
		{"three failures open it", 3, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := newBreakerGitHub(t)
			b.failTimes(t, tc.failures)
			b.mode.Store(gitHubAnswers)
			before := b.requests.Load()
			err := b.lookup(t)
			if reached := b.requests.Load() > before; reached != tc.reaches {
				t.Errorf("next call reached GitHub = %v, want %v", reached, tc.reaches)
			}
			if tc.reaches && err != nil {
				t.Errorf("next call error = %v", err)
			}
			if !tc.reaches && (err == nil || !gotNoAnswer(err)) {
				t.Errorf("next call error = %v, want a call without an answer", err)
			}
		})
	}
}

func TestGitHubBreakerCountsOnlyCallsInARow(t *testing.T) {
	t.Parallel()
	for _, answer := range []string{gitHubNoUser, gitHubAnswers} {
		t.Run(answer, func(t *testing.T) {
			t.Parallel()
			b := newBreakerGitHub(t)
			b.failTimes(t, 2)
			b.mode.Store(answer)
			_ = b.lookup(t)
			b.failTimes(t, 2)
			b.mode.Store(gitHubAnswers)
			before := b.requests.Load()
			if err := b.lookup(t); err != nil || b.requests.Load() == before {
				t.Errorf("lookup after 2+2 failures split by an answer: err = %v, reached GitHub = %v; want it closed", err, b.requests.Load() > before)
			}
		})
	}
}

func TestGitHubBreakerFailsAtOnceWhileOpen(t *testing.T) {
	t.Parallel()
	b := newBreakerGitHub(t)
	b.failTimes(t, 3)
	before := b.requests.Load()
	b.clock.advance(githubBreakerOpenFor - time.Nanosecond)
	for range 5 {
		if err := b.lookup(t); !gotNoAnswer(err) {
			t.Fatalf("lookup error = %v, want a call without an answer", err)
		}
	}
	if got := b.requests.Load(); got != before {
		t.Errorf("requests while open = %d, want %d", got-before, 0)
	}
}

func TestGitHubBreakerLetsOneProbeThroughAfterTheOpenTime(t *testing.T) {
	t.Parallel()
	b := newBreakerGitHub(t)
	b.failTimes(t, 3)
	b.clock.advance(githubBreakerOpenFor)
	before := b.requests.Load()

	release := make(chan struct{})
	b.g.http = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		b.requests.Add(1)
		<-release
		return nil, context.Canceled
	})}
	probe := make(chan error, 1)
	go func() { probe <- b.lookup(t) }()
	for b.requests.Load() == before {
		time.Sleep(time.Millisecond)
	}
	for range 3 {
		if err := b.lookup(t); !gotNoAnswer(err) {
			t.Errorf("lookup during the probe: %v, want a call without an answer", err)
		}
	}
	if got := b.requests.Load() - before; got != 1 {
		t.Errorf("requests during the probe = %d, want 1", got)
	}
	close(release)
	if err := <-probe; !gotNoAnswer(err) {
		t.Errorf("probe error = %v, want a call without an answer", err)
	}
}

func TestGitHubBreakerProbeDecidesCloseOrReopen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		mode      string
		reaches   bool
		wantLogs  []string
		noAnswers bool
	}{
		{"an answer closes it", gitHubAnswers, true, []string{"circuit open", "circuit closed"}, false},
		{"a refusal closes it", gitHubNoUser, true, []string{"circuit open", "circuit closed"}, false},
		{"no answer opens it for another period", gitHubDrops, false, []string{"circuit open"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := newBreakerGitHub(t)
			b.failTimes(t, 3)
			b.clock.advance(githubBreakerOpenFor)
			b.mode.Store(tc.mode)
			_ = b.lookup(t)

			b.mode.Store(gitHubAnswers)
			before := b.requests.Load()
			err := b.lookup(t)
			if reached := b.requests.Load() > before; reached != tc.reaches {
				t.Errorf("call after the probe reached GitHub = %v, want %v", reached, tc.reaches)
			}
			if gotNoAnswer(err) != tc.noAnswers {
				t.Errorf("call after the probe error = %v, want no answer = %v", err, tc.noAnswers)
			}
			if tc.noAnswers {
				b.clock.advance(githubBreakerOpenFor)
				before = b.requests.Load()
				if err := b.lookup(t); err != nil || b.requests.Load() != before+1 {
					t.Errorf("probe after the second period: err = %v, requests = %d, want one request and success", err, b.requests.Load()-before)
				}
			}
			logs := b.logged()
			for _, want := range tc.wantLogs {
				if !strings.Contains(logs, want) {
					t.Errorf("log lacks %q:\n%s", want, logs)
				}
			}
		})
	}
}

func TestGitHubBreakerIgnoresACallItsCallerCancelled(t *testing.T) {
	t.Parallel()
	b := newBreakerGitHub(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 5 {
		_, _ = b.g.lookup(ctx, "ada")
	}
	b.mode.Store(gitHubAnswers)
	before := b.requests.Load()
	if err := b.lookup(t); err != nil || b.requests.Load() == before {
		t.Errorf("lookup after cancelled calls: err = %v, reached GitHub = %v; want it closed", err, b.requests.Load() > before)
	}
}
