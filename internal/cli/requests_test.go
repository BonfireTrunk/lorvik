package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

// versioned is a reply that satisfies every GET-then-write command: a card,
// decision, note or org at version 4.
var versioned = map[string]any{
	"id": "1", "number": 7, "slug": "arch", "title": "T", "version": 4, "status": "proposed",
	"settings": map[string]any{"agent_approval": "admin", "token_ttl_default": "720h", "token_ttl_max": "2160h", "project_visibility_default": "org"},
}

func TestRequestShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		args  []string
		reply any
		// calls are "METHOD uri" in order, name lookups left out.
		calls []string
		// body is the JSON body of the last call, "" for none.
		body string
	}{
		// cards
		{"wait", []string{"-p", "demo", "wait", "42", "-m", "need creds"}, versioned,
			[]string{"GET /api/v1/projects/demo/cards/42", "POST /api/v1/projects/demo/cards/42/transition"},
			`{"version":4,"status":"waiting","note":"need creds"}`},
		{"block is an alias of wait", []string{"-p", "demo", "block", "42"}, versioned,
			[]string{"GET /api/v1/projects/demo/cards/42", "POST /api/v1/projects/demo/cards/42/transition"},
			`{"version":4,"status":"waiting"}`},
		{"edit assignee by kind", []string{"-p", "demo", "edit", "42", "--assignee", "agent:55"}, versioned,
			[]string{"GET /api/v1/projects/demo/cards/42", "PATCH /api/v1/projects/demo/cards/42"},
			`{"version":4,"assignee":{"kind":"agent","id":"55"}}`},
		{"edit clears the assignee with an empty id", []string{"-p", "demo", "edit", "42", "--assignee", ""}, versioned,
			[]string{"GET /api/v1/projects/demo/cards/42", "PATCH /api/v1/projects/demo/cards/42"},
			`{"version":4,"assignee":{"kind":"user","id":""}}`},
		// notes
		{"note show by slug", []string{"-p", "web", "note", "show", "arch"}, versioned,
			[]string{"GET /api/v1/notes/arch?project=web"}, ""},
		{"note show by id needs no project", []string{"note", "show", "106666050296328877"}, versioned,
			[]string{"GET /api/v1/notes/106666050296328877"}, ""},
		{"note show global", []string{"-p", "global", "note", "show", "arch"}, versioned,
			[]string{"GET /api/v1/notes/arch?project=global"}, ""},
		{"note show version", []string{"-p", "web", "note", "show", "arch", "--version", "2"}, versioned,
			[]string{"GET /api/v1/notes/arch/revisions/2?project=web"}, ""},
		{"note history", []string{"-p", "web", "note", "history", "arch"}, []apitypes.NoteRevision{},
			[]string{"GET /api/v1/notes/arch/revisions?limit=11&project=web"}, ""},
		{"note archive", []string{"-p", "web", "note", "archive", "arch"}, versioned,
			[]string{"GET /api/v1/notes/arch?project=web", "PATCH /api/v1/notes/arch?project=web"}, `{"version":4,"status":"archived"}`},
		{"note archive undo", []string{"-p", "web", "note", "archive", "arch", "--undo"}, versioned,
			[]string{"GET /api/v1/notes/arch?project=web", "PATCH /api/v1/notes/arch?project=web"}, `{"version":4,"status":"active"}`},
		{"note edit", []string{"-p", "web", "note", "edit", "arch", "--title", "New"}, versioned,
			[]string{"GET /api/v1/notes/arch?project=web", "PATCH /api/v1/notes/arch?project=web"}, `{"version":4,"title":"New"}`},
		{"note new", []string{"-p", "web", "note", "new", "run", "--title", "Run", "-b", "steps"}, versioned,
			[]string{"POST /api/v1/notes"}, `{"project":"web","slug":"run","title":"Run","body":"steps"}`},
		{"note ls archived", []string{"-p", "web", "note", "ls", "--archived", "-q", "x"}, apitypes.NoteList{},
			[]string{"GET /api/v1/notes?archived=true&limit=10&project=web&q=x"}, ""},
		{"note ls global", []string{"-p", "global", "note", "ls"}, apitypes.NoteList{},
			[]string{"GET /api/v1/notes?limit=10&project=global"}, ""},
		{"notes is an alias of note", []string{"notes", "search", "deploy"}, apitypes.NoteList{},
			[]string{"GET /api/v1/notes?limit=10&q=deploy"}, ""},
		// projects
		{"project create", []string{"project", "create", "payments", "--name", "Pay", "--visibility", "private", "--lease", "1h"}, versioned,
			[]string{"POST /api/v1/projects"}, `{"slug":"payments","name":"Pay","visibility":"private","lease":"1h"}`},
		{"project create defaults the name", []string{"project", "create", "payments"}, versioned,
			[]string{"POST /api/v1/projects"}, `{"slug":"payments","name":"payments"}`},
		{"project edit", []string{"-p", "web", "project", "edit", "--lease", "2h"}, versioned,
			[]string{"PATCH /api/v1/projects/web"}, `{"lease":"2h"}`},
		{"project archive", []string{"project", "archive", "web"}, versioned,
			[]string{"PATCH /api/v1/projects/web"}, `{"status":"archived"}`},
		{"project archive undo", []string{"project", "archive", "web", "--undo"}, versioned,
			[]string{"PATCH /api/v1/projects/web"}, `{"status":"active"}`},
		{"project ls", []string{"project", "ls"}, apitypes.ProjectList{}, []string{"GET /api/v1/projects?limit=50"}, ""},
		{"project show", []string{"project", "show", "web"}, versioned, []string{"GET /api/v1/projects/web"}, ""},
		{"project member add", []string{"-p", "web", "project", "member", "add", "user:5", "--role", "reviewer"}, versioned,
			[]string{"PUT /api/v1/projects/web/members/user/5"}, `{"role":"reviewer"}`},
		{"project member add defaults to a user", []string{"-p", "web", "project", "member", "add", "5"}, versioned,
			[]string{"PUT /api/v1/projects/web/members/user/5"}, `{"role":"member"}`},
		{"project member add agent", []string{"-p", "web", "project", "member", "add", "agent:6", "--rights", "work,triage"}, versioned,
			[]string{"PUT /api/v1/projects/web/members/agent/6"}, `{"rights":["work","triage"]}`},
		{"project member add agent defaults to work", []string{"-p", "web", "project", "member", "add", "agent:6"}, versioned,
			[]string{"PUT /api/v1/projects/web/members/agent/6"}, `{"rights":["work"]}`},
		{"project member remove", []string{"-p", "web", "project", "member", "remove", "agent:6"}, nil,
			[]string{"DELETE /api/v1/projects/web/members/agent/6"}, ""},
		{"project member ls", []string{"-p", "web", "project", "member", "ls"}, apitypes.ProjectMemberList{},
			[]string{"GET /api/v1/projects/web/members?limit=50"}, ""},
		// org members
		{"member ls all", []string{"member", "ls", "--all"}, apitypes.MemberList{}, []string{"GET /api/v1/members?all=true&limit=50"}, ""},
		{"member add", []string{"member", "add", "5", "--role", "admin"}, versioned, []string{"PUT /api/v1/members/user/5"}, `{"role":"admin"}`},
		{"member add agent", []string{"member", "add", "agent:6", "--rights", "ready"}, versioned, []string{"PUT /api/v1/members/agent/6"}, `{"rights":["ready"]}`},
		{"member remove", []string{"member", "remove", "5"}, nil, []string{"DELETE /api/v1/members/user/5"}, ""},
		// agents
		{"agent ls all", []string{"agent", "ls", "--all"}, apitypes.AgentList{}, []string{"GET /api/v1/agents?all=true&limit=50"}, ""},
		{"agent show", []string{"agent", "show", "55"}, versioned, []string{"GET /api/v1/agents/55"}, ""},
		{"agent approve", []string{"agent", "approve", "55"}, versioned, []string{"POST /api/v1/agents/55/approve"}, ""},
		{"agent revoke", []string{"agent", "revoke", "55"}, nil, []string{"DELETE /api/v1/agents/55"}, ""},
		{"agent restore", []string{"agent", "restore", "55"}, versioned, []string{"POST /api/v1/agents/55/restore"}, ""},
		{"agent remove", []string{"agent", "remove", "55"}, nil, []string{"POST /api/v1/agents/55/remove"}, ""},
		{"agent bind", []string{"-p", "api", "agent", "bind", "55", "--rights", "work,ready"}, versioned,
			[]string{"PUT /api/v1/agents/55/projects/api"}, `{"rights":["work","ready"]}`},
		{"agent unbind", []string{"-p", "api", "agent", "unbind", "55"}, nil, []string{"DELETE /api/v1/agents/55/projects/api"}, ""},
		// tokens and org
		{"token ls", []string{"token", "ls"}, apitypes.TokenList{}, []string{"GET /api/v1/tokens?limit=50"}, ""},
		{"tokens is an alias of token", []string{"tokens", "revoke", "9"}, nil, []string{"DELETE /api/v1/tokens/9"}, ""},
		{"org show", []string{"org", "show"}, versioned, []string{"GET /api/v1/org"}, ""},
		{"org set name only", []string{"org", "set", "--name", "Acme"}, versioned, []string{"PATCH /api/v1/org"}, `{"name":"Acme"}`},
		{"org set merges into the stored settings", []string{"org", "set", "--token-ttl-max", "100h"}, versioned,
			[]string{"GET /api/v1/org", "PATCH /api/v1/org"},
			`{"settings":{"agent_approval":"admin","token_ttl_default":"720h","token_ttl_max":"100h","project_visibility_default":"org"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{}
			status := 200
			if tt.reply == nil {
				status = 204
			}
			srv := serve(t, map[string]http.HandlerFunc{
				"/": rec.wrap(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodDelete {
						w.WriteHeader(http.StatusNoContent)
						return
					}
					if strings.HasPrefix(r.URL.Path, "/api/v1/actors") {
						reply(200, fixtureActors())(w, r)
						return
					}
					reply(status, tt.reply)(w, r)
				}),
			})
			h := newHarness(t, srv)
			if err := h.run(t, tt.args...); err != nil {
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
				if got := rec.last(t, strings.Join(strings.Fields(tt.calls[len(tt.calls)-1])[:1], "")).Body; got != tt.body {
					t.Errorf("body = %s, want %s", got, tt.body)
				}
			}
		})
	}
}

func TestEditAssigneeByBareIDLooksUpTheKind(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	srv := serve(t, rec.wrapAll(map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42":   reply(200, fixtureCard()),
		"PATCH /api/v1/projects/demo/cards/42": reply(200, fixtureCard()),
		"GET /api/v1/actors":                   reply(200, fixtureActors()),
	}))
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "edit", "42", "--assignee", claude.ID); err != nil {
		t.Fatal(err)
	}
	want := `{"version":7,"assignee":{"kind":"agent","id":"` + claude.ID + `"}}`
	if got := rec.last(t, "PATCH").Body; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}

	// An id nobody in the org has is a usage error, not a guess.
	srv = serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42": reply(200, fixtureCard()),
		"GET /api/v1/actors":                 reply(200, apitypes.Actors{}),
	})
	err := newHarness(t, srv).run(t, "-p", "demo", "edit", "42", "--assignee", "999")
	if err == nil || !strings.Contains(err.Error(), "no user or agent with id 999") {
		t.Errorf("err = %v", err)
	}
}

func TestCardAssigneeResolvesMeAndPrefixedIDs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, value, want string
	}{
		{"me uses the token's actor", "me", `"assignee":{"kind":"agent","id":"` + claude.ID + `"}`},
		{"prefixed id is sent as given", "user:" + ana.ID, `"assignee":{"kind":"user","id":"` + ana.ID + `"}`},
		{"bare id looks up the kind", ana.ID, `"assignee":{"kind":"user","id":"` + ana.ID + `"}`},
	}
	for _, command := range []struct{ name, arg, method string }{
		{"add", "T", "POST"}, {"edit", "42", "PATCH"},
	} {
		for _, tt := range tests {
			t.Run(command.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				rec := &recorder{}
				srv := serve(t, rec.wrapAll(map[string]http.HandlerFunc{
					"POST /api/v1/projects/demo/cards":     reply(201, fixtureCard()),
					"GET /api/v1/projects/demo/cards/42":   reply(200, fixtureCard()),
					"PATCH /api/v1/projects/demo/cards/42": reply(200, fixtureCard()),
					"GET /api/v1/me":                       reply(200, apitypes.Me{Actor: claude}),
					"GET /api/v1/actors":                   reply(200, fixtureActors()),
				}))
				if err := newHarness(t, srv).run(t, append([]string{"-p", "demo", command.name, command.arg, "--assignee"}, tt.value)...); err != nil {
					t.Fatal(err)
				}
				if got := rec.last(t, command.method).Body; !strings.Contains(got, tt.want) {
					t.Errorf("body = %s, want it to contain %s", got, tt.want)
				}
			})
		}
	}

	// Without the flag nothing is sent, and an empty value is a usage error.
	rec := &recorder{}
	srv := serve(t, rec.wrapAll(map[string]http.HandlerFunc{"POST /api/v1/projects/demo/cards": reply(201, fixtureCard())}))
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "add", "T"); err != nil {
		t.Fatal(err)
	}
	if got := rec.last(t, "POST").Body; strings.Contains(got, "assignee") {
		t.Errorf("body = %s, want no assignee", got)
	}
	if err := h.run(t, "-p", "demo", "add", "T", "--assignee", ""); cmdutil.ExitCode(err) != 2 {
		t.Errorf("err = %v, want usage error", err)
	}
}
