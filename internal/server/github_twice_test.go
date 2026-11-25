package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// roundTrip is one side of a fake GitHub: what a try's HTTP client gets back.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// stalled never answers; it ends when the request is cancelled.
func stalled(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

// answers gives status and body after d, unless the request is cancelled first.
func answers(d time.Duration, status int, body string) roundTrip {
	return func(r *http.Request) (*http.Response, error) {
		select {
		case <-time.After(d):
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}
}

// breaks fails after d the way a dropped connection does.
func breaks(d time.Duration) roundTrip {
	return func(r *http.Request) (*http.Response, error) {
		select {
		case <-time.After(d):
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return nil, errors.New("connection reset by peer")
	}
}

// twoTries is a githubClient whose first request goes to first and whose
// second goes to second, and the number of requests it has sent.
func twoTries(first, second roundTrip) (*githubClient, *atomic.Int32) {
	var tries atomic.Int32
	inOrder := roundTrip(func(r *http.Request) (*http.Response, error) {
		if tries.Add(1) == 1 {
			return first(r)
		}
		return second(r)
	})
	return newGitHubClient(ghClientID, ghClientSecret, "https://github.test", "https://api.github.test", &http.Client{Transport: inOrder}, time.Now, slog.New(slog.DiscardHandler)), &tries
}

const (
	ghTokenAnswer   = `{"access_token":"gho_token","token_type":"bearer"}`
	ghBadCodeAnswer = `{"error":"bad_verification_code"}`
)

func TestACallToGitHubIsTriedASecondTimeWhenTheFirstStalls(t *testing.T) {
	t.Parallel()
	const (
		succeeds   = ""
		answered   = "GitHub's answer"
		unanswered = "no answer"
	)
	cases := []struct {
		name          string
		first, second roundTrip
		fails         string
		after         time.Duration
		tries         int32
	}{
		{"the first try answers in time: no second try", answers(4*time.Second, 200, ghTokenAnswer), stalled, succeeds, 4 * time.Second, 1},
		{"the first try fails at once: no second try", breaks(0), stalled, unanswered, 0, 1},
		{"the first try stalls and the second answers", stalled, answers(time.Second, 200, ghTokenAnswer), succeeds, 6 * time.Second, 2},
		{"the first try breaks after the second began", breaks(6 * time.Second), answers(2*time.Second, 200, ghTokenAnswer), succeeds, 7 * time.Second, 2},
		// The first try reached GitHub and spent the code, so GitHub refuses
		// the second: the refusal must not end the call.
		{"the first try answers late and the second is refused", answers(7*time.Second, 200, ghTokenAnswer), answers(0, 200, ghBadCodeAnswer), succeeds, 7 * time.Second, 2},
		{"both tries stall", stalled, stalled, unanswered, 10 * time.Second, 2},
		{"the first try stalls and the second is refused", stalled, answers(0, 200, ghBadCodeAnswer), answered, 10 * time.Second, 2},
		{"the first try breaks and the second is refused later", breaks(6 * time.Second), answers(2*time.Second, 200, ghBadCodeAnswer), answered, 7 * time.Second, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				g, tries := twoTries(tc.first, tc.second)
				start := time.Now()
				token, err := g.exchange(t.Context(), "code", "http://lorvik.test/cb")
				if took := time.Since(start); took != tc.after {
					t.Errorf("the call took %v, want %v", took, tc.after)
				}
				if got := tries.Load(); got != tc.tries {
					t.Errorf("tries = %d, want %d", got, tc.tries)
				}
				switch tc.fails {
				case succeeds:
					if err != nil || token != "gho_token" {
						t.Errorf("exchange = %q, %v; want the token", token, err)
					}
				case answered:
					if err == nil || gotNoAnswer(err) || !strings.Contains(err.Error(), "bad_verification_code") {
						t.Errorf("exchange error = %v, want what GitHub answered", err)
					}
				case unanswered:
					if err == nil || !gotNoAnswer(err) {
						t.Errorf("exchange error = %v, want a call without an answer", err)
					}
				}
			})
		})
	}
}

// A token GitHub refuses on one try is reported as refused even when the other
// try got no answer at all: the caller answers 401 for it, not 502.
func TestATokenRefusedOnOneTryIsRefusedWhateverTheOtherTryDid(t *testing.T) {
	t.Parallel()
	notOurs := answers(time.Second, http.StatusNotFound, `{"message":"Not Found"}`)
	cases := []struct {
		name          string
		first, second roundTrip
	}{
		{"the first try stalls", stalled, notOurs},
		{"the first try breaks before the second is answered", breaks(5500 * time.Millisecond), notOurs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				g, _ := twoTries(tc.first, tc.second)
				if err := g.issuedHere(t.Context(), "gho_supersecret"); !errors.Is(err, errGitHubRejected) {
					t.Errorf("token check = %v, want errGitHubRejected", err)
				}
			})
		})
	}
}

func TestEveryRequestToGitHubOpensAConnectionOfItsOwn(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		https bool
	}{{"HTTP/1.1", false}, {"HTTP/2", true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var conns atomic.Int32
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") }))
			srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
				if s == http.StateNew {
					conns.Add(1)
				}
			}
			hc := githubHTTPClient()
			wantProto := 1
			if tc.https {
				srv.EnableHTTP2 = true
				srv.StartTLS()
				pool := x509.NewCertPool()
				pool.AddCert(srv.Certificate())
				hc.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
				wantProto = 2
			} else {
				srv.Start()
			}
			t.Cleanup(srv.Close)
			for range 2 {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := hc.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.ProtoMajor != wantProto {
					t.Fatalf("answered over HTTP/%d, want HTTP/%d", resp.ProtoMajor, wantProto)
				}
			}
			if got := conns.Load(); got != 2 {
				t.Errorf("two requests opened %d connections, want one each", got)
			}
		})
	}
}

// Every first try of a sign-in's calls to GitHub stalls: the code exchange,
// the profile read and the email read each get through on their second.
func TestGitHubWebFlowSurvivesStalledCallsToGitHub(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	u, _ := e.fs.CreateUser(context.Background(), domain.User{ID: e.ids.New(), Login: "ana", Name: "Ana", Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	_, _ = e.fs.CreateIdentity(context.Background(), domain.Identity{ID: e.ids.New(), UserID: u.ID, Provider: domain.ProviderGitHub, ProviderID: "1003", Login: "ana", CreatedAt: t0})
	_, _ = e.fs.PutMembership(context.Background(), domain.Membership{OrgID: e.org.ID, Actor: u.Ref(), Role: domain.RoleMember, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	e.gh.add(ghUser{ID: 1003, Login: "ana", Name: "Ana"}, "tok-ana", "code-ana")
	var firsts atomic.Int32
	var seen sync.Map
	answering := e.gh.srv.Client().Transport
	e.svc.github.http = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if _, again := seen.LoadOrStore(r.Method+" "+r.URL.Path, true); again {
			return answering.RoundTrip(r)
		}
		firsts.Add(1)
		return stalled(r)
	})}
	e.svc.github.secondAfter = 10 * time.Millisecond

	rec := e.signIn(t, "", "code-ana")
	if c := cookieNamed(rec, handoffCookie); c == nil || rec.Header().Get("Location") != "/login?next=%2F" {
		t.Errorf("location %q handoff %v, want the person signed in", rec.Header().Get("Location"), c)
	}
	if got := firsts.Load(); got != 3 {
		t.Errorf("first tries = %d, want the exchange, the profile and the emails", got)
	}
}

// GitHub answers nothing at all: the callback gives up when the time of its
// first call is over, after both tries, and sends the person back to the
// login page.
func TestGitHubWebFlowGivesUpWhenNoTryIsAnswered(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	e.gh.add(ghUser{ID: 1003, Login: "ana"}, "tok-ana", "code-ana")
	var tries atomic.Int32
	e.svc.github.http = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		tries.Add(1)
		return stalled(r)
	})}
	e.svc.github.secondAfter, e.svc.github.timeout = 10*time.Millisecond, 50*time.Millisecond

	rec := e.signIn(t, "", "code-ana")
	if got := rec.Header().Get("Location"); got != "/login?error=github" {
		t.Errorf("location = %q, want the login page's GitHub error", got)
	}
	if got := tries.Load(); got != 2 {
		t.Errorf("requests to GitHub = %d, want the two tries of the code exchange and nothing after", got)
	}
}
