package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

const (
	oldSecret = "lvk_u_AAAAAAAAAAAAAAAAAAAAAAAAAA"
	newSecret = "lvk_u_BBBBBBBBBBBBBBBBBBBBBBBBBB"
)

func rotatingServer(t *testing.T, rec *recorder) http.HandlerFunc {
	t.Helper()
	return rec.wrap(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(apitypes.HeaderToken, newSecret)
		reply(200, apitypes.Me{Actor: ana, TokenPrefix: "lvk_u_BBBBBB"})(w, r)
	})
}

func TestRotatedTokenFromTheConfigFileIsSavedWithItsCursor(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/me":     rotatingServer(t, rec),
		"GET /api/v1/actors": reply(200, fixtureActors()),
	})
	h := newHarness(t, srv)
	h.useToken(srv, oldSecret, false)
	if err := UpdateUserConfig(func(k string) string { return h.env[k] }, map[string]string{"cursor_" + oldSecret[:12]: "4242"}); err != nil {
		t.Fatal(err)
	}
	if err := h.run(t, "me"); err != nil {
		t.Fatal(err)
	}
	cfg := h.config(t)
	if !strings.Contains(cfg, `token = "`+newSecret+`"`) {
		t.Errorf("config = %q, want the new token stored", cfg)
	}
	if strings.Contains(cfg, "cursor_"+oldSecret[:12]) || !strings.Contains(cfg, `cursor_`+newSecret[:12]+` = "4242"`) {
		t.Errorf("config = %q, want the cursor moved to the new token", cfg)
	}
	if strings.Contains(h.out.String(), newSecret) || strings.Contains(h.errOut.String(), newSecret) {
		t.Errorf("the new secret leaked: stdout %q stderr %q", h.out.String(), h.errOut.String())
	}
	if !strings.Contains(h.errOut.String(), "token rotated and saved") {
		t.Errorf("stderr = %q, want a one-line notice", h.errOut.String())
	}
}

func TestRotatedTokenFromTheEnvironmentIsAnnouncedNotStored(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/me":     rotatingServer(t, rec),
		"GET /api/v1/actors": reply(200, fixtureActors()),
	})
	h := newHarness(t, srv)
	h.useToken(srv, oldSecret, true)
	if err := h.run(t, "me"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.config(t), "token") {
		t.Errorf("config = %q, an env token must not be written to the file", h.config(t))
	}
	if h.errOut.String() != "lorvik: the server rotated your token; LORVIK_TOKEN stops working in 24h; store the token in the config file or mint a new one with lorvik token create\n" {
		t.Errorf("stderr = %q", h.errOut.String())
	}
	if strings.Contains(h.out.String(), newSecret) {
		t.Errorf("stdout = %q, the secret must stay off stdout", h.out.String())
	}
}

func TestRotatedTokenIsUsedByTheNextRequestInTheSameCommand(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	srv := serve(t, rec.wrapAll(map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(apitypes.HeaderToken, newSecret)
			reply(200, fixtureCard())(w, r)
		},
		"GET /api/v1/actors": reply(200, fixtureActors()),
	}))
	h := newHarness(t, srv)
	h.useToken(srv, oldSecret, true)
	if err := h.run(t, "-p", "demo", "show", "42"); err != nil {
		t.Fatal(err)
	}
	if got := rec.last(t, "GET /api/v1/actors").Header.Get("Authorization"); got != "Bearer "+newSecret {
		t.Errorf("name lookup used %q, want the new secret", got)
	}
}

func TestCursorIsKeyedByTokenPrefix(t *testing.T) {
	t.Parallel()
	tests := []struct{ secret, want string }{
		{"", ""},
		{"short", "cursor_short"},
		{"lvk_a_ABCDEFGHIJKLMNOP", "cursor_lvk_a_ABCDEF"},
	}
	for _, tt := range tests {
		if got := cursorKey(tt.secret); got != tt.want {
			t.Errorf("cursorKey(%q) = %q, want %q", tt.secret, got, tt.want)
		}
	}
}
