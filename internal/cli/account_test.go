package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func TestTokenCreatePrintsOnlyTheSecret(t *testing.T) {
	t.Parallel()
	var got apitypes.CreateToken
	srv := serve(t, map[string]http.HandlerFunc{
		"POST /api/v1/tokens": func(w http.ResponseWriter, r *http.Request) {
			got = readJSON[apitypes.CreateToken](t, r)
			reply(201, apitypes.TokenCreated{Token: apitypes.Token{ID: "9", Prefix: "lvk_a_AAAAAA"}, Secret: "lvk_a_secret"})(w, r)
		},
	})
	h := newHarness(t, srv)
	if err := h.run(t, "token", "create", "--agent", "77", "--name", "ci", "--scope", "read", "--expires-in", "24h"); err != nil {
		t.Fatal(err)
	}
	if h.out.String() != "lvk_a_secret\n" {
		t.Errorf("stdout = %q, want only the secret", h.out.String())
	}
	if !strings.Contains(h.errOut.String(), "lvk_a_AAAAAA") || strings.Contains(h.errOut.String(), "lvk_a_secret") {
		t.Errorf("stderr = %q, want the prefix and not the secret", h.errOut.String())
	}
	want := apitypes.CreateToken{Name: "ci", Scope: "read", ExpiresIn: "24h", Agent: "77"}
	if got != want {
		t.Errorf("request = %+v, want %+v", got, want)
	}
	if err := newHarness(t, srv).run(t, "token", "create", "--scope", "admin"); cmdutil.ExitCode(err) != 2 {
		t.Errorf("bad scope: exit = %d, want 2", cmdutil.ExitCode(err))
	}
}

func TestLogin(t *testing.T) {
	t.Parallel()
	loginRoutes := func(rec *recorder) map[string]http.HandlerFunc {
		return rec.wrapAll(map[string]http.HandlerFunc{
			"POST /api/v1/login": reply(201, apitypes.TokenCreated{Token: apitypes.Token{Prefix: "lvk_u_NEWNEW"}, Secret: "lvk_u_NEWNEWsecret"}),
		})
	}
	t.Run("password from the environment", func(t *testing.T) {
		t.Parallel()
		rec := &recorder{}
		srv := serve(t, loginRoutes(rec))
		h := newHarness(t, srv)
		h.f.Client = nil // build the real client from --api-url, as a user's run does
		h.env["LORVIK_PASSWORD"] = "hunter2"
		h.env["LORVIK_TOKEN"] = "lvk_u_stale"
		if err := h.run(t, "login", "--password", "ana", "--org", "55", "--token-name", "laptop", "--api-url", srv.URL); err != nil {
			t.Fatal(err)
		}
		req := rec.last(t, "POST /api/v1/login")
		if req.Body != `{"login":"ana","password":"hunter2","org":"55","token_name":"laptop"}` {
			t.Errorf("body = %s", req.Body)
		}
		if req.Header.Get("Authorization") != "" {
			t.Errorf("login sent a bearer token %q; the route is public", req.Header.Get("Authorization"))
		}
		if h.out.String() != "" {
			t.Errorf("stdout = %q", h.out.String())
		}
		cfg := h.config(t)
		if !strings.Contains(cfg, `token = "lvk_u_NEWNEWsecret"`) || !strings.Contains(cfg, `api_url = "`+srv.URL+`"`) {
			t.Errorf("config = %q", cfg)
		}
		if strings.Contains(h.errOut.String(), "hunter2") || strings.Contains(h.errOut.String(), "NEWNEWsecret") {
			t.Errorf("stderr = %q leaks a secret", h.errOut.String())
		}
	})
	t.Run("password prompted on a terminal", func(t *testing.T) {
		t.Parallel()
		rec := &recorder{}
		h := newHarness(t, serve(t, loginRoutes(rec)))
		h.streams.SetTTY(true, false)
		h.streams.ReadPassword = func() (string, error) { return "typed", nil }
		if err := h.run(t, "login", "--password", "--login", "ana"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(rec.last(t, "POST").Body, `"password":"typed"`) {
			t.Errorf("body = %s", rec.last(t, "POST").Body)
		}
		if !strings.Contains(h.errOut.String(), "Password: ") {
			t.Errorf("stderr = %q, want the prompt", h.errOut.String())
		}
		if strings.Contains(h.out.String(), "Password") {
			t.Errorf("stdout = %q, the prompt belongs on stderr", h.out.String())
		}
	})
	t.Run("no password and no terminal fails before any request", func(t *testing.T) {
		t.Parallel()
		rec := &recorder{}
		h := newHarness(t, serve(t, loginRoutes(rec)))
		err := h.run(t, "login", "ana")
		if cmdutil.ExitCode(err) != 2 || !strings.Contains(err.Error(), "LORVIK_PASSWORD") {
			t.Errorf("err = %v, want a usage error naming LORVIK_PASSWORD", err)
		}
		if len(rec.calls()) != 0 {
			t.Errorf("requests = %v, want none", rec.calls())
		}
		if strings.Contains(h.config(t), "token") {
			t.Errorf("config = %q, nothing should be stored", h.config(t))
		}
	})
	t.Run("--token stores the secret in LORVIK_CONFIG without a request or password", func(t *testing.T) {
		t.Parallel()
		rec := &recorder{}
		h := newHarness(t, serve(t, loginRoutes(rec)))
		agentCfg := filepath.Join(t.TempDir(), "sub", "claude-code.toml")
		h.env["LORVIK_CONFIG"] = agentCfg
		if err := h.run(t, "login", "--token", "lvk_a_AGENTsecret", "--api-url", "http://board.example"); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(agentCfg)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(b); !strings.Contains(got, `token = "lvk_a_AGENTsecret"`) || !strings.Contains(got, `api_url = "http://board.example"`) {
			t.Errorf("config = %q", got)
		}
		if fi, err := os.Stat(agentCfg); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v (%v), want 0600", fi.Mode().Perm(), err)
		}
		if h.config(t) != "" {
			t.Errorf("default config = %q, want it untouched", h.config(t))
		}
		if len(rec.calls()) != 0 {
			t.Errorf("requests = %v, want none", rec.calls())
		}
		if strings.Contains(h.out.String()+h.errOut.String(), "AGENTsecret") {
			t.Errorf("stdout %q stderr %q leak the secret", h.out.String(), h.errOut.String())
		}
	})
	t.Run("--token refuses sign-in arguments", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, serve(t, loginRoutes(&recorder{})))
		err := h.run(t, "login", "ana", "--token", "lvk_a_x")
		if cmdutil.ExitCode(err) != 2 {
			t.Errorf("exit = %d (%v), want 2", cmdutil.ExitCode(err), err)
		}
		if h.config(t) != "" {
			t.Errorf("config = %q, nothing should be stored", h.config(t))
		}
	})
	t.Run("a rejected login stores nothing", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, map[string]http.HandlerFunc{"POST /api/v1/login": problem(401, apitypes.Problem{Title: "Unauthorized"})})
		h := newHarness(t, srv)
		h.env["LORVIK_PASSWORD"] = "bad"
		err := h.run(t, "login", "ana")
		if cmdutil.ExitCode(err) != 5 {
			t.Errorf("exit = %d (%v), want 5", cmdutil.ExitCode(err), err)
		}
		if h.config(t) != "" || h.out.String() != "" {
			t.Errorf("config %q stdout %q, want nothing stored or printed", h.config(t), h.out.String())
		}
	})
}

func TestMeShowsRolesAndResolvesTheName(t *testing.T) {
	t.Parallel()
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/me": reply(200, apitypes.Me{
			Actor: ana, Org: apitypes.Org{ID: "1", Name: "Acme"}, Role: "reviewer",
			Projects:    []apitypes.ProjectRole{{Project: "web", Role: "admin"}, {Project: "api", Role: "member"}},
			TokenPrefix: "lvk_u_AAAAAA", TokenExpiry: fixedNow.Add(72 * time.Hour),
		}),
		"GET /api/v1/actors": reply(200, fixtureActors()),
	})
	h := newHarness(t, srv)
	if err := h.run(t, "me"); err != nil {
		t.Fatal(err)
	}
	want := "Ana Souza (user " + ana.ID + ")  org Acme  token lvk_u_AAAAAA expires in 3d\norg role: reviewer\nproject roles: web=admin api=member\n"
	if h.out.String() != want {
		t.Errorf("stdout = %q, want %q", h.out.String(), want)
	}
}

func TestAgentBoundOnlyToProjectsHasNoOrgRole(t *testing.T) {
	t.Parallel()
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/me":     reply(200, apitypes.Me{Actor: claude, Org: apitypes.Org{Name: "Acme"}, Projects: []apitypes.ProjectRole{{Project: "web", Role: "member"}}}),
		"GET /api/v1/actors": reply(200, fixtureActors()),
	})
	h := newHarness(t, srv)
	if err := h.run(t, "me"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "org role: none\nproject roles: web=member\n") {
		t.Errorf("stdout = %q", h.out.String())
	}
}

func TestOrgSetKeepsUntouchedSettings(t *testing.T) {
	t.Parallel()
	var got apitypes.UpdateOrg
	rec := &recorder{}
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/org": reply(200, apitypes.Org{Name: "Acme", Settings: &apitypes.OrgSettings{AgentApproval: "admin", TokenTTLDefault: "720h"}}),
		"PATCH /api/v1/org": rec.wrap(func(w http.ResponseWriter, r *http.Request) {
			got = readJSON[apitypes.UpdateOrg](t, r)
			reply(200, apitypes.Org{Name: "Acme", Settings: got.Settings})(w, r)
		}),
	})
	h := newHarness(t, srv)
	if err := h.run(t, "org", "set", "--project-visibility", "private"); err != nil {
		t.Fatal(err)
	}
	want := apitypes.OrgSettings{AgentApproval: "admin", TokenTTLDefault: "720h", ProjectVisibilityDefault: "private"}
	if got.Name != nil || got.Settings == nil || *got.Settings != want {
		t.Errorf("update = %+v / %+v, want settings %+v and no name", got, got.Settings, want)
	}
	if !strings.Contains(h.out.String(), "project visibility default: private") {
		t.Errorf("stdout = %q", h.out.String())
	}
}

func TestSignInToAServerNamedOnlyByTheRepoAsksFirst(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		saved    func(srv string) string // the user config's api_url
		args     []string
		tty      bool
		answers  []any
		wantCode int
		wantAsk  []string
		sent     bool
	}{
		{name: "no terminal refuses", saved: func(string) string { return "https://lorvik.example" }, args: []string{"login", "ana"}, wantCode: cmdutil.ExitUnauthorized},
		{name: "--token without a terminal refuses", saved: func(string) string { return "https://lorvik.example" }, args: []string{"login", "--token", "lvk_given"}, wantCode: cmdutil.ExitUnauthorized},
		{name: "terminal, answered no", saved: func(string) string { return "https://lorvik.example" }, args: []string{"login", "ana"}, tty: true, answers: []any{false},
			wantCode: cmdutil.ExitCode(cmdutil.ErrCancelled), wantAsk: []string{"This repo's .lorvik.toml ("}},
		{name: "terminal, answered yes", saved: func(string) string { return "https://lorvik.example" }, args: []string{"login", "ana"}, tty: true, answers: []any{true},
			wantAsk: []string{"This repo's .lorvik.toml"}, sent: true},
		{name: "--api-url is your choice", saved: func(string) string { return "https://lorvik.example" }, args: []string{"login", "ana", "--api-url", ""}, sent: true},
		{name: "the repo names your saved server", saved: func(srv string) string { return srv }, args: []string{"login", "ana"}, sent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var rec recorder
			srv := serve(t, rec.wrapAll(map[string]http.HandlerFunc{
				"POST /api/v1/login": reply(200, apitypes.TokenCreated{Secret: "lvk_new", Token: apitypes.Token{Prefix: "lvk_new"}}),
			}))
			h := newHarness(t, nil)
			h.env["LORVIK_PASSWORD"] = "s3cret"
			saved := "api_url = \"" + tt.saved(srv.URL) + "\"\ntoken = \"lvk_old\"\n"
			write(t, filepath.Join(h.xdg, "lorvik", "config.toml"), saved)
			write(t, filepath.Join(h.cwd, bindingFile), "project = \"p\"\napi_url = \""+srv.URL+"\"\n")
			if tt.tty {
				h.streams.SetTTY(true, true)
				h.streams.SetStderrTTY(true)
			}
			fp := newFakePrompter(t, tt.answers...)
			h.f.Prompter = fp
			args := tt.args
			if i := slices.Index(args, "--api-url"); i >= 0 {
				args = slices.Clone(args)
				args[i+1] = srv.URL
			}
			err := h.run(t, args...)
			if got := cmdutil.ExitCode(err); got != tt.wantCode {
				t.Fatalf("exit = %d (%v), want %d", got, err, tt.wantCode)
			}
			asked := fp.titles()
			if len(asked) != len(tt.wantAsk) {
				t.Fatalf("asked %q, want %d questions", asked, len(tt.wantAsk))
			}
			for i, want := range tt.wantAsk {
				if !strings.HasPrefix(asked[i], want) || !strings.Contains(asked[i], srv.URL) {
					t.Errorf("question %q, want it to start %q and name %s", asked[i], want, srv.URL)
				}
			}
			if got := len(rec.calls()) > 0; got != tt.sent {
				t.Errorf("server got %v, want a request: %v", rec.calls(), tt.sent)
			}
			if !tt.sent && h.config(t) != saved {
				t.Errorf("config changed to %q", h.config(t))
			}
			if tt.wantCode == cmdutil.ExitUnauthorized && !strings.Contains(err.Error(), "nothing sent. If you trust it, sign in with --api-url "+srv.URL) {
				t.Errorf("error = %v", err)
			}
		})
	}
}
