package cli

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

type fakeGitHub struct {
	srv     *httptest.Server
	polls   atomic.Int32
	answers []map[string]any
}

// newFakeGitHub answers the device code request, then the token polls in
// order, repeating the last answer.
func newFakeGitHub(t *testing.T, answers ...map[string]any) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{answers: answers}
	g.srv = serve(t, map[string]http.HandlerFunc{
		"POST /login/device/code": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Accept") != "application/json" {
				t.Errorf("device code Accept = %q", r.Header.Get("Accept"))
			}
			_ = r.ParseForm()
			if r.PostForm.Get("client_id") != "cid" || r.PostForm.Get("scope") != "read:user user:email" {
				t.Errorf("device code form = %v", r.PostForm)
			}
			reply(200, map[string]any{"device_code": "dev123", "user_code": "WDJB-MJHT", "verification_uri": "https://github.com/login/device", "expires_in": 900, "interval": 5})(w, r)
		},
		"POST /login/oauth/access_token": func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			f := r.PostForm
			if f.Get("client_id") != "cid" || f.Get("device_code") != "dev123" || f.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || f.Get("client_secret") != "" {
				t.Errorf("poll form = %v", f)
			}
			n := int(g.polls.Add(1)) - 1
			reply(200, g.answers[min(n, len(g.answers)-1)])(w, r)
		},
	})
	return g
}

func ghAuthRoute(enabled bool) http.HandlerFunc {
	if !enabled {
		return reply(200, apitypes.GitHubAuth{})
	}
	return reply(200, apitypes.GitHubAuth{Enabled: true, ClientID: "cid"})
}

func TestLoginGitHubDeviceFlowStoresToken(t *testing.T) {
	t.Parallel()
	gh := newFakeGitHub(t,
		map[string]any{"error": "authorization_pending"},
		map[string]any{"error": "slow_down", "interval": 10},
		map[string]any{"access_token": "gho_secret", "token_type": "bearer"},
	)
	var got apitypes.GitHubLogin
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/auth/github": ghAuthRoute(true),
		"POST /api/v1/login/github": func(w http.ResponseWriter, r *http.Request) {
			got = readJSON[apitypes.GitHubLogin](t, r)
			reply(201, apitypes.TokenCreated{Secret: "lvk_new", Token: apitypes.Token{Prefix: "lvk_new"}})(w, r)
		},
	})
	h := newHarness(t, srv)
	h.env["LORVIK_API_URL"] = srv.URL
	h.f.GitHubURL = gh.srv.URL
	var slept []time.Duration
	h.f.Sleep = func(d time.Duration) { slept = append(slept, d) }

	if err := h.run(t, "login", "--org", "7", "--token-name", "laptop"); err != nil {
		t.Fatal(err)
	}
	if got != (apitypes.GitHubLogin{AccessToken: "gho_secret", Org: "7", TokenName: "laptop"}) {
		t.Errorf("login body = %+v", got)
	}
	wantSleeps := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second}
	if len(slept) != 3 || slept[0] != wantSleeps[0] || slept[1] != wantSleeps[1] || slept[2] != wantSleeps[2] {
		t.Errorf("sleeps = %v, want %v", slept, wantSleeps)
	}
	if out := h.out.String(); out != "" {
		t.Errorf("stdout = %q, want nothing: the secret is saved, not printed", out)
	}
	if e := h.errOut.String(); !strings.HasPrefix(e, "Open https://github.com/login/device and enter the code WDJB-MJHT\n") || !strings.Contains(e, "signed in to "+srv.URL+"; token lvk_new saved to ") {
		t.Errorf("stderr = %q", e)
	}
	cfg := h.config(t)
	if !strings.Contains(cfg, `token = "lvk_new"`) || !strings.Contains(cfg, srv.URL) || strings.Contains(cfg, "gho_secret") {
		t.Errorf("config = %q", cfg)
	}
}

func TestLoginGitHubSlowDownWithoutIntervalAddsFive(t *testing.T) {
	t.Parallel()
	gh := newFakeGitHub(t, map[string]any{"error": "slow_down"}, map[string]any{"access_token": "gho_x"})
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/auth/github":   ghAuthRoute(true),
		"POST /api/v1/login/github": reply(201, apitypes.TokenCreated{Secret: "lvk_a", Token: apitypes.Token{Prefix: "lvk_a"}}),
	})
	h := newHarness(t, srv)
	h.env["LORVIK_API_URL"] = srv.URL
	h.f.GitHubURL = gh.srv.URL
	var slept []time.Duration
	h.f.Sleep = func(d time.Duration) { slept = append(slept, d) }
	if err := h.run(t, "login"); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 2 || slept[1] != 10*time.Second {
		t.Errorf("sleeps = %v, want second wait of 10s", slept)
	}
}

func TestLoginGitHubJSON(t *testing.T) {
	t.Parallel()
	gh := newFakeGitHub(t, map[string]any{"access_token": "gho_x"})
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/auth/github":   ghAuthRoute(true),
		"POST /api/v1/login/github": reply(201, apitypes.TokenCreated{Secret: "lvk_a", Token: apitypes.Token{Prefix: "lvk_a"}}),
	})
	h := newHarness(t, srv)
	h.env["LORVIK_API_URL"] = srv.URL
	h.f.GitHubURL = gh.srv.URL
	h.f.Sleep = func(time.Duration) {}
	if err := h.run(t, "--json", "login"); err != nil {
		t.Fatal(err)
	}
	if out := h.out.String(); !strings.Contains(out, `"secret": "lvk_a"`) && !strings.Contains(out, `"secret":"lvk_a"`) {
		t.Errorf("stdout = %q", out)
	}
}

func TestLoginGitHubFailures(t *testing.T) {
	t.Parallel()
	pending := problem(403, apitypes.Problem{Title: "membership pending approval", Hint: "ask an org admin to grant your role on the Access page"})
	tests := []struct {
		name     string
		enabled  bool
		github   []map[string]any
		loginRte http.HandlerFunc
		wantCode int
		wantErr  string
	}{
		{"not enabled", false, nil, nil, 2, "GitHub login is not enabled on this server; sign in with `lorvik login <user>` (password) or `--token`"},
		{"access denied", true, []map[string]any{{"error": "access_denied"}}, nil, 5, "denied"},
		{"expired token", true, []map[string]any{{"error": "expired_token"}}, nil, 5, "expired"},
		{"unexpected error", true, []map[string]any{{"error": "device_flow_disabled", "error_description": "Device flow must be explicitly enabled"}}, nil, 1, "device_flow_disabled"},
		{"membership pending", true, []map[string]any{{"access_token": "gho_x"}}, pending, 5, "hint: ask an org admin to grant your role on the Access page"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			routes := map[string]http.HandlerFunc{"GET /api/v1/auth/github": ghAuthRoute(tt.enabled)}
			if tt.loginRte != nil {
				routes["POST /api/v1/login/github"] = tt.loginRte
			} else {
				routes["POST /api/v1/login/github"] = func(http.ResponseWriter, *http.Request) { t.Error("login/github called") }
			}
			srv := serve(t, routes)
			h := newHarness(t, srv)
			h.env["LORVIK_API_URL"] = srv.URL
			h.f.Sleep = func(time.Duration) {}
			if tt.github != nil {
				h.f.GitHubURL = newFakeGitHub(t, tt.github...).srv.URL
			}
			err := h.run(t, "login")
			if err == nil {
				t.Fatal("login succeeded")
			}
			if got := cmdutil.ExitCode(err); got != tt.wantCode {
				t.Errorf("exit = %d, want %d (%v)", got, tt.wantCode, err)
			}
			if msg := cmdutil.FormatError(err, nil); !strings.Contains(msg, tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", msg, tt.wantErr)
			}
			if h.config(t) != "" {
				t.Errorf("config written: %q", h.config(t))
			}
			if h.out.String() != "" {
				t.Errorf("stdout = %q", h.out.String())
			}
		})
	}
}

func TestLoginGitHubStopsWhenCodeExpires(t *testing.T) {
	t.Parallel()
	gh := newFakeGitHub(t, map[string]any{"error": "authorization_pending"}, map[string]any{"error": "slow_down", "interval": 400}, map[string]any{"error": "authorization_pending"})
	srv := serve(t, map[string]http.HandlerFunc{"GET /api/v1/auth/github": ghAuthRoute(true)})
	h := newHarness(t, srv)
	h.env["LORVIK_API_URL"] = srv.URL
	h.f.GitHubURL = gh.srv.URL
	now := fixedNow
	h.f.Now = func() time.Time { return now }
	var slept []time.Duration
	h.f.Sleep = func(d time.Duration) { slept = append(slept, d); now = now.Add(d) }
	err := h.run(t, "login")
	if cmdutil.ExitCode(err) != 5 {
		t.Fatalf("exit = %d (%v), want 5", cmdutil.ExitCode(err), err)
	}
	want := []time.Duration{5 * time.Second, 5 * time.Second, 400 * time.Second, 400 * time.Second, 90 * time.Second}
	if len(slept) != len(want) {
		t.Fatalf("sleeps = %v, want %v", slept, want)
	}
	for i := range want {
		if slept[i] != want[i] {
			t.Fatalf("sleeps = %v, want %v", slept, want)
		}
	}
	if got := gh.polls.Load(); got != 4 {
		t.Errorf("polls = %d, want 4: the wait that reaches the deadline must not poll", got)
	}
	if !now.Equal(fixedNow.Add(900 * time.Second)) {
		t.Errorf("clock advanced to %v, want exactly the 900s deadline", now.Sub(fixedNow))
	}
}

func TestLoginGitHubRejectsUnusableDeviceCode(t *testing.T) {
	t.Parallel()
	good := map[string]any{"device_code": "dev123", "user_code": "WDJB-MJHT", "verification_uri": "https://github.com/login/device", "expires_in": 900, "interval": 5}
	without := func(k string, v any) map[string]any {
		m := maps.Clone(good)
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
		return m
	}
	tests := []struct {
		name string
		body map[string]any
	}{
		{"missing device_code", without("device_code", nil)},
		{"missing user_code", without("user_code", nil)},
		{"missing verification_uri", without("verification_uri", nil)},
		{"missing expires_in", without("expires_in", nil)},
		{"zero expires_in", without("expires_in", 0)},
		{"negative expires_in", without("expires_in", -1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var polls atomic.Int32
			gh := serve(t, map[string]http.HandlerFunc{
				"POST /login/device/code":        reply(200, tt.body),
				"POST /login/oauth/access_token": func(http.ResponseWriter, *http.Request) { polls.Add(1) },
			})
			srv := serve(t, map[string]http.HandlerFunc{"GET /api/v1/auth/github": ghAuthRoute(true)})
			h := newHarness(t, srv)
			h.env["LORVIK_API_URL"] = srv.URL
			h.f.GitHubURL = gh.URL
			h.f.Sleep = func(time.Duration) {}
			err := h.run(t, "login")
			if err == nil || !strings.Contains(err.Error(), "GitHub answered without a usable device code") {
				t.Fatalf("err = %v", err)
			}
			if polls.Load() != 0 {
				t.Errorf("polled %d times", polls.Load())
			}
		})
	}
}

func TestLoginGitHubRejectsOversizedBody(t *testing.T) {
	t.Parallel()
	gh := serve(t, map[string]http.HandlerFunc{
		"POST /login/device/code": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"device_code":"` + strings.Repeat("a", maxGitHubBody) + `"}`))
		},
	})
	srv := serve(t, map[string]http.HandlerFunc{"GET /api/v1/auth/github": ghAuthRoute(true)})
	h := newHarness(t, srv)
	h.env["LORVIK_API_URL"] = srv.URL
	h.f.GitHubURL = gh.URL
	h.f.Sleep = func(time.Duration) {}
	err := h.run(t, "login")
	if err == nil || !strings.Contains(err.Error(), "GitHub's answer") || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v", err)
	}
}
