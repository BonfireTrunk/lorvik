package cli

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func TestHistory(t *testing.T) {
	t.Parallel()
	list := []apitypes.CardRevision{
		{ID: "2", Version: 3, Title: "Fix login", UpdatedBy: claude, CreatedAt: ago(time.Hour)},
		{ID: "1", Version: 2, Title: "Login bug", UpdatedBy: ana, CreatedAt: ago(5 * time.Hour)},
	}
	one := apitypes.CardRevision{ID: "1", Version: 2, Title: "Login bug", Body: "old text\n", UpdatedBy: ana, CreatedAt: ago(5 * time.Hour)}
	tests := []struct {
		name    string
		args    []string
		route   string
		reply   http.HandlerFunc
		want    string
		wantErr int
	}{
		{"list newest first", []string{"-p", "web", "history", "42"}, "GET /api/v1/projects/web/cards/42/revisions",
			reply(200, list), "v3  @claude-code (agent)  1h ago  Fix login\nv2  @Ana Souza  5h ago  Login bug\n", 0},
		{"empty list", []string{"-p", "web", "history", "42"}, "GET /api/v1/projects/web/cards/42/revisions",
			reply(200, []apitypes.CardRevision{}), "no history\n", 0},
		{"one version", []string{"-p", "web", "history", "42", "--version", "2"}, "GET /api/v1/projects/web/cards/42/revisions/2",
			reply(200, one), "#42  v2  @Ana Souza  5h ago\nLogin bug\n\nold text\n", 0},
		{"unknown version exits 3", []string{"-p", "web", "history", "42", "--version", "9"}, "GET /api/v1/projects/web/cards/42/revisions/9",
			problem(404, apitypes.Problem{Title: "Not Found"}), "", 3},
		{"unknown card exits 3", []string{"-p", "web", "history", "99"}, "GET /api/v1/projects/web/cards/99/revisions",
			problem(404, apitypes.Problem{Title: "Not Found"}), "", 3},
		{"json list passes the API objects through", []string{"-p", "web", "history", "42", "--json"}, "GET /api/v1/projects/web/cards/42/revisions",
			reply(200, list), mustJSON(t, list), 0},
		{"json version passes the API object through", []string{"-p", "web", "history", "42", "--version", "2", "--json"}, "GET /api/v1/projects/web/cards/42/revisions/2",
			reply(200, one), mustJSON(t, one), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := serve(t, map[string]http.HandlerFunc{
				tt.route:                         tt.reply,
				"GET /api/v1/projects/web/cards": reply(200, apitypes.CardList{}),
				"GET /api/v1/actors":             reply(200, fixtureActors()),
			})
			h := newHarness(t, srv)
			err := h.run(t, tt.args...)
			if got := cmdutil.ExitCode(err); got != tt.wantErr {
				t.Fatalf("exit = %d (%v), want %d", got, err, tt.wantErr)
			}
			if h.out.String() != tt.want {
				t.Errorf("stdout:\n%q\nwant:\n%q", h.out.String(), tt.want)
			}
		})
	}
}

// mustJSON encodes v the way the CLI prints it: indented or compact is the
// printer's choice, so compare against what it emits for a decoded copy.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}
