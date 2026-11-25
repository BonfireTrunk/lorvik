package cli

import (
	"maps"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

var stampNow = strconv.FormatInt(fixedNow.Unix(), 10)

func stampedAgo(id string, ago time.Duration) string {
	return stampCursor(id, fixedNow.Add(-ago))
}

func (h *harness) setConfig(t *testing.T, kv map[string]string) {
	t.Helper()
	if err := UpdateUserConfig(func(k string) string { return h.env[k] }, kv); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) configMap(t *testing.T) map[string]string {
	t.Helper()
	m, err := readTOMLFile(filepath.Join(h.xdg, "lorvik", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSplitCursor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in          string
		id          string
		at          int64
		wantStamped bool
	}{
		{"9103", "9103", 0, false},
		{"9103@1792324800", "9103", 1792324800, true},
		{"9103@soon", "9103", 0, false},
		{"", "", 0, false},
	}
	for _, tt := range tests {
		id, at, stamped := splitCursor(tt.in)
		if id != tt.id || at != tt.at || stamped != tt.wantStamped {
			t.Errorf("splitCursor(%q) = %q, %d, %v; want %q, %d, %v", tt.in, id, at, stamped, tt.id, tt.at, tt.wantStamped)
		}
	}
}

func TestNewerCursor(t *testing.T) {
	t.Parallel()
	tests := []struct{ cur, next, want string }{
		{"", "9103", "9103"},
		{"50", "9103", "9103"},
		{"9500", "9103", "9500"},
		{"999", "1000", "1000"},
		{"1000", "999", "1000"},
		{"junk", "9103", "9103"},
		{"9103", "junk", "9103"},
		{"junk", "junk2", "junk2"},
		{eventPosAt(5, 9), eventPosAt(6, 3), eventPosAt(6, 3)},
		{eventPosAt(6, 3), eventPosAt(5, 9), eventPosAt(6, 3)},
		{eventPosAt(6, 3), eventPosAt(6, 4), eventPosAt(6, 4)},
		{eventPos(7), eventPos(7), eventPos(7)},
		{"99999", eventPos(1), eventPos(1)},
		{eventPos(1), "99999", eventPos(1)},
		{"", eventPos(1), eventPos(1)},
		{eventPos(1), "junk", eventPos(1)},
		{"abc-def", eventPos(1), eventPos(1)},
		{eventPos(1), "abc-def", eventPos(1)},
		{"2026-11-07", eventPos(1), eventPos(1)},
	}
	for _, tt := range tests {
		if got := newerCursor(tt.cur, tt.next); got != tt.want {
			t.Errorf("newerCursor(%q, %q) = %q, want %q", tt.cur, tt.next, got, tt.want)
		}
	}
}

func TestSessionSaveMovesTheAgentCursorForwardOnly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		agent string
		want  string
	}{
		{"no agent key yet", "", eventPos(9103)},
		{"older agent key moves forward", eventPos(50), eventPos(9103)},
		{"newer agent key stays", eventPos(9500), eventPos(9500)},
		{"legacy id agent key is replaced by a position", "9500", eventPos(9103)},
		{"non-numeric agent key is replaced", "junk", eventPos(9103)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, serve(t, inboxRoutes(&recorder{}, fixtureInbox())))
			if tt.agent != "" {
				h.setConfig(t, map[string]string{"cursor_tok": tt.agent})
			}
			if err := h.run(t, "inbox", "--from", "oldest"); err != nil {
				t.Fatal(err)
			}
			if got := h.configMap(t)["cursor_tok"]; got != tt.want {
				t.Errorf("agent cursor = %q, want %q", got, tt.want)
			}
		})
	}
}

// The feed is ordered by (xid, id), so the later position can carry the lower
// event id; the agent cursor must follow the position.
func TestAgentCursorFollowsFeedOrderNotEventIDOrder(t *testing.T) {
	t.Parallel()
	later, earlier := eventPosAt(200, 5), eventPosAt(100, 9)
	var mu sync.Mutex
	var next string
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/inbox": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			reply(200, apitypes.Inbox{Events: []apitypes.Event{}, Cursor: next})(w, r)
		},
	}
	h := newHarness(t, serve(t, routes))
	for _, c := range []struct{ session, cursor string }{{"chat-one", later}, {"chat-two", earlier}} {
		mu.Lock()
		next = c.cursor
		mu.Unlock()
		h.env["LORVIK_SESSION"] = c.session
		if err := h.run(t, "inbox"); err != nil {
			t.Fatal(err)
		}
	}
	if got := h.configMap(t)["cursor_tok"]; got != later {
		t.Errorf("agent cursor = %q, want %q (later in the feed, lower event id)", got, later)
	}
}

func TestNewSessionStartsFromWhatAnotherSessionRead(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	h := newHarness(t, serve(t, inboxRoutes(rec, fixtureInbox())))

	h.env["LORVIK_SESSION"] = "chat-one"
	if err := h.run(t, "inbox"); err != nil {
		t.Fatal(err)
	}
	h.env["LORVIK_SESSION"] = "chat-two"
	if err := h.run(t, "inbox", "--peek"); err != nil {
		t.Fatal(err)
	}
	if got := rec.last(t, "GET /api/v1/inbox").URI; got != "/api/v1/inbox?cursor="+eventPos(9103)+"&limit=10" {
		t.Errorf("new session request = %q, want to start after the cursor chat-one advanced", got)
	}
}

func TestSessionSavePrunesIdleSessionKeys(t *testing.T) {
	t.Parallel()
	h := newHarness(t, serve(t, inboxRoutes(&recorder{}, fixtureInbox())))
	h.setConfig(t, map[string]string{
		"cursor_tok_stale":        stampedAgo("10", sessionCursorTTL+time.Hour),
		"cursor_tok_fresh":        stampedAgo("20", sessionCursorTTL-time.Hour),
		"cursor_tok_legacy":       "30",
		"cursor_tokother_stale":   stampedAgo("40", 30*24*time.Hour),
		"cursor_tok_test-session": stampedAgo("50", 30*24*time.Hour),
	})
	if err := h.run(t, "inbox", "--from", "oldest"); err != nil {
		t.Fatal(err)
	}
	m := h.configMap(t)
	if _, ok := m["cursor_tok_stale"]; ok {
		t.Errorf("stale session key survived: %v", m)
	}
	if got := m["cursor_tok_fresh"]; got != stampedAgo("20", sessionCursorTTL-time.Hour) {
		t.Errorf("fresh session key = %q, want it untouched", got)
	}
	if got := m["cursor_tok_legacy"]; got != "30@"+stampNow {
		t.Errorf("legacy session key = %q, want it stamped now, not pruned", got)
	}
	if got := m["cursor_tokother_stale"]; got == "" {
		t.Errorf("another token's key was pruned: %v", m)
	}
	if got := m["cursor_tok_test-session"]; got != eventPos(9103)+"@"+stampNow {
		t.Errorf("own session key = %q, want the new cursor stamped now", got)
	}
}

func TestTheStampNeverReachesTheServer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		config map[string]string
	}{
		{"stamped session key", map[string]string{"cursor_tok_test-session": "70@" + stampNow}},
		{"legacy plain session key", map[string]string{"cursor_tok_test-session": "70"}},
		{"agent key present beside a stamped session key", map[string]string{"cursor_tok": "70", "cursor_tok_test-session": "70@" + stampNow}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{}
			h := newHarness(t, serve(t, inboxRoutes(rec, fixtureInbox())))
			h.setConfig(t, tt.config)
			if err := h.run(t, "inbox"); err != nil {
				t.Fatal(err)
			}
			if got := rec.last(t, "GET /api/v1/inbox").URI; got != "/api/v1/inbox?cursor=70&limit=10" {
				t.Errorf("inbox request = %q", got)
			}
			rec.mu.Lock()
			defer rec.mu.Unlock()
			for _, r := range rec.reqs {
				if strings.Contains(r.URI, "@") || strings.Contains(r.Header.Get(apitypes.HeaderCursor), "@") {
					t.Errorf("request %s %s carries a stamped cursor, header %q", r.Method, r.URI, r.Header.Get(apitypes.HeaderCursor))
				}
			}
		})
	}
}

func TestRotatedCursorsMoveEveryKeyOfTheOldToken(t *testing.T) {
	t.Parallel()
	old, next := oldSecret, newSecret
	oldK, newK := cursorKey(old), cursorKey(next)
	other := "cursor_lvk_a_ZZZZZZ"
	tests := []struct {
		name   string
		config map[string]string
		label  string
		cursor string
		want   map[string]string
	}{
		{
			name: "a lagging chat keeps the agent cursor and its siblings' positions",
			config: map[string]string{
				oldK: "9000", oldK + "_lagging": stampedAgo("5000", time.Hour), oldK + "_sibling": stampedAgo("8000", time.Hour), other: "77",
			},
			label:  "lagging",
			cursor: "5000",
			want: map[string]string{
				"token": next, oldK: "", oldK + "_lagging": "", oldK + "_sibling": "",
				newK: "9000", newK + "_lagging": "5000@" + stampNow, newK + "_sibling": stampedAgo("8000", time.Hour),
			},
		},
		{
			name:   "a chat ahead of the agent cursor moves it forward",
			config: map[string]string{oldK: "9000", oldK + "_ahead": stampedAgo("9000", time.Hour)},
			label:  "ahead",
			cursor: "9500",
			want:   map[string]string{"token": next, oldK: "", oldK + "_ahead": "", newK: "9500", newK + "_ahead": "9500@" + stampNow},
		},
		{
			name:   "without a session label only the agent cursor moves",
			config: map[string]string{oldK: "9000"},
			cursor: "9200",
			want:   map[string]string{"token": next, oldK: "", newK: "9200"},
		},
		{
			name:   "a chat that never read keeps the stored agent cursor",
			config: map[string]string{oldK: "9000"},
			label:  "fresh",
			want:   map[string]string{"token": next, oldK: "", newK: "9000", newK + "_fresh": "@" + stampNow},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := rotatedCursors(tt.config, old, next, tt.label, tt.cursor, fixedNow)
			if !maps.Equal(got, tt.want) {
				t.Errorf("rotatedCursors =\n%v\nwant\n%v", got, tt.want)
			}
		})
	}
}

func TestConcurrentConfigWritersKeepEveryKey(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	getenv := func(k string) string {
		if k == "LORVIK_CONFIG" {
			return path
		}
		return ""
	}
	const writers = 24
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			errs <- UpdateUserConfig(getenv, map[string]string{"cursor_tok_chat" + strconv.Itoa(i): strconv.Itoa(i)})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	m, err := readTOMLFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range writers {
		if k := "cursor_tok_chat" + strconv.Itoa(i); m[k] != strconv.Itoa(i) {
			t.Errorf("%s = %q, want %d: a concurrent write was lost", k, m[k], i)
		}
	}
}

// When another chat renewed the config file's token while this one still held
// the old one, the cursor follows the file's token: no key of the old token
// is written back, where nothing would ever read or prune it.
func TestCursorFollowsATokenAnotherChatRenewed(t *testing.T) {
	t.Parallel()
	h := newHarness(t, serve(t, inboxRoutes(&recorder{}, fixtureInbox())))
	renewed := "lvk_a_renewed-by-another-chat"
	h.setConfig(t, map[string]string{"token": renewed, cursorKey(renewed): eventPos(50)})
	if err := h.run(t, "inbox", "--from", "oldest"); err != nil {
		t.Fatal(err)
	}
	m := h.configMap(t)
	if got := m[cursorKey(renewed)]; got != eventPos(9103) {
		t.Errorf("renewed token's cursor = %q, want %q", got, eventPos(9103))
	}
	for k := range m {
		if strings.HasPrefix(k, cursorKey("tok")) {
			t.Errorf("old token's key %s written back", k)
		}
	}
}
