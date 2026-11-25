package cli

import (
	"net/http"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func TestHumanOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		args  []string
		route string
		reply any
		want  string
	}{
		{"status uses waiting and shows project settings", []string{"-p", "web", "status"}, "GET /api/v1/projects/web",
			apitypes.Project{Slug: "web", Name: "Web", Visibility: "private", Concurrency: "serial", Lease: "30m", Status: "active", Role: "admin",
				Counts: &apitypes.StatusCounts{Backlog: 1, Ready: 2, Working: 3, Waiting: 4, Done: 5, Dropped: 6, OpenQuestions: 7}},
			"web  Web\nsession test-session (LORVIK_SESSION)\nvisibility private  concurrency serial  lease 30m  status active  role admin\n" +
				"backlog 1  ready 2  working 3  waiting 4  done 5  dropped 6\nopen questions: 7\nmy cards (this session): none\nsessions holding cards: none\n"},
		{"comment list", []string{"-p", "web", "comment", "42"}, "GET /api/v1/projects/web/cards/42/comments",
			[]apitypes.Comment{{ID: "1", Card: 42, Actor: claude, Body: "Done: parser.\nLeft: tests.", CreatedAt: ago(2 * time.Hour)}},
			"@claude-code (agent)  2h ago\n  Done: parser.\n  Left: tests.\n"},
		{"comment list pages", []string{"-p", "web", "comment", "42", "-L", "1"}, "GET /api/v1/projects/web/cards/42/comments",
			[]apitypes.Comment{{ID: "9", Actor: claude, Body: "a", CreatedAt: ago(time.Hour)}, {ID: "8", Actor: ana, Body: "b", CreatedAt: ago(2 * time.Hour)}},
			"@claude-code (agent)  1h ago\n  a\nolder, next: lorvik comment 42 --limit 1 --project web --cursor 9\n"},
		{"comment add", []string{"-p", "web", "comment", "42", "-m", "note"}, "POST /api/v1/projects/web/cards/42/comments",
			apitypes.Comment{ID: "1"}, "comment added to #42\n"},
		{"note history", []string{"-p", "web", "note", "history", "arch"}, "GET /api/v1/notes/arch/revisions",
			[]apitypes.NoteRevision{{Version: 3, Title: "Arch", UpdatedBy: claude, CreatedAt: ago(time.Hour)}, {Version: 2, Title: "Arch", UpdatedBy: ana, CreatedAt: ago(5 * time.Hour)}},
			"v3  Arch  @claude-code (agent)  1h ago\nv2  Arch  @Ana Souza  5h ago\n"},
		{"note revision", []string{"-p", "web", "note", "show", "arch", "--version", "2"}, "GET /api/v1/notes/arch/revisions/2",
			apitypes.NoteRevision{Version: 2, Title: "Arch", Body: "old text\n", UpdatedBy: ana, CreatedAt: ago(5 * time.Hour)},
			"arch  v2  Arch  @Ana Souza  5h ago\n\nold text\n"},
		{"project ls", []string{"project", "ls"}, "GET /api/v1/projects",
			apitypes.ProjectList{Items: []apitypes.Project{{Slug: "global", Name: "Global", Visibility: "org", Scope: "global", Role: "admin", Status: "active"}, {Slug: "web", Name: "Web", Visibility: "org", Scope: "project", Role: "admin", Status: "active"}, {Slug: "old", Name: "Old", Visibility: "private", Status: "archived"}}},
			"global  Global  org  admin  active  global\nweb  Web  org  admin  active\nold  Old  private  -  archived\n"},
		{"project member ls", []string{"-p", "web", "project", "member", "ls"}, "GET /api/v1/projects/web/members",
			apitypes.ProjectMemberList{Items: []apitypes.ProjectMember{{Project: "web", Actor: claude, Role: "reviewer"}}},
			"agent  " + claude.ID + "  claude-code  reviewer\n"},
		{"member ls", []string{"member", "ls"}, "GET /api/v1/members",
			apitypes.MemberList{Items: []apitypes.Membership{{Actor: ana, Role: "owner", Status: "active"}}},
			"user  " + ana.ID + "  Ana Souza  owner  active\n"},
		{"agent show with project rights", []string{"agent", "show", "5"}, "GET /api/v1/agents/5",
			apitypes.Agent{ID: "5", Name: "bot", Harness: "codex", Status: "active", CreatedAt: ago(time.Hour), Projects: []apitypes.ProjectRole{
				{Project: "web", Role: "viewer", Rights: []string{"work", "ready"}}, {Project: "api", Role: "viewer"}}},
			"5  bot  codex  -  active  created 1h ago\nproject web: work, ready\nproject api: read only\n"},
		{"agent show with org rights and chats", []string{"agent", "show", "5"}, "GET /api/v1/agents/5",
			apitypes.Agent{ID: "5", Name: "bot", Harness: "codex", Status: "active", CreatedAt: ago(time.Hour), Role: "viewer", Rights: []string{"work"}, Sessions: []apitypes.AgentSession{
				{Label: "quiet-heron-1a2b", LastSeenAt: new(ago(5 * time.Minute)), Grants: []apitypes.SessionGrant{{Project: "web", Rights: []string{"ready"}}, {Project: "api", Rights: []string{"ready", "triage"}}}},
				{Label: "mellow-comet-020d", LastSeenAt: new(ago(2 * time.Hour))}, {Label: "witty-lynx-c123"}}},
			"5  bot  codex  -  active  created 1h ago\nrights work (org-wide)\nsessions:\n  quiet-heron-1a2b  seen 5m ago  web: ready  api: ready, triage\n  mellow-comet-020d  seen 2h ago\n  witty-lynx-c123  never seen\n"},
		{"agent show unbound", []string{"agent", "show", "5"}, "GET /api/v1/agents/5",
			apitypes.Agent{ID: "5", Name: "bot", Status: "active", CreatedAt: ago(time.Hour)},
			"5  bot  -  -  active  created 1h ago\nbound to no project\n"},
		{"token ls", []string{"token", "ls"}, "GET /api/v1/tokens",
			apitypes.TokenList{Items: []apitypes.Token{{ID: "9", Prefix: "lvk_u_AAAAAA", Name: "laptop", Scope: "write", Status: "active", ExpiresAt: fixedNow.Add(48 * time.Hour), LastUsedAt: new(ago(time.Minute))}}},
			"9  lvk_u_AAAAAA  laptop  write  active  expires in 2d  used 1m ago\n"},
		{"org show for a member sees no settings", []string{"org", "show"}, "GET /api/v1/org",
			apitypes.Org{ID: "1", Name: "Acme", Status: "active"}, "1  Acme  active\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			status := 200
			if tt.route[:4] == "POST" {
				status = 201
			}
			srv := serve(t, map[string]http.HandlerFunc{
				tt.route:                         reply(status, tt.reply),
				"GET /api/v1/projects/web/cards": reply(200, apitypes.CardList{}),
				"GET /api/v1/actors":             reply(200, fixtureActors()),
			})
			h := newHarness(t, srv)
			if err := h.run(t, tt.args...); err != nil {
				t.Fatal(err)
			}
			if h.out.String() != tt.want {
				t.Errorf("stdout:\n%q\nwant:\n%q", h.out.String(), tt.want)
			}
		})
	}
}

// An agent caller's project line names its rights, which a bare viewer role
// would hide; a person's keeps the role.
func TestProjectMetaNamesAgentRights(t *testing.T) {
	t.Parallel()
	base := apitypes.Project{Visibility: "org", Concurrency: "parallel", Lease: "30m", Status: "active", Role: "viewer"}
	if got, want := projectMeta(base), "visibility org  concurrency parallel  lease 30m  status active  role viewer"; got != want {
		t.Errorf("person = %q, want %q", got, want)
	}
	base.Rights = []string{"work", "ready"}
	if got, want := projectMeta(base), "visibility org  concurrency parallel  lease 30m  status active  rights work, ready"; got != want {
		t.Errorf("agent = %q, want %q", got, want)
	}
}
