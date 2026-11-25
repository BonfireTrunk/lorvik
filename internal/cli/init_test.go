package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

// ask is one prompt the fake saw.
type ask struct {
	Kind     string
	Q        cmdutil.Question
	Opts     []cmdutil.Option
	DefIndex int
	DefText  string
	DefBool  bool
}

// fakePrompter plays back queued answers: an int for Select, a string for
// Input and Password, a bool for Confirm, or an error (such as
// cmdutil.ErrCancelled). It records every question and fails the test on a
// prompt it has no answer for. An Input answer that fails validate is
// recorded in Rejected and the next answer is used, as a re-prompt would.
type fakePrompter struct {
	t        *testing.T
	mu       sync.Mutex
	answers  []any
	Asked    []ask
	Rejected []string
}

func newFakePrompter(t *testing.T, answers ...any) *fakePrompter {
	return &fakePrompter{t: t, answers: answers}
}

func (f *fakePrompter) next(a ask) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Asked = append(f.Asked, a)
	if len(f.answers) == 0 {
		f.t.Errorf("unexpected %s prompt %q", a.Kind, a.Q.Title)
		return cmdutil.ErrCancelled
	}
	v := f.answers[0]
	f.answers = f.answers[1:]
	return v
}

func answerAs[T any](f *fakePrompter, v any, a ask) (T, error) {
	var zero T
	if err, ok := v.(error); ok {
		return zero, err
	}
	got, ok := v.(T)
	if !ok {
		f.t.Errorf("%s prompt %q got answer %v (%T)", a.Kind, a.Q.Title, v, v)
		return zero, cmdutil.ErrCancelled
	}
	return got, nil
}

func (f *fakePrompter) Select(_ context.Context, q cmdutil.Question, opts []cmdutil.Option, def int) (int, error) {
	a := ask{Kind: "select", Q: q, Opts: opts, DefIndex: def}
	return answerAs[int](f, f.next(a), a)
}

func (f *fakePrompter) Input(_ context.Context, q cmdutil.Question, def string, validate func(string) error) (string, error) {
	a := ask{Kind: "input", Q: q, DefText: def}
	for {
		s, err := answerAs[string](f, f.next(a), a)
		if err != nil || validate == nil {
			return s, err
		}
		verr := validate(s)
		if verr == nil {
			return s, nil
		}
		f.mu.Lock()
		f.Rejected = append(f.Rejected, verr.Error())
		f.mu.Unlock()
	}
}

func (f *fakePrompter) Password(_ context.Context, q cmdutil.Question) (string, error) {
	a := ask{Kind: "password", Q: q}
	return answerAs[string](f, f.next(a), a)
}

func (f *fakePrompter) Confirm(_ context.Context, q cmdutil.Question, def bool) (bool, error) {
	a := ask{Kind: "confirm", Q: q, DefBool: def}
	return answerAs[bool](f, f.next(a), a)
}

func (f *fakePrompter) titles() []string {
	var out []string
	for _, a := range f.Asked {
		out = append(out, a.Q.Title)
	}
	return out
}

func labels(opts []cmdutil.Option) []string {
	var out []string
	for _, o := range opts {
		out = append(out, o.Label)
	}
	return out
}

func owner() apitypes.Me {
	return apitypes.Me{
		Actor: apitypes.Actor{Kind: "user", ID: "1"},
		User:  &apitypes.User{ID: "1", Login: "ada", Name: "Ada"},
		Org:   apitypes.Org{ID: "9", Name: "Acme"},
		Role:  "owner",
	}
}

func asMember(me apitypes.Me) apitypes.Me { me.Role = "member"; return me }

// initEnv is an interactive init run: a terminal, a repository named
// "payments" under HOME, and the scripted prompter.
type initEnv struct {
	*harness
	home string
	repo string
	fp   *fakePrompter
	rec  *recorder
	got  *apitypes.CreateProject
}

type initServer struct {
	me       apitypes.Me
	projects []apitypes.Project
}

func newInitEnv(t *testing.T, s initServer, answers ...any) *initEnv {
	t.Helper()
	e := &initEnv{rec: &recorder{}, got: &apitypes.CreateProject{}}
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/me":       reply(200, s.me),
		"GET /api/v1/projects": reply(200, apitypes.ProjectList{Items: s.projects}),
		"POST /api/v1/projects": func(w http.ResponseWriter, r *http.Request) {
			*e.got = readJSON[apitypes.CreateProject](t, r)
			reply(201, apitypes.Project{Slug: e.got.Slug})(w, r)
		},
		"GET /api/v1/projects/{slug}": func(w http.ResponseWriter, r *http.Request) {
			slug := r.PathValue("slug")
			for _, p := range s.projects {
				if p.Slug == slug {
					reply(200, p)(w, r)
					return
				}
			}
			problem(404, apitypes.Problem{Title: "Not Found"})(w, r)
		},
	}
	srv := serve(t, e.rec.wrapAll(routes))
	e.harness = newHarness(t, srv)
	e.home = e.cwd
	e.repo = filepath.Join(e.home, "payments")
	write(t, filepath.Join(e.repo, ".git", "HEAD"), "ref")
	e.cwd = e.repo
	e.env["HOME"] = e.home
	e.streams.SetTTY(true, true)
	e.streams.SetStderrTTY(true)
	e.fp = newFakePrompter(t, answers...)
	e.f.Prompter = e.fp
	return e
}

func (e *initEnv) binding(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, bindingFile))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func proj(slug, name, role, vis string) apitypes.Project {
	return apitypes.Project{Slug: slug, Name: name, Role: role, Visibility: vis, Status: "active"}
}

func TestInitWritesBindingAndNeverPromptsOffTTY(t *testing.T) {
	t.Parallel()
	var created apitypes.CreateProject
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/newproj": problem(404, apitypes.Problem{Title: "Not Found"}),
		"POST /api/v1/projects": func(w http.ResponseWriter, r *http.Request) {
			created = readJSON[apitypes.CreateProject](t, r)
			reply(201, apitypes.Project{Slug: created.Slug})(w, r)
		},
		"GET /api/v1/projects/existing": reply(200, apitypes.Project{Slug: "existing"}),
	})

	h := newHarness(t, srv)
	err := h.run(t, "init", "-p", "newproj")
	if cmdutil.ExitCode(err) != 2 || !strings.Contains(err.Error(), "--create") {
		t.Fatalf("err = %v, want usage error naming --create", err)
	}
	if _, statErr := os.Stat(filepath.Join(h.cwd, bindingFile)); statErr == nil {
		t.Error("binding file written despite refusal")
	}

	if err := h.run(t, "init", "-p", "newproj", "--create", "--api-url", "http://example.test"); err != nil {
		t.Fatal(err)
	}
	if created.Slug != "newproj" {
		t.Errorf("created project %+v", created)
	}
	b, err := os.ReadFile(filepath.Join(h.cwd, bindingFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "project = \"newproj\"\napi_url = \"http://example.test\"\n" {
		t.Errorf("binding = %q", b)
	}

	h2 := newHarness(t, srv)
	if err := h2.run(t, "init", "-p", "existing"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(h2.cwd, bindingFile))
	if string(b) != "project = \"existing\"\n" {
		t.Errorf("binding = %q, want no api_url unless asked", b)
	}
}

func TestInitInteractivePreselection(t *testing.T) {
	t.Parallel()
	lorvik := proj("lorvik", "Lorvik", "owner", "org")
	pay := proj("payments", "Payments", "member", "private")
	other := proj("web", "Web", "member", "org")
	tests := []struct {
		name     string
		me       apitypes.Me
		projects []apitypes.Project
		binding  string
		wantDef  int
		wantOpts []string
	}{
		{"current binding beats the folder match", owner(), []apitypes.Project{pay, lorvik}, "project = \"lorvik\"\n", 1,
			[]string{"payments", "lorvik", `Create a new project`}},
		{"folder match beats create", owner(), []apitypes.Project{lorvik, pay}, "", 1,
			[]string{"lorvik", "payments", `Create a new project`}},
		{"create when nothing matches", owner(), []apitypes.Project{lorvik, other}, "", 2,
			[]string{"lorvik", "web", `Create a new project "payments"`}},
		{"first project when a member has no match", asMember(owner()), []apitypes.Project{lorvik, other}, "", 0,
			[]string{"lorvik", "web"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newInitEnv(t, initServer{me: tt.me, projects: tt.projects}, tt.wantDef)
			if tt.binding != "" {
				if err := os.WriteFile(filepath.Join(e.repo, bindingFile), []byte(tt.binding), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tt.wantDef == 2 { // the create flow follows; cancel it
				e.fp.answers = append(e.fp.answers, cmdutil.ErrCancelled)
			}
			_ = e.run(t, "init")
			if len(e.fp.Asked) == 0 {
				t.Fatal("no prompt")
			}
			a := e.fp.Asked[0]
			if a.Q.Title != "Which project should ~/payments use?" || a.Q.Description != initProjectDesc {
				t.Errorf("question = %+v", a.Q)
			}
			if a.DefIndex != tt.wantDef {
				t.Errorf("default = %d, want %d (%v)", a.DefIndex, tt.wantDef, labels(a.Opts))
			}
			if got := labels(a.Opts); strings.Join(got, "|") != strings.Join(tt.wantOpts, "|") {
				t.Errorf("options = %q, want %q", got, tt.wantOpts)
			}
		})
	}
}

func TestInitProjectOptionDetails(t *testing.T) {
	t.Parallel()
	e := newInitEnv(t, initServer{me: owner(), projects: []apitypes.Project{proj("payments", "Payments", "admin", "private")}}, 0)
	if err := os.WriteFile(filepath.Join(e.repo, bindingFile), []byte("project = \"payments\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.run(t, "init"); err != nil {
		t.Fatal(err)
	}
	if got, want := e.fp.Asked[0].Opts[0].Detail, "Payments · admin · private · current · matches this folder"; got != want {
		t.Errorf("detail = %q, want %q", got, want)
	}
}

func TestInitCreatesProjectFromTheAnswers(t *testing.T) {
	t.Parallel()
	me := owner()
	me.Org.Settings = &apitypes.OrgSettings{ProjectVisibilityDefault: "private"}
	lorvik := proj("lorvik", "Lorvik", "owner", "org")
	e := newInitEnv(t, initServer{me: me, projects: []apitypes.Project{lorvik}},
		1,                                      // create
		"Pay Ments",                            // name
		"Pay Ments",                            // slug: not a slug
		"lorvik",                               // slug: taken
		"a-very-long-"+strings.Repeat("x", 60), // slug: too long
		"pay-ments",                            // slug
		0,                                      // visibility: everyone
	)
	if err := e.run(t, "init"); err != nil {
		t.Fatal(err)
	}
	want := apitypes.CreateProject{Slug: "pay-ments", Name: "Pay Ments", Visibility: "org"}
	if *e.got != want {
		t.Errorf("created = %+v, want %+v", *e.got, want)
	}
	if got := e.binding(t, e.repo); got != "project = \"pay-ments\"\n" {
		t.Errorf("binding = %q", got)
	}
	name, slug, vis := e.fp.Asked[1], e.fp.Asked[2], e.fp.Asked[len(e.fp.Asked)-1]
	if name.Q.Title != "Name" || name.DefText != "Payments" || name.Q.Description != "Shown on the dashboard." {
		t.Errorf("name prompt = %+v", name)
	}
	if slug.Q.Title != "Slug" || slug.DefText != "pay-ments" {
		t.Errorf("slug prompt = %+v", slug)
	}
	wantRej := []string{"use lowercase letters, digits and single dashes", "lorvik already exists; pick it from the list", "at most 64 characters"}
	if strings.Join(e.fp.Rejected, "|") != strings.Join(wantRej, "|") {
		t.Errorf("rejected = %q, want %q", e.fp.Rejected, wantRej)
	}
	if vis.Q.Title != "Who can see it?" || vis.DefIndex != 1 || vis.Opts[0].Label != "Everyone in Acme" {
		t.Errorf("visibility prompt = %+v", vis)
	}
	out := e.errOut.String()
	for _, w := range []string{"Created project pay-ments", "Bound ~/payments to pay-ments", "Commit .lorvik.toml so teammates", "lorvik next"} {
		if !strings.Contains(out, w) {
			t.Errorf("stderr lacks %q:\n%s", w, out)
		}
	}
	if e.out.String() != "" {
		t.Errorf("stdout = %q, want nothing", e.out.String())
	}
}

func TestInitFlagsAnswerCreatePrompts(t *testing.T) {
	t.Parallel()
	e := newInitEnv(t, initServer{me: owner()}) // no prompt may be asked
	if err := e.run(t, "init", "-p", "payments-web", "--name", "Payments Web", "--visibility", "private"); err != nil {
		t.Fatal(err)
	}
	want := apitypes.CreateProject{Slug: "payments-web", Name: "Payments Web", Visibility: "private"}
	if *e.got != want {
		t.Errorf("created = %+v, want %+v", *e.got, want)
	}
	for _, c := range e.rec.calls() {
		if c == "GET /api/v1/projects" {
			t.Error("-p must skip the project list")
		}
	}
}

func TestInitProjectFlagUsesExistingProjectWithoutPrompts(t *testing.T) {
	t.Parallel()
	e := newInitEnv(t, initServer{me: owner(), projects: []apitypes.Project{proj("lorvik", "Lorvik", "owner", "org")}})
	if err := e.run(t, "init", "-p", "lorvik"); err != nil {
		t.Fatal(err)
	}
	if got := e.binding(t, e.repo); got != "project = \"lorvik\"\n" {
		t.Errorf("binding = %q", got)
	}
	if len(e.fp.Asked) != 0 {
		t.Errorf("asked %v", e.fp.titles())
	}
}

func TestInitMemberCannotCreate(t *testing.T) {
	t.Parallel()
	t.Run("no create option", func(t *testing.T) {
		t.Parallel()
		e := newInitEnv(t, initServer{me: asMember(owner()), projects: []apitypes.Project{proj("lorvik", "Lorvik", "member", "org")}}, 0)
		if err := e.run(t, "init"); err != nil {
			t.Fatal(err)
		}
		if got := labels(e.fp.Asked[0].Opts); len(got) != 1 || got[0] != "lorvik" {
			t.Errorf("options = %q", got)
		}
	})
	t.Run("nothing usable exits 3", func(t *testing.T) {
		t.Parallel()
		e := newInitEnv(t, initServer{me: asMember(owner())})
		err := e.run(t, "init")
		if cmdutil.ExitCode(err) != 3 || !strings.Contains(err.Error(), "only org admins can create one; ask an admin for access") {
			t.Fatalf("err = %v (exit %d)", err, cmdutil.ExitCode(err))
		}
	})
	t.Run("named project that is missing is the agent error", func(t *testing.T) {
		t.Parallel()
		e := newInitEnv(t, initServer{me: asMember(owner()), projects: []apitypes.Project{proj("lorvik", "Lorvik", "member", "org")}})
		err := e.run(t, "init", "-p", "nope")
		if cmdutil.ExitCode(err) != 2 || !strings.Contains(err.Error(), "projects you can use: lorvik") {
			t.Fatalf("err = %v (exit %d)", err, cmdutil.ExitCode(err))
		}
	})
}

func TestInitSubfolderOffersTheRepositoryRoot(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		choice   int
		wantDir  string // relative to the repo
		wantNone string
	}{
		{"default is the whole repository", 0, ".", "web"},
		{"only this folder", 1, "web", "."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newInitEnv(t, initServer{me: owner(), projects: []apitypes.Project{proj("lorvik", "Lorvik", "owner", "org")}}, tt.choice, 0)
			sub := filepath.Join(e.repo, "web")
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			e.cwd = sub
			if err := e.run(t, "init"); err != nil {
				t.Fatal(err)
			}
			first := e.fp.Asked[0]
			if first.Q.Title != "Bind which folder?" || first.DefIndex != 0 ||
				first.Opts[0].Detail != "~/payments" || first.Opts[1].Detail != "~/payments/web" {
				t.Errorf("first prompt = %+v", first)
			}
			if got := e.binding(t, filepath.Join(e.repo, tt.wantDir)); got != "project = \"lorvik\"\n" {
				t.Errorf("binding in %s = %q", tt.wantDir, got)
			}
			if got := e.binding(t, filepath.Join(e.repo, tt.wantNone)); got != "" {
				t.Errorf("binding in %s = %q, want none", tt.wantNone, got)
			}
			if second := e.fp.Asked[1].Q.Title; !strings.Contains(second, map[int]string{0: "~/payments use", 1: "~/payments/web use"}[tt.choice]) {
				t.Errorf("project question = %q", second)
			}
		})
	}
}

func TestInitOutsideAGitRepository(t *testing.T) {
	t.Parallel()
	t.Run("confirm defaults to no and no cancels", func(t *testing.T) {
		t.Parallel()
		e := newInitEnv(t, initServer{me: owner()}, false)
		dir := filepath.Join(e.home, "scratch")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		e.cwd = dir
		err := e.run(t, "init", "-p", "lorvik")
		if cmdutil.ExitCode(err) != 2 || !errors.Is(err, cmdutil.ErrSilent) {
			t.Fatalf("err = %v, want exit 2 and silent", err)
		}
		c := e.fp.Asked[0]
		if c.Kind != "confirm" || c.DefBool || c.Q.Title != "Bind ~/scratch anyway?" {
			t.Errorf("confirm = %+v", c)
		}
		out := e.errOut.String()
		if !strings.Contains(out, "! ~/scratch is not inside a git repository.") || !strings.Contains(out, "  Cancelled. Nothing was changed.\n") {
			t.Errorf("stderr = %q", out)
		}
		if e.binding(t, dir) != "" || len(e.rec.calls()) > 1 {
			t.Errorf("wrote or called: binding %q calls %v", e.binding(t, dir), e.rec.calls())
		}
	})
	t.Run("yes binds without the commit reminder", func(t *testing.T) {
		t.Parallel()
		e := newInitEnv(t, initServer{me: owner(), projects: []apitypes.Project{proj("lorvik", "Lorvik", "owner", "org")}}, true)
		dir := filepath.Join(e.home, "scratch")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		e.cwd = dir
		if err := e.run(t, "init", "-p", "lorvik"); err != nil {
			t.Fatal(err)
		}
		if e.binding(t, dir) != "project = \"lorvik\"\n" {
			t.Errorf("binding = %q", e.binding(t, dir))
		}
		if strings.Contains(e.errOut.String(), "Commit .lorvik.toml") {
			t.Errorf("stderr = %q, want no commit reminder outside a repo", e.errOut.String())
		}
	})
}

func TestInitCancelWritesAndCreatesNothing(t *testing.T) {
	t.Parallel()
	e := newInitEnv(t, initServer{me: owner()}, "Payments", cmdutil.ErrCancelled)
	err := e.run(t, "init")
	if cmdutil.ExitCode(err) != 2 || !errors.Is(err, cmdutil.ErrSilent) {
		t.Fatalf("err = %v, want exit 2 and silent", err)
	}
	if !strings.HasSuffix(e.errOut.String(), "  Cancelled. Nothing was changed.\n") {
		t.Errorf("stderr = %q", e.errOut.String())
	}
	if e.binding(t, e.repo) != "" {
		t.Error("binding written after cancel")
	}
	for _, c := range e.rec.calls() {
		if strings.HasPrefix(c, "POST") {
			t.Errorf("made %s after cancel", c)
		}
	}
}

func TestInitKeepsTheAPIURLOfTheOldBinding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"kept", nil, "project = \"web\"\napi_url = \"http://kept.test\"\n"},
		{"flag wins", []string{"--api-url", "http://flag.test"}, "project = \"web\"\napi_url = \"http://flag.test\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newInitEnv(t, initServer{me: owner(), projects: []apitypes.Project{proj("lorvik", "Lorvik", "owner", "org"), proj("web", "Web", "owner", "org")}}, 1)
			old := "project = \"lorvik\"\napi_url = \"http://kept.test\"\n"
			if err := os.WriteFile(filepath.Join(e.repo, bindingFile), []byte(old), 0o644); err != nil {
				t.Fatal(err)
			}
			// The factory client ignores the URL, so only the file matters.
			if err := e.run(t, append([]string{"init"}, tt.args...)...); err != nil {
				t.Fatal(err)
			}
			if got := e.binding(t, e.repo); got != tt.want {
				t.Errorf("binding = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInitHeaderOnStderrWithOptionalColor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		noColor string
		bold    bool
	}{
		{"colour on a terminal", "", true},
		{"NO_COLOR turns it off", "1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newInitEnv(t, initServer{me: owner(), projects: []apitypes.Project{proj("lorvik", "Lorvik", "owner", "org")}})
			e.env["NO_COLOR"] = tt.noColor
			if err := e.run(t, "init", "-p", "lorvik"); err != nil {
				t.Fatal(err)
			}
			out := e.errOut.String()
			if !strings.HasPrefix(out, "\n  ") || !strings.Contains(out, "  http://localhost:8080\n  Signed in as") {
				t.Errorf("header = %q", out)
			}
			if tt.bold != strings.Contains(out, "\x1b[1mlorvik init\x1b[0m") {
				t.Errorf("bold = %v in %q", !tt.bold, out)
			}
			if !strings.Contains(out, "Signed in as Ada · Acme · owner\n\n") {
				t.Errorf("identity line missing in %q", out)
			}
		})
	}
}

func TestInitSignsInInlineWhenThereIsNoToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		password string
		answers  []any
		wantAsk  []string
	}{
		{"asks for the password", "", []any{"ada", "s3cret", 0}, []string{"Login", "Password", "Which project should ~/payments use?"}},
		{"LORVIK_PASSWORD wins", "from-env", []any{"ada", 0}, []string{"Login", "Which project should ~/payments use?"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var login apitypes.Login
			var meAuth string
			srv := serve(t, map[string]http.HandlerFunc{
				"POST /api/v1/login": func(w http.ResponseWriter, r *http.Request) {
					login = readJSON[apitypes.Login](t, r)
					reply(200, apitypes.TokenCreated{Secret: "lvk_secret123", Token: apitypes.Token{Prefix: "lvk_secr"}})(w, r)
				},
				"GET /api/v1/me": func(w http.ResponseWriter, r *http.Request) {
					meAuth = r.Header.Get("Authorization")
					reply(200, owner())(w, r)
				},
				"GET /api/v1/projects": reply(200, apitypes.ProjectList{Items: []apitypes.Project{proj("lorvik", "Lorvik", "owner", "org")}}),
			})
			h := newHarness(t, nil)
			home := h.cwd
			repo := filepath.Join(home, "payments")
			write(t, filepath.Join(repo, ".git", "HEAD"), "ref")
			h.cwd = repo
			h.env["HOME"], h.env["LORVIK_API_URL"] = home, srv.URL
			if tt.password != "" {
				h.env["LORVIK_PASSWORD"] = tt.password
			}
			h.streams.SetTTY(true, true)
			h.streams.SetStderrTTY(true)
			fp := newFakePrompter(t, tt.answers...)
			h.f.Prompter = fp
			if err := h.run(t, "init"); err != nil {
				t.Fatal(err)
			}
			wantPw := tt.password
			if wantPw == "" {
				wantPw = "s3cret"
			}
			if login.Login != "ada" || login.Password != wantPw {
				t.Errorf("login = %+v", login)
			}
			if meAuth != "Bearer lvk_secret123" {
				t.Errorf("GET /me sent %q, want the new token", meAuth)
			}
			if cfg := h.config(t); !strings.Contains(cfg, `token = "lvk_secret123"`) || !strings.Contains(cfg, `api_url = "`+srv.URL+`"`) {
				t.Errorf("config = %q", cfg)
			}
			if got := fp.titles(); strings.Join(got, "|") != strings.Join(tt.wantAsk, "|") {
				t.Errorf("asked %q, want %q", got, tt.wantAsk)
			}
			out := h.errOut.String()
			if !strings.Contains(out, "You are not signed in to "+srv.URL+".") || !strings.Contains(out, "signed in to "+srv.URL+"; token lvk_secr saved") {
				t.Errorf("stderr = %q", out)
			}
			if strings.Contains(h.out.String(), "lvk_secret123") {
				t.Errorf("stdout leaked the secret: %q", h.out.String())
			}
		})
	}
}

func TestInitSignsInInlineThroughTheOrgChoice(t *testing.T) {
	t.Parallel()
	var entered, meAuth string
	srv := serve(t, map[string]http.HandlerFunc{
		"POST /api/v1/login": reply(201, signInToken()),
		"GET /api/v1/login/orgs": reply(200, apitypes.SignInOrgs{Orgs: []apitypes.SignInOrg{
			{ID: "1", Name: "Acme", Role: "owner", Status: "member"},
			{ID: "2", Name: "Beta", Role: "member", Status: "member"},
		}}),
		"POST /api/v1/login/orgs/{id}/token": func(w http.ResponseWriter, r *http.Request) {
			entered = r.PathValue("id")
			reply(201, apitypes.TokenCreated{Secret: "lvk_org", Token: apitypes.Token{Prefix: "lvk_org", Scope: "write"}})(w, r)
		},
		"GET /api/v1/me": func(w http.ResponseWriter, r *http.Request) {
			meAuth = r.Header.Get("Authorization")
			reply(200, owner())(w, r)
		},
		"GET /api/v1/projects": reply(200, apitypes.ProjectList{Items: []apitypes.Project{proj("lorvik", "Lorvik", "owner", "org")}}),
	})
	h := newHarness(t, nil)
	home := h.cwd
	repo := filepath.Join(home, "payments")
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref")
	h.cwd = repo
	h.env["HOME"], h.env["LORVIK_API_URL"], h.env["LORVIK_PASSWORD"] = home, srv.URL, "pw"
	h.terminal("2\n")
	h.f.Prompter = newFakePrompter(t, "ana", 0)
	if err := h.run(t, "init"); err != nil {
		t.Fatal(err)
	}
	if entered != "2" || meAuth != "Bearer lvk_org" {
		t.Errorf("entered org %q and GET /me sent %q, want org 2 and its token", entered, meAuth)
	}
	if cfg := h.config(t); !strings.Contains(cfg, `token = "lvk_org"`) {
		t.Errorf("config = %q, want the org's token saved", cfg)
	}
	if e := h.errOut.String(); !strings.Contains(e, "  1  Acme  owner\n  2  Beta  member\nOrg [1-2]: ") {
		t.Errorf("stderr = %q, want the orgs listed to choose from", e)
	}
	h.assertNoSecret(t)
}

func TestInitOffersSignInWhenTheTokenIsRejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		fromEnv   bool
		wantCode  int
		wantAsk   []string
		wantToken string // in the config file afterwards
	}{
		{"stored token signs in again", false, 0, []string{"Login", "Password", "Which project should ~/payments use?"}, "lvk_new"},
		{"LORVIK_TOKEN keeps the error", true, 5, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := serve(t, map[string]http.HandlerFunc{
				"POST /api/v1/login": reply(200, apitypes.TokenCreated{Secret: "lvk_new", Token: apitypes.Token{Prefix: "lvk_new"}}),
				"GET /api/v1/me": func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer lvk_new" {
						problem(401, apitypes.Problem{Title: "Unauthorized", Detail: "token expired"})(w, r)
						return
					}
					reply(200, owner())(w, r)
				},
				"GET /api/v1/projects": reply(200, apitypes.ProjectList{Items: []apitypes.Project{proj("lorvik", "Lorvik", "owner", "org")}}),
			})
			h := newHarness(t, nil)
			home := h.cwd
			repo := filepath.Join(home, "payments")
			write(t, filepath.Join(repo, ".git", "HEAD"), "ref")
			h.cwd = repo
			h.env["HOME"], h.env["LORVIK_API_URL"] = home, srv.URL
			if tt.fromEnv {
				h.env["LORVIK_TOKEN"] = "lvk_old"
			} else {
				_ = os.MkdirAll(filepath.Join(h.xdg, "lorvik"), 0o700)
				_ = os.WriteFile(filepath.Join(h.xdg, "lorvik", "config.toml"), []byte("token = \"lvk_old\"\n"), 0o600)
			}
			h.streams.SetTTY(true, true)
			h.streams.SetStderrTTY(true)
			fp := newFakePrompter(t, "ada", "s3cret", 0)
			h.f.Prompter = fp
			err := h.run(t, "init")
			if got := cmdutil.ExitCode(err); got != tt.wantCode {
				t.Fatalf("exit = %d (%v), want %d", got, err, tt.wantCode)
			}
			if got := fp.titles(); strings.Join(got, "|") != strings.Join(tt.wantAsk, "|") {
				t.Errorf("asked %q, want %q", got, tt.wantAsk)
			}
			if tt.wantToken != "" {
				if cfg := h.config(t); !strings.Contains(cfg, `token = "`+tt.wantToken+`"`) {
					t.Errorf("config = %q, want the new token", cfg)
				}
				if !strings.Contains(h.errOut.String(), "Your sign-in to "+srv.URL+" has expired or was revoked.") {
					t.Errorf("stderr = %q", h.errOut.String())
				}
			}
		})
	}
}

func TestInitNeverPrompts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		set  func(e *initEnv)
		args []string
	}{
		{"json", func(*initEnv) {}, []string{"--json"}},
		{"CI", func(e *initEnv) { e.env["CI"] = "1" }, nil},
		{"Claude Code session", func(e *initEnv) { e.env["CLAUDE_CODE_SESSION_ID"] = "abc" }, nil},
		{"OpenCode session", func(e *initEnv) { e.env["OPENCODE_SESSION_ID"] = "abc" }, nil},
		{"stdin not a terminal", func(e *initEnv) { e.streams.SetTTY(false, true) }, nil},
		{"stdout not a terminal", func(e *initEnv) { e.streams.SetTTY(true, false) }, nil},
		{"stderr not a terminal", func(e *initEnv) { e.streams.SetStderrTTY(false) }, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newInitEnv(t, initServer{me: owner(), projects: []apitypes.Project{proj("lorvik", "Lorvik", "owner", "org")}})
			tt.set(e)
			if err := e.run(t, append([]string{"init", "-p", "lorvik"}, tt.args...)...); err != nil {
				t.Fatal(err)
			}
			if len(e.fp.Asked) != 0 {
				t.Errorf("prompted: %v", e.fp.titles())
			}
			if got := e.binding(t, e.repo); got != "project = \"lorvik\"\n" {
				t.Errorf("binding = %q", got)
			}
		})
	}
}

func TestInitNotSignedInFailsBeforeAnyRequest(t *testing.T) {
	t.Parallel()
	srv := serve(t, map[string]http.HandlerFunc{
		"/": func(http.ResponseWriter, *http.Request) { t.Error("a request reached the server") },
	})
	h := newHarness(t, nil)
	h.env["LORVIK_API_URL"] = srv.URL
	err := h.run(t, "init", "-p", "lorvik")
	want := fmt.Sprintf("not signed in to %s; run lorvik login (agents and CI: LORVIK_TOKEN or LORVIK_CONFIG)", srv.URL)
	if cmdutil.ExitCode(err) != 5 || err == nil || err.Error() != want {
		t.Fatalf("err = %v (exit %d), want exit 5 %q", err, cmdutil.ExitCode(err), want)
	}
	if _, statErr := os.Stat(filepath.Join(h.cwd, bindingFile)); statErr == nil {
		t.Error("binding written")
	}
}

func TestInitMissingProjectMessage(t *testing.T) {
	t.Parallel()
	many := make([]apitypes.Project, 12)
	for i := range many {
		many[i] = proj(fmt.Sprintf("p%02d", i), "P", "member", "org")
	}
	archived := proj("old", "Old", "member", "org")
	archived.Status = "archived"
	tests := []struct {
		name     string
		args     []string
		projects []apitypes.Project // nil: the list call fails
		want     string
	}{
		{"derived from the folder", nil,
			[]apitypes.Project{proj("lorvik", "L", "member", "org"), proj("payments", "P", "member", "org"), archived},
			"project \"ada\" does not exist (named after this folder)\n" +
				"projects you can use: lorvik, payments\n" +
				"hint: lorvik init -p lorvik, or create it: lorvik init -p ada --create"},
		{"named by flag has no parenthesis", []string{"-p", "ada"},
			[]apitypes.Project{proj("lorvik", "L", "member", "org")},
			"project \"ada\" does not exist\n" +
				"projects you can use: lorvik\n" +
				"hint: lorvik init -p lorvik, or create it: lorvik init -p ada --create"},
		{"list is capped at ten", []string{"-p", "ada"}, many,
			"project \"ada\" does not exist\n" +
				"projects you can use: p00, p01, p02, p03, p04, p05, p06, p07, p08, p09, ...\n" +
				"hint: lorvik init -p p00, or create it: lorvik init -p ada --create"},
		{"no visible project drops the list and the -p half", []string{"-p", "ada"}, []apitypes.Project{archived},
			"project \"ada\" does not exist\n" +
				"hint: create it: lorvik init -p ada --create"},
		{"a failed list call drops the list", []string{"-p", "ada"}, nil,
			"project \"ada\" does not exist\n" +
				"hint: create it: lorvik init -p ada --create"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			routes := map[string]http.HandlerFunc{
				"GET /api/v1/projects/{slug}": problem(404, apitypes.Problem{Title: "Not Found"}),
			}
			if tt.projects != nil {
				routes["GET /api/v1/projects"] = reply(200, apitypes.ProjectList{Items: tt.projects})
			}
			h := newHarness(t, serve(t, routes))
			dir := filepath.Join(h.cwd, "ada")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			h.cwd = dir
			err := h.run(t, append([]string{"init"}, tt.args...)...)
			if cmdutil.ExitCode(err) != 2 || err == nil || err.Error() != tt.want {
				t.Fatalf("err = %v (exit %d), want exit 2:\n%s", err, cmdutil.ExitCode(err), tt.want)
			}
			if _, statErr := os.Stat(filepath.Join(dir, bindingFile)); statErr == nil {
				t.Error("binding written")
			}
		})
	}
}

func TestInitAgentPathWarnsOutsideAGitRepository(t *testing.T) {
	t.Parallel()
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/lorvik": reply(200, apitypes.Project{Slug: "lorvik"}),
	}))
	if err := h.run(t, "init", "-p", "lorvik"); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("lorvik: %s is not inside a git repository; .lorvik.toml applies to every folder below it\n", h.cwd)
	if h.errOut.String() != want {
		t.Errorf("stderr = %q, want %q", h.errOut.String(), want)
	}
	if want := "bound " + filepath.Join(h.cwd, bindingFile) + " to project lorvik\n"; h.out.String() != want {
		t.Errorf("stdout = %q, want %q", h.out.String(), want)
	}
}

func TestInitCreateUsesNameAndVisibilityFlags(t *testing.T) {
	t.Parallel()
	var created apitypes.CreateProject
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/payments": problem(404, apitypes.Problem{Title: "Not Found"}),
		"POST /api/v1/projects": func(w http.ResponseWriter, r *http.Request) {
			created = readJSON[apitypes.CreateProject](t, r)
			reply(201, apitypes.Project{Slug: created.Slug})(w, r)
		},
	}))
	if err := h.run(t, "init", "-p", "payments", "--create", "--name", "Payments", "--visibility", "private"); err != nil {
		t.Fatal(err)
	}
	if want := (apitypes.CreateProject{Slug: "payments", Name: "Payments", Visibility: "private"}); created != want {
		t.Errorf("created = %+v, want %+v", created, want)
	}
	err := h.run(t, "init", "-p", "payments", "--create", "--visibility", "everyone")
	if cmdutil.ExitCode(err) != 2 || !strings.Contains(err.Error(), "--visibility") {
		t.Errorf("err = %v, want a usage error naming --visibility", err)
	}
}

func TestGitRootStopsAtCeilingDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	write(t, filepath.Join(root, ".git", "HEAD"), "ref")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, ceilings, from, want string
	}{
		{"no ceiling finds the repository above", "", deep, root},
		{"a ceiling below the repository hides it", filepath.Join(root, "a"), deep, ""},
		{"the start directory is checked even when it is a ceiling", root, root, root},
		{"a ceiling among others in the list counts", "/nowhere" + string(filepath.ListSeparator) + filepath.Join(root, "a") + "/", deep, ""},
		{"a ceiling above the repository changes nothing", filepath.Dir(root), deep, root},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &app{f: &cmdutil.Factory{Env: func(k string) string {
				if k == "GIT_CEILING_DIRECTORIES" {
					return tt.ceilings
				}
				return ""
			}}}
			got, ok := a.gitRoot(tt.from)
			if got != tt.want || ok != (tt.want != "") {
				t.Errorf("gitRoot(%s) = %q, %v; want %q", tt.from, got, ok, tt.want)
			}
		})
	}
}
