package server

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// openStream opens GET /events/stream as secret and returns the card numbers
// of the events it sends, and a channel closed when the server ends it.
func openStream(t *testing.T, srv *httptest.Server, secret, query string) (<-chan int64, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+apiPrefix+"/events/stream?"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream = %d", resp.StatusCode)
	}
	cards, ended := make(chan int64, 16), make(chan struct{})
	go func() {
		defer close(ended)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			data, ok := strings.CutPrefix(sc.Text(), "data:")
			if !ok {
				continue
			}
			var e apitypes.Event
			if json.Unmarshal([]byte(strings.TrimSpace(data)), &e) == nil && e.Subject.Type == "card" {
				cards <- e.Subject.Number
			}
		}
	}()
	return cards, ended
}

// An open stream ends as soon as its caller loses the rights it opened with,
// and sends nothing written after that.
func TestAnOpenStreamEndsWhenItsCallerLosesItsRights(t *testing.T) {
	t.Parallel()
	const shortSecret = "lvk_u_short-lived-secret"
	tests := []struct {
		name   string
		secret string
		query  string
		lose   func(e *testEnv)
		ends   bool
		// writer writes the card that follows the loss; the owner by default.
		writer string
	}{
		{"nothing changes", agentSecret, "", func(*testEnv) {}, false, ""},
		{"agent revoked", agentSecret, "", func(e *testEnv) {
			call[struct{}](e, userSecret, http.MethodDelete, "/agents/"+e.agent.ID.String(), nil, http.StatusNoContent)
		}, true, ""},
		{"agent's owner suspended", agent2Secret, "", func(e *testEnv) {
			u, _ := e.fs.GetUser(context.Background(), e.user.ID)
			u.Status = domain.RecordSuspended
			if _, err := e.fs.UpdateUser(context.Background(), u); err != nil {
				e.t.Fatal(err)
			}
		}, true, memberSecret},
		{"agent removed from the project", agent2Secret, "project=lorvik", func(e *testEnv) {
			call[struct{}](e, userSecret, http.MethodDelete, "/agents/"+e.agent2.ID.String()+"/projects/lorvik", nil, http.StatusNoContent)
		}, true, ""},
		{"user removed from the org", memberSecret, "", func(e *testEnv) {
			call[struct{}](e, userSecret, http.MethodDelete, "/members/user/"+e.member.ID.String(), nil, http.StatusNoContent)
		}, true, ""},
		{"token expired", shortSecret, "", func(e *testEnv) { e.advance(2 * time.Minute) }, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.seedToken(shortSecret, e.member, domain.ScopeWrite, t0.Add(time.Minute))
			srv := httptest.NewServer(e.h)
			t.Cleanup(srv.Close)
			// A card from before the stream opened must not be sent.
			e.newCard(userSecret, apitypes.CreateCard{Title: "earlier"})
			cards, ended := openStream(t, srv, tt.secret, tt.query)

			// The first card arriving shows the stream is past its start.
			before := e.newCard(userSecret, apitypes.CreateCard{Title: "before"}).Number
			select {
			case n := <-cards:
				if n != before {
					t.Fatalf("first event on #%d, want #%d", n, before)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the stream sent nothing")
			}

			tt.lose(e)
			after := e.newCard(cmp.Or(tt.writer, userSecret), apitypes.CreateCard{Title: "after"}).Number
			deadline := time.After(5 * time.Second)
			for {
				select {
				case n := <-cards:
					if n == after {
						if tt.ends {
							t.Fatalf("the stream sent #%d after the caller lost its rights", n)
						}
						return
					}
				case <-ended:
					if !tt.ends {
						t.Fatal("the stream ended, want it open")
					}
					return
				case <-deadline:
					t.Fatalf("neither #%d nor the stream's end arrived", after)
				}
			}
		})
	}
}

// A stream's start is fixed when it is resolved, before the client sees the
// stream open: what is written after that is sent, however late the stream
// begins to read.
func TestAStreamSendsWhatWasWrittenAfterItsStartWasResolved(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	tok := e.seedToken("lvk_u_start-then-write", e.member, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
	p, prob := e.svc.standing(t.Context(), tok, "", e.clock())
	if prob != nil {
		t.Fatal(prob)
	}
	ctx, cancel := context.WithCancel(WithPrincipal(t.Context(), p))
	defer cancel()
	e.newCard(userSecret, apitypes.CreateCard{Title: "earlier"})
	start, err := e.svc.StreamStart(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	after := e.newCard(userSecret, apitypes.CreateCard{Title: "after the start"}).Number

	var got []int64
	err = e.svc.StreamEvents(ctx, "", start, func(ev apitypes.Event) error {
		got = append(got, ev.Subject.Number)
		cancel()
		return nil
	}, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != after {
		t.Errorf("the stream sent cards %v, want only #%d", got, after)
	}
}

// A quiet stream ends when its token expires, without waiting for an event
// or the next poll.
func TestAQuietStreamEndsAtItsTokensExpiry(t *testing.T) {
	t.Parallel()
	const secret = "lvk_u_one-second-secret"
	e := newEnv(t)
	e.seedToken(secret, e.member, domain.ScopeWrite, t0.Add(time.Second))
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	cards, ended := openStream(t, srv, secret, "")
	e.newCard(userSecret, apitypes.CreateCard{Title: "before"})
	select {
	case <-cards:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream sent nothing")
	}
	e.advance(time.Minute)
	select {
	case <-ended:
	case <-time.After(streamPoll / 3):
		t.Fatal("the stream outlived its token")
	}
}
