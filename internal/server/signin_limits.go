package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// The sign-in limits. They are fixed on purpose: a number an operator can
// raise is a number an operator can raise too far.
const (
	// passwordChecks is how many password hashes run at once, which bounds
	// what a burst of sign-ins can make the server allocate. Each takes the
	// memory written into the hash it reads: what domain.HashPassword is set
	// to, or more for a hash stored under earlier parameters.
	passwordChecks = 4
	// passwordCheckWait is how long a sign-in waits for one of them.
	passwordCheckWait = 2 * time.Second

	// A wrong password counts against the login from the address that sent
	// it, and against the login from anywhere.
	pairFailures  = 5
	pairWindow    = 15 * time.Minute
	loginFailures = 20
	loginWindow   = time.Hour
	// loginShare is how many of one address's wrong passwords count toward
	// loginFailures inside its window. Without it a single address, kept
	// under its own limit, would fill the login's count from anywhere in 45
	// minutes and shut the owner out from every address. With it that takes
	// loginFailures/loginShare addresses.
	loginShare = 5
	// failedEventWindow is how often a login's wrong passwords are recorded
	// as an event of one type: the first in the window is, the rest are only
	// logged. Rules 2 and 3 still let a few addresses make about 80 wrong
	// passwords an hour against one login, from callers who are not signed
	// in, and an event for each would grow the event log at their will.
	failedEventWindow = time.Hour

	// Every request to a public sign-in route counts against its address.
	addrRequests = 30
	addrWindow   = time.Minute
)

// Events of the security log.
const (
	eventUnknownLogin  = "signin.unknown_login"
	eventWrongPassword = "signin.wrong_password"
	eventThrottledAddr = "signin.throttled.address"
	eventThrottledPair = "signin.throttled.login_address"
	eventThrottledAny  = "signin.throttled.login"
	eventBusy          = "signin.busy"
)

// forwardedIgnored is logged once, the first time a request carries
// X-Forwarded-For from a peer that is not a trusted proxy.
const forwardedIgnored = "X-Forwarded-For ignored: the peer is not in LORVIK_TRUSTED_PROXIES; behind a reverse proxy every client then shares the proxy's sign-in allowance"

// signInLimits holds what the public sign-in routes count and bound.
type signInLimits struct {
	// addr, pair and login are separate so a flood of one kind of key cannot
	// push another kind's counts out of a full in-memory map.
	addr, pair, login Counters
	// failed spaces out the events recorded for wrong passwords.
	failed  Counters
	gate    *hashGate
	proxies proxies
	logger  *slog.Logger
	// key keys the hashes below. It is made at start and never leaves the
	// process, so neither a counter key nor a log line can be turned back
	// into a login or an address by guessing.
	key []byte

	dummyOnce sync.Once
	dummy     []byte
	dummyErr  error

	forwardedOnce sync.Once
}

func newSignInLimits(now func() time.Time, logger *slog.Logger) *signInLimits {
	key := make([]byte, 32)
	_, _ = rand.Read(key) // never fails
	return &signInLimits{
		addr:   newMemCounters(now, maxCounterKeys),
		pair:   newMemCounters(now, maxCounterKeys),
		login:  newMemCounters(now, maxCounterKeys),
		failed: newMemCounters(now, maxCounterKeys),
		gate:   newHashGate(passwordChecks, passwordCheckWait),
		logger: logger,
		key:    key,
	}
}

// share makes every limit count in c.
func (l *signInLimits) share(c Counters) { l.addr, l.pair, l.login, l.failed = c, c, c, c }

// recordsFailure reports whether a wrong password for the user's login is
// due an event of type typ: the first one in failedEventWindow.
func (l *signInLimits) recordsFailure(ctx context.Context, typ domain.EventType, userID domain.ID) bool {
	_, ok := l.failed.Take(ctx, l.failedKey(typ, userID), 1, failedEventWindow)
	return ok
}

// forgetFailure gives back what recordsFailure took, for a wrong password
// that was due an event and got none.
func (l *signInLimits) forgetFailure(ctx context.Context, typ domain.EventType, userID domain.ID) {
	l.failed.Forget(ctx, l.failedKey(typ, userID))
}

func (l *signInLimits) failedKey(typ domain.EventType, userID domain.ID) string {
	return l.hash("failed", typ.String(), userID.String())
}

// hash is the keyed hash of parts under a kind. Only the last part may hold
// arbitrary bytes: the others must not contain a NUL.
func (l *signInLimits) hash(kind string, parts ...string) string {
	m := hmac.New(sha256.New, l.key)
	m.Write([]byte(kind))
	for _, p := range parts {
		m.Write([]byte{0})
		m.Write([]byte(p))
	}
	return hex.EncodeToString(m.Sum(nil)[:16])
}

// attempt is one password sign-in try: who sent it and the keys its limits
// count under.
type attempt struct {
	caller                      caller
	login                       string
	pairKey, shareKey, loginKey string
}

func (l *signInLimits) attempt(ctx context.Context, login string) attempt {
	c := callerFrom(ctx)
	login = domain.PasswordKey(login)
	return attempt{
		caller:   c,
		login:    login,
		pairKey:  l.hash("pair", limitAddr(c.addr), login),
		shareKey: l.hash("share", limitAddr(c.addr), login),
		loginKey: l.hash("login", login),
	}
}

// throttled refuses an attempt whose login has had too many wrong passwords,
// from this address or from anywhere. It reads no database and checks no
// password, and it answers the same for a login that exists and one that
// does not.
func (l *signInLimits) throttled(ctx context.Context, a attempt) error {
	if retry, full := l.pair.Full(ctx, a.pairKey, pairFailures, pairWindow); full {
		l.log(ctx, eventThrottledPair, a.caller, a.login, 0)
		return tooManyAttempts(retry)
	}
	if retry, full := l.login.Full(ctx, a.loginKey, loginFailures, loginWindow); full {
		l.log(ctx, eventThrottledAny, a.caller, a.login, 0)
		return tooManyAttempts(retry)
	}
	return nil
}

// count records how a password check ended. A wrong password counts against
// the login from this address and, for the address's first loginShare in the
// window, from anywhere. A right one forgets the
// wrong passwords this address sent for this login and nothing else: the
// count for the login from anywhere stays, because a right password from one
// address says nothing about the others, and clearing it would hand someone
// guessing from many addresses a fresh allowance each time the owner signs
// in. No key of another login is touched, so signing in to one's own account
// cannot reset the count on someone else's.
//
// filled reports that this wrong password was the one that brought a count
// to its limit, so the tries that follow are refused.
func (l *signInLimits) count(ctx context.Context, a attempt, right bool) (filled bool) {
	if right {
		l.pair.Forget(ctx, a.pairKey)
		return false
	}
	if _, counted := l.pair.Take(ctx, a.pairKey, pairFailures, pairWindow); counted {
		_, filled = l.pair.Full(ctx, a.pairKey, pairFailures, pairWindow)
	}
	if _, counts := l.pair.Take(ctx, a.shareKey, loginShare, loginWindow); counts {
		if _, counted := l.login.Take(ctx, a.loginKey, loginFailures, loginWindow); counted && !filled {
			_, filled = l.login.Full(ctx, a.loginKey, loginFailures, loginWindow)
		}
	}
	return filled
}

// dummyHash is a password hash nobody knows the password of, made once with
// the parameters in force. An unknown login is checked against it, so it
// costs what a wrong password costs.
func (l *signInLimits) dummyHash() ([]byte, error) {
	l.dummyOnce.Do(func() { l.dummy, l.dummyErr = domain.HashPassword(rand.Text()) })
	return l.dummy, l.dummyErr
}

// log is the one writer of security log lines. It takes the source address,
// the user agent, the attempted login (written only as a keyed hash) and the
// user id when one is known; a call site has no way to add anything else.
func (l *signInLimits) log(ctx context.Context, event string, c caller, login string, userID domain.ID) {
	attrs := []slog.Attr{slog.String("event", event), slog.String("request_id", RequestIDFrom(ctx))}
	if c.addr.IsValid() {
		attrs = append(attrs, slog.String("source_ip", c.addr.String()))
	}
	if c.userAgent != "" {
		attrs = append(attrs, slog.String("user_agent", c.userAgent))
	}
	if login != "" {
		attrs = append(attrs, slog.String("login_hash", l.hash("log", login)))
	}
	if userID != 0 {
		attrs = append(attrs, slog.String("user_id", userID.String()))
	}
	l.logger.LogAttrs(ctx, slog.LevelWarn, "security", attrs...)
}

// maxLoggedUserAgent bounds how much of a client-chosen header a log line
// carries.
const maxLoggedUserAgent = 200

// signInRoute reports whether r is one of the public sign-in routes, and
// whether it is one a browser opens. HEAD counts with GET: the mux serves it
// with the GET handler, so it does the same work.
func signInRoute(r *http.Request) (ok, browser bool) {
	switch r.URL.Path {
	case apiPrefix + "/login", apiPrefix + "/login/github":
		return r.Method == http.MethodPost, false
	case oauthPath + "/start", oauthPath + "/callback":
		return r.Method == http.MethodGet || r.Method == http.MethodHead, true
	}
	return false, false
}

// guard wraps a handler that serves public sign-in routes. It notes who is
// calling for the handlers behind it and counts the request against its
// address before anything else runs; over the limit it answers 429. Other
// routes pass through untouched.
func (l *signInLimits) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if l == nil {
			next.ServeHTTP(w, r)
			return
		}
		counted, browser := signInRoute(r)
		if !counted {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		if r.Header.Get("X-Forwarded-For") != "" && !l.proxies.trusted(parseAddr(r.RemoteAddr)) {
			l.forwardedOnce.Do(func() { l.logger.WarnContext(ctx, forwardedIgnored) })
		}
		c := caller{addr: l.proxies.clientAddr(r), userAgent: r.UserAgent()}
		if len(c.userAgent) > maxLoggedUserAgent {
			c.userAgent = c.userAgent[:maxLoggedUserAgent]
		}
		retry, ok := l.addr.Take(ctx, l.hash("addr", limitAddr(c.addr)), addrRequests, addrWindow)
		if !ok {
			// One line per address and window: a flood must not also flood
			// the log.
			if _, first := l.addr.Take(ctx, l.hash("addr-logged", limitAddr(c.addr)), 1, addrWindow); first {
				l.log(ctx, eventThrottledAddr, c, "", 0)
			}
			p := tooManyAttempts(retry)
			if browser {
				// The browser routes answer a person, not a client.
				maps.Copy(w.Header(), p.GetHeaders())
				http.Error(w, p.Detail, p.Status)
				return
			}
			writeProblem(w, p)
			return
		}
		next.ServeHTTP(w, r.WithContext(withCaller(ctx, c)))
	})
}

func tooManyAttempts(retry time.Duration) *problemError {
	p := newProblem(http.StatusTooManyRequests, "too many sign-in attempts; try again in "+waitText(retry),
		"wait, then try once; tokens you already hold keep working")
	p.retryAfter = retry
	return p
}

func signInBusy() *problemError {
	p := newProblem(http.StatusServiceUnavailable, "sign-in is busy; try again in a few seconds", "retry the same request")
	p.retryAfter = passwordCheckWait
	return p
}

// waitText words a wait for a person: whole seconds under a minute, else
// whole minutes, both rounded up.
func waitText(d time.Duration) string {
	if d < time.Minute {
		return plural(retrySeconds(d), "second")
	}
	return plural(int(math.Ceil(d.Minutes())), "minute")
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

// retrySeconds is a wait as Retry-After carries it: whole seconds, rounded
// up, never zero.
func retrySeconds(d time.Duration) int {
	return max(1, int(math.Ceil(d.Seconds())))
}

// hashGate bounds how many password checks run at once.
type hashGate struct {
	slots chan struct{}
	wait  time.Duration
}

func newHashGate(n int, wait time.Duration) *hashGate {
	return &hashGate{slots: make(chan struct{}, n), wait: wait}
}

// acquire takes a slot, waiting up to the gate's wait for one. The caller
// runs release when its check is done. ok is false when no slot came free in
// time or the request went away.
func (g *hashGate) acquire(ctx context.Context) (release func(), ok bool) {
	select {
	case g.slots <- struct{}{}:
		return g.release, true
	default:
	}
	if g.wait <= 0 {
		return nil, false
	}
	t := time.NewTimer(g.wait)
	defer t.Stop()
	select {
	case g.slots <- struct{}{}:
		return g.release, true
	case <-t.C:
		return nil, false
	case <-ctx.Done():
		return nil, false
	}
}

func (g *hashGate) release() { <-g.slots }

// checkPassword reports whether password is the one stored in secret, and
// counts the outcome, all inside the gate. known is false for a login with no
// identity: the check then runs against the dummy hash and reports false, so
// the caller cannot tell the two apart by how long the answer took. filled
// is count's: this wrong password brought a limit to its bound.
func (l *signInLimits) checkPassword(ctx context.Context, a attempt, secret []byte, known bool, password string) (right, filled bool, err error) {
	release, ok := l.gate.acquire(ctx)
	if !ok {
		if ctx.Err() == nil {
			l.log(ctx, eventBusy, a.caller, a.login, 0)
		}
		return false, false, signInBusy()
	}
	defer release()
	// Again, now that at most passwordChecks callers are between this read
	// and their count: requests that arrived together overshoot a limit by
	// at most the others in the gate.
	if err := l.throttled(ctx, a); err != nil {
		return false, false, err
	}
	dummy, err := l.dummyHash()
	if err != nil {
		return false, false, fmt.Errorf("make dummy hash: %w", err)
	}
	if known {
		right, err = domain.VerifyPassword(secret, password)
	}
	if !known || err != nil {
		_, _ = domain.VerifyPassword(dummy, password)
	}
	if err != nil {
		// A stored hash that cannot be read is refused like a wrong
		// password, after the same work, and reported to the operator.
		l.logger.ErrorContext(ctx, "verify password", "error", err, "request_id", RequestIDFrom(ctx))
	}
	return right, l.count(ctx, a, right), nil
}
