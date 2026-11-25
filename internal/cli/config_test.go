package cli

import (
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestParseTOML(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    map[string]string
		wantErr string
	}{
		{"pairs and comments", "# c\nproject = \"demo\"\n\napi_url='http://x:1'\n", map[string]string{"project": "demo", "api_url": "http://x:1"}, ""},
		{"escapes", `token = "a\"b\\c"`, map[string]string{"token": `a"b\c`}, ""},
		{"numbers are skipped", "n = 3\nproject = \"p\"\n", map[string]string{"project": "p"}, ""},
		{"a bare true or false is read as the word", "a = false # why\nb = true\nc = maybe\n", map[string]string{"a": "false", "b": "true"}, ""},
		{"table entries are not root keys", "[t]\nn = 3\nproject = \"p\"\n", map[string]string{}, ""},
		{"root keys before a table win over its entries", "project = \"root\"\n[t]\nproject = \"p\"\ntoken = \"x\"\n", map[string]string{"project": "root"}, ""},
		{"trailing comment after string", `project = "p" # note`, map[string]string{"project": "p"}, ""},
		{"missing equals", "project\n", nil, "line 1"},
		{"unterminated", `project = "p`, nil, "unterminated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseTOML(strings.NewReader(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestResolvePrecedence(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	repo := filepath.Join(home, "repo")
	write(t, filepath.Join(xdg, "lorvik", "config.toml"), "api_url = \"http://file\"\ntoken = \"filetok\"\n")
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref")
	write(t, filepath.Join(repo, bindingFile), "project = \"bound\"\napi_url = \"http://bound\"\n")
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		fl   Flags
		env  map[string]string
		want Settings
	}{
		{"file and binding only", Flags{}, nil, Settings{APIURL: "http://bound", Token: "filetok", Project: "bound", TokenServer: "http://file"}},
		{"env beats files", Flags{}, map[string]string{"LORVIK_API_URL": "http://env", "LORVIK_TOKEN": "envtok", "LORVIK_PROJECT": "envp"},
			Settings{APIURL: "http://env", Token: "envtok", Project: "envp", TokenFromEnv: true, TokenServer: "http://env"}},
		{"flag beats env", Flags{APIURL: "http://flag", Project: "flagp"}, map[string]string{"LORVIK_API_URL": "http://env", "LORVIK_PROJECT": "envp"},
			Settings{APIURL: "http://flag", Token: "filetok", Project: "flagp", TokenServer: "http://file"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := map[string]string{"XDG_CONFIG_HOME": xdg}
			maps.Copy(e, tt.env)
			got, err := Resolve(tt.fl, env(e), sub)
			if err != nil {
				t.Fatal(err)
			}
			got.Binding, got.APIURLFrom = "", ""
			if got != tt.want {
				t.Errorf("Resolve = %+v, want %+v", got, tt.want)
			}
		})
	}

	t.Run("default url when nothing is set", func(t *testing.T) {
		t.Parallel()
		got, err := Resolve(Flags{}, env(map[string]string{"XDG_CONFIG_HOME": t.TempDir()}), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if got.APIURL != defaultAPIURL || got.Token != "" || got.Project != "" {
			t.Errorf("Resolve = %+v", got)
		}
	})

	t.Run("LORVIK_CONFIG beats XDG and home", func(t *testing.T) {
		t.Parallel()
		own := filepath.Join(t.TempDir(), "claude-code.toml")
		write(t, own, "api_url = \"http://own\"\ntoken = \"owntok\"\n")
		got, err := Resolve(Flags{}, env(map[string]string{"LORVIK_CONFIG": own, "XDG_CONFIG_HOME": xdg}), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if got.Token != "owntok" || got.APIURL != "http://own" || got.TokenFromEnv {
			t.Errorf("Resolve = %+v, want the LORVIK_CONFIG file", got)
		}
	})

	t.Run("home fallback", func(t *testing.T) {
		t.Parallel()
		h := t.TempDir()
		write(t, filepath.Join(h, ".config", "lorvik", "config.toml"), "token = \"hometok\"\n")
		got, err := Resolve(Flags{}, env(map[string]string{"HOME": h}), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if got.Token != "hometok" {
			t.Errorf("token = %q, want hometok", got.Token)
		}
	})
}

func TestTokenIsSentOnlyToTheServerItIsFor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		user     string
		binding  string
		fl       Flags
		env      map[string]string
		want     string
		refusals []string
	}{
		{name: "binding names the token's server", user: "api_url = \"https://lorvik.example\"\ntoken = \"filetok\"\n", binding: "api_url = \"https://LORVIK.example:443/\"\n", want: "filetok"},
		{name: "binding names another server", user: "api_url = \"https://lorvik.example\"\ntoken = \"filetok\"\n", binding: "api_url = \"https://attacker.example\"\n",
			refusals: []string{".lorvik.toml points at https://attacker.example", "your token is for https://lorvik.example", "no credential sent", "lorvik login --api-url https://attacker.example"}},
		{name: "same host on another port", user: "api_url = \"http://localhost:8080\"\ntoken = \"filetok\"\n", binding: "api_url = \"http://localhost:9999\"\n",
			refusals: []string{"points at http://localhost:9999"}},
		{name: "https downgraded to http", user: "api_url = \"https://lorvik.example\"\ntoken = \"filetok\"\n", binding: "api_url = \"http://lorvik.example\"\n",
			refusals: []string{"points at http://lorvik.example"}},
		{name: "token saved without a url is for the default server", user: "token = \"filetok\"\n", binding: "api_url = \"https://attacker.example\"\n",
			refusals: []string{"your token is for " + defaultAPIURL}},
		{name: "token saved without a url goes to LORVIK_API_URL", user: "token = \"filetok\"\n", env: map[string]string{"LORVIK_API_URL": "https://lorvik.example"}, want: "filetok"},
		{name: "a flag does not move a saved token", user: "api_url = \"https://lorvik.example\"\ntoken = \"filetok\"\n", fl: Flags{APIURL: "https://other.example"},
			refusals: []string{"--api-url points at https://other.example"}},
		{name: "LORVIK_TOKEN goes to LORVIK_API_URL", env: map[string]string{"LORVIK_TOKEN": "envtok", "LORVIK_API_URL": "https://ci.example"}, want: "envtok"},
		{name: "LORVIK_TOKEN with a binding naming another server", env: map[string]string{"LORVIK_TOKEN": "envtok"}, binding: "api_url = \"https://attacker.example\"\n",
			refusals: []string{"your token is for " + defaultAPIURL, "set LORVIK_API_URL to the server LORVIK_TOKEN is for"}},
		{name: "no token sends nothing and is not refused", binding: "api_url = \"https://attacker.example\"\n", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			xdg, repo := t.TempDir(), t.TempDir()
			if tt.user != "" {
				write(t, filepath.Join(xdg, "lorvik", "config.toml"), tt.user)
			}
			if tt.binding != "" {
				write(t, filepath.Join(repo, bindingFile), "project = \"p\"\n"+tt.binding)
			}
			e := map[string]string{"XDG_CONFIG_HOME": xdg}
			maps.Copy(e, tt.env)
			s, err := Resolve(tt.fl, env(e), repo)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.credential()
			if tt.refusals == nil {
				if err != nil || got != tt.want {
					t.Fatalf("credential = %q, %v; want %q", got, err, tt.want)
				}
				return
			}
			if got != "" {
				t.Errorf("credential = %q, want no token", got)
			}
			if code := cmdutil.ExitCode(err); code != cmdutil.ExitUnauthorized {
				t.Errorf("exit code = %d, want %d", code, cmdutil.ExitUnauthorized)
			}
			for _, want := range tt.refusals {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestCommandInARepoNamingAnotherServerSendsItNothing(t *testing.T) {
	t.Parallel()
	var rec recorder
	srv := serve(t, rec.wrapAll(map[string]http.HandlerFunc{"GET /api/v1/me": reply(200, apitypes.Me{})}))
	h := newHarness(t, nil)
	write(t, filepath.Join(h.xdg, "lorvik", "config.toml"), "api_url = \"https://lorvik.example\"\ntoken = \"filetok\"\n")
	write(t, filepath.Join(h.cwd, bindingFile), "project = \"p\"\napi_url = \""+srv.URL+"\"\n")
	err := h.run(t, "me")
	if code := cmdutil.ExitCode(err); code != cmdutil.ExitUnauthorized {
		t.Fatalf("me = %v (exit %d), want exit %d", err, code, cmdutil.ExitUnauthorized)
	}
	if calls := rec.calls(); len(calls) != 0 {
		t.Errorf("the repo's server got %v, want no request", calls)
	}

	write(t, filepath.Join(h.xdg, "lorvik", "config.toml"), "api_url = \""+srv.URL+"\"\ntoken = \"filetok\"\n")
	if err := h.run(t, "me"); err != nil {
		t.Fatalf("me with the token's own server: %v", err)
	}
	if got := rec.last(t, "GET /api/v1/me").Header.Get("Authorization"); got != "Bearer filetok" {
		t.Errorf("Authorization = %q, want the saved token", got)
	}
}

// An empty .git directory is not a repository: a stray one above the working
// folder must neither end the search for a binding nor be offered as the
// repository to bind.
func TestOnlyARealGitDirectoryOrPointerFileIsARepositoryRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := func(name string) string { return filepath.Join(root, name) }
	if err := os.MkdirAll(filepath.Join(dir("empty"), ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir("plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir("repo"), ".git", "HEAD"), "ref: refs/heads/prime\n")
	write(t, filepath.Join(dir("worktree"), ".git"), "gitdir: /elsewhere/.git/worktrees/w\n")
	for name, want := range map[string]bool{"empty": false, "plain": false, "repo": true, "worktree": true, "missing": false} {
		if got := isGitRoot(dir(name)); got != want {
			t.Errorf("isGitRoot(%s) = %v, want %v", name, got, want)
		}
	}

	write(t, filepath.Join(root, bindingFile), "project = \"outer\"\n")
	below := filepath.Join(dir("empty"), "pkg")
	if err := os.MkdirAll(below, 0o755); err != nil {
		t.Fatal(err)
	}
	if p, m, err := findBinding(below); err != nil || p != filepath.Join(root, bindingFile) || m["project"] != "outer" {
		t.Errorf("findBinding below an empty .git = %q, %v, %v; want the binding above it", p, m, err)
	}
	write(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/prime\n")
	a := &app{f: &cmdutil.Factory{Env: func(string) string { return "" }}}
	if got, ok := a.gitRoot(below); !ok || got != root {
		t.Errorf("gitRoot below an empty .git = %q, %v; want the repository above it, %s", got, ok, root)
	}
}

func TestFindBindingStopsAtGitRoot(t *testing.T) {
	t.Parallel()
	outer := t.TempDir()
	write(t, filepath.Join(outer, bindingFile), "project = \"outer\"\n")
	inner := filepath.Join(outer, "repo")
	write(t, filepath.Join(inner, ".git", "HEAD"), "ref")
	deep := filepath.Join(inner, "pkg")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	path, m, err := findBinding(deep)
	if err != nil {
		t.Fatal(err)
	}
	if path != "" || len(m) != 0 {
		t.Errorf("binding above the git root leaked in: %q %v", path, m)
	}

	write(t, filepath.Join(inner, bindingFile), "project = \"inner\"\n")
	path, m, err = findBinding(deep)
	if err != nil || m["project"] != "inner" || path != filepath.Join(inner, bindingFile) {
		t.Errorf("findBinding = %q %v %v", path, m, err)
	}
}

func TestFindBindingWalksUpWithoutGit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, bindingFile), "project = \"top\"\n")
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	_, m, err := findBinding(deep)
	if err != nil || m["project"] != "top" {
		t.Errorf("findBinding = %v %v", m, err)
	}
}

func TestResolveBadFileNamesPath(t *testing.T) {
	t.Parallel()
	xdg := t.TempDir()
	p := filepath.Join(xdg, "lorvik", "config.toml")
	write(t, p, "garbage\n")
	_, err := Resolve(Flags{}, env(map[string]string{"XDG_CONFIG_HOME": xdg}), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), p) {
		t.Errorf("err = %v, want it to name %s", err, p)
	}
}

func TestUpdateUserConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		initial string // empty means no file
		updates map[string]string
		want    string
	}{
		{"creates the file", "", map[string]string{"api_url": "http://s", "token": `to"k`}, "api_url = \"http://s\"\ntoken = \"to\\\"k\"\n"},
		{"replaces in place and keeps comments", "# mine\napi_url = \"http://old\"\n# keep\ntoken = \"t\"\n", map[string]string{"token": "new"},
			"# mine\napi_url = \"http://old\"\n# keep\ntoken = \"new\"\n"},
		{"empty value removes the key", "token = \"t\"\ncursor_lvk_u_ab = \"5\"\n", map[string]string{"cursor_lvk_u_ab": ""}, "token = \"t\"\n"},
		{"new keys land before the first table", "token = \"t\"\n[extra]\nn = 3\n", map[string]string{"cursor_x": "9"},
			"token = \"t\"\ncursor_x = \"9\"\n[extra]\nn = 3\n"},
		{"a key inside a table is not touched", "[extra]\ntoken = \"inner\"\n", map[string]string{"token": "outer"},
			"token = \"outer\"\n[extra]\ntoken = \"inner\"\n"},
		{"removing an absent key is a no-op", "token = \"t\"\n", map[string]string{"nope": ""}, "token = \"t\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			xdg := t.TempDir()
			p := filepath.Join(xdg, "lorvik", "config.toml")
			if tt.initial != "" {
				write(t, p, tt.initial)
			}
			if err := UpdateUserConfig(env(map[string]string{"XDG_CONFIG_HOME": xdg}), tt.updates); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("file = %q, want %q", got, tt.want)
			}
			st, _ := os.Stat(p)
			if st.Mode().Perm() != 0o600 {
				t.Errorf("mode = %v, want 0600", st.Mode().Perm())
			}
		})
	}
}

func TestUpdateUserConfigRoundTripsThroughResolve(t *testing.T) {
	t.Parallel()
	xdg := t.TempDir()
	e := env(map[string]string{"XDG_CONFIG_HOME": xdg})
	if err := UpdateUserConfig(e, map[string]string{"api_url": "http://s", "token": `to"k`}); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(Flags{}, e, t.TempDir())
	if err != nil || got.Token != `to"k` || got.APIURL != "http://s" || got.TokenFromEnv {
		t.Errorf("round trip = %+v %v", got, err)
	}
}
