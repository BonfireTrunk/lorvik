package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

func wakeEvent(id string, number int64) apitypes.Event {
	n, _ := strconv.ParseInt(id, 10, 64)
	return apitypes.Event{ID: id, Position: eventPos(n), Type: "card.comment", Actor: ana, Subject: apitypes.Subject{Type: "card", Number: number}, CreatedAt: ago(time.Minute), Wake: true}
}

func plainEvent(id string, number int64) apitypes.Event {
	e := wakeEvent(id, number)
	e.Wake = false
	return e
}

func TestFollowOncePrintsOnlyWakeEventsAndLeavesTheStoredCursorAlone(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	in := apitypes.Inbox{Cursor: "9", Events: []apitypes.Event{plainEvent("7", 7), wakeEvent("8", 42), plainEvent("9", 9)}}
	h := newHarness(t, serve(t, rec.wrapAll(map[string]http.HandlerFunc{
		"GET /api/v1/inbox":  reply(200, in),
		"GET /api/v1/actors": reply(200, fixtureActors()),
	})))
	if err := writeCursor(h, "5"); err != nil {
		t.Fatal(err)
	}
	before := h.config(t)

	if err := h.run(t, "inbox", "--follow", "--once"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(h.out.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "commented on #42") {
		t.Errorf("stdout = %q, want one line, for the wake event on #42 only", h.out.String())
	}
	if after := h.config(t); after != before {
		t.Errorf("config changed from %q to %q, want the stored cursor untouched", before, after)
	}
	if got := rec.last(t, "GET /api/v1/inbox").URI; !strings.Contains(got, "cursor=5") {
		t.Errorf("inbox request %q, want it to start at the stored cursor 5", got)
	}
}

func TestFollowJSONPrintsOneCompactEventPerLine(t *testing.T) {
	t.Parallel()
	in := apitypes.Inbox{Cursor: "9", Events: []apitypes.Event{wakeEvent("7", 7), plainEvent("8", 8), wakeEvent("9", 9)}}
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{"GET /api/v1/inbox": reply(200, in)}))
	if err := writeCursor(h, "5"); err != nil {
		t.Fatal(err)
	}
	if err := h.run(t, "--json", "inbox", "--follow", "--once"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(h.out.String()), "\n")
	var ids []string
	for _, l := range lines {
		var e apitypes.Event
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("line %q is not one JSON object: %v", l, err)
		}
		ids = append(ids, e.ID)
	}
	if strings.Join(ids, ",") != "7,9" {
		t.Errorf("event ids = %v, want 7,9 (wake events only, one per line)", ids)
	}
}

func TestFollowUsageErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{"from", []string{"inbox", "--follow", "--from", "now"}},
		{"once alone", []string{"inbox", "--once"}},
		{"exit-on-eof alone", []string{"inbox", "--exit-on-eof"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := newHarness(t, serve(t, nil)).run(t, tt.args...)
			if cmdutil.ExitCode(err) != 2 {
				t.Errorf("err = %v (exit %d), want a usage error (exit 2)", err, cmdutil.ExitCode(err))
			}
		})
	}
}

func TestInboxNamesTheProjectTheChatWorksIn(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		bound       string
		args        []string
		wantChat    string
		wantProject string
	}{
		{"follow, project from the environment", "demo", []string{"inbox", "--follow", "--once"}, "demo", ""},
		{"follow, project from the flag", "", []string{"-p", "demo", "inbox", "--follow", "--once"}, "demo", ""},
		{"follow, no project bound", "", []string{"inbox", "--follow", "--once"}, "", ""},
		{"plain inbox", "demo", []string{"inbox"}, "demo", ""},
		{"plain inbox narrowed to a project", "demo", []string{"-p", "other", "inbox"}, "other", "other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{}
			in := apitypes.Inbox{Cursor: "9", Events: []apitypes.Event{wakeEvent("8", 42)}}
			h := newHarness(t, serve(t, rec.wrapAll(map[string]http.HandlerFunc{
				"GET /api/v1/inbox":  reply(200, in),
				"GET /api/v1/actors": reply(200, fixtureActors()),
			})))
			if tt.bound != "" {
				h.env["LORVIK_PROJECT"] = tt.bound
			}
			if err := writeCursor(h, "5"); err != nil {
				t.Fatal(err)
			}
			if err := h.run(t, tt.args...); err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(rec.last(t, "GET /api/v1/inbox").URI)
			if err != nil {
				t.Fatal(err)
			}
			q := u.Query()
			if got := q.Get("chat_project"); got != tt.wantChat {
				t.Errorf("chat_project = %q, want %q", got, tt.wantChat)
			}
			if got := q.Get("project"); got != tt.wantProject {
				t.Errorf("project = %q, want %q: the chat's project must not narrow the listing", got, tt.wantProject)
			}
		})
	}
}

func TestOnlyTheListenerMarksItsRequests(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		args []string
		want bool
	}{
		{"follow", []string{"inbox", "--follow", "--once"}, true},
		{"plain inbox", []string{"inbox"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{}
			in := apitypes.Inbox{Cursor: "9", Events: []apitypes.Event{wakeEvent("8", 42)}}
			h := newHarness(t, serve(t, rec.wrapAll(map[string]http.HandlerFunc{
				"GET /api/v1/inbox":  reply(200, in),
				"GET /api/v1/actors": reply(200, fixtureActors()),
			})))
			if err := writeCursor(h, "5"); err != nil {
				t.Fatal(err)
			}
			if err := h.run(t, tt.args...); err != nil {
				t.Fatal(err)
			}
			for _, prefix := range []string{"GET /api/v1/inbox", "GET /api/v1/actors"} {
				if got := rec.last(t, prefix).Header.Get(apitypes.HeaderListener) != ""; got != tt.want {
					t.Errorf("%s marked as a listener's = %v, want %v", prefix, got, tt.want)
				}
			}
		})
	}
}

func TestFollowExitOnEOFEndsWhenStdinCloses(t *testing.T) {
	t.Parallel()
	listening := make(chan struct{})
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/inbox": reply(200, apitypes.Inbox{Cursor: "5"}),
		"GET /api/v1/events/stream": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, ": keepalive\n\n")
			w.(http.Flusher).Flush()
			close(listening)
			<-r.Context().Done()
		},
	}))
	if err := writeCursor(h, "5"); err != nil {
		t.Fatal(err)
	}
	stdin, parent := io.Pipe()
	h.f.IOStreams.In = stdin
	done := make(chan error, 1)
	go func() { done <- h.run(t, "inbox", "--follow", "--exit-on-eof") }()

	select {
	case err := <-done:
		t.Fatalf("follow returned %v while stdin was still open, want it to keep listening", err)
	case <-listening:
	}
	select {
	case err := <-done:
		t.Fatalf("follow returned %v while stdin was still open, want it to keep listening", err)
	default:
	}
	_ = parent.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("err = %v after stdin closed, want a clean exit", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("follow still running 5s after stdin closed")
	}
}

func TestFollowExitOnEOFWhileFindingTheStartIsACleanExit(t *testing.T) {
	t.Parallel()
	asked := make(chan struct{})
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/events": func(_ http.ResponseWriter, r *http.Request) {
			close(asked)
			<-r.Context().Done()
		},
	}))
	stdin, parent := io.Pipe()
	h.f.IOStreams.In = stdin
	done := make(chan error, 1)
	go func() { done <- h.run(t, "inbox", "--follow", "--exit-on-eof") }()
	<-asked
	_ = parent.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("err = %v (exit %d), want exit 0 when stdin closes before the start is found", err, cmdutil.ExitCode(err))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("follow still running 5s after stdin closed")
	}
}

type followFixture struct {
	mu      sync.Mutex
	cursors []string
}

func (f *followFixture) inboxCursors() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cursors...)
}

func newFollower(t *testing.T, routes map[string]http.HandlerFunc, stored string) (*follower, *[]apitypes.Event, *bytes.Buffer) {
	t.Helper()
	c, err := client.New(serve(t, routes).URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	c.Cursor = stored
	var printed []apitypes.Event
	errOut := &bytes.Buffer{}
	return &follower{
		c: c, now: func() time.Time { return fixedNow }, limit: 10, once: true, errOut: errOut,
		minWait: time.Millisecond, maxWait: 4 * time.Millisecond, stable: time.Hour,
		print: func(_ context.Context, wake []apitypes.Event) error {
			printed = append(printed, wake...)
			return nil
		},
	}, &printed, errOut
}

func TestFollowAfterADroppedStreamReadsFromTheInMemoryCursor(t *testing.T) {
	t.Parallel()
	fx := &followFixture{}
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/inbox": func(w http.ResponseWriter, r *http.Request) {
			fx.mu.Lock()
			fx.cursors = append(fx.cursors, r.URL.Query().Get("cursor"))
			n := len(fx.cursors)
			fx.mu.Unlock()
			in := apitypes.Inbox{Cursor: "6", Events: []apitypes.Event{plainEvent("6", 6)}}
			if n > 1 {
				in = apitypes.Inbox{Cursor: "8", Events: []apitypes.Event{wakeEvent("8", 42)}}
			}
			reply(200, in)(w, r)
		},
		"GET /api/v1/events/stream": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
		},
	}
	f, printed, errOut := newFollower(t, routes, "5")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := f.run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fx.inboxCursors(), ","); got != "5,6" {
		t.Errorf("inbox cursors = %q, want the stored 5 then the in-memory 6 after the stream dropped", got)
	}
	if len(*printed) != 1 || (*printed)[0].ID != "8" {
		t.Errorf("printed = %+v, want the wake event written while disconnected", *printed)
	}
	if !strings.Contains(errOut.String(), "event stream closed; retrying in 1ms") {
		t.Errorf("stderr = %q, want one retry line naming the drop", errOut.String())
	}
}

func TestFollowRereadsOnlyWhenTheStreamShowsANewerEvent(t *testing.T) {
	t.Parallel()
	fx := &followFixture{}
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/inbox": func(w http.ResponseWriter, r *http.Request) {
			fx.mu.Lock()
			fx.cursors = append(fx.cursors, r.URL.Query().Get("cursor"))
			n := len(fx.cursors)
			fx.mu.Unlock()
			in := apitypes.Inbox{Cursor: eventPos(5)}
			if n > 1 {
				in = apitypes.Inbox{Cursor: eventPos(8), Events: []apitypes.Event{wakeEvent("8", 42)}}
			}
			reply(200, in)(w, r)
		},
		"GET /api/v1/events/stream": func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("after"); got != eventPos(5) {
				t.Errorf("stream after = %q, want the catch-up cursor %s", got, eventPos(5))
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, ": keepalive\n\n")
			sseEvent(w, plainEvent("5", 5))
			sseEvent(w, plainEvent("8", 8))
			<-r.Context().Done()
		},
	}
	f, printed, _ := newFollower(t, routes, eventPos(5))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := f.run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fx.inboxCursors(), ","); got != eventPos(5)+","+eventPos(5) || len(*printed) != 1 {
		t.Errorf("inbox cursors = %q, printed %d, want exactly one reread, triggered by event 8 and not by the replayed 5 or the keepalive", got, len(*printed))
	}
}

func TestFollowDrainsEveryPageBeforeWaiting(t *testing.T) {
	t.Parallel()
	fx := &followFixture{}
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/inbox": func(w http.ResponseWriter, r *http.Request) {
			fx.mu.Lock()
			fx.cursors = append(fx.cursors, r.URL.Query().Get("cursor"))
			fx.mu.Unlock()
			if r.URL.Query().Get("cursor") == "1" {
				reply(200, apitypes.Inbox{Cursor: "2", Events: []apitypes.Event{wakeEvent("2", 2)}})(w, r)
				return
			}
			reply(200, apitypes.Inbox{Cursor: "1", Remaining: 1, Events: []apitypes.Event{plainEvent("1", 1)}})(w, r)
		},
	}
	f, printed, _ := newFollower(t, routes, "0")
	if err := f.run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fx.inboxCursors(), ","); got != "0,1" || len(*printed) != 1 {
		t.Errorf("inbox cursors = %q, printed %d, want the second page read at once and its wake event printed", got, len(*printed))
	}
}

func TestFollowBacksOffWithDoublingPausesUpToTheCap(t *testing.T) {
	t.Parallel()
	fx := &followFixture{}
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/inbox": func(w http.ResponseWriter, r *http.Request) {
			fx.mu.Lock()
			fx.cursors = append(fx.cursors, "")
			n := len(fx.cursors)
			fx.mu.Unlock()
			if n <= 4 {
				problem(503, apitypes.Problem{Title: "unavailable"})(w, r)
				return
			}
			reply(200, apitypes.Inbox{Cursor: "1", Events: []apitypes.Event{wakeEvent("1", 1)}})(w, r)
		},
	}
	f, printed, errOut := newFollower(t, routes, "3")
	if err := f.run(t.Context()); err != nil {
		t.Fatal(err)
	}
	var waits []string
	for l := range strings.SplitSeq(strings.TrimSpace(errOut.String()), "\n") {
		waits = append(waits, l[strings.LastIndex(l, " ")+1:])
	}
	if got := strings.Join(waits, ","); got != "1ms,2ms,4ms,4ms" || len(*printed) != 1 {
		t.Errorf("pauses = %q, printed %d, want 1ms,2ms,4ms,4ms then the event", got, len(*printed))
	}
}

func TestFollowDoesNotRetryARejectedRequest(t *testing.T) {
	t.Parallel()
	fx := &followFixture{}
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/inbox": func(w http.ResponseWriter, r *http.Request) {
			fx.mu.Lock()
			fx.cursors = append(fx.cursors, "")
			fx.mu.Unlock()
			problem(401, apitypes.Problem{Title: "unauthorized"})(w, r)
		},
	}
	f, _, _ := newFollower(t, routes, "3")
	err := f.run(t.Context())
	if !isUnauthorized(err) || len(fx.inboxCursors()) != 1 {
		t.Errorf("err = %v after %d reads, want the 401 returned after one read", err, len(fx.inboxCursors()))
	}
}

// Without a usable cursor the follower starts at the newest event: replaying
// the whole log would wake the chat for every old event at once.
func TestFollowWithoutAUsableCursorStartsAtTheNewestEvent(t *testing.T) {
	t.Parallel()
	head := apitypes.EventList{Items: []apitypes.Event{plainEvent("41", 1)}}
	tests := []struct {
		name, stored string
		wantURI      []string
		wantErrOut   string
	}{
		{"no stored cursor", "", []string{"/api/v1/inbox?cursor=" + eventPos(41) + "&limit=10"}, ""},
		{"stale stored cursor", "50", []string{"/api/v1/inbox?cursor=50&limit=10", "/api/v1/inbox?cursor=" + eventPos(41) + "&limit=10"}, "following from the newest event"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{}
			inbox := func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("cursor") != eventPos(41) {
					problem(422, apitypes.Problem{Title: "Unprocessable Entity", Errors: []apitypes.ProblemField{{Field: "cursor", Message: "no such event"}}})(w, r)
					return
				}
				reply(200, apitypes.Inbox{Cursor: "42", Events: []apitypes.Event{wakeEvent("42", 42)}})(w, r)
			}
			h := newHarness(t, serve(t, map[string]http.HandlerFunc{
				"GET /api/v1/inbox":  rec.wrap(inbox),
				"GET /api/v1/events": reply(200, head),
				"GET /api/v1/actors": reply(200, fixtureActors()),
			}))
			if tt.stored != "" {
				if err := writeCursor(h, tt.stored); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.run(t, "inbox", "--follow", "--once"); err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, c := range rec.calls() {
				got = append(got, strings.TrimPrefix(c, "GET "))
			}
			if strings.Join(got, " ") != strings.Join(tt.wantURI, " ") {
				t.Errorf("inbox requests = %v, want %v", got, tt.wantURI)
			}
			if !strings.Contains(h.errOut.String(), tt.wantErrOut) {
				t.Errorf("stderr = %q, want %q", h.errOut.String(), tt.wantErrOut)
			}
			if !strings.Contains(h.out.String(), "#42") {
				t.Errorf("stdout = %q, want the wake event after the newest event", h.out.String())
			}
		})
	}
}

func TestAlreadyRead(t *testing.T) {
	t.Parallel()
	cursor := eventPosAt(100, 50)
	tests := []struct {
		name string
		e    apitypes.Event
		want bool
	}{
		{"replay of the event the drain returned", apitypes.Event{ID: "50", Position: cursor}, true},
		{"earlier position", apitypes.Event{ID: "49", Position: eventPosAt(100, 49)}, true},
		{"later position with a lower id", apitypes.Event{ID: "5", Position: eventPosAt(200, 5)}, false},
		{"later position with a higher id", apitypes.Event{ID: "60", Position: eventPosAt(200, 60)}, false},
		{"no position", apitypes.Event{ID: "5"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := alreadyRead(cursor, tt.e); got != tt.want {
				t.Errorf("alreadyRead(%q, id %s position %q) = %v, want %v", cursor, tt.e.ID, tt.e.Position, got, tt.want)
			}
		})
	}
}

// The feed is ordered by (xid, id): an event committed later can carry a lower
// id than the cursor. Comparing ids would drop it and the chat would never wake.
func TestFollowWakesOnAStreamedEventWithALowerIDThanTheCursor(t *testing.T) {
	t.Parallel()
	drained, later := eventPosAt(100, 50), eventPosAt(200, 5)
	fx := &followFixture{}
	woken := wakeEvent("5", 42)
	woken.Position = later
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/inbox": func(w http.ResponseWriter, r *http.Request) {
			fx.mu.Lock()
			fx.cursors = append(fx.cursors, r.URL.Query().Get("cursor"))
			n := len(fx.cursors)
			fx.mu.Unlock()
			in := apitypes.Inbox{Cursor: drained}
			if n > 1 {
				in = apitypes.Inbox{Cursor: later, Events: []apitypes.Event{woken}}
			}
			reply(200, in)(w, r)
		},
		"GET /api/v1/events/stream": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			sseEvent(w, woken)
			<-r.Context().Done()
		},
	}
	f, printed, _ := newFollower(t, routes, drained)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := f.run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fx.inboxCursors(), ","); got != drained+","+drained || len(*printed) != 1 {
		t.Errorf("inbox cursors = %q, printed %d, want one reread triggered by the streamed event and its wake printed", got, len(*printed))
	}
}
