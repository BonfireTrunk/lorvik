package cli

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

// claudeSessions writes Claude Code session files, pid to (chat id, name),
// into a fresh config directory and returns it.
func claudeSessions(t *testing.T, files map[int][2]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	for pid, f := range files {
		b, err := json.Marshal(map[string]any{"pid": pid, "sessionId": f[0], "name": f[1], "kind": "interactive"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sessions", strconv.Itoa(pid)+".json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestChatNameSources(t *testing.T) {
	t.Parallel()
	tree := fakeTree{
		300: {Name: "bash", PPID: 200},
		200: {Name: "claude", PPID: 100},
		100: {Name: "bash", PPID: 1},
	}
	long := "a" + strings.Repeat("é", maxSentName)
	tests := []struct {
		name   string
		env    map[string]string
		files  map[int][2]string
		config string
		want   chatName
	}{
		{
			name: "LORVIK_SESSION_NAME is cleaned and sent",
			env:  map[string]string{envSessionName: "  billing\n migration  "},
			want: chatName{Name: "billing migration", Source: envSessionName},
		},
		{
			name:  "LORVIK_SESSION_NAME comes before the harness's name",
			env:   map[string]string{envSessionName: "mine", "CLAUDE_CODE_SESSION_ID": "chat-1", "CLAUDE_PID": "200"},
			files: map[int][2]string{200: {"chat-1", "from claude"}},
			want:  chatName{Name: "mine", Source: envSessionName},
		},
		{
			name:  "Claude Code's name is read from the file CLAUDE_PID points at",
			env:   map[string]string{"CLAUDE_CODE_SESSION_ID": "chat-1", "CLAUDE_PID": "777"},
			files: map[int][2]string{777: {"chat-1", "lorvik-main"}, 200: {"chat-1", "wrong file"}},
			want:  chatName{Name: "lorvik-main", Source: "claude-code"},
		},
		{
			name:  "a CLAUDE_PID of another chat is skipped for the ancestor that has this chat's file",
			env:   map[string]string{"CLAUDE_CODE_SESSION_ID": "chat-1", "CLAUDE_PID": "777"},
			files: map[int][2]string{777: {"chat-2", "someone else"}, 200: {"chat-1", "lorvik-main"}},
			want:  chatName{Name: "lorvik-main", Source: "claude-code"},
		},
		{
			name:  "without CLAUDE_PID the ancestors are searched",
			env:   map[string]string{"CLAUDE_CODE_SESSION_ID": "chat-1"},
			files: map[int][2]string{200: {"chat-1", "lorvik-main"}},
			want:  chatName{Name: "lorvik-main", Source: "claude-code"},
		},
		{
			name:  "a file of another chat is never used",
			env:   map[string]string{"CLAUDE_CODE_SESSION_ID": "chat-1"},
			files: map[int][2]string{200: {"chat-2", "someone else"}},
			want:  chatName{},
		},
		{
			name: "no session file means no name",
			env:  map[string]string{"CLAUDE_CODE_SESSION_ID": "chat-1", "CLAUDE_PID": "200"},
			want: chatName{},
		},
		{
			name:  "a harness whose chat name cannot be read has none",
			env:   map[string]string{"CODEX_THREAD_ID": "chat-1"},
			files: map[int][2]string{200: {"chat-1", "not codex's"}},
			want:  chatName{},
		},
		{
			name:   "the off switch stops every source",
			env:    map[string]string{envSessionName: "mine", "CLAUDE_CODE_SESSION_ID": "chat-1", "CLAUDE_PID": "200"},
			files:  map[int][2]string{200: {"chat-1", "lorvik-main"}},
			config: "session_names = \"off\"\n",
			want:   chatName{Off: true},
		},
		{
			name:   "on leaves names on",
			env:    map[string]string{envSessionName: "mine"},
			config: "session_names = \"On\"\n",
			want:   chatName{Name: "mine", Source: envSessionName},
		},
		{
			name:   "a mistyped switch counts as off",
			env:    map[string]string{envSessionName: "mine"},
			config: "session_names = \"OFF \"\n",
			want:   chatName{Off: true},
		},
		{
			name:   "a bare false counts as off",
			env:    map[string]string{envSessionName: "mine"},
			config: "session_names = false\n",
			want:   chatName{Off: true},
		},
		{
			name:   "a config that does not parse counts as off",
			env:    map[string]string{envSessionName: "mine"},
			config: "session_names\n",
			want:   chatName{Off: true},
		},
		{
			name: "control characters never reach the terminal",
			env:  map[string]string{envSessionName: "red\x1b[31m alert\x07"},
			want: chatName{Name: "red [31m alert", Source: envSessionName},
		},
		{
			name:  "the search stops at the first ancestor that is not a shell",
			env:   map[string]string{"CLAUDE_CODE_SESSION_ID": "chat-1"},
			files: map[int][2]string{100: {"chat-1", "too far up"}},
			want:  chatName{},
		},
		{
			name: "an oversized name is cut on a rune boundary",
			env:  map[string]string{envSessionName: long},
			want: chatName{Name: long[:maxSentName-1], Source: envSessionName},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{"XDG_CONFIG_HOME": t.TempDir(), "CLAUDE_CONFIG_DIR": claudeSessions(t, tt.files)}
			maps.Copy(env, tt.env)
			if tt.config != "" {
				p := userConfigPath(func(k string) string { return env[k] })
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(tt.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			a, _ := sessionApp(t, env, "box", 300, tree)
			if got := a.chatName(); got != tt.want {
				t.Errorf("chatName = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestTheChatsNameGoesOutWithEveryRequest(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, token, config, want string
	}{
		{name: "sent percent-encoded", token: "tok", want: url.PathEscape("Büro plan")},
		{name: "not sent when names are off", token: "tok", config: "session_names = \"off\"\n"},
		// A cloned repo's .lorvik.toml can name a server the user never
		// signed in to; it gets no token and no name.
		{name: "not sent without a token"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var rec recorder
			srv := serve(t, rec.wrapAll(map[string]http.HandlerFunc{"GET /api/v1/me": reply(200, apitypes.Me{})}))
			h := newHarness(t, srv)
			h.f.Client = func() (*client.Client, error) { return client.New(srv.URL, tt.token) }
			h.env[envSessionName] = "Büro  plan"
			if tt.config != "" {
				if err := os.MkdirAll(filepath.Join(h.xdg, "lorvik"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(h.xdg, "lorvik", "config.toml"), []byte(tt.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.run(t, "me"); err != nil {
				t.Fatal(err)
			}
			if got := rec.last(t, "GET /api/v1/me").Header.Get(apitypes.HeaderSessionName); got != tt.want {
				t.Errorf("%s = %q, want %q", apitypes.HeaderSessionName, got, tt.want)
			}
		})
	}
}

func TestSessionNameCommand(t *testing.T) {
	t.Parallel()
	type result struct {
		err    error
		out    string
		bodies []string
	}
	run := func(t *testing.T, env map[string]string, config string, args ...string) result {
		t.Helper()
		var rec recorder
		srv := serve(t, rec.wrapAll(map[string]http.HandlerFunc{
			"PUT /api/v1/me/session-name": func(w http.ResponseWriter, r *http.Request) {
				in := readJSON[apitypes.PutSessionName](t, r)
				reply(200, apitypes.AgentSession{Label: "test-session", Name: in.Name})(w, r)
			},
		}))
		h := newHarness(t, srv)
		maps.Copy(h.env, env)
		if config != "" {
			if err := os.MkdirAll(filepath.Join(h.xdg, "lorvik"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.xdg, "lorvik", "config.toml"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		res := result{err: h.run(t, append([]string{"session", "name"}, args...)...), out: h.out.String()}
		for _, q := range rec.reqs {
			res.bodies = append(res.bodies, q.Body)
		}
		return res
	}
	off := "session_names = \"off\"\n"
	tests := []struct {
		name     string
		env      map[string]string
		config   string
		args     []string
		wantCode int
		wantErr  string
		wantBody string
		wantOut  string
	}{
		{
			name:     "a name is stored",
			args:     []string{" billing  migration "},
			wantBody: `{"name":"billing migration"}`,
			wantOut:  "session test-session is named billing migration\n",
		},
		{
			name:     "--clear stores the empty name",
			args:     []string{"--clear"},
			wantBody: `{"name":""}`,
			wantOut:  "session test-session has no name now\n",
		},
		{
			name:     "--clear works with names off",
			config:   off,
			args:     []string{"--clear"},
			wantBody: `{"name":""}`,
			wantOut:  "session test-session has no name now\n",
		},
		{
			name:     "a chat whose name is read elsewhere is refused",
			env:      map[string]string{envSessionName: "from the harness"},
			args:     []string{"mine"},
			wantCode: cmdutil.ExitConflict,
			wantErr:  `this chat's name comes from LORVIK_SESSION_NAME ("from the harness")`,
		},
		{
			name:     "clearing a name that is read elsewhere is refused",
			env:      map[string]string{envSessionName: "from the harness"},
			args:     []string{"--clear"},
			wantCode: cmdutil.ExitConflict,
			wantErr:  "this chat's name comes from LORVIK_SESSION_NAME",
		},
		{
			name:     "a name is refused with names off",
			config:   off,
			args:     []string{"mine"},
			wantCode: cmdutil.ExitConflict,
			wantErr:  `chat names are off: session_names is "off" in `,
		},
		{name: "no name and no --clear", wantCode: cmdutil.ExitUsage, wantErr: "or --clear to remove it"},
		{name: "a blank name", args: []string{"  "}, wantCode: cmdutil.ExitUsage, wantErr: "or --clear to remove it"},
		{name: "a name with --clear", args: []string{"mine", "--clear"}, wantCode: cmdutil.ExitUsage, wantErr: "not both"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := run(t, tt.env, tt.config, tt.args...)
			if code := cmdutil.ExitCode(got.err); code != tt.wantCode {
				t.Fatalf("exit = %d (%v), want %d", code, got.err, tt.wantCode)
			}
			if tt.wantErr != "" {
				if !strings.Contains(got.err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", got.err, tt.wantErr)
				}
				if len(got.bodies) != 0 {
					t.Errorf("a refused command sent %v", got.bodies)
				}
				return
			}
			if len(got.bodies) != 1 || got.bodies[0] != tt.wantBody {
				t.Errorf("bodies = %v, want one %s", got.bodies, tt.wantBody)
			}
			if got.out != tt.wantOut {
				t.Errorf("stdout = %q, want %q", got.out, tt.wantOut)
			}
		})
	}
}

func TestSessionCommandNameLine(t *testing.T) {
	t.Parallel()
	t.Run("a read name is shown with its source", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, nil)
		h.env[envSessionName] = "billing migration"
		if err := h.run(t, "session"); err != nil {
			t.Fatal(err)
		}
		if want := "name billing migration (LORVIK_SESSION_NAME): the board follows it, rename the chat there\n"; !strings.HasSuffix(h.out.String(), want) {
			t.Errorf("stdout = %q, want it to end %q", h.out.String(), want)
		}
	})
	t.Run("json carries the name, or off as its source", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, nil)
		h.env[envSessionName] = "billing migration"
		if err := h.run(t, "session", "--json"); err != nil {
			t.Fatal(err)
		}
		var got sessionView
		if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.SessionName != "billing migration" || got.SessionNameSource != envSessionName {
			t.Errorf("json = %+v, want the name and its source", got)
		}

		h = newHarness(t, nil)
		h.env[envSessionName] = "billing migration"
		if err := os.MkdirAll(filepath.Join(h.xdg, "lorvik"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.xdg, "lorvik", "config.toml"), []byte("session_names = \"off\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := h.run(t, "session", "--json"); err != nil {
			t.Fatal(err)
		}
		got = sessionView{}
		if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.SessionName != "" || got.SessionNameSource != sessionNamesOff {
			t.Errorf("json = %+v, want no name and source off", got)
		}
	})
}
