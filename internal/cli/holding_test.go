package cli

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

// saysHolding makes every route answer as a server that was asked what the
// chat holds, and records whether each request asked.
func saysHolding(t *testing.T, h apitypes.Holding, routes map[string]http.HandlerFunc) map[string]http.HandlerFunc {
	t.Helper()
	raw, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]http.HandlerFunc, len(routes))
	for pattern, next := range routes {
		out[pattern] = func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(apitypes.HeaderHolding) == "" {
				t.Errorf("%s %s did not ask what the chat holds", r.Method, r.URL.Path)
			}
			w.Header().Set(apitypes.HeaderHolding, base64.StdEncoding.EncodeToString(raw))
			next(w, r)
		}
	}
	return out
}

func fixtureHolding() apitypes.Holding {
	return apitypes.Holding{Cards: []apitypes.HeldCard{
		{Project: "demo", Number: 42, Title: "Fix login", ExpiresAt: fixedNow.Add(4 * time.Minute)},
		{Project: "web", Number: 7, Title: "Ship the page", ExpiresAt: fixedNow.Add(28 * time.Minute)},
	}, More: 3}
}

const fixtureHoldingLines = "holding  #42  demo  Fix login  4m left\nholding  #7  web  Ship the page  28m left\nholding  3 more\n"

// The list follows a command's own output on stdout, the card the command
// names included, and -Q does not hide it. Under --json it is the last field
// of the object instead.
func TestACommandEndsWithWhatTheChatHolds(t *testing.T) {
	t.Parallel()
	srv := serve(t, saysHolding(t, fixtureHolding(), fixtureRoutes()))

	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "-Q", "show", "42"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	if !strings.HasPrefix(out, "#42 ") || !strings.HasSuffix(out, "\n"+fixtureHoldingLines) {
		t.Errorf("show output = %q, want the card, then the held cards as its last lines", out)
	}

	h = newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "show", "42", "--json"); err != nil {
		t.Fatal(err)
	}
	var card struct {
		Number  int64             `json:"number"`
		Holding *apitypes.Holding `json:"holding"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &card); err != nil {
		t.Fatalf("show --json is not one JSON value: %v\n%s", err, h.out.String())
	}
	if card.Number != 42 || card.Holding == nil || len(card.Holding.Cards) != 2 || card.Holding.More != 3 {
		t.Errorf("show --json = number %d, holding %+v; want the card with the holding field", card.Number, card.Holding)
	}
	if strings.Contains(h.out.String(), "holding  #") {
		t.Errorf("show --json has the text lines too:\n%s", h.out.String())
	}

	h = newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "ls", "--json"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), `"holding": {`) {
		t.Errorf("ls --json lacks the holding field:\n%s", h.out.String())
	}
}

func TestAChatThatHoldsNothingGetsNoLineAndAnEmptyField(t *testing.T) {
	t.Parallel()
	srv := serve(t, saysHolding(t, apitypes.Holding{Cards: []apitypes.HeldCard{}}, fixtureRoutes()))
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "show", "42"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.out.String(), "holding") {
		t.Errorf("show output = %q, want no holding line", h.out.String())
	}
	h = newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "show", "42", "--json"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(h.out.String(), "  \"holding\": {\n    \"cards\": []\n  }\n}\n") {
		t.Errorf("show --json = %q, want an empty holding field last", h.out.String())
	}
}

// Where the list has no place it is left out: after a page's source or a
// secret shown once, which a script captures whole; after inbox, which prints
// the chat's claims itself; after a command that failed.
func TestTheHeldCardsAreLeftOutWhereTheyDoNotBelong(t *testing.T) {
	t.Parallel()
	page := apitypes.HTMLPage{HTML: "<!doctype html><p>plan</p>", Bytes: 26}
	routes := fixtureRoutes()
	routes["GET /api/v1/projects/demo/cards/42/page"] = reply(200, page)
	routes["GET /api/v1/projects/demo/cards/43"] = problem(404, apitypes.Problem{Title: "Not Found"})
	routes["POST /api/v1/tokens"] = reply(201, apitypes.TokenCreated{Secret: "secret-one"})
	routes["POST /api/v1/agents"] = reply(201, apitypes.AgentCreated{Agent: apitypes.Agent{Name: "bot", Status: "active", Role: "member"}, Token: "secret-two"})
	srv := serve(t, saysHolding(t, fixtureHolding(), routes))

	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"token", "create", "--name", "ci"}, "secret-one\n"},
		{[]string{"agent", "register", "bot", "--org-wide"}, "secret-two\n"},
	} {
		h := newHarness(t, srv)
		if err := h.run(t, tt.args...); err != nil {
			t.Fatal(err)
		}
		if h.out.String() != tt.want {
			t.Errorf("%v printed %q, want the secret and nothing else", tt.args, h.out.String())
		}
	}

	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "show", "42", "--page"); err != nil {
		t.Fatal(err)
	}
	if h.out.String() != page.HTML {
		t.Errorf("show --page = %q, want the source and nothing else", h.out.String())
	}

	h = newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "inbox"); err != nil {
		t.Fatal(err)
	}
	if out := h.out.String(); strings.Contains(out, "holding  #") || !strings.Contains(out, "my claims (renewed):\n  #42  working  high  Fix login  @claude-code (agent) (witty-lynx-3f9a)  30m left\n") {
		t.Errorf("inbox output = %q, want its own claim lines with the time left and no holding lines", out)
	}

	h = newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "show", "43"); err == nil {
		t.Fatal("show 43 succeeded, want not found")
	}
	if strings.Contains(h.out.String(), "holding") {
		t.Errorf("stdout after a failed command = %q, want nothing", h.out.String())
	}
}

// A listener ends a batch that woke the chat with what the chat holds, and
// prints nothing for a batch that did not: every line it prints starts a
// turn.
func TestFollowEndsAWakingBatchWithWhatTheChatHolds(t *testing.T) {
	t.Parallel()
	woke := apitypes.Inbox{Cursor: "9", Events: []apitypes.Event{wakeEvent("8", 42), plainEvent("9", 9)}}
	routes := func(in apitypes.Inbox) map[string]http.HandlerFunc {
		return saysHolding(t, fixtureHolding(), map[string]http.HandlerFunc{
			"GET /api/v1/inbox":  reply(200, in),
			"GET /api/v1/actors": reply(200, fixtureActors()),
		})
	}

	h := newHarness(t, serve(t, routes(woke)))
	if err := writeCursor(h, "5"); err != nil {
		t.Fatal(err)
	}
	if err := h.run(t, "inbox", "--follow", "--once"); err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(h.out.String(), "\n")
	if len(lines) != 5 || !strings.Contains(lines[0], "commented on #42") || strings.Join(lines[1:], "") != fixtureHoldingLines {
		t.Errorf("stdout = %q, want the wake event, then the held cards", h.out.String())
	}

	h = newHarness(t, serve(t, routes(woke)))
	if err := writeCursor(h, "5"); err != nil {
		t.Fatal(err)
	}
	if err := h.run(t, "--json", "inbox", "--follow", "--once"); err != nil {
		t.Fatal(err)
	}
	lines = strings.Split(strings.TrimSpace(h.out.String()), "\n")
	var ev apitypes.Event
	var last struct {
		Holding *apitypes.Holding `json:"holding"`
	}
	if len(lines) != 2 || json.Unmarshal([]byte(lines[0]), &ev) != nil || ev.ID != "8" ||
		json.Unmarshal([]byte(lines[1]), &last) != nil || last.Holding == nil || len(last.Holding.Cards) != 2 {
		t.Errorf("stdout = %q, want the event, then one line with the holding object", h.out.String())
	}
}

func TestFollowPrintsNoHeldCardsForABatchThatWokeNobody(t *testing.T) {
	t.Parallel()
	quiet := apitypes.Inbox{Cursor: "9", Events: []apitypes.Event{plainEvent("9", 9)}}
	f, printed, _ := newFollower(t, saysHolding(t, fixtureHolding(), map[string]http.HandlerFunc{"GET /api/v1/inbox": reply(200, quiet)}), "")
	f.c.Holding = true
	told := 0
	f.held = func(apitypes.Holding) error { told++; return nil }
	cursor := "5"
	if done, err := f.drain(t.Context(), &cursor); err != nil || done {
		t.Fatalf("drain = %v, %v", done, err)
	}
	if len(*printed) != 0 || told != 0 || cursor != "9" {
		t.Errorf("printed %d events and the held cards %d times, cursor %s; want nothing printed and the cursor moved", len(*printed), told, cursor)
	}
}
