package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func storyEvent(typ string, num int64, payload string) apitypes.Event {
	return apitypes.Event{ID: "1", Type: typ, Actor: ana, Subject: apitypes.Subject{Type: "card", ID: "9", Number: num},
		Payload: json.RawMessage(payload), CreatedAt: ago(time.Hour)}
}

func storyCard() apitypes.Card {
	return apitypes.Card{
		ID: "6020", Number: 20, Project: "demo", Kind: "story", Status: "working", Title: "Webhook delivery", Priority: "normal",
		Body: "As an integrator, I want signed deliveries.", Lane: "webhooks", CreatedBy: ana, Version: 7,
		CreatedAt: ago(48 * time.Hour), UpdatedAt: ago(time.Hour), Progress: &apitypes.Progress{Done: 3, Total: 3},
		Children: []apitypes.CardBrief{
			{ID: "6021", Number: 21, Kind: "task", Status: "done", Title: "Add HMAC signing", Priority: "high", Parent: new(int64(20))},
			{ID: "6022", Number: 22, Kind: "task", Status: "done", Title: "Add retry policy", Priority: "normal", Lane: "api"},
			{ID: "6023", Number: 23, Kind: "task", Status: "dropped", Title: "Write the runbook", Priority: "normal"},
			{ID: "6024", Number: 24, Kind: "decision", Status: "done", Title: "Use a pgx pool", Priority: "normal"},
		},
		Links: []apitypes.CardLink{
			{Kind: "applies", Project: "global", Number: 3, Title: "Postgres table with SKIP LOCKED", Status: "done", Direction: "in"},
			{Kind: "relates", Number: 9, Title: "Old delivery", Status: "done", Direction: "out"},
		},
	}
}

func decisionCard() apitypes.Card {
	return apitypes.Card{
		ID: "6024", Number: 24, Project: "demo", Kind: "decision", Status: "done", Title: "Use a pgx pool", Priority: "normal",
		Body:      "## Context\n\nQueue needs SKIP LOCKED.\n\n## Decision\n\npgxpool, no database/sql.\n\n## Consequences\n\nNo driver portability.",
		Parent:    new(int64(20)),
		CreatedBy: claude, Version: 3, CreatedAt: ago(5 * time.Hour), UpdatedAt: ago(time.Hour),
		Decision: &apitypes.CardDecision{AcceptedBy: &ana, AcceptedAt: new(ago(2 * time.Hour)), Supersedes: new(int64(5))},
		Fold:     &apitypes.CardFold{Into: "docs/requirements/webhooks.md", At: ago(time.Minute), By: ana},
	}
}

func globalDecision() apitypes.Card {
	return apitypes.Card{
		ID: "7003", Number: 3, Project: "global", Kind: "decision", Status: "done", Title: "Postgres table with SKIP LOCKED", Priority: "normal",
		Body:    "## Context\n\nRedis adds a self-host dependency.\n\n## Decision\n\nPostgres table + SKIP LOCKED.\n\n## Consequences\n\nThroughput caps near 1k/s.",
		Version: 2, CreatedBy: ana,
	}
}

func child(num int64, title, note string) apitypes.Card {
	c := apitypes.Card{ID: "60" + itoa(num), Number: num, Project: "demo", Kind: "task", Status: "done", Title: title, Priority: "normal", Version: 3}
	c.ClosingNote = note
	return c
}

func storyRoutes() map[string]http.HandlerFunc {
	comments := map[int64][]apitypes.Comment{
		21: {{Actor: claude, Body: "ignored later chatter", CreatedAt: ago(30 * time.Minute)}, {Actor: claude, Body: "Done: signing in sign.go.\nLeft: nothing.", CreatedAt: ago(time.Hour)}},
		22: {{Actor: claude, Body: "Done: backoff 1s to 16s.", CreatedAt: ago(time.Hour)}},
	}
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/actors":                          reply(200, fixtureActors()),
		"GET /api/v1/projects/demo/cards/20":          reply(200, storyCard()),
		"GET /api/v1/projects/demo/cards/21":          reply(200, child(21, "Add HMAC signing", "hmac signing, tests green")),
		"GET /api/v1/projects/demo/cards/22":          reply(200, child(22, "Add retry policy", "five tries with jitter")),
		"GET /api/v1/projects/demo/cards/23":          reply(200, child(23, "Write the runbook", "")),
		"GET /api/v1/projects/demo/cards/24":          reply(200, decisionCard()),
		"GET /api/v1/projects/global/cards/3":         reply(200, globalDecision()),
		"GET /api/v1/projects/demo/cards/21/comments": reply(200, comments[21]),
		"GET /api/v1/projects/demo/cards/22/comments": reply(200, comments[22]),
		"GET /api/v1/projects/demo/cards/23/comments": reply(200, []apitypes.Comment{}),
	}
	return routes
}

func TestStoryOutputGolden(t *testing.T) {
	t.Parallel()
	unfolded := apitypes.CardList{Items: []apitypes.CardBrief{
		{ID: "6031", Number: 31, Kind: "task", Status: "done", Title: "Fix typo in README quickstart", Priority: "normal", UpdatedAt: ago(2 * time.Hour), ClosedAt: new(ago(30 * time.Minute))},
	}}
	decisions := apitypes.CardList{Items: []apitypes.CardBrief{
		{ID: "7003", Project: "global", Number: 3, Kind: "decision", Status: "done", Title: "Postgres table with SKIP LOCKED", Priority: "normal"},
		{ID: "6024", Project: "demo", Number: 24, Kind: "decision", Status: "ready", Title: "Use a pgx pool", Priority: "normal", Assignee: &ana},
	}}
	mixed := apitypes.CardList{Items: []apitypes.CardBrief{
		{ID: "6020", Number: 20, Kind: "story", Status: "working", Title: "Webhook delivery", Priority: "normal"},
		{ID: "6024", Number: 24, Kind: "decision", Status: "ready", Title: "Use a pgx pool", Priority: "normal"},
		{ID: "5003", Number: 17, Kind: "question", Status: "ready", Title: "Which db?", Priority: "normal"},
	}}
	routes := storyRoutes()
	routes["GET /api/v1/projects/demo/unfolded"] = reply(200, unfolded)
	routes["GET /api/v1/projects/demo/cards"] = reply(200, mixed)
	routes["GET /api/v1/cards"] = reply(200, decisions)
	routes["GET /api/v1/projects/demo/cards/31"] = reply(200, child(31, "Fix typo in README quickstart", ""))
	routes["POST /api/v1/projects/demo/cards/31/fold"] = reply(200, child(31, "Fix typo in README quickstart", ""))
	srv := serve(t, routes)

	tests := []struct {
		name string
		args []string
		// docs are requirements documents that exist in the working tree.
		docs []string
	}{
		{"show_story", []string{"show", "20"}, nil},
		{"show_decision", []string{"decisions", "show", "24"}, nil},
		{"fold_brief", []string{"fold", "20"}, []string{"api"}},
		{"fold_list", []string{"fold"}, nil},
		{"fold_done", []string{"fold", "31", "--no-contract-change"}, nil},
		{"decisions_ls", []string{"decisions", "ls", "--all"}, nil},
		{"ls_kinds", []string{"ls"}, nil},
	}
	for _, tt := range tests {
		for _, mode := range []string{"human", "json"} {
			t.Run(tt.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				h := newHarness(t, srv)
				for _, d := range tt.docs {
					path := filepath.Join(h.cwd, "docs", "requirements", d+".md")
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("# "+d), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				args := append([]string{"-p", "demo"}, tt.args...)
				if mode == "json" {
					args = append(args, "--json")
				}
				if err := h.run(t, args...); err != nil {
					t.Fatal(err)
				}
				if h.errOut.String() != "" {
					t.Errorf("stderr = %q, want empty", h.errOut.String())
				}
				golden(t, tt.name+"."+mode+".golden", h.out.String())
			})
		}
	}
}

func TestStoryRequests(t *testing.T) {
	t.Parallel()
	meAgent := reply(200, apitypes.Me{Actor: claude})
	meUser := reply(200, apitypes.Me{Actor: ana})
	card := reply(200, fixtureCard())
	decision := decisionCard()
	proposed := decision
	proposed.Status = "ready"
	proposed.Fold = nil
	tests := []struct {
		name   string
		args   []string
		routes map[string]http.HandlerFunc
		// calls are "METHOD uri" in order, name lookups left out.
		calls []string
		// body is the JSON body of the last call, "" for none.
		body string
	}{
		{"add story", []string{"add", "Webhooks", "--kind", "story", "--lane", "webhooks", "--ready"},
			map[string]http.HandlerFunc{"POST /api/v1/projects/demo/cards": card},
			[]string{"POST /api/v1/projects/demo/cards"}, `{"kind":"story","title":"Webhooks","status":"ready","lane":"webhooks"}`},
		{"add decision supersedes under a story", []string{"add", "Use pgx v6", "--kind", "decision", "--parent", "20", "--supersedes", "7"},
			map[string]http.HandlerFunc{"POST /api/v1/projects/demo/cards": card},
			[]string{"POST /api/v1/projects/demo/cards"}, `{"kind":"decision","title":"Use pgx v6","parent":20,"supersedes":7}`},
		{"ask under a story", []string{"ask", "Which queue?", "--parent", "20"},
			map[string]http.HandlerFunc{"POST /api/v1/projects/demo/cards": card},
			[]string{"POST /api/v1/projects/demo/cards"}, `{"kind":"question","title":"Which queue?","parent":20}`},
		{"ls story", []string{"ls", "--story", "20"},
			map[string]http.HandlerFunc{"GET /api/v1/projects/demo/cards": reply(200, apitypes.CardList{})},
			[]string{"GET /api/v1/projects/demo/cards?limit=10&parent=20"}, ""},
		{"ls kind decision", []string{"ls", "--kind", "decision"},
			map[string]http.HandlerFunc{"GET /api/v1/projects/demo/cards": reply(200, apitypes.CardList{})},
			[]string{"GET /api/v1/projects/demo/cards?kind=decision&limit=10"}, ""},
		{"done story with a fold target does not claim", []string{"done", "20", "-m", "shipped", "--into", "docs/requirements/webhooks.md"},
			map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/20":             reply(200, storyCard()),
				"POST /api/v1/projects/demo/cards/20/transition": card,
			},
			[]string{"GET /api/v1/projects/demo/cards/20", "POST /api/v1/projects/demo/cards/20/transition"},
			`{"version":7,"status":"done","note":"shipped","fold":{"into":"docs/requirements/webhooks.md"}}`},
		{"done story without a contract change", []string{"done", "20", "--no-contract-change"},
			map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/20":             reply(200, storyCard()),
				"POST /api/v1/projects/demo/cards/20/transition": card,
			},
			[]string{"GET /api/v1/projects/demo/cards/20", "POST /api/v1/projects/demo/cards/20/transition"},
			`{"version":7,"status":"done","fold":{"none":true}}`},
		{"drop story cascades", []string{"drop", "20", "--reason", "wontfix", "--cascade", "-m", "abandoned"},
			map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/20":             reply(200, storyCard()),
				"POST /api/v1/projects/demo/cards/20/transition": card,
			},
			[]string{"GET /api/v1/projects/demo/cards/20", "POST /api/v1/projects/demo/cards/20/transition"},
			`{"version":7,"status":"dropped","drop_reason":"wontfix","note":"abandoned","cascade":true}`},
		{"fold list", []string{"fold", "-L", "5", "--cursor", "tok"},
			map[string]http.HandlerFunc{"GET /api/v1/projects/demo/unfolded": reply(200, apitypes.CardList{})},
			[]string{"GET /api/v1/projects/demo/unfolded?cursor=tok&limit=5"}, ""},
		{"fold into", []string{"fold", "31", "--into", "docs/requirements/errors.md"},
			map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/31":       card,
				"POST /api/v1/projects/demo/cards/31/fold": card,
			},
			[]string{"GET /api/v1/projects/demo/cards/31", "POST /api/v1/projects/demo/cards/31/fold"},
			`{"version":7,"into":"docs/requirements/errors.md"}`},
		{"fold without a contract change", []string{"fold", "31", "--no-contract-change"},
			map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/31":       card,
				"POST /api/v1/projects/demo/cards/31/fold": card,
			},
			[]string{"GET /api/v1/projects/demo/cards/31", "POST /api/v1/projects/demo/cards/31/fold"},
			`{"version":7,"none":true}`},
		{"fold brief only reads", []string{"fold", "20"}, storyRoutes(),
			[]string{
				"GET /api/v1/projects/demo/cards/20",
				"GET /api/v1/projects/demo/cards/21", "GET /api/v1/projects/demo/cards/21/comments?limit=200",
				"GET /api/v1/projects/demo/cards/22", "GET /api/v1/projects/demo/cards/22/comments?limit=200",
				"GET /api/v1/projects/demo/cards/23", "GET /api/v1/projects/demo/cards/23/comments?limit=200",
				"GET /api/v1/projects/demo/cards/24", "GET /api/v1/projects/global/cards/3",
			}, ""},
		{"decide as an agent proposes", []string{"decide", "Use a pgx pool", "-m", "pgxpool.", "--context", "Needs SKIP LOCKED.", "--parent", "2"},
			map[string]http.HandlerFunc{"GET /api/v1/me": meAgent, "POST /api/v1/projects/demo/cards": card},
			[]string{"GET /api/v1/me", "POST /api/v1/projects/demo/cards"},
			`{"kind":"decision","title":"Use a pgx pool","body":"## Context\n\nNeeds SKIP LOCKED.\n\n## Decision\n\npgxpool.","parent":2}`},
		{"decide as a human records it done", []string{"decide", "Use pgx", "-m", "pgxpool.", "--consequences", "No portability.", "--supersedes", "7"},
			map[string]http.HandlerFunc{"GET /api/v1/me": meUser, "POST /api/v1/projects/demo/cards": card},
			[]string{"GET /api/v1/me", "POST /api/v1/projects/demo/cards"},
			`{"kind":"decision","title":"Use pgx","body":"## Decision\n\npgxpool.\n\n## Consequences\n\nNo portability.","status":"done","supersedes":7}`},
		{"decide --proposed as a human", []string{"decide", "Use pgx", "-m", "pgxpool.", "--proposed"},
			map[string]http.HandlerFunc{"POST /api/v1/projects/demo/cards": card},
			[]string{"POST /api/v1/projects/demo/cards"}, `{"kind":"decision","title":"Use pgx","body":"## Decision\n\npgxpool."}`},
		{"decisions ls defaults to the project", []string{"decisions", "ls"},
			map[string]http.HandlerFunc{"GET /api/v1/cards": reply(200, apitypes.CardList{})},
			[]string{"GET /api/v1/cards?kind=decision&limit=10&project=demo"}, ""},
		{"decisions ls all in force", []string{"decisions", "ls", "--all", "--in-force"},
			map[string]http.HandlerFunc{"GET /api/v1/cards": reply(200, apitypes.CardList{})},
			[]string{"GET /api/v1/cards?kind=decision&limit=10&project=all&status=done"}, ""},
		{"decisions ls global", []string{"-p", "global", "decisions", "ls", "--status", "ready,done"},
			map[string]http.HandlerFunc{"GET /api/v1/cards": reply(200, apitypes.CardList{})},
			[]string{"GET /api/v1/cards?kind=decision&limit=10&project=global&status=ready%2Cdone"}, ""},
		{"decisions search", []string{"decisions", "search", "queue", "backend", "-L", "3"},
			map[string]http.HandlerFunc{"GET /api/v1/cards": reply(200, apitypes.CardList{})},
			[]string{"GET /api/v1/cards?kind=decision&limit=3&project=demo&q=queue+backend"}, ""},
		{"decisions accept", []string{"decisions", "accept", "24"},
			map[string]http.HandlerFunc{"GET /api/v1/projects/demo/cards/24": reply(200, decision), "POST /api/v1/projects/demo/cards/24/transition": card},
			[]string{"GET /api/v1/projects/demo/cards/24", "POST /api/v1/projects/demo/cards/24/transition"}, `{"version":3,"status":"done"}`},
		{"decisions reject", []string{"decisions", "reject", "24", "-m", "not now"},
			map[string]http.HandlerFunc{"GET /api/v1/projects/demo/cards/24": reply(200, proposed), "POST /api/v1/projects/demo/cards/24/transition": card},
			[]string{"GET /api/v1/projects/demo/cards/24", "POST /api/v1/projects/demo/cards/24/transition"},
			`{"version":3,"status":"dropped","drop_reason":"rejected","note":"not now"}`},
		{"decisions revoke", []string{"decisions", "revoke", "24"},
			map[string]http.HandlerFunc{"GET /api/v1/projects/demo/cards/24": reply(200, decision), "POST /api/v1/projects/demo/cards/24/transition": card},
			[]string{"GET /api/v1/projects/demo/cards/24", "POST /api/v1/projects/demo/cards/24/transition"},
			`{"version":3,"status":"dropped","drop_reason":"revoked"}`},
		{"decisions edit keeps the other sections", []string{"decisions", "edit", "24", "--consequences", "Portability lost.", "--title", "Pool"},
			map[string]http.HandlerFunc{"GET /api/v1/projects/demo/cards/24": reply(200, proposed), "PATCH /api/v1/projects/demo/cards/24": card},
			[]string{"GET /api/v1/projects/demo/cards/24", "PATCH /api/v1/projects/demo/cards/24"},
			`{"version":3,"title":"Pool","body":"## Context\n\nQueue needs SKIP LOCKED.\n\n## Decision\n\npgxpool, no database/sql.\n\n## Consequences\n\nPortability lost."}`},
		{"decisions edit can drop a section", []string{"decisions", "edit", "24", "--context", ""},
			map[string]http.HandlerFunc{"GET /api/v1/projects/demo/cards/24": reply(200, proposed), "PATCH /api/v1/projects/demo/cards/24": card},
			[]string{"GET /api/v1/projects/demo/cards/24", "PATCH /api/v1/projects/demo/cards/24"},
			`{"version":3,"body":"## Decision\n\npgxpool, no database/sql.\n\n## Consequences\n\nNo driver portability."}`},
		{"decisions link across projects", []string{"-p", "global", "decisions", "link", "3", "42", "--to-project", "web"},
			map[string]http.HandlerFunc{"POST /api/v1/projects/global/cards/3/links": card},
			[]string{"POST /api/v1/projects/global/cards/3/links"}, `{"kind":"applies","to":42,"project":"web"}`},
		{"decisions unlink", []string{"decisions", "unlink", "7", "42", "--to-project", "web"},
			map[string]http.HandlerFunc{"DELETE /api/v1/projects/demo/cards/7/links/applies/42": reply(204, nil)},
			[]string{"DELETE /api/v1/projects/demo/cards/7/links/applies/42?project=web"}, ""},
		{"link applies", []string{"link", "7", "applies", "42"},
			map[string]http.HandlerFunc{"POST /api/v1/projects/demo/cards/7/links": card},
			[]string{"POST /api/v1/projects/demo/cards/7/links"}, `{"kind":"applies","to":42}`},
		{"answer decide", []string{"answer", "17", "postgres", "--decide"},
			map[string]http.HandlerFunc{"GET /api/v1/projects/demo/cards/17": card, "POST /api/v1/projects/demo/cards/17/answer": card},
			[]string{"GET /api/v1/projects/demo/cards/17", "POST /api/v1/projects/demo/cards/17/answer"}, `{"version":7,"answer":"postgres","decide":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{}
			routes := tt.routes
			routes["GET /api/v1/actors"] = reply(200, fixtureActors())
			h := newHarness(t, serve(t, rec.wrapAll(routes)))
			if err := h.run(t, append([]string{"-p", "demo"}, tt.args...)...); err != nil {
				t.Fatalf("%v: %v", tt.args, err)
			}
			var calls []string
			for _, c := range rec.calls() {
				if !strings.Contains(c, "/actors") {
					calls = append(calls, c)
				}
			}
			if strings.Join(calls, "\n") != strings.Join(tt.calls, "\n") {
				t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(tt.calls, "\n"))
			}
			if tt.body != "" {
				method := strings.Fields(tt.calls[len(tt.calls)-1])[0]
				if got := rec.last(t, method).Body; got != tt.body {
					t.Errorf("body = %s, want %s", got, tt.body)
				}
			}
		})
	}
}

func TestAnswerDecidePrintsTheNewCardOnStderr(t *testing.T) {
	t.Parallel()
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/17": reply(200, fixtureCard()),
		"POST /api/v1/projects/demo/cards/17/answer": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "/api/v1/projects/demo/cards/8")
			reply(200, fixtureCard())(w, r)
		},
		"GET /api/v1/actors": reply(200, fixtureActors()),
	})
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "answer", "17", "postgres", "--decide"); err != nil {
		t.Fatal(err)
	}
	if got, want := h.errOut.String(), "lorvik: decision recorded as #8\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestStoryUsageErrors(t *testing.T) {
	t.Parallel()
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/17": reply(200, fixtureCard()),
		"GET /api/v1/projects/demo/cards/20": reply(200, storyCard()),
		"GET /api/v1/actors":                 reply(200, fixtureActors()),
	})
	tests := []struct {
		name string
		args []string
	}{
		{"story with a parent", []string{"add", "S", "--kind", "story", "--parent", "3"}},
		{"supersedes on a task", []string{"add", "T", "--supersedes", "3"}},
		{"unknown kind", []string{"add", "T", "--kind", "epic"}},
		{"decide needs a message", []string{"decide", "Use pgx"}},
		{"done with both fold flags", []string{"done", "20", "--into", "a.md", "--no-contract-change"}},
		{"fold flags need a number", []string{"fold", "--no-contract-change"}},
		{"ls parent and story disagree", []string{"ls", "--parent", "1", "--story", "2"}},
		{"decisions accept on a task", []string{"decisions", "accept", "17"}},
		{"decisions show on a task", []string{"decisions", "show", "17"}},
		{"decisions edit nothing", []string{"decisions", "edit", "17"}},
		{"decisions ls in-force with status", []string{"decisions", "ls", "--in-force", "--status", "ready"}},
		{"fold brief of a task", []string{"fold", "17"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := newHarness(t, srv).run(t, append([]string{"-p", "demo"}, tt.args...)...)
			if got := cmdutil.ExitCode(err); got != 2 {
				t.Errorf("exit = %d (%v), want 2", got, err)
			}
		})
	}
}

func TestDecisionEditRefusesABodyOutsideTheSections(t *testing.T) {
	t.Parallel()
	free := decisionCard()
	free.Body = "We chose pgx because it is fast."
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/24": reply(200, free),
		"GET /api/v1/actors":                 reply(200, fixtureActors()),
	})
	err := newHarness(t, srv).run(t, "-p", "demo", "decisions", "edit", "24", "--decision", "x")
	if cmdutil.ExitCode(err) != 2 || !strings.Contains(err.Error(), "lorvik edit 24 -b") {
		t.Errorf("err = %v, want a usage error naming `lorvik edit 24 -b`", err)
	}
}

func TestServerRefusalsKeepTheirExitCodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		args   []string
		routes map[string]http.HandlerFunc
		want   int
		hint   string
	}{
		{"decide below reviewer is refused", []string{"decide", "Use pgx", "-m", "x"},
			map[string]http.HandlerFunc{
				"GET /api/v1/me":                   reply(200, apitypes.Me{Actor: ana}),
				"POST /api/v1/projects/demo/cards": problem(403, apitypes.Problem{Title: "Forbidden", Detail: "recording a decision as accepted needs the reviewer role", Hint: "pass --proposed to leave it for review"}),
			}, 5, "hint: pass --proposed to leave it for review"},
		{"agent cannot accept", []string{"decisions", "accept", "24"},
			map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/24":             reply(200, decisionCard()),
				"POST /api/v1/projects/demo/cards/24/transition": problem(403, apitypes.Problem{Title: "Forbidden", Hint: "ask a human reviewer"}),
			}, 5, "hint: ask a human reviewer"},
		{"story with open children", []string{"done", "20", "--into", "a.md"},
			map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/20":             reply(200, storyCard()),
				"POST /api/v1/projects/demo/cards/20/transition": problem(409, apitypes.Problem{Title: "Conflict", Detail: "story #20 has open children: #22", Hint: "finish or drop them, then run `lorvik fold 20`"}),
			}, 4, "hint: finish or drop them, then run `lorvik fold 20`"},
		{"story without a fold", []string{"done", "20"},
			map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/20":             reply(200, storyCard()),
				"POST /api/v1/projects/demo/cards/20/transition": problem(422, apitypes.Problem{Title: "Unprocessable", Detail: "a story closes with a fold", Hint: "pass --into <path> or --no-contract-change"}),
			}, 2, "hint: pass --into <path> or --no-contract-change"},
		{"fold of a card with a parent", []string{"fold", "31", "--no-contract-change"},
			map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/31":       reply(200, fixtureCard()),
				"POST /api/v1/projects/demo/cards/31/fold": problem(409, apitypes.Problem{Title: "Conflict", Hint: "fold the story instead"}),
			}, 4, "hint: fold the story instead"},
		{"drop story with open children", []string{"drop", "20", "--reason", "wontfix"},
			map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/20":             reply(200, storyCard()),
				"POST /api/v1/projects/demo/cards/20/transition": problem(409, apitypes.Problem{Title: "Conflict", Hint: "pass --cascade to drop #22 too"}),
			}, 4, "hint: pass --cascade to drop #22 too"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.routes["GET /api/v1/actors"] = reply(200, fixtureActors())
			err := newHarness(t, serve(t, tt.routes)).run(t, append([]string{"-p", "demo"}, tt.args...)...)
			if got := cmdutil.ExitCode(err); got != tt.want {
				t.Fatalf("exit = %d (%v), want %d", got, err, tt.want)
			}
			if msg := cmdutil.FormatError(err, nil); !strings.Contains(msg, tt.hint) {
				t.Errorf("message %q missing %q", msg, tt.hint)
			}
		})
	}
}

func TestSearchMarksKinds(t *testing.T) {
	t.Parallel()
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/search": reply(200, apitypes.SearchResult{Items: []apitypes.SearchHit{
			{Type: "card", Kind: "decision", Number: 3, Title: "Use pgx", Status: "done", Project: new("demo")},
			{Type: "card", Kind: "story", Number: 20, Title: "Webhooks", Status: "working", Project: new("demo")},
			{Type: "card", Kind: "question", Number: 17, Title: "Which db?", Status: "ready", Project: new("demo")},
			{Type: "card", Kind: "task", Number: 42, Title: "Fix login", Status: "ready"},
			{Type: "note", Slug: "arch", Title: "Architecture"},
		}}),
	})
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "search", "pgx"); err != nil {
		t.Fatal(err)
	}
	want := "card #3  done  D Use pgx  (demo)\ncard #20  working  S Webhooks  (demo)\ncard #17  ready  ? Which db?  (demo)\n" +
		"card #42  ready  Fix login  (org)\nnote arch  Architecture  (org)\n"
	if h.out.String() != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", h.out.String(), want)
	}
}

func TestDecisionBodyRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		in     decisionBody
		wantOK bool
	}{
		{"all sections", decisionBody{Context: "c", Decision: "d", Consequences: "q"}, true},
		{"only a decision", decisionBody{Decision: "d"}, true},
		{"multi-line text", decisionBody{Context: "a\n\nb", Decision: "d"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseDecisionBody(tt.in.render())
			if ok != tt.wantOK || got != tt.in {
				t.Errorf("parse(render) = %+v, %v; want %+v", got, ok, tt.in)
			}
		})
	}
	for _, body := range []string{"free text", "## Alternatives\n\nx", "intro\n\n## Decision\n\nd"} {
		if _, ok := parseDecisionBody(body); ok {
			t.Errorf("parseDecisionBody(%q) accepted a body outside the sections", body)
		}
	}
}
