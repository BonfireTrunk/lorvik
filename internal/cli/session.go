package cli

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

var (
	sessionAdjectives = []string{
		"brisk", "calm", "clever", "eager", "gentle", "golden", "hazy", "jolly",
		"keen", "lively", "mellow", "nimble", "quiet", "rapid", "steady", "witty",
	}
	sessionNouns = []string{
		"otter", "heron", "falcon", "badger", "lynx", "maple", "comet", "ember",
		"harbor", "meadow", "pebble", "quartz", "river", "sparrow", "thistle", "willow",
	}

	// sessionHarnesses maps a harness to the environment variable it exports
	// to every command it runs. Add a row only for a variable name that has
	// been verified; never guess one.
	sessionHarnesses = []struct{ name, env string }{
		{"claude-code", "CLAUDE_CODE_SESSION_ID"},
		{"opencode", "OPENCODE_SESSION_ID"},
		{"codex", "CODEX_THREAD_ID"},
		{"codex", "CODEX_SESSION_ID"},
		{"pi", "PI_SESSION_ID"},
		{"qwen", "QWEN_CODE_SESSION_ID"},
		{"copilot", "COPILOT_AGENT_SESSION_ID"},
		{"dsh", "DSH_SESSION_ID"},
		// Goose's name is generic, so it is checked after every named one.
		{"goose", "AGENT_SESSION_ID"},
	}

	// sessionTerminals are terminal tab or pane ids, checked in order after
	// the process walk fails. They identify a terminal tab or pane, not a
	// chat, so they sit below the harness and process tiers. Windows Terminal
	// forwards WT_SESSION into WSL through WSLENV, which is how a
	// Windows-launched agent calling into WSL gets a per-tab label.
	sessionTerminals = []string{"TMUX_PANE", "KITTY_WINDOW_ID", "WEZTERM_PANE", "ITERM_SESSION_ID", "WT_SESSION"}

	// sessionShells are executables the process walk skips: they are the
	// shell the harness runs the command in, not the harness itself.
	sessionShells = map[string]bool{
		"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true,
		"ksh": true, "pwsh": true, "cmd": true, "powershell": true,
	}
)

// sessionBoundaries are processes that sit outside any chat: relays, session
// leaders, init systems, remote-login daemons and container shims. Their pid
// is shared by or reused across unrelated commands, so the walk never hashes
// one. Names are compared lower-case; "Relay(" matches by prefix.
var sessionBoundaries = map[string]bool{
	"sessionleader": true, "init": true, "systemd": true, "sshd": true,
	"sshd-session": true, "containerd-shim": true, "docker-init": true,
	"tini": true, "wslhost": true,
}

// isBoundary reports whether an executable name is a boundary process.
func isBoundary(name string) bool {
	n := strings.ToLower(strings.TrimSuffix(name, ".exe"))
	return sessionBoundaries[n] || strings.HasPrefix(n, "relay(")
}

// maxWalk bounds the climb through parent processes.
const maxWalk = 32

// sessionLabel renders identity as adjective-noun-xxxx: the first four hex
// characters of its SHA-256 and two words drawn from later bytes of the same
// hash, so the label is stable for an identity and spread across identities.
func sessionLabel(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return formatSessionLabel(sum[2], sum[3], sum[:2])
}

// formatSessionLabel joins the word picked by adjective, the word picked by
// noun and the hex of suffix into adjective-noun-xxxx.
func formatSessionLabel(adjective, noun byte, suffix []byte) string {
	return sessionAdjectives[int(adjective)%len(sessionAdjectives)] + "-" +
		sessionNouns[int(noun)%len(sessionNouns)] + "-" + hex.EncodeToString(suffix)
}

// isShell reports whether an executable name is a shell: case-insensitive,
// without a login dash or a .exe suffix.
func isShell(name string) bool {
	n := strings.ToLower(strings.TrimPrefix(name, "-"))
	n = strings.TrimSuffix(n, ".exe")
	return sessionShells[n]
}

// sourceRepo is the Source of the repo fallback, the only label that is not
// tied to one chat; `lorvik session` then hands out a generated one.
const sourceRepo = "repo, no chat process found"

// sessionInfo is the resolved label and where it came from: "--session",
// LORVIK_SESSION, a harness name, "process <name>", "terminal <VAR>",
// sourceRepo or "parent process". Label is empty when the flag or variable
// held something the server would refuse.
type sessionInfo struct {
	Label  string
	Source string
}

// session returns this process's session label, or "" when LORVIK_SESSION is
// set to something the server would refuse.
func (a *app) session() string {
	return a.sessionInfo().Label
}

// sessionInfo resolves the label once. Precedence: --session, LORVIK_SESSION,
// a harness variable, the launching process, a terminal id, then the repo.
// Nothing is stored.
func (a *app) sessionInfo() sessionInfo {
	if !a.sessDone {
		a.sessInfo = a.resolveSession()
		a.sessDone = true
	}
	return a.sessInfo
}

func (a *app) resolveSession() sessionInfo {
	if v := strings.TrimSpace(a.sessionFlag); v != "" {
		return a.explicitSession("--session", v)
	}
	if v := strings.TrimSpace(a.getenv("LORVIK_SESSION")); v != "" {
		return a.explicitSession("LORVIK_SESSION", v)
	}
	for _, h := range sessionHarnesses {
		if v := strings.TrimSpace(a.getenv(h.env)); v != "" {
			return sessionInfo{Label: sessionLabel(h.name + ":" + v), Source: h.name}
		}
	}
	host := a.hostname()
	pid, p, ok := a.launcher()
	if ok {
		return sessionInfo{
			Label:  sessionLabel(fmt.Sprintf("proc:%d:%s:%s", pid, p.Start, host)),
			Source: "process " + p.Name,
		}
	}
	for _, name := range sessionTerminals {
		if v := strings.TrimSpace(a.getenv(name)); v != "" {
			return sessionInfo{
				Label:  sessionLabel(fmt.Sprintf("term:%s:%s:%s:%s", name, v, a.userName(), host)),
				Source: "terminal " + name,
			}
		}
	}
	if pid == boundaryReached {
		return sessionInfo{
			Label:  sessionLabel(fmt.Sprintf("repo:%s:%s:%s", a.repoRoot(), a.userName(), host)),
			Source: sourceRepo,
		}
	}
	return sessionInfo{Label: sessionLabel(fmt.Sprintf("ppid:%d:%s", pid, host)), Source: "parent process"}
}

// explicitSession validates a label given by flag or environment. An invalid
// one is reported on stderr and leaves the label empty.
func (a *app) explicitSession(source, v string) sessionInfo {
	if apitypes.ValidSession(v) {
		return sessionInfo{Label: v, Source: source}
	}
	fmt.Fprintf(a.errOut(), "lorvik: ignoring %s %q: use up to %d lowercase letters, digits and hyphens\n", source, v, apitypes.MaxSession)
	return sessionInfo{Source: source}
}

// generateSession returns a fresh random label of the adjective-noun-xxxx
// shape from crypto/rand (or the factory's Rand).
func (a *app) generateSession() (string, error) {
	r := a.f.Rand
	if r == nil {
		r = rand.Reader
	}
	var b [4]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("generate session label: %w", err)
	}
	return formatSessionLabel(b[0], b[1], b[2:]), nil
}

// boundaryReached is the pid launcher returns, with ok false, when the walk
// ended at a boundary process or at pid 1 instead of a chat process.
const boundaryReached = -1

// repoRoot is the directory holding the .lorvik.toml the CLI resolved, or the
// working directory when there is none.
func (a *app) repoRoot() string {
	getwd := os.Getwd
	if a.f.Getwd != nil {
		getwd = a.f.Getwd
	}
	cwd, err := getwd()
	if err != nil {
		return "unknown"
	}
	if p, _, err := findBinding(cwd); err == nil && p != "" {
		return filepath.Dir(p)
	}
	return cwd
}

func (a *app) userName() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "unknown"
}

func (a *app) hostname() string {
	hn := os.Hostname
	if a.f.Hostname != nil {
		hn = a.f.Hostname
	}
	h, err := hn()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}

// launcher walks up from the CLI's parent to the first process that is not a
// shell. When the walk fails it returns the CLI's parent pid and ok false;
// when it reaches a boundary process or pid 1 it returns boundaryReached and
// ok false.
func (a *app) launcher() (pid int, p cmdutil.ProcInfo, ok bool) {
	ppid := os.Getppid
	if a.f.Getppid != nil {
		ppid = a.f.Getppid
	}
	lookup := lookupProc
	if a.f.Proc != nil {
		lookup = a.f.Proc
	}
	first := ppid()
	pid = first
	for range maxWalk {
		if pid <= 1 {
			return boundaryReached, cmdutil.ProcInfo{}, false
		}
		info, err := lookup(pid)
		if err != nil {
			return first, cmdutil.ProcInfo{}, false
		}
		if isBoundary(info.Name) {
			return boundaryReached, cmdutil.ProcInfo{}, false
		}
		if !isShell(info.Name) {
			return pid, info, true
		}
		pid = info.PPID
	}
	return first, cmdutil.ProcInfo{}, false
}

// parseLinuxStat reads /proc/<pid>/stat. The command name sits in
// parentheses and may itself contain spaces and parentheses, so everything
// is located from the last ')'. After it come state (field 3), ppid (field 4)
// and, as field 22, the start time in clock ticks since boot.
func parseLinuxStat(b []byte) (cmdutil.ProcInfo, error) {
	s := string(b)
	open := strings.IndexByte(s, '(')
	end := strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return cmdutil.ProcInfo{}, fmt.Errorf("malformed stat %q", s)
	}
	f := strings.Fields(s[end+1:])
	if len(f) < 20 {
		return cmdutil.ProcInfo{}, fmt.Errorf("stat has %d fields after the command name, want at least 20", len(f))
	}
	ppid, err := strconv.Atoi(f[1])
	if err != nil {
		return cmdutil.ProcInfo{}, fmt.Errorf("stat ppid: %w", err)
	}
	return cmdutil.ProcInfo{Name: s[open+1 : end], PPID: ppid, Start: f[19]}, nil
}

// parsePS reads `ps -o ppid=,lstart=,comm=` output: the parent pid, a
// five-word start time (Mon Jan  2 15:04:05 2026) and the command path,
// which may contain spaces.
func parsePS(out string) (cmdutil.ProcInfo, error) {
	f := strings.Fields(out)
	if len(f) < 7 {
		return cmdutil.ProcInfo{}, fmt.Errorf("unexpected ps output %q", out)
	}
	ppid, err := strconv.Atoi(f[0])
	if err != nil {
		return cmdutil.ProcInfo{}, fmt.Errorf("ps ppid: %w", err)
	}
	comm := strings.Join(f[6:], " ")
	if i := strings.LastIndexByte(comm, '/'); i >= 0 {
		comm = comm[i+1:]
	}
	return cmdutil.ProcInfo{Name: comm, PPID: ppid, Start: strings.Join(f[1:6], " ")}, nil
}
