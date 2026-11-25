package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

const (
	ownerPassword  = "correct horse"
	memberPassword = "battery staple"
	addrA          = "203.0.113.10"
	addrB          = "203.0.113.20"
)

// cheapHash is a password hash in the stored format with the smallest
// parameters argon2 takes, so a test that signs in many times stays fast.
// VerifyPassword reads the parameters from the hash itself.
func cheapHash(password string) []byte {
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte(password), salt, 1, 8, 1, 32)
	enc := base64.RawStdEncoding
	return fmt.Appendf(nil, "$argon2id$v=%d$m=8,t=1,p=1$%s$%s", argon2.Version, enc.EncodeToString(salt), enc.EncodeToString(key))
}

// newSignInEnv is newEnv with password login on, passwords for the owner
// ("ada") and the member ("sam"), and a cheap dummy hash.
func newSignInEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newEnv(t)
	e.svc.password = true
	e.svc.limits.dummyOnce.Do(func() { e.svc.limits.dummy = cheapHash(rand.Text()) })
	for login, who := range map[string]struct {
		user     domain.ActorRef
		password string
	}{"ada": {e.user, ownerPassword}, "sam": {e.member, memberPassword}} {
		_, _ = e.fs.CreateIdentity(context.Background(), domain.Identity{ID: e.ids.New(), UserID: who.user.ID, Provider: domain.ProviderPassword, ProviderID: login, Secret: cheapHash(who.password), Login: login, CreatedAt: t0})
	}
	return e
}

// passwordSignIn posts to /login from addr.
func (e *testEnv) passwordSignIn(addr, login, password string) *httptest.ResponseRecorder {
	e.t.Helper()
	body, err := json.Marshal(apitypes.Login{Login: login, Password: password})
	if err != nil {
		e.t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "lorvik-test/1")
	req.RemoteAddr = net.JoinHostPort(addr, "40000")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// wantSignIn requires a status and, for 429 and 503, the Retry-After header.
func wantSignIn(t *testing.T, what string, rec *httptest.ResponseRecorder, status int, retryAfter string) {
	t.Helper()
	if rec.Code != status || rec.Header().Get("Retry-After") != retryAfter {
		t.Fatalf("%s = %d (Retry-After %q), want %d (%q): %s", what, rec.Code, rec.Header().Get("Retry-After"), status, retryAfter, rec.Body)
	}
}

func TestLoginRefusesAnUnknownLoginAWrongPasswordAndAnUnreadableHashAlike(t *testing.T) {
	t.Parallel()
	e := newSignInEnv(t)
	var logs bytes.Buffer
	e.svc.limits.logger = slog.New(slog.NewTextHandler(&logs, nil))
	_, _ = e.fs.CreateIdentity(context.Background(), domain.Identity{ID: e.ids.New(), UserID: e.member.ID, Provider: domain.ProviderPassword, ProviderID: "broken", Secret: []byte("not a hash"), Login: "broken", CreatedAt: t0})

	wrong := e.passwordSignIn(addrA, "ada", "wrong password")
	unknown := e.passwordSignIn(addrA, "nobody", "wrong password")
	broken := e.passwordSignIn(addrA, "broken", "wrong password")
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401: %s", wrong.Code, wrong.Body)
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{"unknown login": unknown, "unreadable hash": broken} {
		if rec.Code != wrong.Code || rec.Body.String() != wrong.Body.String() || rec.Header().Get("Content-Type") != wrong.Header().Get("Content-Type") {
			t.Errorf("%s = %d %s, want exactly the wrong-password answer %d %s", name, rec.Code, rec.Body, wrong.Code, wrong.Body)
		}
	}

	// The log is the only place the cases differ, and it never holds what was typed.
	out := logs.String()
	for _, want := range []string{
		"event=" + eventWrongPassword, "event=" + eventUnknownLogin, "user_id=" + e.user.ID.String(),
		"source_ip=" + addrA, "user_agent=lorvik-test/1", "login_hash=",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("security log lacks %q:\n%s", want, out)
		}
	}
	for _, never := range []string{"ada", "nobody", "wrong password"} {
		if strings.Contains(out, never) {
			t.Errorf("security log holds %q:\n%s", never, out)
		}
	}
	if n := strings.Count(out, "user_id="); n != 2 {
		t.Errorf("lines with a user id = %d, want 2 (the two known logins):\n%s", n, out)
	}
}

func TestLoginThrottlesWrongPasswordsForALoginFromOneAddress(t *testing.T) {
	t.Parallel()
	for _, login := range []string{"ada", "nobody"} {
		t.Run(login, func(t *testing.T) {
			t.Parallel()
			e := newSignInEnv(t)
			for i := range pairFailures {
				wantSignIn(t, fmt.Sprintf("wrong password %d", i+1), e.passwordSignIn(addrA, login, "wrong password"), http.StatusUnauthorized, "")
			}
			// Throttled whatever the password, and the same for a login that
			// does not exist.
			rec := e.passwordSignIn(addrA, strings.ToUpper(login)+" ", ownerPassword)
			wantSignIn(t, "attempt after the limit", rec, http.StatusTooManyRequests, "900")
			var p apitypes.Problem
			_ = json.Unmarshal(rec.Body.Bytes(), &p)
			if p.Detail != "too many sign-in attempts; try again in 15 minutes" {
				t.Errorf("detail = %q", p.Detail)
			}
			if login != "ada" {
				return
			}
			wantSignIn(t, "right password from another address", e.passwordSignIn(addrB, login, ownerPassword), http.StatusCreated, "")
			e.advance(pairWindow - time.Second)
			wantSignIn(t, "one second before the window ends", e.passwordSignIn(addrA, login, ownerPassword), http.StatusTooManyRequests, "1")
			e.advance(time.Second)
			wantSignIn(t, "after the window", e.passwordSignIn(addrA, login, ownerPassword), http.StatusCreated, "")
		})
	}
}

func TestLoginThrottlesALoginAcrossAddresses(t *testing.T) {
	t.Parallel()
	e := newSignInEnv(t)
	addr := func(i int) string { return fmt.Sprintf("198.51.100.%d", i+1) }
	for i := range loginFailures - 1 {
		wantSignIn(t, "wrong password", e.passwordSignIn(addr(i), "ada", "wrong password"), http.StatusUnauthorized, "")
	}
	// A right password in between must not hand the guessers a new allowance.
	wantSignIn(t, "the owner signs in", e.passwordSignIn(addrA, "ada", ownerPassword), http.StatusCreated, "")
	wantSignIn(t, "last wrong password", e.passwordSignIn(addr(100), "ada", "wrong password"), http.StatusUnauthorized, "")

	wantSignIn(t, "right password from a new address", e.passwordSignIn(addrB, "ada", ownerPassword), http.StatusTooManyRequests, "3600")
	wantSignIn(t, "another login", e.passwordSignIn(addrB, "sam", memberPassword), http.StatusCreated, "")
	e.advance(loginWindow)
	wantSignIn(t, "after the window", e.passwordSignIn(addrB, "ada", ownerPassword), http.StatusCreated, "")
}

// One address, however long it keeps guessing, fills only its share of the
// login's count from anywhere: the owner still signs in from elsewhere.
func TestLoginOneAddressCannotThrottleALoginForEveryone(t *testing.T) {
	t.Parallel()
	e := newSignInEnv(t)
	for round := range loginFailures / pairFailures {
		for range pairFailures {
			wantSignIn(t, fmt.Sprintf("wrong password, round %d", round+1), e.passwordSignIn(addrA, "ada", "wrong password"), http.StatusUnauthorized, "")
		}
		e.advance(pairWindow)
	}
	// loginFailures wrong passwords inside loginWindow, all from one address.
	wantSignIn(t, "the owner from another address", e.passwordSignIn(addrB, "ada", ownerPassword), http.StatusCreated, "")

	// Enough addresses, each at its share, do fill it.
	many := newSignInEnv(t)
	for i := range loginFailures / loginShare {
		for range loginShare {
			wantSignIn(t, "wrong password", many.passwordSignIn(fmt.Sprintf("198.51.100.%d", i+1), "ada", "wrong password"), http.StatusUnauthorized, "")
		}
	}
	wantSignIn(t, "the owner from another address", many.passwordSignIn(addrB, "ada", ownerPassword), http.StatusTooManyRequests, "3600")
}

func TestLoginSuccessClearsOnlyItsOwnLoginAndAddress(t *testing.T) {
	t.Parallel()
	e := newSignInEnv(t)
	wrong := func(n int) {
		t.Helper()
		for range n {
			wantSignIn(t, "wrong password", e.passwordSignIn(addrA, "ada", "wrong password"), http.StatusUnauthorized, "")
		}
	}
	wrong(pairFailures - 1)
	// Someone else signing in from the same address clears nothing of the owner's.
	wantSignIn(t, "the member signs in", e.passwordSignIn(addrA, "sam", memberPassword), http.StatusCreated, "")
	wrong(1)
	wantSignIn(t, "owner after the member's sign-in", e.passwordSignIn(addrA, "ada", ownerPassword), http.StatusTooManyRequests, "900")

	e.advance(pairWindow)
	wrong(pairFailures - 1)
	wantSignIn(t, "the owner signs in", e.passwordSignIn(addrA, "ada", ownerPassword), http.StatusCreated, "")
	wrong(pairFailures - 1)
	wantSignIn(t, "owner after their own sign-in cleared the count", e.passwordSignIn(addrA, "ada", ownerPassword), http.StatusCreated, "")
}

func TestLoginIsBusyWhenEveryPasswordCheckIsTaken(t *testing.T) {
	t.Parallel()
	e := newSignInEnv(t)
	e.svc.limits.gate.wait = 0
	var held []func()
	for range passwordChecks {
		release, ok := e.svc.limits.gate.acquire(context.Background())
		if !ok {
			t.Fatal("could not take a free slot")
		}
		held = append(held, release)
	}

	// An unknown login waits for a slot like a known one: it is checked too.
	for _, login := range []string{"ada", "nobody"} {
		rec := e.passwordSignIn(addrA, login, ownerPassword)
		wantSignIn(t, login+" with no slot free", rec, http.StatusServiceUnavailable, "2")
	}
	for _, release := range held {
		release()
	}
	// Busy answers counted nothing against the login.
	for range pairFailures - 1 {
		wantSignIn(t, "wrong password", e.passwordSignIn(addrA, "ada", "wrong password"), http.StatusUnauthorized, "")
	}
	wantSignIn(t, "right password", e.passwordSignIn(addrA, "ada", ownerPassword), http.StatusCreated, "")
}

// A throttled attempt is refused before it could take a password check.
func TestLoginThrottleAnswersBeforeThePasswordCheck(t *testing.T) {
	t.Parallel()
	e := newSignInEnv(t)
	for range pairFailures {
		wantSignIn(t, "wrong password", e.passwordSignIn(addrA, "ada", "wrong password"), http.StatusUnauthorized, "")
	}
	e.svc.limits.gate.wait = 0
	for range passwordChecks {
		if _, ok := e.svc.limits.gate.acquire(context.Background()); !ok {
			t.Fatal("could not take a free slot")
		}
	}
	wantSignIn(t, "throttled attempt with no slot free", e.passwordSignIn(addrA, "ada", ownerPassword), http.StatusTooManyRequests, "900")
}

func TestLoginChecksAnUnknownLoginAgainstAHashWithTheCurrentParameters(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.svc.password = true
	wantSignIn(t, "unknown login", e.passwordSignIn(addrA, "nobody", "whatever it is"), http.StatusUnauthorized, "")
	fresh, err := domain.HashPassword("any password")
	if err != nil {
		t.Fatal(err)
	}
	// Same format and the same parameters as a hash made now: everything up
	// to the salt is equal.
	params := func(h []byte) string { return string(h[:bytes.LastIndexByte(h[:bytes.LastIndexByte(h, '$')], '$')]) }
	if got, want := params(e.svc.limits.dummy), params(fresh); got != want {
		t.Errorf("dummy hash parameters = %q, want %q", got, want)
	}
}

func TestSignInRoutesThrottleRequestsPerAddress(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	e.svc.password = true
	routes := []func() *httptest.ResponseRecorder{
		func() *httptest.ResponseRecorder {
			return e.do("", http.MethodPost, "/login", apitypes.Login{Login: "nobody", Password: "x"})
		},
		func() *httptest.ResponseRecorder {
			return e.do("", http.MethodPost, "/login/github", apitypes.GitHubLogin{AccessToken: "junk"})
		},
		func() *httptest.ResponseRecorder { return e.get(e.web, "/auth/github/start") },
		func() *httptest.ResponseRecorder { return e.get(e.web, "/auth/github/callback?code=x&state=y") },
	}
	// One allowance for the address, shared by the four routes. Most of it
	// goes on the start route, which neither hashes nor calls GitHub.
	for i := range addrRequests {
		route := routes[2]
		if i < len(routes) {
			route = routes[i]
		}
		if rec := route(); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d was throttled", i+1)
		}
	}
	for i, route := range routes {
		rec := route()
		wantSignIn(t, fmt.Sprintf("route %d over the limit", i), rec, http.StatusTooManyRequests, "60")
		if !strings.Contains(rec.Body.String(), "too many sign-in attempts; try again in 1 minute") {
			t.Errorf("route %d body = %s", i, rec.Body)
		}
	}
	if rec := e.do("", http.MethodGet, "/auth/github", nil); rec.Code != http.StatusOK {
		t.Errorf("a public route that is not a sign-in = %d, want 200", rec.Code)
	}
	if rec := e.do(ghAdminSecret, http.MethodGet, "/me", nil); rec.Code != http.StatusOK {
		t.Errorf("an authenticated route = %d, want 200", rec.Code)
	}
	if rec := e.passwordSignIn(addrB, "nobody", "x"); rec.Code != http.StatusUnauthorized {
		t.Errorf("another address = %d, want 401", rec.Code)
	}
	e.advance(addrWindow)
	if rec := routes[2](); rec.Code != http.StatusFound {
		t.Errorf("after the window = %d, want 302", rec.Code)
	}
}

// The mux serves HEAD with the GET handler, so HEAD is counted and refused
// like GET.
func TestSignInRoutesCountHEADLikeGET(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	head := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodHead, path, nil)
		rec := httptest.NewRecorder()
		e.web.ServeHTTP(rec, req)
		return rec
	}
	for i := range addrRequests {
		if rec := head("/auth/github/start"); rec.Code != http.StatusFound {
			t.Fatalf("HEAD %d = %d, want 302", i+1, rec.Code)
		}
	}
	for _, path := range []string{"/auth/github/start", "/auth/github/callback?code=x&state=y"} {
		wantSignIn(t, "HEAD "+path+" over the limit", head(path), http.StatusTooManyRequests, "60")
	}
}

// A flood of throttled requests writes one line per address and window, and
// a header from an untrusted peer is reported once.
func TestSignInGuardDoesNotFloodTheLog(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	var logs bytes.Buffer
	e.svc.limits.logger = slog.New(slog.NewTextHandler(&logs, nil))
	start := func(addr string) int {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/github/start", nil)
		req.RemoteAddr = net.JoinHostPort(addr, "40000")
		req.Header.Set("X-Forwarded-For", "198.51.100.77")
		rec := httptest.NewRecorder()
		e.web.ServeHTTP(rec, req)
		return rec.Code
	}
	for range addrRequests + 5 {
		start(addrA)
	}
	if got := start(addrA); got != http.StatusTooManyRequests {
		t.Fatalf("request over the limit = %d, want 429", got)
	}
	start(addrB)
	out := logs.String()
	if n := strings.Count(out, "event="+eventThrottledAddr); n != 1 {
		t.Errorf("throttled lines = %d, want 1 for six refusals of one address:\n%s", n, out)
	}
	if n := strings.Count(out, "X-Forwarded-For ignored"); n != 1 {
		t.Errorf("ignored-header warnings = %d, want 1:\n%s", n, out)
	}
	if strings.Contains(out, "198.51.100.77") {
		t.Errorf("the log holds the address a client wrote into the header:\n%s", out)
	}

	e.advance(addrWindow)
	for range addrRequests + 2 {
		start(addrA)
	}
	if n := strings.Count(logs.String(), "event="+eventThrottledAddr); n != 2 {
		t.Errorf("throttled lines after a second window = %d, want 2", n)
	}
}

// The header is believed only from a listed proxy; otherwise a client would
// write a new address into it on every request and never be throttled.
func TestLoginTakesTheClientAddressFromATrustedProxyOnly(t *testing.T) {
	t.Parallel()
	const peer = "192.0.2.1" // httptest's RemoteAddr
	wrongFrom := func(e *testEnv, forwarded string) int {
		return e.do("", http.MethodPost, "/login", apitypes.Login{Login: "ada", Password: "wrong password"}, "X-Forwarded-For", forwarded).Code
	}

	untrusted := newSignInEnv(t)
	for i := range pairFailures {
		if got := wrongFrom(untrusted, fmt.Sprintf("198.51.100.%d", i+1)); got != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, got)
		}
	}
	if got := wrongFrom(untrusted, "198.51.100.99"); got != http.StatusTooManyRequests {
		t.Errorf("with no trusted proxy, a new X-Forwarded-For = %d, want 429: the peer is the client", got)
	}

	trusted := newSignInEnv(t)
	trusted.svc.limits.proxies = proxies{netip.MustParsePrefix(peer + "/32")}
	for i := range pairFailures {
		if got := wrongFrom(trusted, "198.51.100.1"); got != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i+1, got)
		}
	}
	if got := wrongFrom(trusted, "198.51.100.1"); got != http.StatusTooManyRequests {
		t.Errorf("behind the proxy, the same client = %d, want 429", got)
	}
	if got := wrongFrom(trusted, "198.51.100.2"); got != http.StatusUnauthorized {
		t.Errorf("behind the proxy, another client = %d, want 401", got)
	}
}

func TestClientAddr(t *testing.T) {
	t.Parallel()
	trusted := proxies{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("2001:db8:1::/48")}
	tests := []struct {
		name      string
		proxies   proxies
		remote    string
		forwarded []string
		want      string
	}{
		{"no proxies: the peer, whatever the header says", nil, "203.0.113.5:4000", []string{"198.51.100.1"}, "203.0.113.5"},
		{"untrusted peer: the header is ignored", trusted, "203.0.113.5:4000", []string{"198.51.100.1"}, "203.0.113.5"},
		{"trusted peer: the address it appended", trusted, "10.0.0.1:4000", []string{"198.51.100.1"}, "198.51.100.1"},
		{"what the client wrote on the left is not believed", trusted, "10.0.0.1:4000", []string{"1.2.3.4, 198.51.100.1"}, "198.51.100.1"},
		{"a chain of trusted proxies is skipped", trusted, "10.0.0.1:4000", []string{"198.51.100.1, 10.0.0.7, 2001:db8:1::9"}, "198.51.100.1"},
		{"several header lines read as one list", trusted, "10.0.0.1:4000", []string{"198.51.100.1", "10.0.0.7"}, "198.51.100.1"},
		{"every hop trusted: the first one", trusted, "10.0.0.1:4000", []string{"10.0.0.9, 10.0.0.7"}, "10.0.0.9"},
		{"garbage stops the walk at the last good hop", trusted, "10.0.0.1:4000", []string{"198.51.100.1, unknown, 10.0.0.7"}, "10.0.0.7"},
		{"trusted peer without the header: the peer", trusted, "10.0.0.1:4000", nil, "10.0.0.1"},
		{"an IPv4 peer written as IPv6 is the IPv4 address", trusted, "[::ffff:10.0.0.1]:4000", []string{"198.51.100.1"}, "198.51.100.1"},
		{"IPv6 peer", nil, "[2001:db8:2::1]:4000", nil, "2001:db8:2::1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			for _, v := range tc.forwarded {
				req.Header.Add("X-Forwarded-For", v)
			}
			if got := tc.proxies.clientAddr(req).String(); got != tc.want {
				t.Errorf("clientAddr = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestLimitAddrCountsAnIPv6NetworkAsOne(t *testing.T) {
	t.Parallel()
	a, b := netip.MustParseAddr("2001:db8:2:3::1"), netip.MustParseAddr("2001:db8:2:3:ffff::9")
	if limitAddr(a) != limitAddr(b) {
		t.Errorf("two addresses of one /64 count apart: %s, %s", limitAddr(a), limitAddr(b))
	}
	if other := netip.MustParseAddr("2001:db8:2:4::1"); limitAddr(a) == limitAddr(other) {
		t.Errorf("two /64s count together: %s", limitAddr(a))
	}
	if got := limitAddr(netip.MustParseAddr("203.0.113.5")); got != "203.0.113.5" {
		t.Errorf("IPv4 = %s, want the address itself", got)
	}
}

func TestMemCounters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := t0
	c := newMemCounters(func() time.Time { return now }, 3)
	take := func(key string) (time.Duration, bool) { return c.Take(ctx, key, 2, time.Minute) }

	if _, ok := take("a"); !ok {
		t.Fatal("first event refused")
	}
	if _, full := c.Full(ctx, "a", 2, time.Minute); full {
		t.Error("full after one event of two")
	}
	now = now.Add(20 * time.Second)
	take("a")
	if retry, ok := take("a"); ok || retry != 40*time.Second {
		t.Errorf("third event = %v, %t; want refused until the first leaves in 40s", retry, ok)
	}
	if retry, full := c.Full(ctx, "a", 2, time.Minute); !full || retry != 40*time.Second {
		t.Errorf("Full = %v, %t; want 40s, true", retry, full)
	}

	// The window slides: the first event leaves, the second still counts.
	now = now.Add(40 * time.Second)
	if _, ok := take("a"); !ok {
		t.Error("event refused after the oldest left the window")
	}
	if retry, ok := take("a"); ok || retry != 20*time.Second {
		t.Errorf("event = %v, %t; want refused for 20s", retry, ok)
	}
	c.Forget(ctx, "a")
	if _, ok := take("a"); !ok {
		t.Error("event refused after Forget")
	}
}

func TestMemCountersStayUnderTheirBound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := t0
	c := newMemCounters(func() time.Time { return now }, 3)
	for _, key := range []string{"first", "second", "third"} {
		c.Take(ctx, key, 5, time.Minute)
		now = now.Add(10 * time.Second)
	}
	c.Take(ctx, "second", 5, time.Minute) // now the last to run out

	// Full and nothing has run out: of the keys holding the fewest events,
	// the one that would run out first goes.
	c.Take(ctx, "fourth", 5, time.Minute)
	if _, kept := c.byKey["first"]; kept || len(c.byKey) != 3 {
		t.Errorf("keys = %d, first kept %t; want 3 keys without the one nearest its end", len(c.byKey), kept)
	}

	// Keys whose window has ended go before any live one.
	now = now.Add(55 * time.Second) // third has run out; second and fourth have not
	c.Take(ctx, "fifth", 5, time.Minute)
	for key, want := range map[string]bool{"second": true, "third": false, "fourth": true, "fifth": true} {
		if _, kept := c.byKey[key]; kept != want {
			t.Errorf("%s kept = %t, want %t", key, kept, want)
		}
	}
}

// A flood of new keys must not push out a key at its limit: that is the one
// someone flooding would want gone.
func TestMemCountersKeepAKeyAtItsLimitThroughAFlood(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := t0
	c := newMemCounters(func() time.Time { return now }, 50)
	for range 5 {
		c.Take(ctx, "locked", 5, time.Hour)
	}
	for i := range 5000 {
		c.Take(ctx, fmt.Sprintf("flood-%d", i), 5, time.Hour)
	}
	if _, full := c.Full(ctx, "locked", 5, time.Hour); !full {
		t.Error("the key at its limit was dropped by a flood of new keys")
	}
	if len(c.byKey) != 50 {
		t.Errorf("keys = %d, want the bound, 50", len(c.byKey))
	}
}

func TestHashGateWaitsForASlot(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := newHashGate(1, passwordCheckWait)
		release, ok := g.acquire(t.Context())
		if !ok {
			t.Fatal("a free slot was refused")
		}

		start := time.Now()
		if _, ok := g.acquire(t.Context()); ok || time.Since(start) != passwordCheckWait {
			t.Errorf("second caller: ok %t after %v, want refused after %v", ok, time.Since(start), passwordCheckWait)
		}

		// A slot that frees during the wait goes to the waiter.
		got := make(chan bool)
		go func() {
			_, ok := g.acquire(t.Context())
			got <- ok
		}()
		synctest.Wait()
		time.Sleep(passwordCheckWait / 2)
		release()
		if !<-got {
			t.Error("the waiter did not get the slot that came free")
		}

		// A caller who goes away stops waiting.
		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			_, ok := g.acquire(ctx)
			got <- ok
		}()
		synctest.Wait()
		cancel()
		if <-got {
			t.Error("a cancelled caller was given a slot")
		}
	})
}

// refusingCounters is a Counters that is always full.
type refusingCounters struct{}

func (refusingCounters) Take(context.Context, string, int, time.Duration) (time.Duration, bool) {
	return time.Minute, false
}

func (refusingCounters) Full(context.Context, string, int, time.Duration) (time.Duration, bool) {
	return time.Minute, true
}

func (refusingCounters) Forget(context.Context, string) {}

func TestNewWiresTheSignInLimitsFromDeps(t *testing.T) {
	t.Parallel()
	// lastOf sends one request as forwarded and then a full allowance as
	// another client, through the same peer, and returns the last status.
	lastOf := func(t *testing.T, d Deps, forwarded string) int {
		t.Helper()
		d.Logger, d.Store = slog.New(slog.DiscardHandler), newFakeStore()
		srv, err := New(d)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/github/start", nil)
		rec := httptest.NewRecorder()
		for range addrRequests + 1 {
			req.Header.Set("X-Forwarded-For", forwarded)
			rec = httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			forwarded = "198.51.100.2" // every request after the first
		}
		return rec.Code
	}

	t.Run("counters passed in are the ones used", func(t *testing.T) {
		t.Parallel()
		srv, err := New(Deps{Logger: slog.New(slog.DiscardHandler), Store: newFakeStore(), Counters: refusingCounters{}})
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/github/start", nil))
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("first request = %d, want 429 from the counters in Deps", rec.Code)
		}
	})
	t.Run("without trusted proxies the peer is counted", func(t *testing.T) {
		t.Parallel()
		if got := lastOf(t, Deps{}, "198.51.100.1"); got != http.StatusTooManyRequests {
			t.Errorf("request %d from one peer = %d, want 429", addrRequests+1, got)
		}
	})
	t.Run("a trusted proxy's clients are counted apart", func(t *testing.T) {
		t.Parallel()
		d := Deps{}
		d.Config.TrustedProxies = []string{"192.0.2.0/24"}
		if got := lastOf(t, d, "198.51.100.1"); got == http.StatusTooManyRequests {
			t.Errorf("request %d from the second client = 429: the first client's request counted against it", addrRequests)
		}
	})
	t.Run("a bad proxy list stops the server", func(t *testing.T) {
		t.Parallel()
		d := Deps{Logger: slog.New(slog.DiscardHandler), Store: newFakeStore()}
		d.Config.TrustedProxies = []string{"proxy.internal"}
		if _, err := New(d); err == nil || !strings.Contains(err.Error(), "LORVIK_TRUSTED_PROXIES") {
			t.Errorf("New() error = %v, want one naming LORVIK_TRUSTED_PROXIES", err)
		}
	})
}
