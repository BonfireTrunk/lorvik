package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

const humanToken = "lvk_u_human-login"

// agentServer answers the calls a harness chat makes: registration and
// token minting (which must carry your login), the join request and the
// card list (which must carry the agent's token). join is the answer to the
// join request.
type agentServer struct {
	t      *testing.T
	rec    recorder
	mu     sync.Mutex
	tokens []string // the agent tokens minted, in order
	join   apitypes.JoinResult
	// mint, when set, answers POST /tokens instead of minting.
	mint http.HandlerFunc
	// extra routes must carry the agent's latest token.
	extra map[string]http.HandlerFunc
}

func (s *agentServer) minted(prefix string, h *harness) (string, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	secret := prefix + strings.Repeat("x", len(s.tokens))
	s.tokens = append(s.tokens, secret)
	return secret, h.f.Now().Add(time.Hour)
}

func (s *agentServer) start(h *harness) string {
	login := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer "+humanToken {
				s.t.Errorf("%s %s sent %q, want your login", r.Method, r.URL.Path, got)
			}
			next(w, r)
		}
	}
	asAgent := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			s.mu.Lock()
			want := "Bearer " + s.tokens[len(s.tokens)-1]
			s.mu.Unlock()
			if got := r.Header.Get("Authorization"); got != want {
				s.t.Errorf("%s %s sent %q, want the agent's latest token", r.Method, r.URL.Path, got)
			}
			next(w, r)
		}
	}
	routes := map[string]http.HandlerFunc{
		"POST /api/v1/agents": login(func(w http.ResponseWriter, _ *http.Request) {
			secret, exp := s.minted("lvk_a_reg", h)
			reply(201, apitypes.AgentCreated{Agent: apitypes.Agent{ID: "77", Name: "claude-code@box"}, Token: secret, TokenExpiresAt: exp})(w, nil)
		}),
		"POST /api/v1/tokens": login(func(w http.ResponseWriter, r *http.Request) {
			if s.mint != nil {
				s.mint(w, r)
				return
			}
			secret, exp := s.minted("lvk_a_new", h)
			reply(201, apitypes.TokenCreated{Secret: secret, Token: apitypes.Token{ExpiresAt: exp}})(w, nil)
		}),
		"POST /api/v1/projects/p/join": asAgent(func(w http.ResponseWriter, _ *http.Request) { reply(200, s.join)(w, nil) }),
		"GET /api/v1/projects/p/cards": asAgent(reply(200, apitypes.CardList{Items: []apitypes.CardBrief{}})),
	}
	for k, v := range s.extra {
		routes[k] = asAgent(v)
	}
	return serve(s.t, s.rec.wrapAll(routes)).URL
}

// newAgentHarness is a Claude Code chat on machine box, in a repo bound to
// project p, with you signed in to the server.
func newAgentHarness(t *testing.T, s *agentServer) *harness {
	h := newHarness(t, nil)
	h.env["CLAUDE_CODE_SESSION_ID"] = "chat-1"
	h.f.Hostname = func() (string, error) { return "box", nil }
	url := s.start(h)
	write(t, filepath.Join(h.xdg, "lorvik", "config.toml"), "api_url = \""+url+"\"\ntoken = \""+humanToken+"\"\n")
	write(t, filepath.Join(h.cwd, bindingFile), "project = \"p\"\n")
	return h
}

func (h *harness) agentFile(t *testing.T) map[string]string {
	t.Helper()
	path := filepath.Join(h.xdg, "lorvik", "agents", "claude-code@box.toml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("agent file mode = %v, want 0600", info.Mode().Perm())
	}
	m, err := readTOMLFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// A chat of a recognised harness registers its own agent from your login the
// first time, works under the agent's token from then on and never sends
// yours, asks to join a project once per token, and renews the token through
// your login when it nears expiry.
func TestHarnessChatActsAsItsOwnAgent(t *testing.T) {
	t.Parallel()
	s := &agentServer{t: t, join: apitypes.JoinResult{Project: "p", Status: "joined", Rights: []string{"work"}, Message: "claude-code@box joined p with work, allowed by Ana's settings"}}
	h := newAgentHarness(t, s)
	now := fixedNow
	h.f.Now = func() time.Time { return now }
	before := h.config(t)

	if err := h.run(t, "ls"); err != nil {
		t.Fatalf("first run: %v\n%s", err, h.errOut.String())
	}
	if got := paths(s.rec.calls()); !slices.Equal(got, []string{"POST /api/v1/agents", "POST /api/v1/projects/p/join", "GET /api/v1/projects/p/cards"}) {
		t.Errorf("calls = %v, want registration, the join, then the list", got)
	}
	if body := s.rec.last(t, "POST /api/v1/agents").Body; !strings.Contains(body, `"name":"claude-code@box"`) || !strings.Contains(body, `"harness":"claude-code"`) || !strings.Contains(body, `"auto":true`) {
		t.Errorf("registration body = %s", body)
	}
	for _, want := range []string{"registered agent claude-code@box", "joined p with work"} {
		if !strings.Contains(h.errOut.String(), want) {
			t.Errorf("stderr = %q, want %q", h.errOut.String(), want)
		}
	}
	f := h.agentFile(t)
	if f["agent_id"] != "77" || f["harness"] != "claude-code" || f["joined"] != "p" || f["token"] != s.tokens[0] {
		t.Errorf("agent file = %v", f)
	}
	if h.config(t) != before {
		t.Errorf("your config changed:\n%s", h.config(t))
	}

	n := len(s.rec.calls())
	if err := h.run(t, "ls"); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := paths(s.rec.calls()[n:]); !slices.Equal(got, []string{"GET /api/v1/projects/p/cards"}) {
		t.Errorf("second run calls = %v, want only the list", got)
	}

	now = now.Add(55 * time.Minute)
	n = len(s.rec.calls())
	if err := h.run(t, "ls"); err != nil {
		t.Fatalf("run near expiry: %v", err)
	}
	if got := paths(s.rec.calls()[n:]); !slices.Equal(got, []string{"POST /api/v1/tokens", "POST /api/v1/projects/p/join", "GET /api/v1/projects/p/cards"}) {
		t.Errorf("calls near expiry = %v, want a renewal, the join again, then the list", got)
	}
	if body := s.rec.last(t, "POST /api/v1/tokens").Body; !strings.Contains(body, `"agent":"77"`) {
		t.Errorf("renewal body = %s", body)
	}
	if f := h.agentFile(t); f["token"] != s.tokens[1] {
		t.Errorf("agent file token = %q, want the renewed one", f["token"])
	}
}

// inbox --follow outlives the agent's hour-long token: the read after the
// token nears expiry renews it first, through your login.
func TestFollowRenewsTheAgentTokenItOutlives(t *testing.T) {
	t.Parallel()
	var elapsed atomic.Int64
	var reads atomic.Int32
	s := &agentServer{t: t, extra: map[string]http.HandlerFunc{
		"GET /api/v1/events": reply(200, apitypes.EventList{Items: []apitypes.Event{plainEvent("5", 5)}}),
		"GET /api/v1/inbox": func(w http.ResponseWriter, r *http.Request) {
			in := apitypes.Inbox{Cursor: "6", Events: []apitypes.Event{plainEvent("6", 6)}}
			if reads.Add(1) > 1 {
				in = apitypes.Inbox{Cursor: "8", Events: []apitypes.Event{wakeEvent("8", 42)}}
			}
			reply(200, in)(w, r)
		},
		// The stream drops after 55 minutes, so the follow reads again.
		"GET /api/v1/events/stream": func(w http.ResponseWriter, _ *http.Request) {
			elapsed.Store(int64(55 * time.Minute))
			w.Header().Set("Content-Type", "text/event-stream")
		},
		"GET /api/v1/actors": reply(200, fixtureActors()),
	}}
	h := newAgentHarness(t, s)
	h.f.Now = func() time.Time { return fixedNow.Add(time.Duration(elapsed.Load())) }
	if err := h.run(t, "inbox", "--follow", "--once"); err != nil {
		t.Fatalf("follow: %v\n%s", err, h.errOut.String())
	}
	var got []string
	for _, c := range paths(s.rec.calls()) {
		if c == "POST /api/v1/agents" || c == "POST /api/v1/tokens" || c == "GET /api/v1/inbox" {
			got = append(got, c)
		}
	}
	if want := []string{"POST /api/v1/agents", "GET /api/v1/inbox", "POST /api/v1/tokens", "GET /api/v1/inbox"}; !slices.Equal(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
	if !strings.Contains(h.out.String(), "commented on #42") {
		t.Errorf("stdout = %q, want the wake event read with the renewed token", h.out.String())
	}
}

// With nobody signed in, a harness chat stops before sending anything; it
// never falls back to acting as a person.
func TestHarnessChatWithoutALoginSendsNothing(t *testing.T) {
	t.Parallel()
	s := &agentServer{t: t}
	h := newAgentHarness(t, s)
	write(t, filepath.Join(h.xdg, "lorvik", "config.toml"), "")
	err := h.run(t, "ls")
	if cmdutil.ExitCode(err) != cmdutil.ExitUnauthorized || !strings.Contains(err.Error(), "run `lorvik login` in a terminal") {
		t.Errorf("err = %v, want exit 5 naming lorvik login", err)
	}
	if got := s.rec.calls(); len(got) != 0 {
		t.Errorf("calls = %v, want none", got)
	}
}

// A join waiting for you stops the command with the server's message, and
// the project is asked about again on the next run.
func TestHarnessChatWaitsToBeAllowed(t *testing.T) {
	t.Parallel()
	s := &agentServer{t: t, join: apitypes.JoinResult{Project: "p", Status: "pending", Rights: []string{"work"}, Message: "waiting for Ana to allow claude-code@box in p on the dashboard"}}
	h := newAgentHarness(t, s)
	err := h.run(t, "ls")
	if cmdutil.ExitCode(err) != cmdutil.ExitUnauthorized || !strings.Contains(err.Error(), "waiting for Ana to allow claude-code@box in p") {
		t.Errorf("err = %v, want exit 5 with the server's message", err)
	}
	if slices.ContainsFunc(s.rec.calls(), func(c string) bool { return strings.Contains(c, "/cards") }) {
		t.Errorf("calls = %v, want no card request", s.rec.calls())
	}
	if f := h.agentFile(t); f["joined"] != "" {
		t.Errorf("joined = %q, want nothing yet", f["joined"])
	}
}

// An agent the server refuses to renew, because you revoked it, stays
// revoked: the chat reports the refusal and registers nothing new.
func TestRevokedHarnessAgentIsNotRegisteredAgain(t *testing.T) {
	t.Parallel()
	s := &agentServer{t: t, mint: reply(409, apitypes.Problem{Status: 409, Detail: `agent "claude-code@box" was revoked: already exists`, Hint: "register a new agent by hand"})}
	h := newAgentHarness(t, s)
	write(t, filepath.Join(h.xdg, "lorvik", "agents", "claude-code@box.toml"),
		"api_url = \""+h.configMap(t)["api_url"]+"\"\nagent_id = \"77\"\ntoken = \"lvk_a_old\"\ntoken_expires_at = \"2026-10-18T11:00:00Z\"\n")
	err := h.run(t, "ls")
	if cmdutil.ExitCode(err) != cmdutil.ExitConflict || !strings.Contains(err.Error(), "revoked") {
		t.Errorf("err = %v, want the revocation", err)
	}
	if got := s.rec.calls(); !slices.Equal(got, []string{"POST /api/v1/tokens"}) {
		t.Errorf("calls = %v, want only the refused renewal", got)
	}
}

// An agent file that lost its token but not its expiry is renewed; the chat
// never sends your login in the token's place.
func TestAgentFileWithoutATokenIsRenewed(t *testing.T) {
	t.Parallel()
	s := &agentServer{t: t, join: apitypes.JoinResult{Project: "p", Status: "member"}}
	h := newAgentHarness(t, s)
	write(t, filepath.Join(h.xdg, "lorvik", "agents", "claude-code@box.toml"),
		"api_url = \""+h.configMap(t)["api_url"]+"\"\nagent_id = \"77\"\ntoken_expires_at = \"2026-10-18T12:50:00Z\"\n")
	if err := h.run(t, "ls"); err != nil {
		t.Fatalf("run: %v\n%s", err, h.errOut.String())
	}
	if got := paths(s.rec.calls()); len(got) == 0 || got[0] != "POST /api/v1/tokens" {
		t.Errorf("calls = %v, want a renewal first", got)
	}
}

// LORVIK_CONFIG still chooses the credential in a harness chat, and naming
// another harness's agent file earns a warning.
func TestBorrowedAgentConfigIsWarnedAbout(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	h.env["CLAUDE_CODE_SESSION_ID"] = "chat-1"
	path := filepath.Join(h.xdg, "codex.toml")
	h.env["LORVIK_CONFIG"] = path
	var rec recorder
	srv := serve(t, rec.wrapAll(map[string]http.HandlerFunc{"GET /api/v1/me": reply(200, apitypes.Me{})}))
	write(t, path, "api_url = \""+srv.URL+"\"\nharness = \"codex\"\nagent = \"codex@box\"\ntoken = \"lvk_a_codex\"\n")
	_ = h.run(t, "me")
	if !strings.Contains(h.errOut.String(), "this chat runs in claude-code, but LORVIK_CONFIG names the file of codex agent codex@box") {
		t.Errorf("stderr = %q", h.errOut.String())
	}
	if got := rec.last(t, "GET /api/v1/me").Header.Get("Authorization"); got != "Bearer lvk_a_codex" {
		t.Errorf("Authorization = %q, want the LORVIK_CONFIG token", got)
	}
}

func TestAgentName(t *testing.T) {
	t.Parallel()
	if got := agentName("claude-code", "box"); got != "claude-code@box" {
		t.Errorf("agentName = %q", got)
	}
	if got := agentName("codex", strings.Repeat("m", 80)); len(got) != maxAgentName || !strings.HasPrefix(got, "codex@mmm") {
		t.Errorf("long machine name = %q (%d)", got, len(got))
	}
	if got := agentName("pi", "a/b"); got != "pi@a-b" {
		t.Errorf("slash = %q", got)
	}
	if serverHarness("copilot") != "copilot-cli" || serverHarness("goose") != "goose" {
		t.Error("serverHarness")
	}
}

// paths drops the query from each "METHOD uri".
func paths(calls []string) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i], _, _ = strings.Cut(c, "?")
	}
	return out
}

// watch outlives the agent's token: when the server ends the stream with the
// token, watch renews it and reopens the stream where the first one stopped.
func TestWatchReopensTheStreamWithARenewedAgentToken(t *testing.T) {
	t.Parallel()
	var elapsed atomic.Int64
	var streams atomic.Int32
	s := &agentServer{t: t, join: apitypes.JoinResult{Project: "p", Status: "member"}, extra: map[string]http.HandlerFunc{
		"GET /api/v1/events": reply(200, apitypes.EventList{Items: []apitypes.Event{plainEvent("5", 5)}}),
		"GET /api/v1/events/stream": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			if streams.Add(1) == 1 {
				// The token's hour is up: the server ends the stream.
				sseEvent(w, plainEvent("6", 7))
				elapsed.Store(int64(time.Hour))
				return
			}
			sseEvent(w, wakeEvent("8", 42))
			<-r.Context().Done()
		},
		"GET /api/v1/actors": reply(200, fixtureActors()),
	}}
	h := newAgentHarness(t, s)
	h.f.Now = func() time.Time { return fixedNow.Add(time.Duration(elapsed.Load())) }
	if err := h.run(t, "watch", "42"); err != nil {
		t.Fatalf("watch: %v\n%s", err, h.errOut.String())
	}
	var got []string
	for _, c := range s.rec.calls() {
		if strings.HasPrefix(c, "GET /api/v1/events/stream") || c == "POST /api/v1/tokens" {
			got = append(got, c)
		}
	}
	first, second := "GET /api/v1/events/stream?after="+eventPos(5)+"&project=p", "GET /api/v1/events/stream?after="+eventPos(6)+"&project=p"
	if want := []string{first, "POST /api/v1/tokens", second}; !slices.Equal(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
	if !strings.Contains(h.out.String(), "commented on #42") {
		t.Errorf("stdout = %q, want the event on #42", h.out.String())
	}
}

// A token the server refuses while this machine's clock still calls it fresh
// (the clock runs behind the server's) is renewed once and the command sent
// again, instead of failing until the clock catches up.
func TestARefusedAgentTokenIsRenewedOnce(t *testing.T) {
	t.Parallel()
	var refusals atomic.Int32
	s := &agentServer{t: t, join: apitypes.JoinResult{Project: "p", Status: "member"}}
	s.extra = map[string]http.HandlerFunc{
		"GET /api/v1/projects/p/cards": func(w http.ResponseWriter, r *http.Request) {
			if refusals.Add(1) == 1 {
				reply(401, apitypes.Problem{Status: 401, Title: "Unauthorized", Detail: "token expired"})(w, r)
				return
			}
			reply(200, apitypes.CardList{Items: []apitypes.CardBrief{}})(w, r)
		},
	}
	h := newAgentHarness(t, s)
	if err := h.run(t, "ls"); err != nil {
		t.Fatalf("ls: %v\n%s", err, h.errOut.String())
	}
	if got := paths(s.rec.calls()); !slices.Equal(got, []string{"POST /api/v1/agents", "POST /api/v1/projects/p/join", "GET /api/v1/projects/p/cards", "POST /api/v1/tokens", "GET /api/v1/projects/p/cards"}) {
		t.Errorf("calls = %v, want one renewal between the refused list and its retry", got)
	}
	if f := h.agentFile(t); f["token"] != s.tokens[1] {
		t.Errorf("agent file token = %q, want the renewed one", f["token"])
	}
}

// Signing out stops the harness's agent at once, though its cached token
// still has most of its hour.
func TestSigningOutStopsTheAgentAtOnce(t *testing.T) {
	t.Parallel()
	s := &agentServer{t: t, join: apitypes.JoinResult{Project: "p", Status: "member"}}
	h := newAgentHarness(t, s)
	if err := h.run(t, "ls"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	write(t, filepath.Join(h.xdg, "lorvik", "config.toml"), "")
	n := len(s.rec.calls())
	err := h.run(t, "ls")
	if cmdutil.ExitCode(err) != cmdutil.ExitUnauthorized || !strings.Contains(err.Error(), "nobody is signed in") {
		t.Errorf("err = %v, want exit 5 nobody is signed in", err)
	}
	if got := s.rec.calls()[n:]; len(got) != 0 {
		t.Errorf("calls after signing out = %v, want none", got)
	}
}
