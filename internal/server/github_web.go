package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/config"
)

const (
	oauthCookie = "lorvik_oauth"
	// oauthHostCookie is the oauth cookie's name over HTTPS. Browsers take a
	// __Host- cookie only from this exact host, so another site under the
	// same parent domain cannot plant one that carries the state of a link it
	// started and have a visitor finish that link.
	oauthHostCookie = "__Host-" + oauthCookie
	oauthPath       = "/auth/github"
	oauthLifetime   = 10 * time.Minute

	handoffCookie = "lorvik_handoff"
	// handoffHostCookie is the handoff cookie's name over HTTPS, for the same
	// reason as oauthHostCookie: a handoff cookie planted from a sibling
	// subdomain would sign the visitor in to the planter's account.
	handoffHostCookie = "__Host-" + handoffCookie
	handoffPath       = "/login"
	handoffLifetime   = 60 * time.Second

	linkPath    = apiPrefix + "/me/github-link"
	linkDefault = "/admin"
	// linkFlow marks an oauth cookie as a link. It only picks the path the
	// callback takes; who the link is for comes from Service.links.
	linkFlow = "link"
	// chooseFlow marks a sign-in that asked to choose its org.
	chooseFlow = "choose"
)

// githubWeb serves the browser half of GitHub sign-in on root paths: it
// redirects and sets cookies, so it sits outside huma and the bearer
// middleware. The one exception is link, which needs the caller's identity
// and is registered inside the API's middleware (newAPI).
type githubWeb struct {
	svc       *Service
	publicURL string
	logger    *slog.Logger
}

func newGitHubWeb(svc *Service, cfg config.Server, logger *slog.Logger) *githubWeb {
	return &githubWeb{svc: svc, publicURL: strings.TrimRight(cfg.PublicURL, "/"), logger: logger}
}

func (g *githubWeb) register(mux *http.ServeMux) {
	var limits *signInLimits
	if g.svc != nil {
		limits = g.svc.limits
	}
	mux.Handle("GET "+oauthPath+"/start", limits.guard(http.HandlerFunc(g.start)))
	mux.Handle("GET "+oauthPath+"/callback", limits.guard(http.HandlerFunc(g.callback)))
}

// registerLink adds the authenticated link route to the API mux, which is
// wrapped by the bearer middleware.
func (g *githubWeb) registerLink(mux *http.ServeMux) {
	mux.HandleFunc("POST "+linkPath, g.link)
}

func (g *githubWeb) enabled() bool { return g.svc != nil && g.svc.github != nil }

// baseURL is the externally visible origin: LORVIK_PUBLIC_URL, else what the
// request says, trusting X-Forwarded-Proto.
func (g *githubWeb) baseURL(r *http.Request) string {
	if g.publicURL != "" {
		return g.publicURL
	}
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	proto = strings.TrimSpace(proto)
	if proto == "" {
		proto = "http"
		if r.TLS != nil {
			proto = "https"
		}
	}
	return proto + "://" + r.Host
}

func (g *githubWeb) redirectURI(r *http.Request) string {
	return g.baseURL(r) + oauthPath + "/callback"
}

// oauthCookie writes or, with an empty value, clears the oauth cookie. The
// __Host- form must cover the whole origin.
func (g *githubWeb) oauthCookie(r *http.Request, value string, lifetime time.Duration) *http.Cookie {
	name, path := oauthCookie, oauthPath
	if g.secure(r) {
		name, path = oauthHostCookie, "/"
	}
	return g.cookie(r, name, value, path, lifetime, http.SameSiteLaxMode, true)
}

// handoffCookieFor writes the handoff cookie. The page reads it, so it is not
// HttpOnly; the __Host- form must cover the whole origin.
func (g *githubWeb) handoffCookieFor(r *http.Request, value string) *http.Cookie {
	name, path := handoffCookie, handoffPath
	if g.secure(r) {
		name, path = handoffHostCookie, "/"
	}
	return g.cookie(r, name, value, path, handoffLifetime, http.SameSiteStrictMode, false)
}

func (g *githubWeb) secure(r *http.Request) bool {
	return strings.HasPrefix(g.baseURL(r), "https://")
}

func (g *githubWeb) cookie(r *http.Request, name, value, path string, lifetime time.Duration, sameSite http.SameSite, httpOnly bool) *http.Cookie {
	c := &http.Cookie{Name: name, Value: value, Path: path, HttpOnly: httpOnly, SameSite: sameSite, Secure: g.secure(r)}
	if lifetime > 0 {
		c.MaxAge = int(lifetime / time.Second)
	} else {
		c.MaxAge = -1
	}
	return c
}

// safeNext keeps only same-origin paths so the callback cannot be turned
// into an open redirect.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n") {
		return "/"
	}
	return next
}

func (g *githubWeb) start(w http.ResponseWriter, r *http.Request) {
	if !g.enabled() {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	flow := ""
	if q.Get("choose") != "" {
		flow = chooseFlow
	}
	authorize, _ := g.begin(w, r, safeNext(q.Get("next")), q.Get("org"), flow)
	http.Redirect(w, r, authorize, http.StatusFound)
}

// begin stores a fresh state in the oauth cookie and returns GitHub's
// authorize URL with that state. flow is linkFlow, chooseFlow, or empty for a
// plain sign-in.
func (g *githubWeb) begin(w http.ResponseWriter, r *http.Request, next, org, flow string) (authorize, state string) {
	state = rand.Text()
	enc := base64.RawURLEncoding
	value := state + "." + enc.EncodeToString([]byte(next)) + "." + enc.EncodeToString([]byte(org)) + "." + flow
	http.SetCookie(w, g.oauthCookie(r, value, oauthLifetime))
	w.Header().Set("Cache-Control", "no-store")
	return g.svc.github.authorizeURL(g.redirectURI(r), state), state
}

// link starts the flow for a signed-in user: the callback then adds the
// GitHub identity to that user instead of signing anyone in. It answers the
// authorize URL as JSON because the page, not the browser, holds the bearer.
func (g *githubWeb) link(w http.ResponseWriter, r *http.Request) {
	fail := func(err error) { writeProblem(w, toProblem(err)) }
	if g.svc == nil {
		fail(githubOff())
		return
	}
	p, err := g.svc.userPrincipal(r.Context())
	if err == nil {
		err = p.requireWritable()
	}
	if err != nil {
		fail(err)
		return
	}
	if !g.enabled() {
		fail(githubOff())
		return
	}
	var in apitypes.GitHubLinkStart
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		fail(invalidField("body", "must be JSON"))
		return
	}
	next := linkDefault
	if in.Next != "" {
		next = safeNext(in.Next)
	}
	authorize, state := g.begin(w, r, next, "", linkFlow)
	now := g.svc.now()
	g.svc.links.put(state, pendingLink{actor: p.Actor, tokenID: p.TokenID, session: p.Session, expires: now.Add(oauthLifetime)}, now)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(apitypes.GitHubLink{URL: authorize})
}

// parseOAuthCookie splits the cookie written by begin. flow is linkFlow,
// chooseFlow or empty. Every part is the browser's to change, so none of
// them says who is signing in or linking.
func parseOAuthCookie(v string) (state, next, org, flow string, ok bool) {
	parts := strings.Split(v, ".")
	if len(parts) != 4 || parts[0] == "" || !slices.Contains([]string{"", linkFlow, chooseFlow}, parts[3]) {
		return "", "", "", "", false
	}
	n, err1 := base64.RawURLEncoding.DecodeString(parts[1])
	o, err2 := base64.RawURLEncoding.DecodeString(parts[2])
	if err1 != nil || err2 != nil {
		return "", "", "", "", false
	}
	return parts[0], safeNext(string(n)), string(o), parts[3], true
}

// withQuery adds one parameter to a same-origin path that may already have a
// query.
func withQuery(path, key, value string) string {
	u, err := url.Parse(path)
	if err != nil {
		return "/?" + url.QueryEscape(key) + "=" + url.QueryEscape(value)
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String()
}

func (g *githubWeb) callback(w http.ResponseWriter, r *http.Request) {
	if !g.enabled() {
		http.NotFound(w, r)
		return
	}
	cleared := g.oauthCookie(r, "", 0)
	c, err := r.Cookie(cleared.Name)
	var state, next, org, flow string
	ok := false
	if err == nil {
		state, next, org, flow, ok = parseOAuthCookie(c.Value)
	}
	link := flow == linkFlow
	http.SetCookie(w, cleared)
	w.Header().Set("Cache-Control", "no-store")
	got := r.URL.Query().Get("state")
	if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(state)) != 1 {
		http.Error(w, "this sign-in did not start in this browser; go back and try again", http.StatusBadRequest)
		return
	}
	to := func(q url.Values) { http.Redirect(w, r, "/login?"+q.Encode(), http.StatusFound) }
	fail := func(code string) { to(url.Values{"error": {code}}) }
	var pending pendingLink
	if link {
		fail = func(code string) { http.Redirect(w, r, withQuery(next, "error", code), http.StatusFound) }
		// Taken before anything else can fail, so a state is spent by its
		// first callback whatever GitHub answered.
		if pending, ok = g.svc.links.take(state, g.svc.now()); !ok {
			fail("expired")
			return
		}
	}

	if e := r.URL.Query().Get("error"); e != "" {
		if e == "access_denied" {
			fail("denied")
			return
		}
		fail("github")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		fail("github")
		return
	}
	ctx := r.Context()
	token, err := g.svc.github.exchange(ctx, code, g.redirectURI(r))
	if err != nil {
		g.logger.WarnContext(ctx, "github code exchange failed", "error", err)
		fail("github")
		return
	}
	prof, err := g.svc.github.profile(ctx, token)
	if err != nil {
		g.logger.WarnContext(ctx, "github profile read failed", "error", err)
		fail("github")
		return
	}
	if link {
		g.finishLink(w, r, pending, next, prof, fail)
		return
	}
	res, err := g.svc.signInGitHub(ctx, prof, signInAsk{Org: org, Choose: flow == chooseFlow, TokenName: webTokenName})
	if err != nil {
		p := toProblem(err)
		if isInternal(p) {
			g.logger.ErrorContext(ctx, "github sign-in failed", "error", err, "request_id", RequestIDFrom(ctx))
		}
		switch {
		case hasProblemField(p, "org"):
			fail("org")
		default:
			fail("github")
		}
		return
	}
	if res.Pending {
		to(url.Values{"error": {"pending"}, "login": {prof.Login}, "org_name": {res.OrgName}})
		return
	}
	// The handoff carries the token, or the sign-in token of a person who
	// has an org to choose or create first; choose tells the page which.
	http.SetCookie(w, g.handoffCookieFor(r, res.Token.Secret))
	q := url.Values{"next": {next}}
	if res.Choose {
		q.Set("choose", "1")
	}
	to(q)
}

func (g *githubWeb) finishLink(w http.ResponseWriter, r *http.Request, pending pendingLink, next string, prof githubProfile, fail func(string)) {
	ctx := r.Context()
	outcome, err := g.svc.linkGitHub(ctx, pending, prof)
	if err != nil {
		g.logger.WarnContext(ctx, "github link failed", "error", err, "request_id", RequestIDFrom(ctx))
		fail("github")
		return
	}
	switch outcome {
	case linkWithdrawn:
		fail("expired")
	case linkElsewhere:
		fail("linked_elsewhere")
	case linkSecond:
		fail("already_linked")
	default:
		http.Redirect(w, r, withQuery(next, "linked", "1"), http.StatusFound)
	}
}

func hasProblemField(p *problemError, field string) bool {
	for _, f := range p.Errors {
		if f.Field == field {
			return true
		}
	}
	return false
}
