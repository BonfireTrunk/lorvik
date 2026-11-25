package cli

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

const signInSecret = "lvk_signin_secret"

// orgServer is a server whose sign-in answers a sign-in token or an org token.
type orgServer struct {
	mu       sync.Mutex
	loginReq []map[string]any
	calls    []string
	bearers  map[string]string
	entered  []string
	enterReq []apitypes.EnterOrg
}

func (o *orgServer) note(r *http.Request) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, r.Method+" "+r.URL.Path)
	if o.bearers == nil {
		o.bearers = map[string]string{}
	}
	o.bearers[r.URL.Path] = r.Header.Get("Authorization")
}

// newOrgServer answers both sign-in routes with token, and serves the org
// routes of a sign-in token with the given orgs.
func newOrgServer(t *testing.T, token apitypes.TokenCreated, orgs []apitypes.SignInOrg) (*orgServer, string) {
	t.Helper()
	o := &orgServer{}
	login := func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		o.loginReq = append(o.loginReq, readJSON[map[string]any](t, r))
		o.mu.Unlock()
		reply(201, token)(w, r)
	}
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/auth/github":   ghAuthRoute(true),
		"POST /api/v1/login":        login,
		"POST /api/v1/login/github": login,
		"GET /api/v1/login/orgs": func(w http.ResponseWriter, r *http.Request) {
			o.note(r)
			reply(200, apitypes.SignInOrgs{Orgs: orgs})(w, r)
		},
		"POST /api/v1/login/orgs": func(w http.ResponseWriter, r *http.Request) {
			o.note(r)
			in := readJSON[apitypes.CreateOrg](t, r)
			reply(201, apitypes.SignInOrg{ID: "900", Name: in.Name, Role: "owner", Status: "member"})(w, r)
		},
		"POST /api/v1/login/orgs/{id}/token": func(w http.ResponseWriter, r *http.Request) {
			o.note(r)
			o.mu.Lock()
			o.entered = append(o.entered, r.PathValue("id"))
			o.enterReq = append(o.enterReq, readJSON[apitypes.EnterOrg](t, r))
			o.mu.Unlock()
			reply(201, apitypes.TokenCreated{Secret: "lvk_org", Token: apitypes.Token{Prefix: "lvk_org", Scope: "write"}})(w, r)
		},
	})
	return o, srv.URL
}

func signInToken() apitypes.TokenCreated {
	return apitypes.TokenCreated{Secret: signInSecret, Token: apitypes.Token{Prefix: "lvk_signin", Scope: apitypes.ScopeSignIn}}
}

type loginForm struct {
	name string
	args []string
}

// loginForms are the two ways to sign in; both end in the same step.
var loginForms = []loginForm{
	{"github", nil},
	{"password", []string{"ana"}},
}

func (f loginForm) harness(t *testing.T, url string) *harness {
	t.Helper()
	h := newHarness(t, nil)
	h.env["LORVIK_API_URL"] = url
	h.env["LORVIK_PASSWORD"] = "pw"
	h.f.Sleep = func(time.Duration) {}
	h.f.GitHubURL = newFakeGitHub(t, map[string]any{"access_token": "gho_x"}).srv.URL
	return h
}

func (f loginForm) run(t *testing.T, h *harness, extra ...string) error {
	t.Helper()
	return h.run(t, append(append([]string{"login"}, f.args...), extra...)...)
}

func (h *harness) terminal(answer string) {
	h.streams.SetTTY(true, true)
	h.streams.SetStderrTTY(true)
	h.streams.In = strings.NewReader(answer)
}

func (h *harness) assertNothingSaved(t *testing.T) {
	t.Helper()
	if h.config(t) != "" {
		t.Errorf("config written: %q", h.config(t))
	}
	if h.out.String() != "" {
		t.Errorf("stdout = %q", h.out.String())
	}
}

func (h *harness) assertNoSecret(t *testing.T) {
	t.Helper()
	for name, got := range map[string]string{"config": h.config(t), "stdout": h.out.String(), "stderr": h.errOut.String()} {
		if strings.Contains(got, signInSecret) {
			t.Errorf("%s contains the sign-in secret: %q", name, got)
		}
	}
}

func TestLoginOneOrgSavesTheOrgTokenUnchanged(t *testing.T) {
	t.Parallel()
	for _, f := range loginForms {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			o, url := newOrgServer(t, apitypes.TokenCreated{Secret: "lvk_one", Token: apitypes.Token{Prefix: "lvk_one", Scope: "write"}}, nil)
			h := f.harness(t, url)
			if err := f.run(t, h); err != nil {
				t.Fatal(err)
			}
			if len(o.calls) != 0 {
				t.Errorf("org routes called: %v", o.calls)
			}
			if h.out.String() != "" || !strings.Contains(h.config(t), `token = "lvk_one"`) {
				t.Errorf("stdout = %q, config = %q", h.out.String(), h.config(t))
			}
		})
	}
}

func TestLoginCreateOrgEntersTheNewOrg(t *testing.T) {
	t.Parallel()
	for _, f := range loginForms {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			o, url := newOrgServer(t, signInToken(), nil)
			h := f.harness(t, url)
			if err := f.run(t, h, "--create-org", " Acme ", "--token-name", "laptop"); err != nil {
				t.Fatal(err)
			}
			if len(o.loginReq) != 1 || o.loginReq[0]["choose"] != true {
				t.Errorf("sign-in request = %v, want choose true", o.loginReq)
			}
			wantCalls := []string{"POST /api/v1/login/orgs", "POST /api/v1/login/orgs/900/token"}
			if strings.Join(o.calls, ",") != strings.Join(wantCalls, ",") {
				t.Errorf("calls = %v, want %v", o.calls, wantCalls)
			}
			for path, got := range o.bearers {
				if got != "Bearer "+signInSecret {
					t.Errorf("%s authorization = %q", path, got)
				}
			}
			if len(o.enterReq) != 1 || o.enterReq[0].TokenName != "laptop" {
				t.Errorf("enter body = %+v", o.enterReq)
			}
			if h.out.String() != "" || !strings.Contains(h.config(t), `token = "lvk_org"`) {
				t.Errorf("stdout = %q, config = %q", h.out.String(), h.config(t))
			}
			if e := h.errOut.String(); !strings.Contains(e, "lorvik: created org Acme (900)\n") || !strings.Contains(e, "token lvk_org saved to ") {
				t.Errorf("stderr = %q", e)
			}
			h.assertNoSecret(t)
		})
	}
}

func TestLoginCreateOrgJSONPrintsOnlyTheOrgToken(t *testing.T) {
	t.Parallel()
	_, url := newOrgServer(t, signInToken(), nil)
	f := loginForms[1]
	h := f.harness(t, url)
	if err := h.run(t, "--json", "login", "ana", "--create-org", "Acme"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "lvk_org") {
		t.Errorf("stdout = %q", h.out.String())
	}
	h.assertNoSecret(t)
}

func TestLoginSignInTokenWithNoEnterableOrgExits5(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		orgs []apitypes.SignInOrg
		want []string
	}{
		{"no org", nil, []string{`you belong to no org on `, `create one with "lorvik login --create-org NAME", or ask an admin of an org to invite your GitHub username`}},
		{"only a request", []apitypes.SignInOrg{{ID: "7", Name: "Beta", Role: "viewer", Status: "requested"}}, []string{"you belong to no org on ", "your request to join Beta is waiting for an admin"}},
	}
	for _, f := range loginForms {
		for _, tt := range tests {
			t.Run(f.name+" "+tt.name, func(t *testing.T) {
				t.Parallel()
				_, url := newOrgServer(t, signInToken(), tt.orgs)
				h := f.harness(t, url)
				err := f.run(t, h)
				if cmdutil.ExitCode(err) != 5 {
					t.Fatalf("exit = %d (%v), want 5", cmdutil.ExitCode(err), err)
				}
				msg := cmdutil.FormatError(err, nil)
				for _, w := range tt.want {
					if !strings.Contains(msg, w) {
						t.Errorf("error = %q, want it to contain %q", msg, w)
					}
				}
				h.assertNothingSaved(t)
				h.assertNoSecret(t)
			})
		}
	}
}

func TestLoginSeveralOrgsWithoutTerminalExits4ListingThem(t *testing.T) {
	t.Parallel()
	orgs := []apitypes.SignInOrg{
		{ID: "1", Name: "Acme", Role: "owner", Status: "member"},
		{ID: "2", Name: "Beta", Role: "member", Status: "invited"},
		{ID: "3", Name: "Gamma", Role: "viewer", Status: "requested"},
	}
	for _, f := range loginForms {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			o, url := newOrgServer(t, signInToken(), orgs)
			h := f.harness(t, url)
			err := f.run(t, h)
			if cmdutil.ExitCode(err) != 4 {
				t.Fatalf("exit = %d (%v), want 4", cmdutil.ExitCode(err), err)
			}
			want := "pass --org <id> to choose the org; naming one you are invited to accepts the invitation\n1  Acme  owner\n2  Beta  member, invited"
			if msg := cmdutil.FormatError(err, nil); !strings.Contains(msg, want) || strings.Contains(msg, "Gamma") {
				t.Errorf("error = %q, want %q and no requested org", msg, want)
			}
			if len(o.entered) != 0 {
				t.Errorf("entered %v", o.entered)
			}
			h.assertNothingSaved(t)
			h.assertNoSecret(t)
		})
	}
}

func TestLoginOneEnterableOrgWithoutTerminal(t *testing.T) {
	t.Parallel()
	t.Run("a membership is entered", func(t *testing.T) {
		t.Parallel()
		orgs := []apitypes.SignInOrg{
			{ID: "2", Name: "Beta", Role: "member", Status: "member"},
			{ID: "3", Name: "Gamma", Role: "viewer", Status: "requested"},
		}
		o, url := newOrgServer(t, signInToken(), orgs)
		h := loginForms[1].harness(t, url)
		if err := loginForms[1].run(t, h); err != nil {
			t.Fatal(err)
		}
		if len(o.entered) != 1 || o.entered[0] != "2" {
			t.Errorf("entered = %v, want [2]", o.entered)
		}
		if h.out.String() != "" {
			t.Errorf("stdout = %q", h.out.String())
		}
	})
	// Entering it would accept the invitation for the person; a script must name the org.
	t.Run("an invitation is listed, not entered", func(t *testing.T) {
		t.Parallel()
		orgs := []apitypes.SignInOrg{
			{ID: "2", Name: "Beta", Role: "member", Status: "invited"},
			{ID: "3", Name: "Gamma", Role: "viewer", Status: "requested"},
		}
		o, url := newOrgServer(t, signInToken(), orgs)
		h := loginForms[1].harness(t, url)
		err := loginForms[1].run(t, h)
		if cmdutil.ExitCode(err) != 4 {
			t.Fatalf("exit = %d (%v), want 4", cmdutil.ExitCode(err), err)
		}
		want := "pass --org <id> to choose the org; naming one you are invited to accepts the invitation\n2  Beta  member, invited"
		if msg := cmdutil.FormatError(err, nil); !strings.Contains(msg, want) {
			t.Errorf("error = %q, want %q", msg, want)
		}
		if len(o.entered) != 0 {
			t.Errorf("entered %v, want none", o.entered)
		}
		h.assertNothingSaved(t)
		h.assertNoSecret(t)
	})
}

func TestLoginSeveralOrgsOnTerminalEntersTheChosenOne(t *testing.T) {
	t.Parallel()
	orgs := []apitypes.SignInOrg{
		{ID: "1", Name: "Acme", Role: "owner", Status: "member"},
		{ID: "2", Name: "Beta", Role: "member", Status: "invited"},
	}
	for _, f := range loginForms {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			o, url := newOrgServer(t, signInToken(), orgs)
			h := f.harness(t, url)
			h.terminal(" 2\n")
			if err := f.run(t, h); err != nil {
				t.Fatal(err)
			}
			if len(o.entered) != 1 || o.entered[0] != "2" {
				t.Errorf("entered = %v, want [2]", o.entered)
			}
			if e := h.errOut.String(); !strings.Contains(e, "  1  Acme  owner\n  2  Beta  invited as member\nOrg [1-2]: ") {
				t.Errorf("stderr = %q", e)
			}
			if h.out.String() != "" || !strings.Contains(h.config(t), `token = "lvk_org"`) {
				t.Errorf("stdout = %q, config = %q", h.out.String(), h.config(t))
			}
			h.assertNoSecret(t)
		})
	}
}

func TestLoginBadOrgAnswerIsAUsageErrorAndSavesNothing(t *testing.T) {
	t.Parallel()
	orgs := []apitypes.SignInOrg{
		{ID: "1", Name: "Acme", Role: "owner", Status: "member"},
		{ID: "2", Name: "Beta", Role: "member", Status: "member"},
	}
	for _, answer := range []string{"\n", "", "x\n", "0\n", "3\n", "-1\n"} {
		t.Run(fmt.Sprintf("%q", answer), func(t *testing.T) {
			t.Parallel()
			o, url := newOrgServer(t, signInToken(), orgs)
			h := loginForms[1].harness(t, url)
			h.terminal(answer)
			err := loginForms[1].run(t, h)
			if cmdutil.ExitCode(err) != 2 {
				t.Fatalf("exit = %d (%v), want 2", cmdutil.ExitCode(err), err)
			}
			if len(o.entered) != 0 {
				t.Errorf("entered %v", o.entered)
			}
			h.assertNothingSaved(t)
			h.assertNoSecret(t)
		})
	}
}

func TestLoginCreateOrgFlagConflicts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"with --org", []string{"--create-org", "Acme", "--org", "7"}, "do not combine it with --org or --token"},
		{"with --token", []string{"--create-org", "Acme", "--token", "lvk_x"}, "do not combine it with --org or --token"},
		{"empty name", []string{"--create-org", ""}, "--create-org needs a name"},
		{"blank name", []string{"--create-org", "  "}, "--create-org needs a name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			o, url := newOrgServer(t, signInToken(), nil)
			h := loginForms[1].harness(t, url)
			err := h.run(t, append([]string{"login", "ana"}, tt.args...)...)
			if cmdutil.ExitCode(err) != 2 {
				t.Fatalf("exit = %d (%v), want 2", cmdutil.ExitCode(err), err)
			}
			if msg := cmdutil.FormatError(err, nil); !strings.Contains(msg, tt.want) {
				t.Errorf("error = %q, want it to contain %q", msg, tt.want)
			}
			if len(o.loginReq) != 0 || len(o.calls) != 0 {
				t.Errorf("requests were sent: %v %v", o.loginReq, o.calls)
			}
			h.assertNothingSaved(t)
		})
	}
}
