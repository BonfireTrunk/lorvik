package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

// fakeTree is a process table for the walk: pid to process.
type fakeTree map[int]cmdutil.ProcInfo

func (ft fakeTree) lookup(pid int) (cmdutil.ProcInfo, error) {
	p, ok := ft[pid]
	if !ok {
		return cmdutil.ProcInfo{}, fmt.Errorf("no process %d", pid)
	}
	return p, nil
}

// sessionApp builds an app whose environment, host and process tree are fake,
// so no test touches the real process table.
func sessionApp(t *testing.T, env map[string]string, host string, ppid int, tree fakeTree) (*app, *harness) {
	t.Helper()
	h := newHarness(t, nil)
	h.env = env
	h.f.Hostname = func() (string, error) { return host, nil }
	h.f.Getppid = func() int { return ppid }
	h.f.Proc = tree.lookup
	return &app{f: h.f}, h
}

func TestSessionLabelFormat(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`^[a-z]+-[a-z]+-[0-9a-f]{4}$`)
	for i := range 200 {
		label := sessionLabel(fmt.Sprintf("identity-%d", i))
		if !re.MatchString(label) || !apitypes.ValidSession(label) {
			t.Fatalf("label %q is not adjective-noun-xxxx accepted by ValidSession", label)
		}
	}
	for _, adj := range sessionAdjectives {
		for _, noun := range sessionNouns {
			if label := adj + "-" + noun + "-ffff"; !apitypes.ValidSession(label) {
				t.Errorf("label %q fails ValidSession", label)
			}
		}
	}
}

func TestSessionPrecedence(t *testing.T) {
	t.Parallel()
	tree := fakeTree{
		100: {Name: "bash", PPID: 50, Start: "1"},
		50:  {Name: "claude", PPID: 1, Start: "7"},
	}
	harnessLabel := sessionLabel("claude-code:abc")
	procLabel := sessionLabel("proc:50:7:box")
	tests := []struct {
		name       string
		env        map[string]string
		want       string
		wantSource string
	}{
		{"env beats harness and process", map[string]string{"LORVIK_SESSION": "mine", "CLAUDE_CODE_SESSION_ID": "abc"}, "mine", "LORVIK_SESSION"},
		{"harness beats process", map[string]string{"CLAUDE_CODE_SESSION_ID": "abc"}, harnessLabel, "claude-code"},
		{"codex thread id", map[string]string{"CODEX_THREAD_ID": "t1", "CODEX_SESSION_ID": "t1", "WT_SESSION": "w"}, sessionLabel("codex:t1"), "codex"},
		{"codex session id alone", map[string]string{"CODEX_SESSION_ID": "s1"}, sessionLabel("codex:s1"), "codex"},
		{"pi session id beats process and terminal", map[string]string{"PI_SESSION_ID": "p1", "WT_SESSION": "w"}, sessionLabel("pi:p1"), "pi"},
		{"qwen session id", map[string]string{"QWEN_CODE_SESSION_ID": "q1"}, sessionLabel("qwen:q1"), "qwen"},
		{"copilot session id", map[string]string{"COPILOT_AGENT_SESSION_ID": "c1"}, sessionLabel("copilot:c1"), "copilot"},
		{"deepseek harness session id", map[string]string{"DSH_SESSION_ID": "d1"}, sessionLabel("dsh:d1"), "dsh"},
		{"goose session id", map[string]string{"AGENT_SESSION_ID": "g1"}, sessionLabel("goose:g1"), "goose"},
		{"a named harness beats goose's generic variable", map[string]string{"AGENT_SESSION_ID": "g1", "QWEN_CODE_SESSION_ID": "q1"}, sessionLabel("qwen:q1"), "qwen"},
		{"process when nothing else is set", map[string]string{}, procLabel, "process claude"},
		{"blank env falls through", map[string]string{"LORVIK_SESSION": "  ", "CLAUDE_CODE_SESSION_ID": ""}, procLabel, "process claude"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, h := sessionApp(t, tt.env, "box", 100, tree)
			got := a.sessionInfo()
			if got.Label != tt.want || got.Source != tt.wantSource {
				t.Errorf("sessionInfo = %+v, want label %q source %q", got, tt.want, tt.wantSource)
			}
			if h.errOut.String() != "" {
				t.Errorf("stderr = %q, want silence", h.errOut.String())
			}
		})
	}
}

func TestSessionInvalidEnvIsIgnoredWithWarning(t *testing.T) {
	t.Parallel()
	a, h := sessionApp(t, map[string]string{"LORVIK_SESSION": "Bad Label", "CLAUDE_CODE_SESSION_ID": "abc"}, "box", 100, fakeTree{})
	if got := a.session(); got != "" {
		t.Errorf("session = %q, want empty", got)
	}
	if !strings.Contains(h.errOut.String(), "ignoring LORVIK_SESSION") {
		t.Errorf("stderr = %q, want a warning", h.errOut.String())
	}
}

func TestSessionProcessIdentity(t *testing.T) {
	t.Parallel()
	tree := fakeTree{
		100: {Name: "zsh", PPID: 50, Start: "1"},
		50:  {Name: "claude", PPID: 1, Start: "7"},
	}
	label := func(host string, ppid int, tree fakeTree) string {
		a, _ := sessionApp(t, map[string]string{}, host, ppid, tree)
		return a.session()
	}
	base := label("box", 100, tree)
	if again := label("box", 100, tree); again != base {
		t.Errorf("same identity gave %q then %q", base, again)
	}
	// A shell started later under the same claude process is still the same chat.
	other := fakeTree{100: {Name: "bash", PPID: 50, Start: "99"}, 50: tree[50]}
	if got := label("box", 100, other); got != base {
		t.Errorf("another shell under the same launcher gave %q, want %q", got, base)
	}
	if got := label("other-box", 100, tree); got == base {
		t.Errorf("a different hostname gave the same label %q", got)
	}
	diffPID := fakeTree{100: tree[100], 50: {Name: "claude", PPID: 1, Start: "7"}, 60: {Name: "claude", PPID: 1, Start: "7"}}
	diffPID[100] = cmdutil.ProcInfo{Name: "zsh", PPID: 60, Start: "1"}
	if got := label("box", 100, diffPID); got == base {
		t.Errorf("a different pid gave the same label %q", got)
	}
	reused := fakeTree{100: tree[100], 50: {Name: "claude", PPID: 1, Start: "8"}}
	if got := label("box", 100, reused); got == base {
		t.Errorf("a different start time gave the same label %q", got)
	}
}

func TestSessionWalkSkipsShells(t *testing.T) {
	t.Parallel()
	tree := fakeTree{
		30: {Name: "-bash", PPID: 20, Start: "3"},
		20: {Name: "pwsh.exe", PPID: 10, Start: "2"},
		10: {Name: "Code", PPID: 1, Start: "1"},
	}
	a, _ := sessionApp(t, map[string]string{}, "box", 30, tree)
	pid, p, ok := a.launcher()
	if !ok || pid != 10 || p.Name != "Code" {
		t.Errorf("launcher = %d %+v %v, want pid 10 Code", pid, p, ok)
	}
}

func TestSessionWalkFailureFallsBackToParentPid(t *testing.T) {
	t.Parallel()
	// The chain breaks at pid 20, which the table does not know.
	tree := fakeTree{30: {Name: "bash", PPID: 20, Start: "3"}}
	a, _ := sessionApp(t, map[string]string{}, "box", 30, tree)
	got := a.sessionInfo()
	if want := sessionLabel("ppid:30:box"); got.Label != want || got.Source != "parent process" {
		t.Errorf("sessionInfo = %+v, want label %q from the parent process", got, want)
	}
	a2, _ := sessionApp(t, map[string]string{}, "box", 31, tree)
	if a2.session() == got.Label {
		t.Error("a different parent pid gave the same fallback label")
	}
}

func TestSessionBoundaryFallsBackToRepo(t *testing.T) {
	t.Parallel()
	const src = "repo, no chat process found"
	for _, name := range []string{"Relay(2406680)", "SessionLeader", "init", "systemd", "sshd", "sshd-session", "containerd-shim", "docker-init", "tini", "wslhost"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tree := fakeTree{
				100: {Name: "bash", PPID: 50, Start: "1"},
				50:  {Name: name, PPID: 2, Start: "7"},
				2:   {Name: "other", PPID: 1, Start: "3"},
			}
			a, h := sessionApp(t, map[string]string{}, "box", 100, tree)
			h.f.Getwd = func() (string, error) { return "/work/a", nil }
			got := a.sessionInfo()
			if got.Source != src {
				t.Fatalf("source = %q, want %q", got.Source, src)
			}
			// A different relay pid or start time must not change the label.
			tree[50] = cmdutil.ProcInfo{Name: name, PPID: 2, Start: "99"}
			b, h2 := sessionApp(t, map[string]string{}, "box", 100, tree)
			h2.f.Getwd = func() (string, error) { return "/work/a", nil }
			if b.session() != got.Label {
				t.Errorf("label changed with the boundary process: %q vs %q", b.session(), got.Label)
			}
		})
	}
}

func TestSessionReachingPid1FallsBackToRepo(t *testing.T) {
	t.Parallel()
	tree := fakeTree{100: {Name: "bash", PPID: 1, Start: "1"}}
	a, h := sessionApp(t, map[string]string{}, "box", 100, tree)
	h.f.Getwd = func() (string, error) { return "/work/a", nil }
	if got := a.sessionInfo(); got.Source != "repo, no chat process found" {
		t.Errorf("source = %q", got.Source)
	}
}

func TestSessionRepoFallbackIdentity(t *testing.T) {
	t.Parallel()
	tree := fakeTree{100: {Name: "bash", PPID: 50, Start: "1"}, 50: {Name: "Relay(5)", PPID: 1, Start: "7"}}
	label := func(host, cwd string) string {
		a, h := sessionApp(t, map[string]string{}, host, 100, tree)
		h.f.Getwd = func() (string, error) { return cwd, nil }
		return a.session()
	}
	base := label("box", t.TempDir())
	dirA := t.TempDir()
	if label("box", dirA) == base {
		t.Error("a different repo path gave the same label")
	}
	if label("other", dirA) == label("box", dirA) {
		t.Error("a different host gave the same label")
	}
	first := label("box", dirA)
	if label("box", dirA) != first {
		t.Error("label is not stable")
	}
	// Under a bound repo the root is the .lorvik.toml directory, so a
	// subdirectory shares the label.
	if err := os.WriteFile(filepath.Join(dirA, ".lorvik.toml"), []byte("project = \"p\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dirA, "x", "y")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if label("box", sub) != label("box", dirA) {
		t.Error("subdirectory of a bound repo gave another label")
	}
}

func TestIsBoundary(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"Relay(12)": true, "SessionLeader": true, "SSHD": true, "tini.exe": true,
		"claude": false, "relay": false, "initd": false, "Code": false,
	} {
		if got := isBoundary(name); got != want {
			t.Errorf("isBoundary(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestSessionConfigFileKeyIsIgnoredAndNotRewritten(t *testing.T) {
	t.Parallel()
	a, h := sessionApp(t, map[string]string{}, "box", 100, fakeTree{100: {Name: "claude", PPID: 1, Start: "7"}})
	path := filepath.Join(t.TempDir(), "config.toml")
	h.env["LORVIK_CONFIG"] = path
	const body = "session = \"from-cfg\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := a.session(); got == "from-cfg" || got == "" {
		t.Errorf("session = %q, want the derived label, not the config value", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != body {
		t.Errorf("config = %q (err %v), want it untouched", raw, err)
	}
	if h.errOut.String() != "" {
		t.Errorf("stderr = %q, want silence", h.errOut.String())
	}
}

func TestParseLinuxStat(t *testing.T) {
	t.Parallel()
	tail := " S 4242 4242 4242 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 987654 1000 100 18446744073709551615"
	tests := []struct {
		name    string
		in      string
		want    cmdutil.ProcInfo
		wantErr bool
	}{
		{"plain", "1234 (bash)" + tail, cmdutil.ProcInfo{Name: "bash", PPID: 4242, Start: "987654"}, false},
		{"spaces in comm", "1234 (Web Content)" + tail, cmdutil.ProcInfo{Name: "Web Content", PPID: 4242, Start: "987654"}, false},
		{"parentheses in comm", "1234 (a) S 1 (b)" + tail, cmdutil.ProcInfo{Name: "a) S 1 (b", PPID: 4242, Start: "987654"}, false},
		{"truncated", "1234 (bash) S 1 2", cmdutil.ProcInfo{}, true},
		{"no parentheses", "1234 bash S 1", cmdutil.ProcInfo{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseLinuxStat([]byte(tt.in))
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("parseLinuxStat = %+v, %v; want %+v, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestParsePS(t *testing.T) {
	t.Parallel()
	got, err := parsePS("  812 Mon Jan  2 15:04:05 2026 /Applications/Visual Studio Code.app/Contents/MacOS/Electron\n")
	want := cmdutil.ProcInfo{Name: "Electron", PPID: 812, Start: "Mon Jan 2 15:04:05 2026"}
	if err != nil || got != want {
		t.Errorf("parsePS = %+v, %v; want %+v", got, err, want)
	}
	if _, err := parsePS(""); err == nil {
		t.Error("parsePS of empty output succeeded")
	}
}

func TestRequestsCarrySessionHeader(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	srv := serve(t, rec.wrapAll(map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/next": reply(200, fixtureCard()),
		"GET /api/v1/actors":                   reply(200, fixtureActors()),
	}))
	h := newHarness(t, srv)
	h.env["LORVIK_SESSION"] = "calm-lynx"
	if err := h.run(t, "-p", "demo", "next"); err != nil {
		t.Fatal(err)
	}
	if got := rec.last(t, "GET /api/v1/projects/demo/cards/next").Header.Get(apitypes.HeaderSession); got != "calm-lynx" {
		t.Errorf("%s = %q, want calm-lynx", apitypes.HeaderSession, got)
	}
}

func TestStatusSplitsCardsByWhoOwnsTheClaim(t *testing.T) {
	t.Parallel()
	mine := apitypes.CardList{Items: []apitypes.CardBrief{
		{Number: 1, Status: "working", Priority: "normal", Title: "Here", Claim: &apitypes.Claim{Actor: claude, Session: "calm-lynx", Owned: true, ExpiresAt: fixedNow.Add(12 * time.Minute)}},
		{Number: 2, Status: "working", Priority: "normal", Title: "Elsewhere", Claim: &apitypes.Claim{Actor: claude, Session: "brisk-otter", ExpiresAt: fixedNow.Add(20 * time.Minute)}},
		{Number: 3, Status: "working", Priority: "normal", Title: "Legacy", Claim: &apitypes.Claim{Actor: claude, ExpiresAt: fixedNow.Add(-5 * time.Minute)}},
		{Number: 4, Status: "ready", Priority: "normal", Title: "Assigned only", Assignee: &claude},
		{Number: 5, Status: "working", Priority: "normal", Title: "Mine under another label", Claim: &apitypes.Claim{Actor: claude, Session: "browser", Owned: true, ExpiresAt: fixedNow.Add(3 * time.Minute)}},
	}}
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo":       reply(200, apitypes.Project{Slug: "demo", Name: "Demo"}),
		"GET /api/v1/projects/demo/cards": reply(200, mine),
		"GET /api/v1/actors":              reply(200, fixtureActors()),
	})
	h := newHarness(t, srv)
	h.env["LORVIK_SESSION"] = "calm-lynx"
	if err := h.run(t, "-p", "demo", "status"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	// A claim line ends with the time left on its lease; a card that is only
	// assigned has none.
	want := "my cards (this session):\n  #1  working  normal  Here  @claude-code (agent) (calm-lynx)  12m left\n  #4  ready  normal  Assigned only  @claude-code (agent)\n  #5  working  normal  Mine under another label  @claude-code (agent) (browser)  3m left\n" +
		"my cards (other sessions):\n  #2  working  normal  Elsewhere  @claude-code (agent) (brisk-otter)  20m left\n  #3  working  normal  Legacy  @claude-code (agent)  lease ran out\n"
	if !strings.Contains(out, want) {
		t.Errorf("status output:\n%s\nwant it to contain:\n%s", out, want)
	}
}

func TestSessionFlagAndTerminalPrecedence(t *testing.T) {
	t.Parallel()
	chat := fakeTree{
		100: {Name: "bash", PPID: 50, Start: "1"},
		50:  {Name: "opencode", PPID: 1, Start: "7"},
	}
	boundary := fakeTree{100: {Name: "bash", PPID: 50, Start: "1"}, 50: {Name: "SessionLeader", PPID: 1, Start: "7"}}
	termLabel := func(v, name string) string {
		return sessionLabel("term:" + name + ":" + v + ":" + (&app{}).userName() + ":box")
	}
	tests := []struct {
		name       string
		env        map[string]string
		flag       string
		tree       fakeTree
		wantLabel  string
		wantSource string
	}{
		{"flag beats env", map[string]string{"LORVIK_SESSION": "from-env"}, "from-flag", chat, "from-flag", "--session"},
		{"opencode harness var beats process", map[string]string{"OPENCODE_SESSION_ID": "ses_1"}, "", chat, sessionLabel("opencode:ses_1"), "opencode"},
		{"process beats terminal", map[string]string{"WT_SESSION": "w"}, "", chat, sessionLabel("proc:50:7:box"), "process opencode"},
		{"terminal after boundary", map[string]string{"WT_SESSION": "w"}, "", boundary, termLabel("w", "WT_SESSION"), "terminal WT_SESSION"},
		{"terminal after failed walk", map[string]string{"TMUX_PANE": "%3"}, "", fakeTree{}, termLabel("%3", "TMUX_PANE"), "terminal TMUX_PANE"},
		{"terminal order", map[string]string{"WT_SESSION": "w", "KITTY_WINDOW_ID": "4", "TMUX_PANE": "%1"}, "", boundary, termLabel("%1", "TMUX_PANE"), "terminal TMUX_PANE"},
		{"term session id is not used", map[string]string{"TERM_SESSION_ID": "x"}, "", boundary, "", sourceRepo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, _ := sessionApp(t, tt.env, "box", 100, tt.tree)
			a.sessionFlag = tt.flag
			got := a.sessionInfo()
			if got.Source != tt.wantSource || (tt.wantLabel != "" && got.Label != tt.wantLabel) {
				t.Errorf("sessionInfo = %+v, want label %q source %q", got, tt.wantLabel, tt.wantSource)
			}
		})
	}
}

func TestSessionFlagInvalid(t *testing.T) {
	t.Parallel()
	a, h := sessionApp(t, map[string]string{"LORVIK_SESSION": "ok"}, "box", 100, fakeTree{})
	a.sessionFlag = "Bad Label"
	if got := a.session(); got != "" {
		t.Errorf("session = %q, want empty (an invalid flag does not fall through)", got)
	}
	if !strings.Contains(h.errOut.String(), `ignoring --session "Bad Label"`) {
		t.Errorf("stderr = %q, want a warning naming --session", h.errOut.String())
	}
}

func TestSessionCommand(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`^[a-z]+-[a-z]+-[0-9a-f]{4}$`)
	t.Run("derived label has nothing to pass", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, nil)
		h.env["LORVIK_SESSION"] = "calm-lynx"
		if err := h.run(t, "session"); err != nil {
			t.Fatal(err)
		}
		want := "session calm-lynx (LORVIK_SESSION)\nnothing to pass: this label is derived for this chat\n" +
			"name none found: when your human names this chat, run lorvik session name \"<that name>\"\n"
		if got := h.out.String(); got != want {
			t.Errorf("stdout = %q, want %q", got, want)
		}
	})
	t.Run("flag label says to keep passing it", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, nil)
		if err := h.run(t, "--session", "quiet-badger-4aa4", "session"); err != nil {
			t.Fatal(err)
		}
		want := "session quiet-badger-4aa4 (--session)\nkeep passing --session quiet-badger-4aa4 on every lorvik call in this chat\n" +
			"name none found: when your human names this chat, run lorvik session name \"<that name>\"\n"
		if got := h.out.String(); got != want {
			t.Errorf("stdout = %q, want %q", got, want)
		}
	})
	t.Run("repo fallback generates a fresh label each call", func(t *testing.T) {
		t.Parallel()
		labels := map[string]bool{}
		for range 2 {
			h := newHarness(t, nil)
			h.f.Hostname = func() (string, error) { return "box", nil }
			delete(h.env, "LORVIK_SESSION")
			h.f.Getppid = func() int { return 1 }
			h.f.Proc = fakeTree{}.lookup
			if err := h.run(t, "session", "--json"); err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			label, _ := got["session"].(string)
			if !re.MatchString(label) || !apitypes.ValidSession(label) || got["generated"] != true || got["session_source"] != "generated" {
				t.Fatalf("json = %v, want a generated adjective-noun-xxxx label", got)
			}
			labels[label] = true
		}
		if len(labels) != 2 {
			t.Errorf("two calls gave the same label: %v", labels)
		}
	})
	t.Run("repo fallback human text", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, nil)
		h.f.Getppid = func() int { return 1 }
		delete(h.env, "LORVIK_SESSION")
		h.f.Proc = fakeTree{}.lookup
		h.f.Rand = bytes.NewReader([]byte{0, 1, 0xab, 0xcd})
		if err := h.run(t, "session"); err != nil {
			t.Fatal(err)
		}
		want := "session brisk-heron-abcd (generated)\npass --session brisk-heron-abcd on every lorvik call in this chat\n" +
			"name none found: when your human names this chat, run lorvik session name \"<that name>\"\n"
		if got := h.out.String(); got != want {
			t.Errorf("stdout = %q, want %q", got, want)
		}
	})
}

func TestHoldingSessions(t *testing.T) {
	t.Parallel()
	w := func(s string) apitypes.CardBrief {
		c := apitypes.CardBrief{Status: "working"}
		if s != "" {
			c.Claim = &apitypes.Claim{Session: s}
		}
		return c
	}
	got := holdingSessions([]apitypes.CardBrief{w("b-b"), w("a-a"), w("c-c"), w("c-c"), w(""), {Status: "working"}})
	want := []sessionHolding{{"c-c", 2}, {"a-a", 1}, {"b-b", 1}}
	if !slices.Equal(got, want) {
		t.Errorf("holdingSessions = %v, want %v", got, want)
	}
	if got := holdingSessions(nil); got == nil || len(got) != 0 {
		t.Errorf("holdingSessions(nil) = %#v, want empty non-nil", got)
	}
}

func TestStatusSessionsLine(t *testing.T) {
	t.Parallel()
	working := apitypes.CardList{Items: []apitypes.CardBrief{
		{Number: 1, Status: "working", Claim: &apitypes.Claim{Actor: claude, Session: "witty-lynx-3f9a"}},
		{Number: 2, Status: "working", Claim: &apitypes.Claim{Actor: claude, Session: "keen-otter-a0f7"}},
		{Number: 3, Status: "working", Claim: &apitypes.Claim{Actor: claude, Session: "witty-lynx-3f9a"}},
	}}
	var query string
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo": reply(200, apitypes.Project{Slug: "demo", Name: "Demo"}),
		"GET /api/v1/projects/demo/cards": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("mine") == "" {
				query = r.URL.RawQuery
				reply(200, working)(w, r)
				return
			}
			reply(200, apitypes.CardList{Items: []apitypes.CardBrief{}})(w, r)
		},
		"GET /api/v1/actors": reply(200, fixtureActors()),
	})
	h := newHarness(t, srv)
	h.env["LORVIK_SESSION"] = "calm-lynx"
	if err := h.run(t, "-p", "demo", "status"); err != nil {
		t.Fatal(err)
	}
	if want := "sessions holding cards: witty-lynx-3f9a (2), keen-otter-a0f7 (1)\n"; !strings.HasSuffix(h.out.String(), want) {
		t.Errorf("status output:\n%s\nwant suffix %q", h.out.String(), want)
	}
	if want := "limit=200&status=working"; query != want {
		t.Errorf("working query = %q, want %q", query, want)
	}
	h2 := newHarness(t, srv)
	h2.env["LORVIK_SESSION"] = "calm-lynx"
	if err := h2.run(t, "-p", "demo", "status", "--json"); err != nil {
		t.Fatal(err)
	}
	if want := `"sessions": [`; !strings.Contains(h2.out.String(), want) || !strings.Contains(h2.out.String(), `"label": "witty-lynx-3f9a"`) {
		t.Errorf("json lacks sessions: %s", h2.out.String())
	}
}

// status is where a chat reads what its owner gave it: the rights, when they
// end, and the owner's instructions, each line of them indented.
func TestStatusShowsWhatTheChatWasGiven(t *testing.T) {
	t.Parallel()
	end := fixedNow.Add(7*time.Hour + 30*time.Minute)
	tests := []struct {
		name  string
		grant *apitypes.SessionGrant
		want  string
	}{
		{"no grant", nil, ""},
		{"rights only", &apitypes.SessionGrant{Project: "demo", Rights: []string{"ready"}}, "this chat was given ready\n"},
		{"an end and instructions", &apitypes.SessionGrant{Project: "demo", Rights: []string{"answer", "ready"}, EndsAt: &end, Instructions: "lanes server and cli\nnothing about pricing"},
			"this chat was given answer, ready, ending in 7h\ninstructions:\n  lanes server and cli\n  nothing about pricing\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := serve(t, map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo":       reply(200, apitypes.Project{Slug: "demo", Name: "Demo", Visibility: "org", Rights: []string{"work"}, ChatGrant: tt.grant}),
				"GET /api/v1/projects/demo/cards": reply(200, apitypes.CardList{Items: []apitypes.CardBrief{}}),
				"GET /api/v1/actors":              reply(200, fixtureActors()),
			})
			h := newHarness(t, srv)
			if err := h.run(t, "-p", "demo", "status"); err != nil {
				t.Fatal(err)
			}
			_, after, _ := strings.Cut(h.out.String(), "rights work\n")
			if got, _, _ := strings.Cut(after, "my cards"); got != tt.want {
				t.Errorf("between the project line and my cards:\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestStatusCountsHoldersAcrossEveryPageOfWorkingCards(t *testing.T) {
	t.Parallel()
	held := func(session string) apitypes.CardBrief {
		return apitypes.CardBrief{Status: "working", Claim: &apitypes.Claim{Session: session}}
	}
	var cursors []string
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/actors":        reply(200, fixtureActors()),
		"GET /api/v1/locks":         reply(200, apitypes.LockList{}),
		"GET /api/v1/projects/demo": reply(200, apitypes.Project{Slug: "demo"}),
		"GET /api/v1/projects/demo/cards": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("status") != "working" {
				reply(200, apitypes.CardList{})(w, r)
				return
			}
			cursor := r.URL.Query().Get("cursor")
			cursors = append(cursors, cursor)
			if cursor == "" {
				list := apitypes.CardList{Items: []apitypes.CardBrief{held("a-a")}}
				list.Next, list.Remaining = "p2", 1
				reply(200, list)(w, r)
				return
			}
			reply(200, apitypes.CardList{Items: []apitypes.CardBrief{held("a-a")}})(w, r)
		},
	})
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "status", "--json"); err != nil {
		t.Fatal(err)
	}
	var got statusView
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if want := []string{"", "p2"}; !slices.Equal(cursors, want) {
		t.Errorf("cursors = %q, want %q", cursors, want)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].Cards != 2 {
		t.Errorf("sessions = %+v, want a-a holding 2", got.Sessions)
	}
}
