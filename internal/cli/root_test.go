package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

var update = flag.Bool("update", false, "rewrite golden files")

var fixedNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// harness is one command run against an httptest server.
type harness struct {
	f      *cmdutil.Factory
	out    *cmdutil.Buffer
	errOut *cmdutil.Buffer
	cwd    string
	// env is the environment the command sees; tests may add to it.
	env map[string]string
	// xdg is the directory holding the user config.
	xdg     string
	streams *cmdutil.IOStreams
}

// config returns the user config file as written so far.
func (h *harness) config(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.xdg, "lorvik", "config.toml"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// useToken points the harness client at srv with the given secret and, like
// a real run, makes that secret the configured one.
func (h *harness) useToken(srv *httptest.Server, secret string, fromEnv bool) {
	if fromEnv {
		h.env["LORVIK_TOKEN"] = secret
	} else {
		_ = os.MkdirAll(filepath.Join(h.xdg, "lorvik"), 0o700)
		_ = os.WriteFile(filepath.Join(h.xdg, "lorvik", "config.toml"), []byte("token = \""+secret+"\"\n"), 0o600)
	}
	h.f.Client = func() (*client.Client, error) { return client.New(srv.URL, secret) }
}

// request is one request a recorder saw.
type request struct {
	Method string
	URI    string
	Header http.Header
	Body   string
}

// recorder remembers the requests that reach the handlers it wraps.
type recorder struct {
	mu   sync.Mutex
	reqs []request
}

func (r *recorder) wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(b))
		r.mu.Lock()
		r.reqs = append(r.reqs, request{Method: req.Method, URI: req.URL.RequestURI(), Header: req.Header.Clone(), Body: string(b)})
		r.mu.Unlock()
		next(w, req)
	}
}

// wrapAll wraps every route.
func (r *recorder) wrapAll(routes map[string]http.HandlerFunc) map[string]http.HandlerFunc {
	out := make(map[string]http.HandlerFunc, len(routes))
	for k, v := range routes {
		out[k] = r.wrap(v)
	}
	return out
}

// calls lists "METHOD uri" for each recorded request.
func (r *recorder) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.reqs))
	for _, q := range r.reqs {
		out = append(out, q.Method+" "+q.URI)
	}
	return out
}

// last returns the most recent request whose "METHOD path" starts with prefix.
func (r *recorder) last(t *testing.T, prefix string) request {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range slices.Backward(r.reqs) {
		if strings.HasPrefix(v.Method+" "+v.URI, prefix) {
			return v
		}
	}
	t.Fatalf("no request matching %q", prefix)
	return request{}
}

// newHarness wires a factory with a clean environment, a fixed clock and a
// client for srv. Nothing here touches the real HOME, cwd or environment.
func newHarness(t *testing.T, srv *httptest.Server) *harness {
	t.Helper()
	streams, out, errOut := cmdutil.Test(nil)
	h := &harness{out: out, errOut: errOut, cwd: t.TempDir(), xdg: t.TempDir(), streams: streams}
	// The ceiling keeps a repository that appears above the temp dir, such as
	// a stray /tmp/.git, out of the tests that run outside one.
	h.env = map[string]string{"XDG_CONFIG_HOME": h.xdg, "XDG_STATE_HOME": t.TempDir(), "LORVIK_SESSION": "test-session", "GIT_CEILING_DIRECTORIES": filepath.Dir(h.cwd)}
	h.f = &cmdutil.Factory{
		IOStreams: streams,
		Env:       func(k string) string { return h.env[k] },
		Getwd:     func() (string, error) { return h.cwd, nil },
		Now:       func() time.Time { return fixedNow },
	}
	if srv != nil {
		h.f.Client = func() (*client.Client, error) { return client.New(srv.URL, "tok") }
	}
	return h
}

func (h *harness) run(t *testing.T, args ...string) error {
	t.Helper()
	root := NewRoot(h.f)
	root.SetArgs(args)
	return root.ExecuteContext(t.Context())
}

func serve(t *testing.T, routes map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for pattern, h := range routes {
		mux.HandleFunc(pattern, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func reply(status int, v any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if v != nil {
			_ = json.NewEncoder(w).Encode(v)
		}
	}
}

func problem(status int, p apitypes.Problem) http.HandlerFunc {
	p.Status = status
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(p)
	}
}

func readJSON[T any](t *testing.T, r *http.Request) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		t.Errorf("decode %s %s body: %v", r.Method, r.URL.Path, err)
	}
	return v
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from golden\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestPing(t *testing.T) {
	t.Parallel()
	srv := serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/healthz": reply(200, apitypes.Health{Status: "ok", Version: "1.2.3", Database: "ok"}),
	})
	h := newHarness(t, srv)
	if err := h.run(t, "ping"); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if got := h.out.String(); got != "ok server 1.2.3 database=ok\n" {
		t.Errorf("ping output = %q", got)
	}

	h.out.Reset()
	if err := h.run(t, "ping", "--json"); err != nil {
		t.Fatalf("ping --json: %v", err)
	}
	if got := h.out.String(); !strings.Contains(got, "\"version\": \"1.2.3\"") {
		t.Errorf("ping --json output = %q, want the Health document", got)
	}
}

func TestPingUnreachableNamesTheURL(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	h.f.Client = func() (*client.Client, error) { return client.New(url, "") }
	err := h.run(t, "ping")
	if err == nil || !strings.Contains(err.Error(), "server unreachable") {
		t.Fatalf("ping error = %v, want unreachable", err)
	}
	if msg := cmdutil.FormatError(err, nil); !strings.Contains(msg, url) || !strings.Contains(msg, "LORVIK_API_URL") {
		t.Errorf("error text = %q, want the URL and the env var", msg)
	}
}
