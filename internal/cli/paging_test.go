package cli

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func TestListSendsCursorAndLimitAndFilters(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	h := newHarness(t, serve(t, rec.wrapAll(fixtureRoutes())))
	err := h.run(t, "-p", "demo", "ls", "--status", "ready,waiting", "--mine", "--open", "--kind", "task", "--parent", "12",
		"-q", "login", "-L", "25", "--cursor", "tok_en-1")
	if err != nil {
		t.Fatal(err)
	}
	want := "/api/v1/projects/demo/cards?cursor=tok_en-1&kind=task&limit=25&mine=true&open=true&parent=12&q=login&status=ready%2Cwaiting"
	if got := rec.last(t, "GET /api/v1/projects/demo/cards").URI; got != want {
		t.Errorf("request = %q, want %q", got, want)
	}
}

func TestPagingLineRepeatsTheFiltersSoTheNextPageIsTheSameList(t *testing.T) {
	t.Parallel()
	h := newHarness(t, serve(t, fixtureRoutes()))
	if err := h.run(t, "-p", "demo", "ls", "--status", "ready,waiting", "--mine", "-q", "log in", "-L", "3"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(h.out.String(), "\n"), "\n")
	got := lines[len(lines)-1]
	want := "remaining 4, next: lorvik ls --limit 3 --mine --project demo --query 'log in' --status ready --status waiting --cursor eyJsIjoiY2FyZHMifQ"
	if got != want {
		t.Errorf("paging line = %q, want %q", got, want)
	}
}

func TestPagingRemainingAtTheCapPrintsAPlus(t *testing.T) {
	t.Parallel()
	list := fixtureCards()
	list.Remaining = 100
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards": reply(200, list),
		"GET /api/v1/actors":              reply(200, fixtureActors()),
	})
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "ls"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(h.out.String(), "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "remaining 100+, next: lorvik ls ") {
		t.Errorf("paging line = %q, want it to start with %q", last, "remaining 100+, next: lorvik ls ")
	}
}

func TestPagingOnAnExhaustedListPrintsNothingExtra(t *testing.T) {
	t.Parallel()
	list := fixtureCards()
	list.Remaining = 0
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards": reply(200, list),
		"GET /api/v1/actors":              reply(200, fixtureActors()),
	})
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "ls"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.out.String(), "remaining") {
		t.Errorf("stdout = %q, want no paging line", h.out.String())
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"ready", "ready"},
		{"a_b-c.d/e:f=g@h,i+j", "a_b-c.d/e:f=g@h,i+j"},
		{"log in", "'log in'"},
		{"", "''"},
		{"it's", `'it'\''s'`},
		{"$HOME", "'$HOME'"},
	}
	for _, tt := range tests {
		if got := shellQuote(tt.in); got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNextCommandDropsOutputFlags(t *testing.T) {
	t.Parallel()
	root := NewRoot(&cmdutil.Factory{IOStreams: &cmdutil.IOStreams{}})
	var got string
	for _, c := range root.Commands() {
		if c.Name() == "ls" {
			c.RunE = func(cmd *cobra.Command, _ []string) error {
				got = (&app{}).nextCommand(cmd)("abc")
				return nil
			}
		}
	}
	root.SetArgs([]string{"ls", "--json", "-Q", "--api-url", "http://x", "--cursor", "old", "--open"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if want := "lorvik ls --open --cursor abc"; got != want {
		t.Errorf("next command = %q, want %q", got, want)
	}
}

func TestRankedSearchesPrintANextCommandThatKeepsTheQuery(t *testing.T) {
	t.Parallel()
	notes := apitypes.NoteList{}
	notes.Next, notes.Remaining = "tok", 3
	cards := apitypes.CardList{Items: []apitypes.CardBrief{{Number: 7, Kind: "decision", Status: "done", Title: "Use pgx", Priority: "normal"}}}
	cards.Next, cards.Remaining = "tok", 3
	notes.Items = []apitypes.NoteBrief{{ID: "1", Slug: "arch", Title: "Architecture"}}
	tests := []struct {
		name  string
		route string
		reply any
		args  []string
		want  string
	}{
		{"note search", "GET /api/v1/notes", notes, []string{"note", "search", "deploy", "keys"},
			"remaining 3, next: lorvik note search deploy keys --cursor tok\n"},
		{"decisions search", "GET /api/v1/cards", cards, []string{"decisions", "search", "queue", "backend"},
			"remaining 3, next: lorvik decisions search queue backend --cursor tok\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var cursors []string
			srv := serve(t, map[string]http.HandlerFunc{
				"GET /api/v1/actors": reply(200, fixtureActors()),
				tt.route: func(w http.ResponseWriter, r *http.Request) {
					cursors = append(cursors, r.URL.Query().Get("cursor"))
					reply(200, tt.reply)(w, r)
				},
			})
			h := newHarness(t, srv)
			if err := h.run(t, tt.args...); err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(h.out.String(), tt.want) {
				t.Errorf("stdout = %q, want suffix %q", h.out.String(), tt.want)
			}
			h = newHarness(t, srv)
			if err := h.run(t, append(tt.args, "--cursor", "tok")...); err != nil {
				t.Fatal(err)
			}
			if want := []string{"", "tok"}; !slices.Equal(cursors, want) {
				t.Errorf("cursors = %q, want %q", cursors, want)
			}
		})
	}
}

func TestOrgListsPageAndPrintTheNextCommand(t *testing.T) {
	t.Parallel()
	page := apitypes.Page{Next: "tok", Remaining: 3}
	tests := []struct {
		name  string
		args  []string
		route string
		reply any
		want  string
	}{
		{"projects", []string{"project", "ls"}, "GET /api/v1/projects", apitypes.ProjectList{Page: page}, "lorvik project ls --cursor tok"},
		{"project members", []string{"-p", "web", "project", "member", "ls"}, "GET /api/v1/projects/web/members", apitypes.ProjectMemberList{Page: page}, "lorvik project member ls --project web --cursor tok"},
		{"members", []string{"member", "ls", "--all"}, "GET /api/v1/members", apitypes.MemberList{Page: page}, "lorvik member ls --all --cursor tok"},
		{"agents", []string{"agent", "ls"}, "GET /api/v1/agents", apitypes.AgentList{Page: page}, "lorvik agent ls --cursor tok"},
		{"tokens", []string{"token", "ls"}, "GET /api/v1/tokens", apitypes.TokenList{Page: page}, "lorvik token ls --cursor tok"},
		{"locks", []string{"locks"}, "GET /api/v1/locks", apitypes.LockList{Page: page}, "lorvik locks --cursor tok"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var queries []string
			srv := serve(t, map[string]http.HandlerFunc{
				"GET /api/v1/actors": reply(200, fixtureActors()),
				tt.route: func(w http.ResponseWriter, r *http.Request) {
					queries = append(queries, r.URL.Query().Get("limit")+"/"+r.URL.Query().Get("cursor"))
					reply(200, tt.reply)(w, r)
				},
			})
			h := newHarness(t, srv)
			if err := h.run(t, tt.args...); err != nil {
				t.Fatal(err)
			}
			if want := "remaining 3, next: " + tt.want + "\n"; !strings.HasSuffix(h.out.String(), want) {
				t.Errorf("stdout = %q, want suffix %q", h.out.String(), want)
			}
			h = newHarness(t, srv)
			if err := h.run(t, append(tt.args, "-L", "7", "--cursor", "tok")...); err != nil {
				t.Fatal(err)
			}
			if want := []string{"50/", "7/tok"}; !slices.Equal(queries, want) {
				t.Errorf("limit/cursor = %q, want %q", queries, want)
			}
		})
	}
}

func TestCommandsThatNeedEveryRowFollowAllPages(t *testing.T) {
	t.Parallel()
	brief := func(n int64, kind, title string) apitypes.CardBrief {
		return apitypes.CardBrief{Number: n, Kind: kind, Status: "ready", Title: title, Priority: "normal"}
	}
	story := apitypes.Card{
		Number: 20, Project: "demo", Kind: "story", Status: "working", Title: "Big", Priority: "normal", Version: 1,
		Children: []apitypes.CardBrief{brief(21, "task", "Embedded")}, ChildrenRemaining: 2,
	}
	children := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("parent") != "20" {
			reply(200, apitypes.CardList{})(w, r)
			return
		}
		if r.URL.Query().Get("cursor") == "" {
			list := apitypes.CardList{Items: []apitypes.CardBrief{brief(22, "decision", "Later decision"), brief(21, "task", "Embedded")}}
			list.Next, list.Remaining = "p2", 1
			reply(200, list)(w, r)
			return
		}
		reply(200, apitypes.CardList{Items: []apitypes.CardBrief{brief(23, "task", "Third")}})(w, r)
	}
	lockOf := func(name string) apitypes.Lock { l := fixtureLock(); l.Name = name; return l }
	locks := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			list := apitypes.LockList{Items: []apitypes.Lock{lockOf("first-lock")}}
			list.Next, list.Remaining = "p2", 1
			reply(200, list)(w, r)
			return
		}
		reply(200, apitypes.LockList{Items: []apitypes.Lock{lockOf("second-lock")}})(w, r)
	}
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/actors":                 reply(200, fixtureActors()),
		"GET /api/v1/locks":                  locks,
		"GET /api/v1/projects/demo":          reply(200, apitypes.Project{Slug: "demo"}),
		"GET /api/v1/projects/demo/cards/20": reply(200, story),
		"GET /api/v1/projects/demo/cards":    children,
	}
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"show lists every child and decision", []string{"-p", "demo", "show", "20"}, []string{"Embedded", "Later decision", "Third"}},
		{"status lists every lock", []string{"-p", "demo", "status"}, []string{"first-lock", "second-lock"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, serve(t, routes))
			if err := h.run(t, tt.args...); err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(h.out.String(), want) {
					t.Errorf("stdout lacks %q:\n%s", want, h.out.String())
				}
			}
		})
	}
	t.Run("show json reports nothing left out", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, serve(t, routes))
		if err := h.run(t, "-p", "demo", "show", "20", "--json"); err != nil {
			t.Fatal(err)
		}
		if out := h.out.String(); strings.Contains(out, "children_remaining") || !strings.Contains(out, "Third") {
			t.Errorf("stdout = %s", out)
		}
	})
}
