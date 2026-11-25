package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

const pageHTML = "<!doctype html>\n<p class=\"a\">1 < 2 && 3 > 2</p>"

func writePage(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "page.html")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPageSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		n    int
		want string
	}{
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1 KB"},
		{43_000, "41 KB"},
		{1<<20 - 1, "1023 KB"},
		{1 << 20, "1.0 MB"},
		{1<<20 + 1<<19, "1.5 MB"},
	}
	for _, tt := range tests {
		if got := pageSize(tt.n); got != tt.want {
			t.Errorf("pageSize(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestShowPrintsThePageLineAfterTheMetaLine(t *testing.T) {
	t.Parallel()
	card := fixtureCard()
	card.PageBytes = 43_000
	note := fixtureNote()
	note.PageBytes = 900
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42": reply(200, card),
		"GET /api/v1/notes/architecture":     reply(200, note),
	})
	tests := []struct {
		name string
		args []string
	}{
		{"card_page", []string{"show", "42"}},
		{"note_page", []string{"note", "show", "architecture"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, srv)
			if err := h.run(t, append([]string{"-p", "demo"}, tt.args...)...); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(h.out.String(), "<") {
				t.Errorf("output holds HTML:\n%s", h.out.String())
			}
			golden(t, tt.name+".human.golden", strings.ReplaceAll(h.out.String(), srv.URL, "http://dashboard.test"))
		})
	}
}

func TestShowWithAPageAndNoBodyPrintsNoEmptyBodySection(t *testing.T) {
	t.Parallel()
	card := fixtureCard()
	card.Body, card.PageBytes = "", 10
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42": reply(200, card),
	}))
	if err := h.run(t, "-p", "demo", "show", "42"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	if !strings.Contains(out, "\nPage: 10 B, open http://") || strings.Contains(out, "\n\n\n") {
		t.Errorf("want the Page line and no empty body section:\n%s", out)
	}
}

func TestShowPageSourceIsExactAndJSONIsTheHTMLPage(t *testing.T) {
	t.Parallel()
	page := apitypes.HTMLPage{HTML: pageHTML, Bytes: len(pageHTML), Version: 7}
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42/page": reply(200, page),
		"GET /api/v1/notes/architecture/page":     reply(200, page),
	})
	tests := []struct {
		name string
		args []string
	}{
		{"card", []string{"show", "42", "--page"}},
		{"note", []string{"note", "show", "architecture", "--page"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, srv)
			if err := h.run(t, append([]string{"-p", "demo"}, tt.args...)...); err != nil {
				t.Fatal(err)
			}
			if h.out.String() != pageHTML {
				t.Errorf("stdout = %q, want exactly the page source %q", h.out.String(), pageHTML)
			}
			h.out.Reset()
			if err := h.run(t, append(append([]string{"-p", "demo"}, tt.args...), "--json")...); err != nil {
				t.Fatal(err)
			}
			if got := h.out.String(); !strings.Contains(got, `"html": "<!doctype html>\n<p class=\"a\">1 < 2 && 3 > 2</p>"`) || !strings.Contains(got, `"version": 7`) {
				t.Errorf("--json = %s, want the HTMLPage unescaped", got)
			}
		})
	}
}

func TestPastVersionPage(t *testing.T) {
	t.Parallel()
	withPage := apitypes.CardRevision{ID: "1", Version: 2, Title: "Old", Body: "text\n", Page: pageHTML, UpdatedBy: ana, CreatedAt: ago(5 * time.Hour)}
	noPage := withPage
	noPage.Page = ""
	noteWith := apitypes.NoteRevision{ID: "1", Version: 2, Title: "Old", Body: "text\n", Page: pageHTML, UpdatedBy: ana, CreatedAt: ago(5 * time.Hour)}
	noteNone := noteWith
	noteNone.Page = ""
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42/revisions/2": reply(200, withPage),
		"GET /api/v1/projects/demo/cards/43/revisions/2": reply(200, noPage),
		"GET /api/v1/notes/arch/revisions/2":             reply(200, noteWith),
		"GET /api/v1/notes/plain/revisions/2":            reply(200, noteNone),
		"GET /api/v1/actors":                             reply(200, fixtureActors()),
	})
	size := pageSize(len(pageHTML))
	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr string
	}{
		{"card --page prints the source", []string{"history", "42", "--version", "2", "--page"}, pageHTML, ""},
		{"note --page prints the source", []string{"note", "show", "arch", "--version", "2", "--page"}, pageHTML, ""},
		{"card without a page exits 3", []string{"history", "43", "--version", "2", "--page"}, "", "card #43 version 2 has no HTML page"},
		{"note without a page exits 3", []string{"note", "show", "plain", "--version", "2", "--page"}, "", "note plain version 2 has no HTML page"},
		{"card view names the print command", []string{"history", "42", "--version", "2"},
			"#42  v2  @Ana Souza  5h ago\nPage: " + size + ", print it with lorvik history 42 --version 2 --page\nOld\n\ntext\n", ""},
		{"note view names the print command", []string{"note", "show", "arch", "--version", "2"},
			"arch  v2  Old  @Ana Souza  5h ago\nPage: " + size + ", print it with lorvik note show arch --page --version 2\n\ntext\n", ""},
		{"card view without a page has no Page line", []string{"history", "43", "--version", "2"}, "#43  v2  @Ana Souza  5h ago\nOld\n\ntext\n", ""},
		{"history --page needs --version", []string{"history", "42", "--page"}, "", "--page needs --version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, srv)
			err := h.run(t, append([]string{"-p", "demo"}, tt.args...)...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(cmdutil.FormatError(err, nil), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if h.out.String() != tt.want {
				t.Errorf("stdout = %q, want %q", h.out.String(), tt.want)
			}
		})
	}
}

func TestShowPageWithoutAPageExitsThree(t *testing.T) {
	t.Parallel()
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42/page": problem(404, apitypes.Problem{Title: "Not Found"}),
		"GET /api/v1/projects/demo/cards/42":      reply(200, fixtureCard()),
		"GET /api/v1/projects/demo/cards/43/page": problem(404, apitypes.Problem{Title: "Not Found"}),
		"GET /api/v1/projects/demo/cards/43":      problem(404, apitypes.Problem{Title: "Not Found", Detail: "card 43 not found"}),
		"GET /api/v1/notes/architecture/page":     problem(404, apitypes.Problem{Title: "Not Found"}),
		"GET /api/v1/notes/architecture":          reply(200, fixtureNote()),
	})
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"card", []string{"show", "42", "--page"}, "card #42 has no HTML page"},
		{"note", []string{"note", "show", "architecture", "--page"}, "note architecture has no HTML page"},
		{"missing card keeps the server error", []string{"show", "43", "--page"}, "card 43 not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, srv)
			err := h.run(t, append([]string{"-p", "demo"}, tt.args...)...)
			if got := cmdutil.ExitCode(err); got != 3 {
				t.Fatalf("exit = %d (%v), want 3", got, err)
			}
			if msg := cmdutil.FormatError(err, nil); !strings.Contains(msg, tt.wantErr) {
				t.Errorf("message = %q, want %q", msg, tt.wantErr)
			}
			if h.out.String() != "" {
				t.Errorf("stdout = %q, want empty", h.out.String())
			}
		})
	}
}

// pageFlagRoutes serve the routes every page-writing command touches and
// record the decoded bodies.
type pageWrites struct {
	create  apitypes.CreateCard
	update  apitypes.UpdateCard
	newNote apitypes.CreateNote
	upNote  apitypes.UpdateNote
}

func pageServer(t *testing.T) (*recorder, *pageWrites, *harness) {
	t.Helper()
	w := &pageWrites{}
	rec := &recorder{}
	srv := serve(t, rec.wrapAll(map[string]http.HandlerFunc{
		"POST /api/v1/projects/demo/cards": func(rw http.ResponseWriter, r *http.Request) {
			w.create = readJSON[apitypes.CreateCard](t, r)
			reply(201, fixtureCard())(rw, r)
		},
		"GET /api/v1/projects/demo/cards/42": reply(200, fixtureCard()),
		"PATCH /api/v1/projects/demo/cards/42": func(rw http.ResponseWriter, r *http.Request) {
			w.update = readJSON[apitypes.UpdateCard](t, r)
			reply(200, fixtureCard())(rw, r)
		},
		"POST /api/v1/notes": func(rw http.ResponseWriter, r *http.Request) {
			w.newNote = readJSON[apitypes.CreateNote](t, r)
			reply(201, fixtureNote())(rw, r)
		},
		"GET /api/v1/notes/architecture": reply(200, fixtureNote()),
		"PATCH /api/v1/notes/architecture": func(rw http.ResponseWriter, r *http.Request) {
			w.upNote = readJSON[apitypes.UpdateNote](t, r)
			reply(200, fixtureNote())(rw, r)
		},
	}))
	return rec, w, newHarness(t, srv)
}

func TestPageFileIsReadFromAFileOrStdinOnEveryWriter(t *testing.T) {
	t.Parallel()
	path := writePage(t, pageHTML)
	tests := []struct {
		name  string
		stdin string
		args  func(page string) []string
		got   func(w *pageWrites) string
	}{
		{"add file", "", func(p string) []string { return []string{"add", "T", "--page-file", p} }, func(w *pageWrites) string { return w.create.Page }},
		{"add stdin", pageHTML, func(string) []string { return []string{"add", "T", "--page-file", "-"} }, func(w *pageWrites) string { return w.create.Page }},
		{"ask file", "", func(p string) []string { return []string{"ask", "Q?", "--page-file", p} }, func(w *pageWrites) string { return w.create.Page }},
		{"edit file", "", func(p string) []string { return []string{"edit", "42", "--page-file", p} }, func(w *pageWrites) string { return derefStr(w.update.Page) }},
		{"edit stdin", pageHTML, func(string) []string { return []string{"edit", "42", "--page-file", "-"} }, func(w *pageWrites) string { return derefStr(w.update.Page) }},
		{"note new file", "", func(p string) []string { return []string{"note", "new", "arch", "--title", "A", "--page-file", p} }, func(w *pageWrites) string { return w.newNote.Page }},
		{"note edit stdin", pageHTML, func(string) []string { return []string{"note", "edit", "architecture", "--page-file", "-"} }, func(w *pageWrites) string { return derefStr(w.upNote.Page) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, w, h := pageServer(t)
			if tt.stdin != "" {
				h.streams.In = strings.NewReader(tt.stdin)
			}
			if err := h.run(t, append([]string{"-p", "demo"}, tt.args(path)...)...); err != nil {
				t.Fatal(err)
			}
			if got := tt.got(w); got != pageHTML {
				t.Errorf("page sent = %q, want the file content byte for byte %q", got, pageHTML)
			}
		})
	}
}

func derefStr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestPageFileEmptyRemovesOnEditAndMeansNoPageOnCreate(t *testing.T) {
	t.Parallel()
	t.Run("edit removes", func(t *testing.T) {
		t.Parallel()
		_, w, h := pageServer(t)
		if err := h.run(t, "-p", "demo", "edit", "42", "--page-file", ""); err != nil {
			t.Fatalf("a page change must count as an edit: %v", err)
		}
		if w.update.Page == nil || *w.update.Page != "" {
			t.Errorf("update page = %v, want a pointer to the empty string", w.update.Page)
		}
	})
	t.Run("note edit removes", func(t *testing.T) {
		t.Parallel()
		_, w, h := pageServer(t)
		if err := h.run(t, "-p", "demo", "note", "edit", "architecture", "--page-file", ""); err != nil {
			t.Fatal(err)
		}
		if w.upNote.Page == nil || *w.upNote.Page != "" {
			t.Errorf("update page = %v, want a pointer to the empty string", w.upNote.Page)
		}
	})
	t.Run("add sends no page", func(t *testing.T) {
		t.Parallel()
		rec, w, h := pageServer(t)
		if err := h.run(t, "-p", "demo", "add", "T", "--page-file", ""); err != nil {
			t.Fatal(err)
		}
		if w.create.Page != "" || strings.Contains(rec.last(t, "POST /api/v1/projects/demo/cards").Body, `"page"`) {
			t.Errorf("create body carries a page: %+v", w.create)
		}
	})
	t.Run("edit without page leaves it alone", func(t *testing.T) {
		t.Parallel()
		_, w, h := pageServer(t)
		if err := h.run(t, "-p", "demo", "edit", "42", "--priority", "high"); err != nil {
			t.Fatal(err)
		}
		if w.update.Page != nil {
			t.Errorf("update page = %q, want nil", *w.update.Page)
		}
	})
}

func TestNoteNewNeedsABodyOrAPage(t *testing.T) {
	t.Parallel()
	rec, w, h := pageServer(t)
	err := h.run(t, "-p", "demo", "note", "new", "arch", "--title", "A")
	if got := cmdutil.ExitCode(err); got != 2 {
		t.Fatalf("exit = %d (%v), want 2", got, err)
	}
	if len(rec.calls()) != 0 {
		t.Errorf("requests = %v, want none", rec.calls())
	}
	if err := h.run(t, "-p", "demo", "note", "new", "arch", "--title", "A", "--page-file", writePage(t, pageHTML)); err != nil {
		t.Fatalf("a page alone must be enough: %v", err)
	}
	if w.newNote.Body != "" || w.newNote.Page != pageHTML {
		t.Errorf("create = %+v", w.newNote)
	}
}

func TestBodyAndPageBothReadingStdinIsAUsageError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{"add", []string{"add", "T", "-b", "-", "--page-file", "-"}},
		{"ask", []string{"ask", "Q?", "-b", "-", "--page-file", "-"}},
		{"edit", []string{"edit", "42", "-b", "-", "--page-file", "-"}},
		{"note new", []string{"note", "new", "arch", "-b", "-", "--page-file", "-"}},
		{"note edit", []string{"note", "edit", "architecture", "-b", "-", "--page-file", "-"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec, _, h := pageServer(t)
			h.streams.In = strings.NewReader("x")
			err := h.run(t, append([]string{"-p", "demo"}, tt.args...)...)
			if got := cmdutil.ExitCode(err); got != 2 {
				t.Fatalf("exit = %d (%v), want 2", got, err)
			}
			for _, c := range rec.calls() {
				if !strings.HasPrefix(c, "GET ") {
					t.Errorf("write request %q sent despite the usage error", c)
				}
			}
		})
	}
}

func TestPageFileOverTheMemoryGuardIsRejectedBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	rec, _, h := pageServer(t)
	big := writePage(t, strings.Repeat("a", maxPageFileBytes+1))
	err := h.run(t, "-p", "demo", "add", "T", "--page-file", big)
	if got := cmdutil.ExitCode(err); got != 2 {
		t.Fatalf("exit = %d (%v), want 2", got, err)
	}
	if len(rec.calls()) != 0 {
		t.Errorf("requests = %v, want none", rec.calls())
	}
	atLimit := writePage(t, strings.Repeat("a", maxPageFileBytes))
	if err := h.run(t, "-p", "demo", "add", "T", "--page-file", atLimit); err != nil {
		t.Errorf("a file exactly at the guard must pass to the server: %v", err)
	}
}

func TestEmptyPageFileIsAUsageErrorNotARemoval(t *testing.T) {
	t.Parallel()
	rec, _, h := pageServer(t)
	empty := writePage(t, "")
	for _, args := range [][]string{{"add", "T", "--page-file", empty}, {"edit", "1", "--page-file", empty}} {
		err := h.run(t, append([]string{"-p", "demo"}, args...)...)
		if got := cmdutil.ExitCode(err); got != 2 || !strings.Contains(err.Error(), `--page-file ""`) {
			t.Errorf("%v: exit = %d (%v), want 2 naming --page-file \"\"", args, got, err)
		}
	}
	for _, c := range rec.calls() {
		if !strings.HasPrefix(c, "GET ") {
			t.Errorf("write request %q sent for an empty page file", c)
		}
	}
}

func TestMissingPageFileIsAnError(t *testing.T) {
	t.Parallel()
	_, _, h := pageServer(t)
	err := h.run(t, "-p", "demo", "add", "T", "--page-file", filepath.Join(t.TempDir(), "nope.html"))
	if err == nil || !strings.Contains(err.Error(), "read page file") {
		t.Fatalf("err = %v, want a read page file error", err)
	}
}

func TestPageLinks(t *testing.T) {
	t.Parallel()
	proj := "my proj"
	tests := []struct{ got, want string }{
		{cardLink("http://h:8080", "lorvik", 42), "http://h:8080/p/lorvik/cards/42"},
		{noteLink("http://h:8080", nil, "arch"), "http://h:8080/notes/arch"},
		{noteLink("http://h:8080", &proj, "arch"), "http://h:8080/notes/arch?project=my+proj"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("link = %q, want %q", tt.got, tt.want)
		}
	}
}
