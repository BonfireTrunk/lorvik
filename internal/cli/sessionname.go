package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// envSessionName names this chat for a harness whose own chat name the
	// CLI cannot read.
	envSessionName = "LORVIK_SESSION_NAME"
	// configSessionNames is the user config key that stops the CLI sending
	// chat names: "off" is the documented value, and any value but "on" or
	// "true" counts as off, so a mistyped switch never sends a name.
	configSessionNames = "session_names"
	sessionNamesOff    = "off"
	// maxSentName bounds the name the CLI sends, in bytes: the limit of
	// PUT /me/session-name, so a runaway value cannot grow the header until
	// the server refuses every command.
	maxSentName = 400
	// maxSessionFile bounds how much of a harness's session file is read.
	maxSessionFile = 1 << 16
)

// harnessChatNames maps a harness to the reader of the name its chat carries.
// Add a row only for a file format that has been verified; never guess one.
var harnessChatNames = map[string]func(a *app, chatID string) string{
	"claude-code": (*app).claudeChatName,
}

// chatName is what people call this chat and where the CLI found it:
// envSessionName or a harness name. Name is empty when the chat has none the
// CLI can read; Off is set when the user config turned names off.
type chatName struct {
	Name   string
	Source string
	Off    bool
}

// chatName resolves the name once. Precedence: the off switch in the user
// config, LORVIK_SESSION_NAME, then the harness's own chat name.
func (a *app) chatName() chatName {
	if !a.nameDone {
		a.nameInfo = a.resolveChatName()
		a.nameDone = true
	}
	return a.nameInfo
}

func (a *app) resolveChatName() chatName {
	user, err := readTOMLFile(userConfigPath(a.getenv))
	if err != nil || !sessionNamesOn(user[configSessionNames]) {
		return chatName{Off: true}
	}
	if v := cleanSentName(a.getenv(envSessionName)); v != "" {
		return chatName{Name: v, Source: envSessionName}
	}
	for _, h := range sessionHarnesses {
		id := strings.TrimSpace(a.getenv(h.env))
		if id == "" {
			continue
		}
		if read := harnessChatNames[h.name]; read != nil {
			if v := cleanSentName(read(a, id)); v != "" {
				return chatName{Name: v, Source: h.name}
			}
		}
		break
	}
	return chatName{}
}

// sessionNamesOn reports whether the switch's value leaves names on: unset,
// "on" or "true". A config that cannot be read counts as off.
func sessionNamesOn(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "on", "true":
		return true
	}
	return false
}

// cleanSentName turns control characters into spaces, so a name never
// carries a terminal escape into `lorvik session`, collapses white space and
// cuts s to maxSentName bytes on a rune boundary. The server applies its
// own, tighter rule to what it stores.
func cleanSentName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= maxSentName {
		return s
	}
	cut := maxSentName
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut])
}

// claudeChatName reads the name of the Claude Code chat chatID from the
// session file of the Claude Code process this command runs under,
// <config dir>/sessions/<pid>.json. CLAUDE_PID is not always set and can be
// another chat's process, so the file must carry chatID; after it the CLI's
// ancestors are tried, through the shells up to the first process that is
// not one, which is Claude Code unless something else launched the command.
// Stopping there keeps the search to a hop or two where a process lookup is
// costly (it runs ps on macOS); a command launched some other way sends no
// name and the server keeps the one it has. The file is Claude Code's own,
// undocumented state: any surprise in it means no name.
func (a *app) claudeChatName(chatID string) string {
	dir := a.getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home := a.getenv("HOME")
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		dir = filepath.Join(home, ".claude")
	}
	read := func(pid int) (string, bool) {
		f, err := os.Open(filepath.Join(dir, "sessions", strconv.Itoa(pid)+".json"))
		if err != nil {
			return "", false
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, maxSessionFile))
		if err != nil {
			return "", false
		}
		var s struct {
			SessionID string `json:"sessionId"`
			Name      string `json:"name"`
		}
		if json.Unmarshal(b, &s) != nil || s.SessionID != chatID {
			return "", false
		}
		return s.Name, true
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(a.getenv("CLAUDE_PID"))); err == nil && pid > 1 {
		if name, ok := read(pid); ok {
			return name
		}
	}
	ppid := os.Getppid
	if a.f.Getppid != nil {
		ppid = a.f.Getppid
	}
	lookup := lookupProc
	if a.f.Proc != nil {
		lookup = a.f.Proc
	}
	pid := ppid()
	for range maxWalk {
		if pid <= 1 {
			break
		}
		if name, ok := read(pid); ok {
			return name
		}
		info, err := lookup(pid)
		if err != nil || !isShell(info.Name) {
			break
		}
		pid = info.PPID
	}
	return ""
}
