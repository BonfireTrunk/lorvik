package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func TestNextWithNothingReadyExitsThree(t *testing.T) {
	t.Parallel()
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/next": problem(404, apitypes.Problem{Title: "Not Found"}),
	})
	tests := []struct {
		name       string
		args       []string
		wantOut    string
		wantStderr string
	}{
		{"human prints on stdout", []string{"-p", "demo", "next"}, "no ready cards\n", ""},
		{"json keeps stdout empty and explains on stderr", []string{"-p", "demo", "next", "--json"}, "", "no ready cards"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, srv)
			err := h.run(t, tt.args...)
			if got := cmdutil.ExitCode(err); got != 3 {
				t.Fatalf("exit = %d (%v), want 3", got, err)
			}
			if h.out.String() != tt.wantOut {
				t.Errorf("stdout = %q, want %q", h.out.String(), tt.wantOut)
			}
			if !errors.Is(err, cmdutil.ErrSilent) && !strings.Contains(err.Error(), tt.wantStderr) {
				t.Errorf("err = %v, want %q", err, tt.wantStderr)
			}
		})
	}
}

func TestEditSendsFetchedVersionAndOnlyChangedFields(t *testing.T) {
	t.Parallel()
	var got apitypes.UpdateCard
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42": reply(200, fixtureCard()),
		"PATCH /api/v1/projects/demo/cards/42": func(w http.ResponseWriter, r *http.Request) {
			got = readJSON[apitypes.UpdateCard](t, r)
			reply(200, fixtureCard())(w, r)
		},
	})
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "edit", "42", "--priority", "urgent", "--parent", "0"); err != nil {
		t.Fatal(err)
	}
	if got.Version != 7 {
		t.Errorf("version = %d, want the fetched 7", got.Version)
	}
	if got.Priority == nil || *got.Priority != "urgent" || got.Title != nil || got.Body != nil {
		t.Errorf("update = %+v, want only priority set", got)
	}
	if got.Parent == nil || *got.Parent != 0 {
		t.Errorf("parent = %v, want explicit 0 to clear", got.Parent)
	}
}

func TestVersionConflictPrintsHintAndExitsFour(t *testing.T) {
	t.Parallel()
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42": reply(200, myCard()),
		"POST /api/v1/projects/demo/cards/42/transition": problem(409, apitypes.Problem{
			Title: "Conflict", Detail: "card changed", Hint: "run `lorvik show 42` and retry", CurrentVersion: 9,
		}),
	})
	h := newHarness(t, srv)
	err := h.run(t, "-p", "demo", "done", "42")
	if cmdutil.ExitCode(err) != 4 {
		t.Fatalf("exit = %d, want 4 (%v)", cmdutil.ExitCode(err), err)
	}
	msg := cmdutil.FormatError(err, nil)
	for _, want := range []string{"hint: run `lorvik show 42` and retry", "current_version: 9"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
}

func TestDropValidation(t *testing.T) {
	t.Parallel()
	var got apitypes.TransitionCard
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/43": reply(200, fixtureCard()),
		"POST /api/v1/projects/demo/cards/43/transition": func(w http.ResponseWriter, r *http.Request) {
			got = readJSON[apitypes.TransitionCard](t, r)
			reply(200, fixtureCard())(w, r)
		},
	})
	h := newHarness(t, srv)
	for _, args := range [][]string{
		{"drop", "43"},
		{"drop", "43", "--reason", "bogus"},
		{"drop", "43", "--reason", "duplicate"},
	} {
		err := h.run(t, append([]string{"-p", "demo"}, args...)...)
		if cmdutil.ExitCode(err) != 2 {
			t.Errorf("%v: exit = %d (%v), want 2", args, cmdutil.ExitCode(err), err)
		}
	}
	if err := h.run(t, "-p", "demo", "drop", "43", "--reason", "duplicate", "--dup", "42", "-m", "same"); err != nil {
		t.Fatal(err)
	}
	if got.Status != "dropped" || got.DropReason != "duplicate" || got.Duplicate == nil || *got.Duplicate != 42 || got.Note != "same" || got.Version != 7 {
		t.Errorf("transition = %+v", got)
	}
}

func TestUnreadNoticeWording(t *testing.T) {
	t.Parallel()
	tests := []struct {
		unread int
		want   string
	}{
		{0, ""},
		{1, "1 unread, run lorvik inbox"},
		{100, "100 unread, run lorvik inbox"},
		{apitypes.UnreadCap + 1, "100+ unread, run lorvik inbox"},
	}
	for _, tt := range tests {
		if got := unreadNotice(tt.unread); got != tt.want {
			t.Errorf("unreadNotice(%d) = %q, want %q", tt.unread, got, tt.want)
		}
	}
}

func TestUnreadNotice(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		unread string
		args   []string
		want   string
	}{
		{"printed once on stderr", "3", []string{"ls"}, "3 unread, run lorvik inbox\n"},
		{"capped above the server cap", "101", []string{"show", "42"}, "100+ unread, run lorvik inbox\n"},
		{"still printed with --json, on stderr", "3", []string{"ls", "--json"}, "3 unread, run lorvik inbox\n"},
		{"suppressed by --quiet", "3", []string{"ls", "--quiet"}, ""},
		{"inbox reports updates itself", "3", []string{"inbox"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			routes := fixtureRoutes()
			for k, v := range routes {
				routes[k] = func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set(apitypes.HeaderUnread, tt.unread)
					v(w, r)
				}
			}
			h := newHarness(t, serve(t, routes))
			if err := h.run(t, append([]string{"-p", "demo"}, tt.args...)...); err != nil {
				t.Fatal(err)
			}
			if h.errOut.String() != tt.want {
				t.Errorf("stderr = %q, want %q", h.errOut.String(), tt.want)
			}
			if strings.Contains(h.out.String(), "unread") {
				t.Error("notice leaked onto stdout")
			}
		})
	}
}

func TestUnreadNoticeIsPrintedOncePerCommand(t *testing.T) {
	t.Parallel()
	unread := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(apitypes.HeaderUnread, "2")
			next(w, r)
		}
	}
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42":             unread(reply(200, myCard())),
		"GET /api/v1/actors":                             unread(reply(200, fixtureActors())),
		"POST /api/v1/projects/demo/cards/42/transition": unread(reply(200, fixtureCard())),
	})
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "done", "42"); err != nil {
		t.Fatal(err)
	}
	if got, want := h.errOut.String(), "2 unread, run lorvik inbox\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestNoNoticeWhenNothingIsUnread(t *testing.T) {
	t.Parallel()
	for _, hdr := range []string{"", "0"} {
		srv := serve(t, map[string]http.HandlerFunc{
			"GET /api/v1/projects/demo/cards": func(w http.ResponseWriter, r *http.Request) {
				if hdr != "" {
					w.Header().Set(apitypes.HeaderUnread, hdr)
				}
				reply(200, fixtureCards())(w, r)
			},
			"GET /api/v1/actors": reply(200, fixtureActors()),
		})
		h := newHarness(t, srv)
		if err := h.run(t, "-p", "demo", "ls"); err != nil {
			t.Fatal(err)
		}
		if h.errOut.String() != "" {
			t.Errorf("header %q: stderr = %q, want empty", hdr, h.errOut.String())
		}
	}
}

func TestAgentRegisterPrintsOnlyTheToken(t *testing.T) {
	t.Parallel()
	var got apitypes.CreateAgent
	srv := serve(t, map[string]http.HandlerFunc{
		"POST /api/v1/agents": func(w http.ResponseWriter, r *http.Request) {
			got = readJSON[apitypes.CreateAgent](t, r)
			reply(201, apitypes.AgentCreated{
				Agent: apitypes.Agent{ID: "106666050296328901", Name: "bot", Status: "active", Role: "reviewer", Projects: []apitypes.ProjectRole{{Project: "web", Role: "reviewer"}}},
				Token: "lvk_a_secret",
			})(w, r)
		},
	})
	h := newHarness(t, srv)
	err := h.run(t, "agent", "register", "--name", "bot", "--harness", "codex", "--machine", "m1", "--expires-in", "24h",
		"--project", "web", "-p", "api", "--rights", "work,ready")
	if err != nil {
		t.Fatal(err)
	}
	if h.out.String() != "lvk_a_secret\n" {
		t.Errorf("stdout = %q, want only the token", h.out.String())
	}
	if !strings.Contains(h.errOut.String(), "106666050296328901") {
		t.Errorf("stderr = %q, want agent details", h.errOut.String())
	}
	want := apitypes.CreateAgent{Name: "bot", Harness: "codex", Machine: "m1", Rights: []string{"work", "ready"}, Projects: []string{"web", "api"}, TokenExpiresIn: "24h"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("request = %+v, want %+v", got, want)
	}
}

func TestAgentRegisterValidation(t *testing.T) {
	t.Parallel()
	var got apitypes.CreateAgent
	var status = "active"
	srv := serve(t, map[string]http.HandlerFunc{
		"POST /api/v1/agents": func(w http.ResponseWriter, r *http.Request) {
			got = readJSON[apitypes.CreateAgent](t, r)
			reply(201, apitypes.AgentCreated{Agent: apitypes.Agent{ID: "9", Name: got.Name, Status: status}, Token: "lvk_a_x"})(w, r)
		},
	})
	for _, args := range [][]string{
		{"agent", "register"},
		{"agent", "register", "a", "--name", "b"},
		{"agent", "register", "a", "--harness", "vim"},
		{"agent", "register", "a", "--rights", "admin"},
		{"agent", "register", "a", "--rights", "work,boss"},
		{"agent", "register", "a", "--rights", ""},
	} {
		h := newHarness(t, srv)
		if err := h.run(t, args...); cmdutil.ExitCode(err) != 2 {
			t.Errorf("%v: exit = %d (%v), want 2", args, cmdutil.ExitCode(err), err)
		}
	}
	h := newHarness(t, srv)
	if err := h.run(t, "agent", "register", "positional", "--org-wide"); err != nil {
		t.Fatal(err)
	}
	if got.Name != "positional" || !got.OrgWide {
		t.Errorf("request = %+v", got)
	}
	if !strings.Contains(h.errOut.String(), "bound to no project yet") && !got.OrgWide {
		t.Errorf("stderr = %q", h.errOut.String())
	}

	status = "pending"
	h = newHarness(t, srv)
	if err := h.run(t, "agent", "register", "p", "--project", "web"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.errOut.String(), "lorvik agent approve 9") || h.out.String() != "lvk_a_x\n" {
		t.Errorf("pending: stdout %q stderr %q", h.out.String(), h.errOut.String())
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"ls", "--nope"}},
		{"unknown command", []string{"frobnicate"}},
		{"unknown subcommand", []string{"note", "frobnicate"}},
		{"missing arg", []string{"show"}},
		{"non-numeric card", []string{"-p", "x", "show", "abc"}},
		{"no project bound", []string{"ls"}},
		{"bad priority", []string{"-p", "x", "add", "t", "--priority", "huge"}},
		{"choice on a task", []string{"-p", "x", "add", "t", "--choice", "a"}},
		{"edit with nothing to change", []string{"-p", "x", "edit", "1"}},
		{"bad link kind", []string{"-p", "x", "link", "1", "causes", "2"}},
		{"tag flag is gone", []string{"-p", "x", "ls", "--tag", "auth"}},
		{"offset flag is gone", []string{"-p", "x", "ls", "--offset", "5"}},
		{"bad inbox from", []string{"inbox", "--from", "yesterday"}},
		{"bad member role", []string{"member", "role", "5", ""}},
		{"member role for an agent", []string{"member", "role", "agent:6", "reviewer"}},
		{"bad actor", []string{"member", "remove", "robot:5"}},
		{"bad project visibility", []string{"project", "create", "x", "--visibility", "secret"}},
		{"org set with nothing", []string{"org", "set"}},
		{"password login without a login name", []string{"login", "--password"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := newHarness(t, nil).run(t, tt.args...)
			if got := cmdutil.ExitCode(err); got != 2 {
				t.Errorf("exit = %d (%v), want 2", got, err)
			}
		})
	}
}

func TestMemberRoleForAnAgentPointsToMemberAdd(t *testing.T) {
	t.Parallel()
	err := newHarness(t, nil).run(t, "member", "role", "agent:6", "reviewer")
	if got := cmdutil.ExitCode(err); got != 2 {
		t.Fatalf("exit = %d (%v), want 2", got, err)
	}
	if want := "lorvik member add agent:6 --rights"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to name %q", err, want)
	}
}

func TestAddJoinsTitleAndSendsFields(t *testing.T) {
	t.Parallel()
	var got apitypes.CreateCard
	srv := serve(t, map[string]http.HandlerFunc{
		"POST /api/v1/projects/demo/cards": func(w http.ResponseWriter, r *http.Request) {
			got = readJSON[apitypes.CreateCard](t, r)
			reply(201, fixtureCard())(w, r)
		},
	})
	h := newHarness(t, srv)
	err := h.run(t, "-p", "demo", "ask", "Which", "db?", "--choice", "pg", "--choice", "sqlite", "--blocks", "42", "-b", "ctx")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "question" || got.Title != "Which db?" || got.Body != "ctx" || len(got.Choices) != 2 || len(got.Blocks) != 1 || got.Blocks[0] != 42 {
		t.Errorf("create = %+v", got)
	}
	if err := h.run(t, "-p", "demo", "add", "T", "--ready", "--parent", "5"); err != nil {
		t.Fatal(err)
	}
	if got.Status != "ready" || got.Parent == nil || *got.Parent != 5 || got.Kind != "" {
		t.Errorf("create = %+v", got)
	}
}

func TestProjectComesFromBinding(t *testing.T) {
	t.Parallel()
	srv := serve(t, fixtureRoutes())
	h := newHarness(t, srv)
	if err := os.WriteFile(filepath.Join(h.cwd, bindingFile), []byte("project = \"demo\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.run(t, "ls"); err != nil {
		t.Fatalf("ls with .lorvik.toml: %v", err)
	}
	if !strings.HasPrefix(h.out.String(), "#42") {
		t.Errorf("stdout = %q", h.out.String())
	}
}

func TestSearchPrintsSnippetOnNextLine(t *testing.T) {
	t.Parallel()
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/search": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("q") != "session expiry" || r.URL.Query().Get("limit") != "10" {
				t.Errorf("query = %q", r.URL.RawQuery)
			}
			reply(200, apitypes.SearchResult{Items: []apitypes.SearchHit{
				{Type: "card", ID: "5001", Number: 42, Project: new("demo"), Title: "Fix login", Status: "done", Snippet: "loop when the\nsession expired"},
				{Type: "note", ID: "6001", Slug: "arch", Title: "Architecture"},
			}})(w, r)
		},
	})
	h := newHarness(t, srv)
	if err := h.run(t, "search", "session", "expiry"); err != nil {
		t.Fatal(err)
	}
	want := "card #42  done  Fix login  (demo)\n  loop when the session expired\nnote arch  Architecture  (org)\n"
	if h.out.String() != want {
		t.Errorf("stdout = %q, want %q", h.out.String(), want)
	}
}

func TestSearchScopesToTheBoundProject(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		bound string
		args  []string
		want  string
	}{
		{"bound project by default", "demo", nil, "demo"},
		{"another project by name", "demo", []string{"-p", "global"}, "global"},
		{"every project with --all", "demo", []string{"--all"}, ""},
		{"every project when none is bound", "", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got *string
			srv := serve(t, map[string]http.HandlerFunc{
				"GET /api/v1/search": func(w http.ResponseWriter, r *http.Request) {
					p := r.URL.Query().Get("project")
					got = &p
					reply(200, apitypes.SearchResult{})(w, r)
				},
			})
			h := newHarness(t, srv)
			if tt.bound != "" {
				h.env["LORVIK_PROJECT"] = tt.bound
			}
			if err := h.run(t, append([]string{"search", "pgx"}, tt.args...)...); err != nil {
				t.Fatal(err)
			}
			if got == nil || *got != tt.want {
				t.Errorf("project = %v, want %q", got, tt.want)
			}
		})
	}
}

func TestQuietInboxPrintsNothingWhenEmpty(t *testing.T) {
	t.Parallel()
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/inbox": reply(200, apitypes.Inbox{OpenQuestions: 2, Cursor: "50"}),
	})
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "inbox", "-Q"); err != nil {
		t.Fatal(err)
	}
	if h.out.String() != "" || h.errOut.String() != "" {
		t.Errorf("quiet empty inbox printed stdout=%q stderr=%q, want nothing", h.out.String(), h.errOut.String())
	}
	h2 := newHarness(t, srv)
	if err := h2.run(t, "-p", "demo", "inbox"); err != nil {
		t.Fatal(err)
	}
	if want := "no new events\nopen questions: 2\n"; h2.out.String() != want {
		t.Errorf("stdout = %q, want %q", h2.out.String(), want)
	}
}

func TestParseNum(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"42", 42, false}, {"#42", 42, false}, {"D7", 7, false}, {" 5 ", 5, false},
		{"", 0, true}, {"4x", 0, true}, {"-3", 0, true},
	}
	for _, tt := range tests {
		got, err := parseNum(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("parseNum(%q) = %d, %v", tt.in, got, err)
		}
	}
}

func TestDoneClaimsFirstWhenCardIsNotWorking(t *testing.T) {
	t.Parallel()
	ready := fixtureCard()
	ready.Status = "ready"
	ready.Claim = nil
	claimed := fixtureCard()
	claimed.Version = 8
	var seq []string
	var got apitypes.TransitionCard
	record := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			seq = append(seq, r.Method+" "+r.URL.Path)
			if r.URL.Path == "/api/v1/projects/demo/cards/42/transition" {
				got = readJSON[apitypes.TransitionCard](t, r)
			}
			next(w, r)
		}
	}
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42":             record(reply(200, ready)),
		"POST /api/v1/projects/demo/cards/42/claim":      record(reply(200, claimed)),
		"POST /api/v1/projects/demo/cards/42/transition": record(reply(200, fixtureCard())),
	})
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "done", "42", "-m", "CI green"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /api/v1/projects/demo/cards/42",
		"POST /api/v1/projects/demo/cards/42/claim",
		"POST /api/v1/projects/demo/cards/42/transition",
	}
	if strings.Join(seq, "|") != strings.Join(want, "|") {
		t.Errorf("requests = %v, want %v", seq, want)
	}
	if got.Status != "done" || got.Version != 8 || got.Note != "CI green" {
		t.Errorf("transition = %+v, want done at the claimed version 8", got)
	}
	if !strings.Contains(h.errOut.String(), "lorvik: claimed #42 before marking it done") {
		t.Errorf("stderr = %q", h.errOut.String())
	}
}

// done claims a free card before closing it. When the chat's own claim on the
// card ran out and nobody touched it since, it says so: the chat believed it
// held the card and has to know it did not.
func TestDoneSaysWhenThisChatsClaimHadRunOut(t *testing.T) {
	t.Parallel()
	me := apitypes.Actor{Kind: "agent", ID: "106666050296328901"}
	other := apitypes.Actor{Kind: "agent", ID: "106666050296328902"}
	person := apitypes.Actor{Kind: "user", ID: "106666050296328903"}
	ev := func(typ string, actor apitypes.Actor, at time.Time, payload string) apitypes.Event {
		return apitypes.Event{Type: typ, Actor: actor, CreatedAt: at, Payload: json.RawMessage(payload), Subject: apitypes.Subject{Type: "card", Number: 42}}
	}
	expired := ev("card.claim_expired", me, fixedNow.Add(-11*time.Minute), `{"title":"Fix login","session":"test-session","expired_at":"`+fixedNow.Add(-12*time.Minute).Format(time.RFC3339)+`"}`)
	const lapsedNote = "lorvik: your claim on #42 ran out 12m ago and nobody has taken the card since; claimed it again before marking it done\n"
	const plainNote = "lorvik: claimed #42 before marking it done\n"
	tests := []struct {
		name string
		card func(*apitypes.Card)
		as   apitypes.Actor
		want string
	}{
		{"swept, nothing since", func(c *apitypes.Card) {
			c.Status, c.Claim = "ready", nil
			c.Events = []apitypes.Event{ev("card.comment", other, fixedNow.Add(-time.Minute), `{}`), expired, ev("card.claimed", me, fixedNow.Add(-50*time.Minute), `{}`)}
		}, me, lapsedNote},
		{"not swept yet", func(c *apitypes.Card) {
			c.Claim = &apitypes.Claim{Actor: me, Session: "test-session", ExpiresAt: fixedNow.Add(-12 * time.Minute), Owned: true}
		}, me, lapsedNote},
		{"a person's claim ran out where it was taken in another session", func(c *apitypes.Card) {
			c.Status, c.Claim = "ready", nil
			theirs := expired
			theirs.Actor = person
			theirs.Payload = json.RawMessage(`{"title":"Fix login","expired_at":"` + fixedNow.Add(-12*time.Minute).Format(time.RFC3339) + `"}`)
			c.Events = []apitypes.Event{theirs}
		}, person, lapsedNote},
		{"another chat's claim ran out", func(c *apitypes.Card) {
			c.Status, c.Claim = "ready", nil
			other := expired
			other.Payload = json.RawMessage(`{"title":"Fix login","session":"calm-heron"}`)
			c.Events = []apitypes.Event{other}
		}, me, plainNote},
		{"another agent's claim in a session of the same label", func(c *apitypes.Card) {
			c.Status, c.Claim = "ready", nil
			theirs := expired
			theirs.Actor = other
			c.Events = []apitypes.Event{theirs}
		}, me, plainNote},
		{"someone held and released it since", func(c *apitypes.Card) {
			c.Status, c.Claim = "ready", nil
			c.Events = []apitypes.Event{ev("card.released", other, fixedNow.Add(-2*time.Minute), `{}`), ev("card.claimed", other, fixedNow.Add(-5*time.Minute), `{}`), expired}
		}, me, plainNote},
		{"never held", func(c *apitypes.Card) { c.Status, c.Claim = "ready", nil }, me, plainNote},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cur := fixtureCard()
			tt.card(&cur)
			claimed := fixtureCard()
			claimed.Claim = &apitypes.Claim{Actor: tt.as, Session: "test-session", ExpiresAt: fixedNow.Add(30 * time.Minute), Mine: true, Owned: true}
			h := newHarness(t, serve(t, map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/42":             reply(200, cur),
				"POST /api/v1/projects/demo/cards/42/claim":      reply(200, claimed),
				"POST /api/v1/projects/demo/cards/42/transition": reply(200, fixtureCard()),
			}))
			if err := h.run(t, "-p", "demo", "done", "42"); err != nil {
				t.Fatal(err)
			}
			if got := h.errOut.String(); got != tt.want {
				t.Errorf("stderr = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDoneOwnClaimSkipsClaim(t *testing.T) {
	t.Parallel()
	var seq []string
	record := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			seq = append(seq, r.Method+" "+r.URL.Path)
			next(w, r)
		}
	}
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42":             record(reply(200, myCard())),
		"POST /api/v1/projects/demo/cards/42/transition": record(reply(200, fixtureCard())),
	})
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "done", "42"); err != nil {
		t.Fatal(err)
	}
	for _, s := range seq {
		if strings.HasSuffix(s, "/claim") {
			t.Errorf("unexpected claim in %v", seq)
		}
	}
	if h.errOut.String() != "" {
		t.Errorf("stderr = %q, want empty", h.errOut.String())
	}
}

func TestDoneOnCardClaimedByOtherExitsFourWithoutTransition(t *testing.T) {
	t.Parallel()
	other := fixtureCard() // working, claimed by claude
	var transitions int
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42": reply(200, other),
		"POST /api/v1/projects/demo/cards/42/claim": problem(409, apitypes.Problem{
			Title: "Conflict", Detail: "claimed by claude",
		}),
		"POST /api/v1/projects/demo/cards/42/transition": func(w http.ResponseWriter, r *http.Request) {
			transitions++
			reply(200, other)(w, r)
		},
	})
	h := newHarness(t, srv)
	err := h.run(t, "-p", "demo", "done", "42")
	if got := cmdutil.ExitCode(err); got != 4 {
		t.Fatalf("exit = %d (%v), want 4", got, err)
	}
	if transitions != 0 {
		t.Errorf("transitions = %d, want 0", transitions)
	}
}

func TestStartIssuesOneRequestWhenClaimShowsWorking(t *testing.T) {
	t.Parallel()
	var seq []string
	srv := serve(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			seq = append(seq, r.Method+" "+r.URL.Path)
			reply(200, fixtureCard())(w, r)
		},
	})
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "start", "42"); err != nil {
		t.Fatal(err)
	}
	// One claim, then the single name lookup for printing; no refetch, no transition.
	want := []string{"POST /api/v1/projects/demo/cards/42/claim", "GET /api/v1/actors"}
	if !reflect.DeepEqual(seq, want) {
		t.Errorf("requests = %v, want %v", seq, want)
	}
}

func TestSearchPrintsTheNextPageAndContinuesFromIt(t *testing.T) {
	t.Parallel()
	var cursors []string
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/search": func(w http.ResponseWriter, r *http.Request) {
			cursors = append(cursors, r.URL.Query().Get("cursor"))
			res := apitypes.SearchResult{Items: []apitypes.SearchHit{{Type: "note", ID: "6001", Slug: "arch", Title: "Architecture"}}}
			res.Next, res.Remaining = "tok", 3
			reply(200, res)(w, r)
		},
	})
	h := newHarness(t, srv)
	if err := h.run(t, "search", "session", "expiry", "-L", "1"); err != nil {
		t.Fatal(err)
	}
	if want := "note arch  Architecture  (org)\nremaining 3, next: lorvik search session expiry --limit 1 --cursor tok\n"; h.out.String() != want {
		t.Errorf("stdout = %q, want %q", h.out.String(), want)
	}
	h = newHarness(t, srv)
	if err := h.run(t, "search", "session", "expiry", "--cursor", "tok"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"", "tok"}; !slices.Equal(cursors, want) {
		t.Errorf("cursors = %q, want %q", cursors, want)
	}
}
