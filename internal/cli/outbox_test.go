package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

const queueToken = "tok-4f9c1e7a-secret"

func closedURL(t *testing.T) string {
	t.Helper()
	dead := httptest.NewServer(http.NotFoundHandler())
	u := dead.URL
	dead.Close()
	return u
}

// fakeAPI is a server whose chosen requests can be made to fail, and later to
// work, at the same address.
type fakeAPI struct {
	srv  *httptest.Server
	rec  *recorder
	mu   sync.Mutex
	fail func(*http.Request) (int, bool)
}

func queueCard() apitypes.Card {
	return apitypes.Card{
		ID: "5001", Number: 12, Project: "demo", Kind: "task", Status: "working", Title: "Fix login", Priority: "normal", Version: 3,
		Claim: &apitypes.Claim{Mine: true, Owned: true, ExpiresAt: fixedNow.Add(30 * time.Minute)},
	}
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{rec: &recorder{}}
	gate := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			fail := f.fail
			f.mu.Unlock()
			if fail != nil {
				if status, ok := fail(r); ok {
					problem(status, apitypes.Problem{Title: "Problem", Detail: "server said no"})(w, r)
					return
				}
			}
			next(w, r)
		}
	}
	f.srv = serve(t, f.rec.wrapAll(map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/{n}":             gate(reply(200, queueCard())),
		"POST /api/v1/projects/demo/cards/{n}/transition": gate(reply(200, queueCard())),
		"POST /api/v1/projects/demo/cards/{n}/comments":   gate(reply(201, apitypes.Comment{ID: "9", Card: 12, Body: "x"})),
		"POST /api/v1/projects/demo/cards":                gate(reply(201, apitypes.Card{Number: 45, Project: "demo", Kind: "task", Status: "backlog", Title: "Fix login", Priority: "normal"})),
		"GET /api/v1/actors":                              reply(200, apitypes.Actors{}),
	}))
	return f
}

func (f *fakeAPI) failWhen(status int, match func(*http.Request) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = func(r *http.Request) (int, bool) { return status, match(r) }
}

func (f *fakeAPI) heal() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = nil
}

func everything(*http.Request) bool { return true }

func isWrite(r *http.Request) bool { return r.Method == http.MethodPost }

func isTransition(r *http.Request) bool { return strings.HasSuffix(r.URL.Path, "/transition") }

// newQueueHarness is a harness signed in with queueToken against url, whose
// chat has a name to send.
func newQueueHarness(t *testing.T, serverURL string) *harness {
	t.Helper()
	h := newHarness(t, nil)
	h.env["LORVIK_PROJECT"] = "demo"
	h.env["LORVIK_SESSION_NAME"] = "Chat A"
	if err := os.MkdirAll(filepath.Join(h.xdg, "lorvik"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "token = \"" + queueToken + "\"\nsession_names = \"on\"\n"
	if err := os.WriteFile(filepath.Join(h.xdg, "lorvik", "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	h.f.Client = func() (*client.Client, error) { return client.New(serverURL, queueToken) }
	return h
}

func (h *harness) owner() string { return "config:" + filepath.Join(h.xdg, "lorvik", "config.toml") }

func (h *harness) runCtx(ctx context.Context, t *testing.T, args ...string) error {
	t.Helper()
	root := NewRoot(h.f)
	root.SetArgs(args)
	return root.ExecuteContext(ctx)
}

func (h *harness) queuePath(t *testing.T, serverURL string) string {
	t.Helper()
	p, err := outboxPath(func(k string) string { return h.env[k] }, serverURL, "demo")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (h *harness) queue(t *testing.T, serverURL string) []outboxEntry {
	t.Helper()
	es, err := outbox{path: h.queuePath(t, serverURL)}.read()
	if err != nil {
		t.Fatal(err)
	}
	return es
}

// seed writes entries into the queue of serverURL, filling what a queued
// command would have: this chat, this user, the server and the project.
func (h *harness) seed(t *testing.T, serverURL string, es ...outboxEntry) {
	t.Helper()
	for i := range es {
		e := &es[i]
		e.At = fixedNow
		if e.Server == "" {
			e.Server = serverURL
		}
		if e.Project == "" {
			e.Project = "demo"
		}
		if e.Owner == "" {
			e.Owner = h.owner()
		}
		if e.Session == "" {
			e.Session = "test-session"
		}
	}
	err := outbox{path: h.queuePath(t, serverURL)}.edit(func(cur []outboxEntry) ([]outboxEntry, bool, error) {
		return append(cur, es...), true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func commentEntry(id string, n int64) outboxEntry {
	return outboxEntry{ID: id, Command: cmdComment, Card: n, Text: "note " + id}
}

func doneEntry(id string, n int64) outboxEntry {
	return outboxEntry{ID: id, Command: cmdDone, Card: n, Note: "finished"}
}

func addEntry(id, title string) outboxEntry {
	return outboxEntry{ID: id, Command: cmdAdd, Title: title}
}

var queuedCommands = []struct {
	name string
	args []string
	what string
	want func(t *testing.T, e outboxEntry)
}{
	{"comment", []string{"comment", "12", "-m", "Parser done"}, "comment on #12", func(t *testing.T, e outboxEntry) {
		if e.Card != 12 || e.Text != "Parser done" {
			t.Errorf("comment inputs = card %d text %q", e.Card, e.Text)
		}
	}},
	{"add", []string{"add", "Fix login", "-b", "Loops on expiry", "--priority", "high", "--ready", "--lane", "web", "--parent", "3"}, `add "Fix login"`, func(t *testing.T, e outboxEntry) {
		if e.Title != "Fix login" || e.Body != "Loops on expiry" || e.Priority != "high" || !e.Ready || e.Lane != "web" || e.Parent == nil || *e.Parent != 3 {
			t.Errorf("add inputs = %+v", e.cardInput)
		}
	}},
	{"done", []string{"done", "12", "-m", "merged", "--into", "docs/x.md"}, "done #12", func(t *testing.T, e outboxEntry) {
		if e.Card != 12 || e.Note != "merged" || e.Into != "docs/x.md" || e.NoContractChange {
			t.Errorf("done inputs = card %d note %q into %q", e.Card, e.Note, e.Into)
		}
	}},
}

func TestWriteIsQueuedWhenTheServerIsUnreachable(t *testing.T) {
	t.Parallel()
	outages := []struct {
		name string
		url  func(t *testing.T) (string, *fakeAPI)
	}{
		{"connection refused", func(t *testing.T) (string, *fakeAPI) { return closedURL(t), nil }},
		{"503", func(t *testing.T) (string, *fakeAPI) {
			f := newFakeAPI(t)
			f.failWhen(503, everything)
			return f.srv.URL, f
		}},
		{"504 on the write only", func(t *testing.T) (string, *fakeAPI) {
			f := newFakeAPI(t)
			f.failWhen(504, isWrite)
			return f.srv.URL, f
		}},
	}
	for _, o := range outages {
		for _, c := range queuedCommands {
			t.Run(o.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				u, f := o.url(t)
				h := newQueueHarness(t, u)
				if err := h.run(t, c.args...); err != nil {
					t.Fatalf("%v: want exit 0, got %v", c.args, err)
				}
				es := h.queue(t, u)
				if len(es) != 1 {
					t.Fatalf("queue holds %d entries, want 1", len(es))
				}
				e := es[0]
				if e.Command != c.name || e.Server != u || e.Project != "demo" || e.Owner != h.owner() || e.Session != "test-session" || !e.At.Equal(fixedNow) || e.Refused != nil {
					t.Errorf("entry = %+v", e)
				}
				if len(e.ID) != 26 {
					t.Errorf("entry id %q is not a request key", e.ID)
				}
				c.want(t, e)
				if want := fmt.Sprintf("queued  %s  id %s\n", c.what, e.ID); h.out.String() != want {
					t.Errorf("stdout = %q, want %q", h.out, want)
				}
				if want := fmt.Sprintf("lorvik: cannot reach %s: queued; lorvik sync sends it\n", u); h.errOut.String() != want {
					t.Errorf("stderr = %q, want %q", h.errOut, want)
				}
				if f != nil {
					for _, q := range f.rec.all() {
						if q.Method == http.MethodPost && q.Header.Get(apitypes.HeaderIdempotencyKey) != e.ID {
							t.Errorf("%s %s sent key %q, entry id is %q", q.Method, q.URI, q.Header.Get(apitypes.HeaderIdempotencyKey), e.ID)
						}
					}
				}
			})
		}
	}
}

func TestQueuedCommandPrintsOneJSONObject(t *testing.T) {
	t.Parallel()
	for _, c := range queuedCommands {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			u := closedURL(t)
			h := newQueueHarness(t, u)
			if err := h.run(t, append(c.args, "--json")...); err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
				t.Fatalf("stdout is not one JSON object: %v\n%s", err, h.out)
			}
			id := h.queue(t, u)[0].ID
			want := map[string]any{"queued": true, "id": id, "command": c.name}
			if c.name == "add" {
				want["title"] = "Fix login"
			} else {
				want["card"] = float64(12)
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("json = %v, want %v", got, want)
			}
		})
	}
}

func TestWriteIsNotQueuedWhenTheFailureIsNotUnreachable(t *testing.T) {
	t.Parallel()
	failures := []struct {
		name   string
		status int
		ctx    func(t *testing.T) context.Context
		closed bool
		exit   int
	}{
		{"409", 409, func(t *testing.T) context.Context { return t.Context() }, false, cmdutil.ExitConflict},
		{"500", 500, func(t *testing.T) context.Context { return t.Context() }, false, cmdutil.ExitGeneric},
		{"cancelled before the first attempt", 0, func(t *testing.T) context.Context {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			return ctx
		}, true, cmdutil.ExitGeneric},
	}
	for _, fl := range failures {
		for _, c := range queuedCommands {
			t.Run(fl.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				var u string
				if fl.closed {
					u = closedURL(t)
				} else {
					f := newFakeAPI(t)
					f.failWhen(fl.status, everything)
					u = f.srv.URL
				}
				h := newQueueHarness(t, u)
				err := h.runCtx(fl.ctx(t), t, c.args...)
				if err == nil {
					t.Fatal("want the error as before, got success")
				}
				if got := cmdutil.ExitCode(err); got != fl.exit {
					t.Errorf("exit code = %d, want %d (%v)", got, fl.exit, err)
				}
				if _, statErr := os.Stat(h.queuePath(t, u)); !errors.Is(statErr, os.ErrNotExist) {
					t.Errorf("queue file exists after %v: %v", err, statErr)
				}
				if strings.Contains(h.out.String(), "queued") || strings.Contains(h.errOut.String(), "queued") {
					t.Errorf("output mentions queueing: %q %q", h.out, h.errOut)
				}
			})
		}
	}
}

func TestReadingCommentsIsNeverQueued(t *testing.T) {
	t.Parallel()
	u := closedURL(t)
	h := newQueueHarness(t, u)
	if err := h.run(t, "comment", "12"); err == nil {
		t.Fatal("listing comments on an unreachable server succeeded")
	}
	if _, err := os.Stat(h.queuePath(t, u)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("queue file exists: %v", err)
	}
}

func TestUnreachableTokenStepStillQueues(t *testing.T) {
	t.Parallel()
	h := newQueueHarness(t, "")
	h.env["LORVIK_API_URL"] = "http://Queue.Example:8080/"
	h.f.Client = func() (*client.Client, error) {
		return nil, &client.ConnError{URL: "http://Queue.Example:8080", Err: errors.New("refused")}
	}
	if err := h.run(t, "comment", "12", "-m", "later"); err != nil {
		t.Fatal(err)
	}
	es := h.queue(t, "http://Queue.Example:8080")
	if len(es) != 1 || es[0].Server != "http://Queue.Example:8080" || es[0].Owner != h.owner() {
		t.Fatalf("queue = %+v", es)
	}
	if _, err := os.Stat(filepath.Join(h.env["XDG_STATE_HOME"], "lorvik", "outbox", "queue.example_8080", "demo.jsonl")); err != nil {
		t.Error(err)
	}
}

func TestQueuedStdinAndPageFileAreReadAtQueueTime(t *testing.T) {
	t.Parallel()
	u := closedURL(t)
	h := newQueueHarness(t, u)
	page := filepath.Join(t.TempDir(), "p.html")
	if err := os.WriteFile(page, []byte("<p>hi</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.streams.In = strings.NewReader("from stdin\n")
	if err := h.run(t, "comment", "12", "-m", "-"); err != nil {
		t.Fatal(err)
	}
	if err := h.run(t, "add", "Doc", "--page-file", page); err != nil {
		t.Fatal(err)
	}
	es := h.queue(t, u)
	if len(es) != 2 || es[0].Text != "from stdin" || es[1].Page != "<p>hi</p>" {
		t.Fatalf("queue = %+v", es)
	}
}

func TestQueueFileModesAndNoToken(t *testing.T) {
	t.Parallel()
	u := closedURL(t)
	h := newQueueHarness(t, u)
	h.env["LORVIK_TOKEN"] = queueToken
	if err := h.run(t, "comment", "12", "-m", "secret-free"); err != nil {
		t.Fatal(err)
	}
	path := h.queuePath(t, u)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{queueToken, queueToken[:8], "Authorization", "Bearer"} {
		if bytes.Contains(raw, []byte(leak)) {
			t.Errorf("queue file contains %q:\n%s", leak, raw)
		}
	}
	if es := h.queue(t, u); es[0].Owner != envOwner(t, h) || !strings.HasPrefix(es[0].Owner, "env:") {
		t.Errorf("owner = %q, want the env owner %q", es[0].Owner, envOwner(t, h))
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
	for dir := filepath.Dir(path); dir != filepath.Clean(h.env["XDG_STATE_HOME"]); dir = filepath.Dir(dir) {
		if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %v, %v; want 0700", dir, fi.Mode().Perm(), err)
		}
	}
}

func TestOutboxPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     map[string]string
		server  string
		project string
		want    string
		wantErr bool
	}{
		{"XDG_STATE_HOME wins", map[string]string{"XDG_STATE_HOME": "/s", "HOME": "/h"}, "http://localhost:8080", "demo", "/s/lorvik/outbox/localhost_8080/demo.jsonl", false},
		{"HOME fallback", map[string]string{"HOME": "/h"}, "http://localhost:8080", "demo", "/h/.local/state/lorvik/outbox/localhost_8080/demo.jsonl", false},
		{"host lowercased, port kept, odd characters replaced", map[string]string{"HOME": "/h"}, "https://API.Example.com:8443", "demo", "/h/.local/state/lorvik/outbox/api.example.com_8443/demo.jsonl", false},
		{"ipv6 host", map[string]string{"HOME": "/h"}, "http://[::1]:80", "demo", "/h/.local/state/lorvik/outbox/___1__80/demo.jsonl", false},
		{"slug with a slash", map[string]string{"HOME": "/h"}, "http://x", "a/b", "", true},
		{"slug that climbs", map[string]string{"HOME": "/h"}, "http://x", "..", "", true},
		{"host that is a dot", map[string]string{"HOME": "/h"}, "http://./", "demo", "", true},
		{"host that climbs", map[string]string{"HOME": "/h"}, "http://../", "demo", "", true},
		{"empty slug", map[string]string{"HOME": "/h"}, "http://x", "", "", true},
		{"slug with a backslash", map[string]string{"HOME": "/h"}, "http://x", `a\b`, "", true},
		{"url without a host", map[string]string{"HOME": "/h"}, "localhost", "demo", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := outboxPath(func(k string) string { return tt.env[k] }, tt.server, tt.project)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUnsafeProjectWritesNothing(t *testing.T) {
	t.Parallel()
	u := closedURL(t)
	h := newQueueHarness(t, u)
	h.env["LORVIK_PROJECT"] = "../evil"
	err := h.run(t, "comment", "12", "-m", "x")
	if err == nil {
		t.Fatal("want the command to fail")
	}
	if _, ok := errors.AsType[*client.ConnError](err); !ok {
		t.Errorf("error = %v, want the connection error it would have been", err)
	}
	if entries, _ := os.ReadDir(h.env["XDG_STATE_HOME"]); len(entries) != 0 {
		t.Errorf("state dir holds %v", entries)
	}
}

func TestOutboxOwner(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(t *testing.T, h *harness)
		want  string
		ok    bool
	}{
		{"user config", func(*testing.T, *harness) {}, "", true},
		{"LORVIK_TOKEN", func(_ *testing.T, h *harness) { h.env["LORVIK_TOKEN"] = "x" }, "env:2d711642", true},
		{"harness agent", func(t *testing.T, h *harness) {
			h.env["CLAUDE_CODE_SESSION_ID"] = "abc"
			h.f.Hostname = func() (string, error) { return "box", nil }
			writeAgentFile(t, h, "api_url = \"http://localhost:8080\"\ntoken = \"agent-tok\"\nagent_id = \"777\"\n")
		}, "agent:777", true},
		{"harness agent not registered yet", func(t *testing.T, h *harness) {
			h.env["CLAUDE_CODE_SESSION_ID"] = "abc"
			h.f.Hostname = func() (string, error) { return "box", nil }
		}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newQueueHarness(t, "http://localhost:8080")
			tt.setup(t, h)
			a := &app{f: h.f}
			got, ok := a.outboxOwner()
			want := tt.want
			if tt.name == "user config" {
				want = h.owner()
			}
			if got != want || ok != tt.ok {
				t.Errorf("owner = %q, %v; want %q, %v", got, ok, want, tt.ok)
			}
		})
	}
}

func writeAgentFile(t *testing.T, h *harness, body string) {
	t.Helper()
	dir := filepath.Join(h.xdg, "lorvik", agentsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "api_url = \"http://localhost:8080\"\ntoken = \"login-tok\"\n"
	if err := os.WriteFile(filepath.Join(h.xdg, "lorvik", "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude-code@box.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDoneIsQueuedWhenOnlyTheTransitionFails(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	f.failWhen(503, isTransition)
	h := newQueueHarness(t, f.srv.URL)
	if err := h.run(t, "done", "12", "-m", "ok"); err != nil {
		t.Fatal(err)
	}
	if got := f.rec.calls(); len(got) != 2 || got[0] != "GET /api/v1/projects/demo/cards/12" || got[1] != "POST /api/v1/projects/demo/cards/12/transition" {
		t.Errorf("calls = %v, want the read, then the transition", got)
	}
	es := h.queue(t, f.srv.URL)
	if len(es) != 1 {
		t.Fatalf("queue = %+v", es)
	}

	f.heal()
	if err := h.run(t, "sync"); err != nil {
		t.Fatal(err)
	}
	first, replay := f.rec.all()[1], f.rec.last(t, "POST /api/v1/projects/demo/cards/12/transition")
	if k := replay.Header.Get(apitypes.HeaderIdempotencyKey); k != es[0].ID || k != first.Header.Get(apitypes.HeaderIdempotencyKey) {
		t.Errorf("replay key %q, first key %q, entry id %q", k, first.Header.Get(apitypes.HeaderIdempotencyKey), es[0].ID)
	}
	if s := replay.Header.Get(apitypes.HeaderSession); s != "test-session" {
		t.Errorf("replay session = %q", s)
	}
	if !strings.Contains(replay.Body, `"note":"ok"`) || !strings.Contains(replay.Body, `"status":"done"`) {
		t.Errorf("replay body = %s", replay.Body)
	}
}

func TestSameKeyOnFirstAttemptAndReplay(t *testing.T) {
	t.Parallel()
	writes := map[string]string{
		"comment": "POST /api/v1/projects/demo/cards/12/comments",
		"add":     "POST /api/v1/projects/demo/cards",
		"done":    "POST /api/v1/projects/demo/cards/12/transition",
	}
	for _, c := range queuedCommands {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeAPI(t)
			f.failWhen(503, isWrite)
			h := newQueueHarness(t, f.srv.URL)
			if err := h.run(t, c.args...); err != nil {
				t.Fatal(err)
			}
			id := h.queue(t, f.srv.URL)[0].ID
			if k := f.rec.last(t, writes[c.name]).Header.Get(apitypes.HeaderIdempotencyKey); k != id {
				t.Fatalf("first attempt key %q, entry id %q", k, id)
			}
			f.heal()
			h.out.Reset()
			if err := h.run(t, "sync"); err != nil {
				t.Fatal(err)
			}
			if k := f.rec.last(t, writes[c.name]).Header.Get(apitypes.HeaderIdempotencyKey); k != id {
				t.Errorf("replay key %q, want %q", k, id)
			}
			if _, err := os.Stat(h.queuePath(t, f.srv.URL)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("queue file still there after a sent entry: %v", err)
			}
			wantLine := "sent     " + c.what + "\n"
			if c.name == "add" {
				wantLine = "sent     add \"Fix login\" as #45\n"
			}
			if h.out.String() != wantLine {
				t.Errorf("sync stdout = %q, want %q", h.out, wantLine)
			}
		})
	}
}

func TestSyncSendsInFileOrderAndRemovesSent(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	h := newQueueHarness(t, f.srv.URL)
	h.seed(t, f.srv.URL, commentEntry("A", 7), addEntry("B", "Second"), doneEntry("C", 12))
	if err := h.run(t, "sync"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, q := range f.rec.all() {
		if q.Method == http.MethodPost {
			got = append(got, q.URI+" "+q.Header.Get(apitypes.HeaderIdempotencyKey))
		}
	}
	want := []string{
		"/api/v1/projects/demo/cards/7/comments A",
		"/api/v1/projects/demo/cards B",
		"/api/v1/projects/demo/cards/12/transition C",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("writes = %v, want %v", got, want)
	}
	wantOut := "sent     comment on #7\nsent     add \"Second\" as #45\nsent     done #12\n"
	if h.out.String() != wantOut {
		t.Errorf("stdout = %q, want %q", h.out, wantOut)
	}
	if _, err := os.Stat(h.queuePath(t, f.srv.URL)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("emptied queue left a file: %v", err)
	}
}

func TestSyncMarksARefusedEntryAndGoesOn(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	f.failWhen(409, isTransition)
	h := newQueueHarness(t, f.srv.URL)
	h.seed(t, f.srv.URL, doneEntry("D", 12), commentEntry("E", 7))
	if err := h.run(t, "sync"); err != nil {
		t.Fatalf("a refused entry must not fail sync: %v", err)
	}
	wantOut := "refused  done #12: server said no\nsent     comment on #7\n"
	if h.out.String() != wantOut {
		t.Errorf("stdout = %q, want %q", h.out, wantOut)
	}
	es := h.queue(t, f.srv.URL)
	if len(es) != 1 || es[0].ID != "D" || es[0].Refused == nil || es[0].Refused.Status != 409 || es[0].Refused.Message != "server said no" || !es[0].Refused.At.Equal(fixedNow) {
		t.Fatalf("queue = %+v", es)
	}

	before := len(f.rec.all())
	f.heal()
	h.out.Reset()
	if err := h.run(t, "sync"); err != nil {
		t.Fatal(err)
	}
	if want := "kept     1 refused entry; lorvik sync --list shows them, --drop removes one\n"; h.out.String() != want {
		t.Errorf("second sync stdout = %q, want %q", h.out, want)
	}
	if len(f.rec.all()) != before {
		t.Errorf("sync sent a refused entry again: %v", f.rec.calls()[before:])
	}

	if err := h.run(t, "sync", "--drop", "D"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.queuePath(t, f.srv.URL)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("drop left a file: %v", err)
	}
}

func TestSyncStopsWhenTheServerCannotBeReachedAgain(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	h := newQueueHarness(t, f.srv.URL)
	f.failWhen(503, func(r *http.Request) bool {
		if !strings.Contains(r.URL.Path, "/cards/8/") {
			return false
		}
		if es, err := (outbox{path: h.queuePath(t, f.srv.URL)}).read(); err != nil || len(es) != 2 || es[0].ID != "B" {
			t.Errorf("while the second entry is sent the file holds %+v (%v); the first must be gone already", es, err)
		}
		return true
	})
	h.seed(t, f.srv.URL, commentEntry("A", 7), commentEntry("B", 8), commentEntry("C", 9))
	err := h.run(t, "sync")
	if err == nil || cmdutil.ExitCode(err) == 0 {
		t.Fatalf("sync = %v, want a failure", err)
	}
	if !unreachable(err) {
		t.Errorf("error = %v, want the unreachable error", err)
	}
	if h.out.String() != "sent     comment on #7\n" {
		t.Errorf("stdout = %q", h.out)
	}
	if !strings.Contains(h.errOut.String(), "queued comment on #8 (id B) was not sent") || !strings.Contains(h.errOut.String(), "it stays queued") || strings.Contains(h.errOut.String(), "--drop") {
		t.Errorf("stderr = %q, want the stop to name the entry and say it stays, without advising to drop it", h.errOut)
	}
	es := h.queue(t, f.srv.URL)
	if len(es) != 2 || es[0].ID != "B" || es[1].ID != "C" || es[0].Refused != nil {
		t.Errorf("queue = %+v, want B and C untouched", es)
	}
	for _, c := range f.rec.calls() {
		if strings.Contains(c, "/cards/9/") {
			t.Errorf("sync went on past the unreachable entry: %v", f.rec.calls())
		}
	}
}

func TestSyncOnAClosedServerKeepsEverything(t *testing.T) {
	t.Parallel()
	u := closedURL(t)
	h := newQueueHarness(t, u)
	h.seed(t, u, commentEntry("A", 7), doneEntry("B", 12))
	err := h.run(t, "sync")
	if _, ok := errors.AsType[*client.ConnError](err); !ok {
		t.Fatalf("sync = %v, want a connection error", err)
	}
	if es := h.queue(t, u); len(es) != 2 {
		t.Errorf("queue = %+v", es)
	}
}

func TestSyncStopsOnAnOtherErrorToo(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	f.failWhen(500, isWrite)
	h := newQueueHarness(t, f.srv.URL)
	h.seed(t, f.srv.URL, commentEntry("A", 7), commentEntry("B", 8))
	if err := h.run(t, "sync"); err == nil {
		t.Fatal("a 500 must fail sync")
	}
	if es := h.queue(t, f.srv.URL); len(es) != 2 || es[0].Refused != nil {
		t.Errorf("queue = %+v, want both kept and unmarked", es)
	}
}

func TestSyncLeavesOtherOwnersAndServersAlone(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	h := newQueueHarness(t, f.srv.URL)
	other := commentEntry("O", 8)
	other.Owner = "agent:99"
	elsewhere := commentEntry("S", 9)
	elsewhere.Server = "http://other.example:9"
	h.seed(t, f.srv.URL, commentEntry("A", 7), other, elsewhere)

	if err := h.run(t, "sync", "--json"); err != nil {
		t.Fatal(err)
	}
	var got syncView
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sent) != 1 || got.Sent[0].ID != "A" || got.Sent[0].Card != 7 || got.Sent[0].Command != "comment" || got.Others != 2 || got.Left != 0 || len(got.Refused) != 0 {
		t.Errorf("sync --json = %+v", got)
	}
	if es := h.queue(t, f.srv.URL); len(es) != 2 || es[0].ID != "O" || es[1].ID != "S" {
		t.Errorf("queue = %+v", es)
	}
	for _, c := range f.rec.calls() {
		if strings.Contains(c, "/cards/8/") || strings.Contains(c, "/cards/9/") {
			t.Errorf("sync touched another owner's or server's entry: %s", c)
		}
	}

	h.out.Reset()
	if err := h.run(t, "sync"); err != nil {
		t.Fatal(err)
	}
	if h.out.String() != "waiting  2 entries of other agents or people\n" {
		t.Errorf("stdout = %q", h.out)
	}
}

func TestSyncSaysWhenNothingIsQueued(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	h := newQueueHarness(t, f.srv.URL)
	if err := h.run(t, "sync"); err != nil {
		t.Fatal(err)
	}
	if h.out.String() != "nothing queued\n" {
		t.Errorf("stdout = %q", h.out)
	}
	if len(f.rec.all()) != 0 {
		t.Errorf("an empty queue cost requests: %v", f.rec.calls())
	}
}

func TestSyncSendsEachEntryUnderItsOwnSession(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	h := newQueueHarness(t, f.srv.URL)
	foreign := commentEntry("F", 8)
	foreign.Session = "other-chat"
	h.seed(t, f.srv.URL, commentEntry("A", 7), foreign)
	if err := h.run(t, "sync"); err != nil {
		t.Fatal(err)
	}
	own := f.rec.last(t, "POST /api/v1/projects/demo/cards/7/comments")
	if own.Header.Get(apitypes.HeaderSession) != "test-session" || own.Header.Get(apitypes.HeaderSessionName) == "" {
		t.Errorf("own entry headers: session %q name %q; the chat's name goes with its own entries",
			own.Header.Get(apitypes.HeaderSession), own.Header.Get(apitypes.HeaderSessionName))
	}
	other := f.rec.last(t, "POST /api/v1/projects/demo/cards/8/comments")
	if other.Header.Get(apitypes.HeaderSession) != "other-chat" {
		t.Errorf("other session's entry sent session %q", other.Header.Get(apitypes.HeaderSession))
	}
	if v, ok := other.Header[http.CanonicalHeaderKey(apitypes.HeaderSessionName)]; ok {
		t.Errorf("other session's entry carries the chat name %q: the server would rename that chat", v)
	}
	if other.Header.Get(apitypes.HeaderIdempotencyKey) != "F" {
		t.Errorf("other session's entry key = %q", other.Header.Get(apitypes.HeaderIdempotencyKey))
	}
}

func TestSyncList(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	h := newQueueHarness(t, f.srv.URL)
	refused := doneEntry("R", 12)
	refused.Refused = &outboxRefusal{At: fixedNow, Status: 409, Message: "held by someone else"}
	other := addEntry("O", "Theirs")
	other.Owner = "agent:99"
	other.Session = "chat-b"
	h.seed(t, f.srv.URL, commentEntry("A", 7), refused, other)
	if err := h.run(t, "sync", "--list"); err != nil {
		t.Fatal(err)
	}
	want := "A  now  test-session  comment on #7\n" +
		"R  now  test-session  done #12  refused: 409 held by someone else\n" +
		"O  now  chat-b  add \"Theirs\"  (other)\n"
	if h.out.String() != want {
		t.Errorf("stdout = %q, want %q", h.out, want)
	}
	if len(f.rec.all()) != 0 {
		t.Errorf("--list made requests: %v", f.rec.calls())
	}

	h.out.Reset()
	if err := h.run(t, "sync", "--list", "--json"); err != nil {
		t.Fatal(err)
	}
	var es []outboxEntry
	if err := json.Unmarshal(h.out.Bytes(), &es); err != nil || len(es) != 3 || es[1].Refused == nil || es[2].Owner != "agent:99" {
		t.Errorf("--list --json = %v, %s", err, h.out)
	}
}

func TestSyncDrop(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	h := newQueueHarness(t, f.srv.URL)
	other := commentEntry("O", 8)
	other.Owner = "agent:99"
	h.seed(t, f.srv.URL, commentEntry("A", 7), other)

	if err := h.run(t, "sync", "--drop", "O"); err != nil {
		t.Fatal(err)
	}
	if h.out.String() != "dropped  comment on #8\n" {
		t.Errorf("stdout = %q", h.out)
	}
	if es := h.queue(t, f.srv.URL); len(es) != 1 || es[0].ID != "A" {
		t.Errorf("queue = %+v", es)
	}

	err := h.run(t, "sync", "--drop", "nope")
	if got := cmdutil.ExitCode(err); err == nil || got != cmdutil.ExitNotFound {
		t.Errorf("unknown id: %v, exit %d, want exit %d", err, got, cmdutil.ExitNotFound)
	}
	if es := h.queue(t, f.srv.URL); len(es) != 1 {
		t.Errorf("a failed drop changed the queue: %+v", es)
	}

	h.out.Reset()
	if err := h.run(t, "sync", "--drop", "A", "--json"); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil || got["dropped"] != true || got["id"] != "A" || got["command"] != "comment" {
		t.Errorf("--drop --json = %v, %s", err, h.out)
	}

	if err := h.run(t, "sync", "--list", "--drop", "A"); cmdutil.ExitCode(err) != cmdutil.ExitUsage {
		t.Errorf("--list with --drop: %v, want a usage error", err)
	}
}

func TestOwnSessionEntriesAreSentBeforeTheCommand(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	h := newQueueHarness(t, f.srv.URL)
	foreign := commentEntry("F", 9)
	foreign.Session = "other-chat"
	notMine := commentEntry("N", 10)
	notMine.Owner = "agent:99"
	h.seed(t, f.srv.URL, commentEntry("A", 7), foreign, notMine)
	if err := h.run(t, "comment", "12", "-m", "now"); err != nil {
		t.Fatal(err)
	}
	calls := f.rec.calls()
	want := []string{"POST /api/v1/projects/demo/cards/7/comments", "POST /api/v1/projects/demo/cards/12/comments"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %v, want the queued entry first and nothing of the others", calls)
	}
	if want := "lorvik: sent queued comment on #7\n" + waitingNotice("1 waiting"); h.errOut.String() != want {
		t.Errorf("stderr = %q, want %q: the caller's other chat counted, the other owner's entry not", h.errOut, want)
	}
	if es := h.queue(t, f.srv.URL); len(es) != 2 || es[0].ID != "F" || es[1].ID != "N" {
		t.Errorf("queue = %+v, want the other session's and owner's entries kept", es)
	}
}

func waitingNotice(counts string) string {
	return "lorvik: queued writes: " + counts + "; lorvik sync sends what waits, lorvik sync --list shows them\n"
}

func TestACommandSaysWhatStillWaitsInTheQueue(t *testing.T) {
	t.Parallel()
	foreign := commentEntry("F", 9)
	foreign.Session = "other-chat"
	refused := commentEntry("R", 8)
	refused.Refused = &outboxRefusal{Status: 409, Message: "closed"}
	notMine := commentEntry("N", 10)
	notMine.Owner = "agent:99"
	elsewhere := commentEntry("E", 11)
	elsewhere.Project = "other"
	tests := []struct {
		name    string
		args    []string
		entries []outboxEntry
		want    string
	}{
		{"another chat's entry", []string{"show", "12"}, []outboxEntry{foreign}, waitingNotice("1 waiting")},
		{"a refusal from an earlier run", []string{"show", "12"}, []outboxEntry{refused}, waitingNotice("1 refused")},
		{"both", []string{"show", "12"}, []outboxEntry{foreign, refused, foreign}, waitingNotice("2 waiting, 1 refused")},
		{"only another owner's or project's entries", []string{"show", "12"}, []outboxEntry{notMine, elsewhere}, ""},
		{"sync prints its own account", []string{"sync", "--list"}, []outboxEntry{foreign, refused}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeAPI(t)
			h := newQueueHarness(t, f.srv.URL)
			h.seed(t, f.srv.URL, tt.entries...)
			if err := h.run(t, tt.args...); err != nil {
				t.Fatal(err)
			}
			if h.errOut.String() != tt.want {
				t.Errorf("stderr = %q, want %q", h.errOut, tt.want)
			}
			if es := h.queue(t, f.srv.URL); len(es) != len(tt.entries) {
				t.Errorf("queue = %+v, want it untouched", es)
			}
		})
	}
}

func TestAutomaticSendingReportsARefusal(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	f.failWhen(409, func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/cards/7/") })
	h := newQueueHarness(t, f.srv.URL)
	h.seed(t, f.srv.URL, commentEntry("A", 7))
	if err := h.run(t, "comment", "12", "-m", "now"); err != nil {
		t.Fatal(err)
	}
	if want := "lorvik: queued comment on #7 was refused: server said no; see lorvik sync --list\n"; h.errOut.String() != want {
		t.Errorf("stderr = %q, want %q", h.errOut, want)
	}
	if es := h.queue(t, f.srv.URL); len(es) != 1 || es[0].Refused == nil {
		t.Errorf("queue = %+v", es)
	}
}

func TestAutomaticSendingStopsQuietlyAndTheCommandQueuesBehind(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	f.failWhen(503, everything)
	h := newQueueHarness(t, f.srv.URL)
	h.seed(t, f.srv.URL, commentEntry("A", 7), commentEntry("B", 8))
	if err := h.run(t, "comment", "12", "-m", "now"); err != nil {
		t.Fatalf("the command is queued, not failed: %v", err)
	}
	if want := fmt.Sprintf("lorvik: cannot reach %s: queued; lorvik sync sends it\n", f.srv.URL); h.errOut.String() != want {
		t.Errorf("stderr = %q, want only the queued line %q", h.errOut, want)
	}
	if got := f.rec.calls(); len(got) != 1 || got[0] != "POST /api/v1/projects/demo/cards/7/comments" {
		t.Errorf("calls = %v, want one failed attempt and nothing for the new write", got)
	}
	es := h.queue(t, f.srv.URL)
	if len(es) != 3 || es[0].ID != "A" || es[1].ID != "B" || es[2].Card != 12 {
		t.Errorf("queue = %+v, want the older entries first and the new one last", es)
	}
}

func TestAutomaticSendingStopOnAnotherErrorLetsTheCommandRun(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	f.failWhen(500, func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/cards/7/") })
	h := newQueueHarness(t, f.srv.URL)
	h.seed(t, f.srv.URL, commentEntry("A", 7))
	if err := h.run(t, "comment", "12", "-m", "now"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.errOut.String(), "queued comment on #7 (id A) was not sent") || !strings.Contains(h.errOut.String(), "lorvik sync --drop A removes it") {
		t.Errorf("stderr = %q, want the stop to name the entry", h.errOut)
	}
	if got := f.rec.calls(); len(got) != 2 || got[1] != "POST /api/v1/projects/demo/cards/12/comments" {
		t.Errorf("calls = %v, want the command's own write to go on", got)
	}
}

func TestCancelledDuringAutomaticSendingDoesNotQueueTheCommand(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	ctx, cancel := context.WithCancel(t.Context())
	f.failWhen(503, func(*http.Request) bool { cancel(); return true })
	h := newQueueHarness(t, f.srv.URL)
	h.seed(t, f.srv.URL, commentEntry("A", 7))
	if err := h.runCtx(ctx, t, "comment", "12", "-m", "now"); err == nil {
		t.Fatal("an interrupted command must fail")
	}
	if es := h.queue(t, f.srv.URL); len(es) != 1 || es[0].ID != "A" {
		t.Errorf("queue = %+v, want only the old entry", es)
	}
}

func TestNoQueueFileMeansNoExtraRequest(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	h := newQueueHarness(t, f.srv.URL)
	if err := h.run(t, "comment", "12", "-m", "now"); err != nil {
		t.Fatal(err)
	}
	if got := f.rec.calls(); len(got) != 1 || got[0] != "POST /api/v1/projects/demo/cards/12/comments" {
		t.Errorf("calls = %v, want the command's own request only", got)
	}
	if _, err := os.Stat(filepath.Dir(h.queuePath(t, f.srv.URL))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a command that queued nothing created the queue directory: %v", err)
	}
}

func TestAutomaticSendingThenQueueingKeepsOrder(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	f.failWhen(503, isWrite)
	h := newQueueHarness(t, f.srv.URL)
	h.seed(t, f.srv.URL, commentEntry("A", 7))
	if err := h.run(t, "comment", "12", "-m", "later"); err != nil {
		t.Fatal(err)
	}
	es := h.queue(t, f.srv.URL)
	if len(es) != 2 || es[0].ID != "A" || es[1].Card != 12 {
		t.Errorf("queue = %+v, want the old entry, then the new", es)
	}
	if got := f.rec.calls(); len(got) != 1 {
		t.Errorf("calls = %v, want one failed attempt in all", got)
	}
}

func TestOutboxFileIsOneJSONObjectPerLine(t *testing.T) {
	t.Parallel()
	u := closedURL(t)
	h := newQueueHarness(t, u)
	for _, c := range queuedCommands {
		if err := h.run(t, c.args...); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(h.queuePath(t, u))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("file has %d lines:\n%s", len(lines), raw)
	}
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line is not JSON: %v: %s", err, l)
		}
		for _, k := range []string{"id", "at", "server", "project", "owner", "session", "command"} {
			if _, ok := m[k]; !ok {
				t.Errorf("entry lacks %q: %s", k, l)
			}
		}
		if _, ok := m["refused"]; ok {
			t.Errorf("a fresh entry is marked refused: %s", l)
		}
	}
}

func (r *recorder) all() []request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.reqs)
}

func TestEntryNamingAnotherProjectIsNeverSent(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	h := newQueueHarness(t, f.srv.URL)
	stray := commentEntry("X", 7)
	stray.Project = "other"
	h.seed(t, f.srv.URL, stray, commentEntry("A", 8))
	if err := h.run(t, "sync", "--json"); err != nil {
		t.Fatal(err)
	}
	var got syncView
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil || got.Others != 1 || len(got.Sent) != 1 {
		t.Errorf("sync --json = %v, %+v", err, got)
	}
	for _, c := range f.rec.calls() {
		if strings.Contains(c, "other") || strings.Contains(c, "/cards/7/") {
			t.Errorf("the stray entry reached the server: %s", c)
		}
	}
	h.seed(t, f.srv.URL)
	if err := h.run(t, "comment", "12", "-m", "now"); err != nil {
		t.Fatal(err)
	}
	if es := h.queue(t, f.srv.URL); len(es) != 1 || es[0].ID != "X" {
		t.Errorf("queue = %+v", es)
	}
}

func TestStaleVersionOnReplayIsRetriedOnceThenStops(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		conflicts int
		wantSent  bool
	}{{"conflicts once", 1, true}, {"conflicts again", 2, false}} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			left := tt.conflicts
			rec := &recorder{}
			srv := serve(t, rec.wrapAll(map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/12": reply(200, queueCard()),
				"POST /api/v1/projects/demo/cards/12/transition": func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					conflict := left > 0
					left--
					mu.Unlock()
					if conflict {
						problem(409, apitypes.Problem{Title: "Conflict", Detail: "card changed", CurrentVersion: 4})(w, r)
						return
					}
					reply(200, queueCard())(w, r)
				},
				"GET /api/v1/actors": reply(200, apitypes.Actors{}),
			}))
			h := newQueueHarness(t, srv.URL)
			h.seed(t, srv.URL, doneEntry("D", 12))
			err := h.run(t, "sync")
			es := h.queue(t, srv.URL)
			if tt.wantSent {
				if err != nil || len(es) != 0 {
					t.Fatalf("sync = %v, queue %+v; want the retry to send it", err, es)
				}
				return
			}
			if err == nil || len(es) != 1 || es[0].Refused != nil {
				t.Fatalf("sync = %v, queue %+v; want a stop with the entry unmarked", err, es)
			}
		})
	}
}

func TestEntryAppendedDuringASyncSurvives(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	h := newQueueHarness(t, f.srv.URL)
	late := commentEntry("L", 20)
	late.Owner = "agent:99"
	f.failWhen(0, func(r *http.Request) bool {
		if strings.Contains(r.URL.Path, "/cards/7/comments") {
			h.seed(t, f.srv.URL, late)
		}
		return false
	})
	h.seed(t, f.srv.URL, commentEntry("A", 7))
	if err := h.run(t, "sync"); err != nil {
		t.Fatal(err)
	}
	if es := h.queue(t, f.srv.URL); len(es) != 1 || es[0].ID != "L" {
		t.Errorf("queue = %+v, want the entry another process appended", es)
	}
}

func TestMalformedLineIsKeptAndNothingElseBreaks(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	h := newQueueHarness(t, f.srv.URL)
	h.seed(t, f.srv.URL, commentEntry("A", 7), commentEntry("B", 8))
	path := h.queuePath(t, f.srv.URL)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(raw), "\n")
	broken := "{this is not json\n"
	if err := os.WriteFile(path, []byte(lines[0]+broken+lines[1]), 0o600); err != nil {
		t.Fatal(err)
	}
	warn := fmt.Sprintf("lorvik: %s: line 2 is not a queue entry; fix or delete it\n", path)

	if err := h.run(t, "sync", "--list"); err != nil {
		t.Fatal(err)
	}
	if h.errOut.String() != warn || !strings.Contains(h.out.String(), "comment on #8") {
		t.Errorf("--list: stderr %q stdout %q", h.errOut, h.out)
	}

	h.errOut.Reset()
	if err := h.run(t, "comment", "12", "-m", "later"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.errOut.String(), "not a queue entry") {
		t.Errorf("the automatic send complained: %q", h.errOut)
	}
	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), broken) {
		t.Fatalf("the malformed line is gone:\n%s", after)
	}

	h.errOut.Reset()
	if err := h.run(t, "sync"); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("lorvik: %s: line 1 is not a queue entry; fix or delete it\n", path); h.errOut.String() != want {
		t.Errorf("sync stderr = %q, want %q (the entries before it were sent, so it moved up)", h.errOut, want)
	}
	left, _ := os.ReadFile(path)
	if string(left) != broken {
		t.Errorf("file = %q, want only the malformed line kept", left)
	}
	for _, c := range f.rec.calls() {
		if strings.Contains(c, "not json") {
			t.Errorf("a request was made for the broken line: %s", c)
		}
	}
	if err := h.run(t, "sync", "--drop", "nope"); cmdutil.ExitCode(err) != cmdutil.ExitNotFound {
		t.Errorf("--drop beside a malformed line: %v", err)
	}
}

func TestServerWithUserinfoKeepsThePasswordOutOfTheQueue(t *testing.T) {
	t.Parallel()
	plain := closedURL(t)
	withInfo := strings.Replace(plain, "http://", "http://ana:hunter2@", 1)
	h := newQueueHarness(t, withInfo)
	if err := h.run(t, "comment", "12", "-m", "x"); err != nil {
		t.Fatal(err)
	}
	if err := h.run(t, "sync", "--list"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(h.queuePath(t, plain))
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"file": string(raw), "stdout": h.out.String(), "stderr": h.errOut.String()} {
		if strings.Contains(text, "hunter2") || strings.Contains(text, "ana:") {
			t.Errorf("%s holds the userinfo: %s", name, text)
		}
	}
	if es := h.queue(t, plain); len(es) != 1 || es[0].Server != plain {
		t.Errorf("queue = %+v, want server %s", es, plain)
	}
}

func TestListAndDropCreateNothingWithoutAQueue(t *testing.T) {
	t.Parallel()
	u := closedURL(t)
	h := newQueueHarness(t, u)
	if err := h.run(t, "sync", "--list"); err != nil {
		t.Fatal(err)
	}
	if err := h.run(t, "sync", "--drop", "nope"); cmdutil.ExitCode(err) != cmdutil.ExitNotFound {
		t.Errorf("--drop: %v, want not found", err)
	}
	if entries, _ := os.ReadDir(h.env["XDG_STATE_HOME"]); len(entries) != 0 {
		t.Errorf("state dir holds %v", entries)
	}
}

func TestOtherCommandsAreNeverQueued(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"ask", "Which?"}, {"start", "12"}, {"claim", "12"}} {
		t.Run(args[0], func(t *testing.T) {
			t.Parallel()
			u := closedURL(t)
			h := newQueueHarness(t, u)
			if err := h.run(t, args...); err == nil {
				t.Fatal("want a failure")
			}
			if _, err := os.Stat(h.queuePath(t, u)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("queue file exists: %v", err)
			}
		})
	}
}

func TestSyncStopsWithoutMarkingOnCredentialOrPaceErrors(t *testing.T) {
	t.Parallel()
	for _, status := range []int{401, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			f := newFakeAPI(t)
			f.failWhen(status, isWrite)
			h := newQueueHarness(t, f.srv.URL)
			h.seed(t, f.srv.URL, commentEntry("A", 7), commentEntry("B", 8))
			if err := h.run(t, "sync"); err == nil {
				t.Fatal("want sync to stop with an error")
			}
			if es := h.queue(t, f.srv.URL); len(es) != 2 || es[0].Refused != nil {
				t.Errorf("queue = %+v, want both kept and unmarked", es)
			}
			if got := f.rec.calls(); len(got) != 1 {
				t.Errorf("calls = %v, want the sync to stop at the first", got)
			}
		})
	}
}

func TestSyncJSONKeepsRefusedAndLeftApart(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	f.failWhen(409, isTransition)
	h := newQueueHarness(t, f.srv.URL)
	earlier := doneEntry("R", 11)
	earlier.Refused = &outboxRefusal{At: fixedNow, Status: 409, Message: "old"}
	h.seed(t, f.srv.URL, earlier, doneEntry("D", 12))
	if err := h.run(t, "sync", "--json"); err != nil {
		t.Fatal(err)
	}
	var got syncView
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Refused) != 1 || got.Refused[0].ID != "D" || got.Left != 1 {
		t.Errorf("sync --json = %+v, want D refused now and only R left", got)
	}
}

// A portal or a proxy that answers with a redirect to somewhere else is not
// an outage: the write fails as it is, and nothing waits for a server that
// was never down.
func TestARefusedRedirectIsNotQueued(t *testing.T) {
	t.Parallel()
	elsewhere := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(elsewhere.Close)
	for _, c := range queuedCommands {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeAPI(t)
			portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
					return
				}
				f.srv.Config.Handler.ServeHTTP(w, r)
			}))
			t.Cleanup(portal.Close)
			h := newQueueHarness(t, portal.URL)
			err := h.run(t, c.args...)
			if err == nil || !strings.Contains(err.Error(), "refused a redirect") {
				t.Fatalf("%v = %v, want the refused redirect", c.args, err)
			}
			if _, statErr := os.Stat(h.queuePath(t, portal.URL)); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("queue file exists after a redirect: %v", statErr)
			}
			if strings.Contains(h.out.String(), "queued") || strings.Contains(h.errOut.String(), "queued") {
				t.Errorf("output mentions queueing: %q %q", h.out, h.errOut)
			}
		})
	}
}

func envOwner(t *testing.T, h *harness) string {
	t.Helper()
	owner, ok := (&app{f: h.f}).outboxOwner()
	if !ok {
		t.Fatal("no owner for LORVIK_TOKEN")
	}
	return owner
}

// LORVIK_TOKEN names nobody, so the token itself tells whose entry it is:
// what one token queued must not go out as whoever the next token is.
func TestEntriesQueuedUnderOneEnvTokenAreNotSentUnderAnother(t *testing.T) {
	t.Parallel()
	const ana, ben = "lvk_u_token-of-ana", "lvk_u_token-of-ben"
	f := newFakeAPI(t)
	h := newQueueHarness(t, f.srv.URL)
	h.env["LORVIK_TOKEN"] = ana
	f.failWhen(503, isWrite)
	if err := h.run(t, "comment", "12", "-m", "from ana"); err != nil {
		t.Fatal(err)
	}
	f.heal()
	sent := func() int {
		n := 0
		for _, c := range f.rec.calls() {
			if c == "POST /api/v1/projects/demo/cards/12/comments" {
				n++
			}
		}
		return n
	}
	before := sent()

	h.env["LORVIK_TOKEN"] = ben
	h.out.Reset()
	if err := h.run(t, "sync"); err != nil {
		t.Fatal(err)
	}
	if got := sent(); got != before {
		t.Errorf("another token sent the entry: %d comment requests, want %d", got, before)
	}
	if h.out.String() != "waiting  1 entry of other agents or people\n" {
		t.Errorf("stdout = %q", h.out)
	}
	raw, err := os.ReadFile(h.queuePath(t, f.srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("token-of-")) {
		t.Errorf("the queue file holds part of a token: %s", raw)
	}

	h.env["LORVIK_TOKEN"] = ana
	h.out.Reset()
	if err := h.run(t, "sync"); err != nil {
		t.Fatal(err)
	}
	if h.out.String() != "sent     comment on #12\n" {
		t.Errorf("stdout under the first token = %q", h.out)
	}
}

// doneAPI is a server for one card and the three requests a done makes,
// each answered by the test.
func doneAPI(t *testing.T, card apitypes.Card, transition http.HandlerFunc) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	held := card
	held.Status, held.Version = "working", card.Version+1
	held.Claim = &apitypes.Claim{Mine: true, Owned: true, ExpiresAt: fixedNow.Add(30 * time.Minute)}
	return serve(t, rec.wrapAll(map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/{n}":             reply(200, card),
		"POST /api/v1/projects/demo/cards/{n}/claim":      reply(200, held),
		"POST /api/v1/projects/demo/cards/{n}/transition": transition,
		"GET /api/v1/actors":                              reply(200, apitypes.Actors{}),
	})), rec
}

// The first send of a done may have closed the card before its reply was
// lost. When the card was reopened since, the entry must find that out by its
// key and never take the reopened card.
func TestAReplayedDoneAsksByItsKeyBeforeItClaims(t *testing.T) {
	t.Parallel()
	reopened := queueCard()
	reopened.Status, reopened.Claim = "ready", nil
	done := reopened
	done.Status = "done"
	const claim, transition = "POST /api/v1/projects/demo/cards/12/claim", "POST /api/v1/projects/demo/cards/12/transition"

	t.Run("the server knows the key", func(t *testing.T) {
		t.Parallel()
		srv, rec := doneAPI(t, reopened, reply(200, done))
		h := newQueueHarness(t, srv.URL)
		h.seed(t, srv.URL, doneEntry("K1", 12))
		if err := h.run(t, "sync"); err != nil {
			t.Fatal(err)
		}
		if got := rec.calls(); slices.Contains(got, claim) {
			t.Errorf("calls = %v: the reopened card was claimed", got)
		}
		if k := rec.last(t, transition).Header.Get(apitypes.HeaderIdempotencyKey); k != "K1" {
			t.Errorf("transition key = %q, want K1", k)
		}
		if es := h.queue(t, srv.URL); len(es) != 0 {
			t.Errorf("queue = %+v, want it empty", es)
		}
	})

	t.Run("the server has not seen the key", func(t *testing.T) {
		t.Parallel()
		var tries atomic.Int32
		srv, rec := doneAPI(t, reopened, func(w http.ResponseWriter, r *http.Request) {
			if tries.Add(1) == 1 {
				problem(409, apitypes.Problem{Title: "Conflict", Detail: "claim the card first"})(w, r)
				return
			}
			reply(200, done)(w, r)
		})
		h := newQueueHarness(t, srv.URL)
		h.seed(t, srv.URL, doneEntry("K2", 12))
		if err := h.run(t, "sync"); err != nil {
			t.Fatal(err)
		}
		want := []string{"GET /api/v1/projects/demo/cards/12", transition, claim, transition}
		if got := rec.calls(); !slices.Equal(got, want) {
			t.Errorf("calls = %v, want %v", got, want)
		}
		for _, q := range rec.all() {
			if q.Method+" "+q.URI == transition && q.Header.Get(apitypes.HeaderIdempotencyKey) != "K2" {
				t.Errorf("a transition went out under key %q, want K2", q.Header.Get(apitypes.HeaderIdempotencyKey))
			}
		}
		if h.out.String() != "sent     done #12\n" {
			t.Errorf("stdout = %q", h.out)
		}
	})

	t.Run("a done run online claims first as before", func(t *testing.T) {
		t.Parallel()
		srv, rec := doneAPI(t, reopened, reply(200, done))
		h := newQueueHarness(t, srv.URL)
		if err := h.run(t, "done", "12", "-m", "ok"); err != nil {
			t.Fatal(err)
		}
		want := []string{"GET /api/v1/projects/demo/cards/12", claim, transition}
		if got := rec.calls(); !slices.Equal(got, want) {
			t.Errorf("calls = %v, want %v", got, want)
		}
	})
}

// A CLI that does not know a field a newer one wrote must not strip it from
// the entries it only passes over.
func TestAnEntryThatDidNotChangeIsWrittenBackAsItWas(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t)
	h := newQueueHarness(t, f.srv.URL)
	path := h.queuePath(t, f.srv.URL)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	line := func(id, owner string, card int) string {
		return fmt.Sprintf(`{"id":%q,"at":"2026-01-01T00:00:00Z","server":%q,"project":"demo","owner":%q,"session":"test-session","command":"comment","card":%d,"text":"x","from_a_newer_cli":{"keep":true}}`,
			id, f.srv.URL, owner, card)
	}
	mine, theirs, refusedLater := line("A", h.owner(), 7), line("B", "agent:99", 8), line("C", h.owner(), 9)
	if err := os.WriteFile(path, []byte(mine+"\n"+theirs+"\n"+refusedLater+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.failWhen(422, func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/cards/9/") })
	if err := h.run(t, "sync"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 2 || lines[0] != theirs {
		t.Fatalf("queue file = %q, want the other owner's line untouched, then the refused entry", raw)
	}
	if !strings.Contains(lines[1], `"id":"C"`) || !strings.Contains(lines[1], `"refused":{`) {
		t.Errorf("second line = %s, want entry C marked refused", lines[1])
	}
}

// A write that was killed leaves its temporary copy of the queue, comment
// text included, beside the queue. The next write removes it.
func TestALeftoverTemporaryCopyOfTheQueueIsRemoved(t *testing.T) {
	t.Parallel()
	u := closedURL(t)
	h := newQueueHarness(t, u)
	dir := filepath.Dir(h.queuePath(t, u))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, ".demo.jsonl-4071988302")
	kept := []string{filepath.Join(dir, ".demo.jsonl-notes"), filepath.Join(dir, ".other.jsonl-4071988302"), filepath.Join(dir, "demo.jsonl-1")}
	for _, p := range append([]string{stale}, kept...) {
		if err := os.WriteFile(p, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.run(t, "comment", "12", "-m", "queued"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the leftover copy is still there: %v", err)
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s is not a leftover of this queue and was removed: %v", filepath.Base(p), err)
		}
	}
}

// Two commands that send the same queue at once pick the same entry before
// either settles it. Both send it under its own id, which is what lets the
// server apply it once, and neither fails or damages the file.
func TestTwoSendsOfTheSameQueueAtOnce(t *testing.T) {
	t.Parallel()
	const token = "lvk_u_shared-by-both-runs"
	f := newFakeAPI(t)
	first, second := newQueueHarness(t, f.srv.URL), newQueueHarness(t, f.srv.URL)
	second.env["XDG_STATE_HOME"] = first.env["XDG_STATE_HOME"]
	first.env["LORVIK_TOKEN"], second.env["LORVIK_TOKEN"] = token, token
	var seeded []outboxEntry
	for i := range 6 {
		e := commentEntry(fmt.Sprintf("E%d", i), int64(20+i))
		e.Owner = envOwner(t, first)
		seeded = append(seeded, e)
	}
	first.seed(t, f.srv.URL, seeded...)

	errs := make(chan error, 2)
	for _, h := range []*harness{first, second} {
		go func() { errs <- h.runCtx(context.WithoutCancel(t.Context()), t, "sync") }()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Errorf("sync = %v", err)
		}
	}
	if _, err := os.Stat(first.queuePath(t, f.srv.URL)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("queue file still there: %v", err)
	}
	sentUnder := map[string]string{}
	for _, q := range f.rec.all() {
		if q.Method != http.MethodPost {
			continue
		}
		key := q.Header.Get(apitypes.HeaderIdempotencyKey)
		if prev, ok := sentUnder[q.URI]; ok && prev != key {
			t.Errorf("%s went out under keys %q and %q", q.URI, prev, key)
		}
		sentUnder[q.URI] = key
	}
	for i, e := range seeded {
		uri := fmt.Sprintf("/api/v1/projects/demo/cards/%d/comments", 20+i)
		if sentUnder[uri] != e.ID {
			t.Errorf("%s was sent under key %q, want %q", uri, sentUnder[uri], e.ID)
		}
	}
}
