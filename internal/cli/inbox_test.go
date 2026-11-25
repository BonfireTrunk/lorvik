package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func inboxRoutes(rec *recorder, in apitypes.Inbox) map[string]http.HandlerFunc {
	return rec.wrapAll(map[string]http.HandlerFunc{
		"GET /api/v1/inbox":               reply(200, in),
		"GET /api/v1/actors":              reply(200, fixtureActors()),
		"GET /api/v1/events":              reply(200, apitypes.EventList{Items: []apitypes.Event{{ID: "9150", Position: eventPos(9150)}, {ID: "9151", Position: eventPos(9151)}}}),
		"GET /api/v1/projects/demo/cards": reply(200, apitypes.CardList{}),
	})
}

func TestInboxStoresTheCursorAndSendsItNextTime(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	h := newHarness(t, serve(t, inboxRoutes(rec, fixtureInbox())))

	if err := h.run(t, "inbox"); err != nil {
		t.Fatal(err)
	}
	first := rec.last(t, "GET /api/v1/inbox")
	if first.URI != "/api/v1/inbox?limit=10" || first.Header.Get(apitypes.HeaderCursor) != "" {
		t.Errorf("first run sent %q with cursor header %q, want no cursor at all", first.URI, first.Header.Get(apitypes.HeaderCursor))
	}
	if !strings.Contains(h.config(t), `cursor_tok_test-session = "`+eventPos(9103)+`@`+stampNow+`"`) {
		t.Errorf("config = %q, want the returned cursor stored under the token prefix", h.config(t))
	}

	if err := h.run(t, "inbox"); err != nil {
		t.Fatal(err)
	}
	second := rec.last(t, "GET /api/v1/inbox")
	if second.URI != "/api/v1/inbox?cursor="+eventPos(9103)+"&limit=10" || second.Header.Get(apitypes.HeaderCursor) != eventPos(9103) {
		t.Errorf("second run sent %q with cursor header %q, want the stored cursor in both", second.URI, second.Header.Get(apitypes.HeaderCursor))
	}

	// Any other command carries the stored cursor too, so the server can count unread.
	if err := h.run(t, "-p", "demo", "ls"); err != nil {
		t.Fatal(err)
	}
	if got := rec.last(t, "GET /api/v1/projects/demo/cards").Header.Get(apitypes.HeaderCursor); got != eventPos(9103) {
		t.Errorf("ls sent cursor header %q, want %s", got, eventPos(9103))
	}
}

func TestInboxCursorSelection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		stored   string
		wantURI  string
		wantSave string // expected stored cursor afterwards; "" means unchanged
	}{
		{"peek lists without storing", []string{"inbox", "--peek"}, "50", "/api/v1/inbox?cursor=50&limit=10", ""},
		{"from oldest starts at zero", []string{"inbox", "--from", "oldest"}, "50", "/api/v1/inbox?cursor=0&limit=10", eventPos(9103)},
		{"from an event id", []string{"inbox", "--from", "9000", "-L", "3"}, "50", "/api/v1/inbox?cursor=9000&limit=3", eventPos(9103)},
		{"from a position", []string{"inbox", "--from", eventPos(9000), "-L", "3"}, "50", "/api/v1/inbox?cursor=" + eventPos(9000) + "&limit=3", eventPos(9103)},
		{"a project filter never stores", []string{"-p", "demo", "inbox"}, "50", "/api/v1/inbox?chat_project=demo&cursor=50&limit=10&project=demo", ""},
		{"all events fetched and cursor unchanged stores nothing", []string{"inbox"}, eventPos(9103), "/api/v1/inbox?cursor=" + eventPos(9103) + "&limit=10", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{}
			h := newHarness(t, serve(t, inboxRoutes(rec, fixtureInbox())))
			if err := writeCursor(h, tt.stored); err != nil {
				t.Fatal(err)
			}
			if err := h.run(t, tt.args...); err != nil {
				t.Fatal(err)
			}
			if got := rec.last(t, "GET /api/v1/inbox").URI; got != tt.wantURI {
				t.Errorf("request = %q, want %q", got, tt.wantURI)
			}
			want := `cursor_tok_test-session = "` + tt.wantSave + "@" + stampNow + `"`
			if tt.wantSave == "" {
				want = `cursor_tok = "` + tt.stored + `"`
			}
			if !strings.Contains(h.config(t), want) {
				t.Errorf("config = %q, want %s", h.config(t), want)
			}
		})
	}
}

// A stored cursor from another database names no event on this server: the
// inbox starts over from the oldest event instead of failing on every run.
// A cursor the caller named with --from still fails.
func TestInboxRestartsWhenTheStoredCursorNamesNoEvent(t *testing.T) {
	t.Parallel()
	stale := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") != "0" {
			problem(422, apitypes.Problem{Title: "Unprocessable Entity", Errors: []apitypes.ProblemField{{Field: "cursor", Message: "no such event"}}})(w, r)
			return
		}
		reply(200, fixtureInbox())(w, r)
	}
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		wantURI []string
	}{
		{"stored cursor", []string{"inbox"}, false, []string{"/api/v1/inbox?cursor=50&limit=10", "/api/v1/inbox?cursor=0&limit=10"}},
		{"named cursor", []string{"inbox", "--from", "9000"}, true, []string{"/api/v1/inbox?cursor=9000&limit=10"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{}
			routes := inboxRoutes(rec, fixtureInbox())
			routes["GET /api/v1/inbox"] = rec.wrap(stale)
			h := newHarness(t, serve(t, routes))
			if err := writeCursor(h, "50"); err != nil {
				t.Fatal(err)
			}
			err := h.run(t, tt.args...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %t", err, tt.wantErr)
			}
			var got []string
			for _, c := range rec.calls() {
				if strings.HasPrefix(c, "GET /api/v1/inbox") {
					got = append(got, strings.TrimPrefix(c, "GET "))
				}
			}
			if !slices.Equal(got, tt.wantURI) {
				t.Errorf("requests = %q, want %q", got, tt.wantURI)
			}
			if tt.wantErr {
				return
			}
			if !strings.Contains(h.errOut.String(), "stored inbox cursor 50 names no event") {
				t.Errorf("stderr = %q, want the restart named", h.errOut.String())
			}
			if !strings.Contains(h.config(t), `cursor_tok_test-session = "`+eventPos(9103)+`@`+stampNow+`"`) {
				t.Errorf("config = %q, want the new cursor stored", h.config(t))
			}
		})
	}
}

func writeCursor(h *harness, cursor string) error {
	return UpdateUserConfig(func(k string) string { return h.env[k] }, map[string]string{"cursor_tok": cursor})
}

func TestInboxFromNowStoresTheHeadWithoutListing(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	h := newHarness(t, serve(t, inboxRoutes(rec, fixtureInbox())))
	if err := h.run(t, "inbox", "--from", "now"); err != nil {
		t.Fatal(err)
	}
	for _, c := range rec.calls() {
		if strings.Contains(c, "/inbox") {
			t.Errorf("--from now listed the inbox: %v", rec.calls())
		}
	}
	if got := rec.last(t, "GET /api/v1/events").URI; got != "/api/v1/events?before=9223372036854775807&limit=1" {
		t.Errorf("head lookup = %q", got)
	}
	if !strings.Contains(h.config(t), `cursor_tok_test-session = "`+eventPos(9151)+`@`+stampNow+`"`) {
		t.Errorf("config = %q, want the newest event position", h.config(t))
	}
	if h.out.String() != "cursor set to event "+eventPos(9151)+"\n" {
		t.Errorf("stdout = %q", h.out.String())
	}

	h2 := newHarness(t, serve(t, inboxRoutes(&recorder{}, fixtureInbox())))
	if err := h2.run(t, "inbox", "--from", "now", "--json"); err != nil {
		t.Fatal(err)
	}
	if h2.out.String() != "{\n  \"cursor\": \""+eventPos(9151)+"\"\n}\n" {
		t.Errorf("json = %q", h2.out.String())
	}
}

func TestInboxQuietAndEmptyStillStoresTheCursor(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	h := newHarness(t, serve(t, inboxRoutes(rec, apitypes.Inbox{Cursor: "77", OpenQuestions: 2})))
	if err := h.run(t, "inbox", "-Q"); err != nil {
		t.Fatal(err)
	}
	if h.out.String() != "" || h.errOut.String() != "" {
		t.Errorf("stdout %q stderr %q, want nothing", h.out.String(), h.errOut.String())
	}
	if !strings.Contains(h.config(t), `cursor_tok_test-session = "77@`+stampNow+`"`) {
		t.Errorf("config = %q", h.config(t))
	}
}

func TestInboxRemainingLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		remaining int
		want      string
	}{
		{0, ""},
		{37, "remaining 37, run lorvik inbox again\n"},
		{100, "remaining 100+, run lorvik inbox again\n"},
	}
	for _, tt := range tests {
		in := apitypes.Inbox{Cursor: "5", Remaining: tt.remaining, Events: fixtureInbox().Events}
		h := newHarness(t, serve(t, inboxRoutes(&recorder{}, in)))
		if err := h.run(t, "inbox", "--peek"); err != nil {
			t.Fatal(err)
		}
		switch {
		case tt.want == "" && strings.Contains(h.out.String(), "remaining"):
			t.Errorf("remaining 0: stdout = %q, want no remaining line", h.out.String())
		case tt.want != "" && !strings.Contains(h.out.String(), tt.want):
			t.Errorf("remaining %d: stdout = %q, want line %q", tt.remaining, h.out.String(), tt.want)
		}
	}
}

func TestNamesAreResolvedInOneBatchAndOnlyForHumans(t *testing.T) {
	t.Parallel()
	t.Run("human output asks once for every actor", func(t *testing.T) {
		t.Parallel()
		rec := &recorder{}
		h := newHarness(t, serve(t, inboxRoutes(rec, fixtureInbox())))
		if err := h.run(t, "inbox", "--peek"); err != nil {
			t.Fatal(err)
		}
		var lookups []string
		for _, c := range rec.calls() {
			if strings.HasPrefix(c, "GET /api/v1/actors") {
				lookups = append(lookups, c)
			}
		}
		if len(lookups) != 1 {
			t.Fatalf("actor lookups = %v, want exactly one batch", lookups)
		}
		for _, id := range []string{ana.ID, claude.ID} {
			if !strings.Contains(lookups[0], id) {
				t.Errorf("lookup %q is missing actor %s", lookups[0], id)
			}
		}
		if !strings.Contains(h.out.String(), "Ana Souza answered #17") || !strings.Contains(h.out.String(), "@claude-code") {
			t.Errorf("stdout = %q, want resolved names", h.out.String())
		}
	})
	t.Run("json prints ids and makes no lookup", func(t *testing.T) {
		t.Parallel()
		rec := &recorder{}
		h := newHarness(t, serve(t, inboxRoutes(rec, fixtureInbox())))
		if err := h.run(t, "inbox", "--peek", "--json"); err != nil {
			t.Fatal(err)
		}
		for _, c := range rec.calls() {
			if strings.Contains(c, "/actors") {
				t.Errorf("--json looked up names: %v", rec.calls())
			}
		}
		if !strings.Contains(h.out.String(), ana.ID) {
			t.Errorf("stdout = %q, want the raw actor id", h.out.String())
		}
	})
	t.Run("a failed lookup degrades to ids", func(t *testing.T) {
		t.Parallel()
		routes := inboxRoutes(&recorder{}, fixtureInbox())
		routes["GET /api/v1/actors"] = problem(500, apitypes.Problem{Title: "boom"})
		h := newHarness(t, serve(t, routes))
		if err := h.run(t, "inbox", "--peek"); err != nil {
			t.Fatalf("a name lookup failure must not fail the command: %v", err)
		}
		if !strings.Contains(h.out.String(), ana.ID+" answered #17") {
			t.Errorf("stdout = %q, want the id as fallback", h.out.String())
		}
	})
}

func TestActorsCommand(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	h := newHarness(t, serve(t, inboxRoutes(rec, apitypes.Inbox{})))
	if err := h.run(t, "actors", ana.ID, claude.ID); err != nil {
		t.Fatal(err)
	}
	want := "user  " + ana.ID + "  Ana Souza\nagent  " + claude.ID + "  claude-code\n"
	if h.out.String() != want {
		t.Errorf("stdout = %q, want %q", h.out.String(), want)
	}
	if got := rec.last(t, "GET /api/v1/actors").URI; got != "/api/v1/actors?id="+ana.ID+"%2C"+claude.ID {
		t.Errorf("request = %q", got)
	}
	if err := newHarness(t, nil).run(t, "actors", "not-an-id"); err == nil {
		t.Error("a non-numeric id must be a usage error")
	}
}

func TestInboxCursorKeyFallsBackToTheTokenOnlyKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		config  map[string]string
		wantURI string
	}{
		{"labelled key wins", map[string]string{"cursor_tok": "50", "cursor_tok_test-session": "70"}, "/api/v1/inbox?cursor=70&limit=10"},
		{"missing labelled key starts from the token-only cursor", map[string]string{"cursor_tok": "50"}, "/api/v1/inbox?cursor=50&limit=10"},
		{"other session's key is not borrowed", map[string]string{"cursor_tok_other": "90"}, "/api/v1/inbox?limit=10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{}
			h := newHarness(t, serve(t, inboxRoutes(rec, apitypes.Inbox{})))
			if err := UpdateUserConfig(func(k string) string { return h.env[k] }, tt.config); err != nil {
				t.Fatal(err)
			}
			if err := h.run(t, "inbox", "--peek"); err != nil {
				t.Fatal(err)
			}
			if got := rec.last(t, "GET /api/v1/inbox").URI; got != tt.wantURI {
				t.Errorf("request = %q, want %q", got, tt.wantURI)
			}
		})
	}
}

func TestSessionCursorKey(t *testing.T) {
	t.Parallel()
	tests := []struct{ secret, label, want string }{
		{"", "lead", ""},
		{"lvk_a_ABCDEFGHIJKLMNOP", "", "cursor_lvk_a_ABCDEF"},
		{"lvk_a_ABCDEFGHIJKLMNOP", "lead-c1", "cursor_lvk_a_ABCDEF_lead-c1"},
	}
	for _, tt := range tests {
		if got := sessionCursorKey(tt.secret, tt.label); got != tt.want {
			t.Errorf("sessionCursorKey(%q, %q) = %q, want %q", tt.secret, tt.label, got, tt.want)
		}
	}
}

func TestPrintInboxGroupsEventsOnYourCardsFirst(t *testing.T) {
	t.Parallel()
	ev := func(id string, mine bool) apitypes.Event {
		return apitypes.Event{ID: id, Type: "card.status_changed", Mine: mine, Subject: apitypes.Subject{Type: "card", ID: id, Number: 1},
			Payload: json.RawMessage(`{"title":"T","from":"backlog","to":"ready"}`)}
	}
	tests := []struct {
		name   string
		events []apitypes.Event
		want   []string // headings in order
		absent []string
	}{
		{"both groups, mine first", []apitypes.Event{ev("1", false), ev("2", true)}, []string{"on your cards:", "others:"}, nil},
		{"only others", []apitypes.Event{ev("1", false)}, []string{"others:"}, []string{"on your cards:"}},
		{"only mine", []apitypes.Event{ev("1", true)}, []string{"on your cards:"}, []string{"others:"}},
		{"none", nil, nil, []string{"on your cards:", "others:"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{}
			h := newHarness(t, serve(t, inboxRoutes(rec, apitypes.Inbox{Events: tt.events, Cursor: "9"})))
			if err := h.run(t, "inbox", "--peek"); err != nil {
				t.Fatal(err)
			}
			out := h.out.String()
			last := -1
			for _, w := range tt.want {
				i := strings.Index(out, w)
				if i < 0 || i < last {
					t.Fatalf("output %q: heading %q missing or out of order", out, w)
				}
				last = i
			}
			for _, w := range tt.absent {
				if strings.Contains(out, w) {
					t.Errorf("output %q has unwanted heading %q", out, w)
				}
			}
		})
	}
}

// eventPos builds a position the way the server does, for fixtures: the
// transaction id and the event id, both zero-padded so strings sort in feed
// order. The tests give both the same number unless a case needs them apart.
func eventPos(id int64) string { return eventPosAt(id, id) }

func eventPosAt(xid, id int64) string { return fmt.Sprintf("%020d-%019d", xid, id) }
