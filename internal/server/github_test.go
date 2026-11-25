package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/config"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

const (
	ghClientID     = "client-id"
	ghClientSecret = "client-secret"
	ghAdminSecret  = "lvk_u_admin-secret"
)

type ghUser struct {
	ID     int64
	Login  string
	Name   string
	Email  string
	Avatar string
}

// fakeGitHub answers the endpoints githubClient uses. Codes map to access
// tokens and access tokens to users; login lookups and token checks need the
// app's basic auth. A token in foreign belongs to its user but was issued to
// another app: /user answers it and the token check does not.
type fakeGitHub struct {
	srv     *httptest.Server
	codes   map[string]string
	tokens  map[string]ghUser
	byName  map[string]ghUser
	foreign map[string]bool
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{codes: map[string]string{}, tokens: map[string]ghUser{}, byName: map[string]ghUser{}, foreign: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /applications/{client_id}/token", func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if !ok || id != ghClientID || secret != ghClientSecret || r.PathValue("client_id") != ghClientID {
			http.Error(w, `{"message":"Requires authentication"}`, http.StatusUnauthorized)
			return
		}
		var in struct {
			AccessToken string `json:"access_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		u, found := f.tokens[in.AccessToken]
		if !found || f.foreign[in.AccessToken] {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"app":  map[string]any{"client_id": ghClientID, "name": "Lorvik"},
			"user": map[string]any{"id": u.ID, "login": u.Login},
		})
	})
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		ok := r.Header.Get("Accept") == "application/json" && r.Form.Get("client_id") == ghClientID && r.Form.Get("client_secret") == ghClientSecret
		if tok, found := f.codes[r.Form.Get("code")]; ok && found {
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": tok, "token_type": "bearer", "scope": "read:user,user:email"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code"})
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		u, ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if !ok {
			http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
			return
		}
		body := map[string]any{"id": u.ID, "login": u.Login, "avatar_url": u.Avatar, "name": nil, "email": nil}
		if u.Name != "" {
			body["name"] = u.Name
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("GET /user/emails", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"email": "other@example.com", "primary": false, "verified": true},
			{"email": "unverified@example.com", "primary": true, "verified": false},
		})
	})
	mux.HandleFunc("GET /users/{login}", func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if !ok || id != ghClientID || secret != ghClientSecret {
			http.Error(w, `{"message":"Requires authentication"}`, http.StatusUnauthorized)
			return
		}
		u, found := f.byName[r.PathValue("login")]
		if !found {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": u.ID, "login": u.Login, "name": u.Name, "avatar_url": u.Avatar})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// add registers a user with an access token and, optionally, a login code.
func (f *fakeGitHub) add(u ghUser, token, code string) {
	f.tokens[token] = u
	f.byName[u.Login] = u
	if code != "" {
		f.codes[code] = token
	}
}

// ghEnv is the API over the fake store with GitHub sign-in switched on
// against a fake GitHub, plus the web handlers and an admin caller.
type ghEnv struct {
	*testEnv
	gh    *fakeGitHub
	web   http.Handler
	api   http.Handler
	admin domain.ActorRef
}

func newGHEnv(t *testing.T, publicURL string) *ghEnv {
	t.Helper()
	e := newEnv(t)
	gh := newFakeGitHub(t)
	e.svc.github = newGitHubClient(ghClientID, ghClientSecret, gh.srv.URL, gh.srv.URL, gh.srv.Client(), time.Now, slog.New(slog.DiscardHandler))
	cfg := config.Server{GitHubClientID: ghClientID, GitHubClientSecret: ghClientSecret, PublicURL: publicURL}
	mux := http.NewServeMux()
	newGitHubWeb(e.svc, cfg, slog.New(slog.DiscardHandler)).register(mux)
	admin := e.seedUser("ada", domain.RoleAdmin)
	e.seedToken(ghAdminSecret, admin, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
	api := newAPI(Deps{Config: cfg, Logger: slog.New(slog.DiscardHandler), Store: e.fs, Now: e.clock}, e.svc)
	return &ghEnv{testEnv: e, gh: gh, web: mux, api: api, admin: admin}
}

func (e *ghEnv) get(h http.Handler, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	req.Host = "lorvik.test"
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// oauthCookieOf is the oauth cookie a response set, under the name it takes
// on HTTP or on HTTPS.
func oauthCookieOf(rec *httptest.ResponseRecorder) *http.Cookie {
	if c := cookieNamed(rec, oauthHostCookie); c != nil {
		return c
	}
	return cookieNamed(rec, oauthCookie)
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// signIn runs start and callback for a login code and returns the callback's answer.
func (e *ghEnv) signIn(t *testing.T, startQuery, code string) *httptest.ResponseRecorder {
	t.Helper()
	start := e.get(e.web, "/auth/github/start"+startQuery)
	if start.Code != http.StatusFound {
		t.Fatalf("start = %d: %s", start.Code, start.Body)
	}
	loc, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return e.get(e.web, "/auth/github/callback?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(loc.Query().Get("state")), oauthCookieOf(start))
}

func (e *ghEnv) invite(t *testing.T, login, role string) apitypes.Membership {
	t.Helper()
	return call[apitypes.Membership](e.testEnv, ghAdminSecret, http.MethodPost, "/members/invite", apitypes.Invite{Login: login, Role: role}, http.StatusOK)
}

func (e *ghEnv) membershipOf(login string) (domain.Membership, bool) {
	e.fs.mu.Lock()
	defer e.fs.mu.Unlock()
	for _, u := range e.fs.users {
		if u.Login == login {
			if m, ok := e.fs.members[memberKey(e.org.ID, u.Ref())]; ok {
				return *m, true
			}
		}
	}
	return domain.Membership{}, false
}

func (e *ghEnv) userByLogin(login string) (domain.User, bool) {
	e.fs.mu.Lock()
	defer e.fs.mu.Unlock()
	for _, u := range e.fs.users {
		if u.Login == login {
			return *u, true
		}
	}
	return domain.User{}, false
}

func TestGitHubStartRedirectsToGitHubWithStateCookie(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		publicURL  string
		forwarded  string
		wantBase   string
		wantSecure bool
		wantName   string
		wantPath   string
	}{
		{"derived from the request", "", "", "http://lorvik.test", false, "lorvik_oauth", "/auth/github"},
		{"forwarded proto", "", "https", "https://lorvik.test", true, "__Host-lorvik_oauth", "/"},
		{"public url wins", "https://lorvik.example.com/", "http", "https://lorvik.example.com", true, "__Host-lorvik_oauth", "/"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newGHEnv(t, tc.publicURL)
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/github/start?next=/board", nil)
			req.Host = "lorvik.test"
			if tc.forwarded != "" {
				req.Header.Set("X-Forwarded-Proto", tc.forwarded)
			}
			rec := httptest.NewRecorder()
			e.web.ServeHTTP(rec, req)
			if rec.Code != http.StatusFound {
				t.Fatalf("status = %d", rec.Code)
			}
			loc, _ := url.Parse(rec.Header().Get("Location"))
			if got, want := loc.Scheme+"://"+loc.Host+loc.Path, e.gh.srv.URL+"/login/oauth/authorize"; got != want {
				t.Errorf("redirect = %s, want %s", got, want)
			}
			q := loc.Query()
			if q.Get("client_id") != ghClientID || q.Get("scope") != "read:user user:email" || q.Get("redirect_uri") != tc.wantBase+"/auth/github/callback" || q.Get("state") == "" {
				t.Errorf("authorize query = %v", q)
			}
			c := oauthCookieOf(rec)
			if c == nil {
				t.Fatal("no lorvik_oauth cookie")
			}
			state, next, _, flow, ok := parseOAuthCookie(c.Value)
			if !ok || state != q.Get("state") || next != "/board" || flow != "" {
				t.Errorf("cookie = %q: state %q next %q flow %q", c.Value, state, next, flow)
			}
			// Over HTTPS the name carries __Host-, which a browser honours only
			// with Secure, Path=/ and no Domain.
			if c.Name != tc.wantName || c.Path != tc.wantPath || c.Domain != "" || c.Secure != tc.wantSecure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 600 {
				t.Errorf("cookie = %+v, want %s on %s, no domain, httponly lax 600s secure=%t", c, tc.wantName, tc.wantPath, tc.wantSecure)
			}
		})
	}
}

func TestGitHubStartNextIsSameOriginPathOnly(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	tests := []struct{ next, want string }{
		{"/board?tab=1", "/board?tab=1"},
		{"", "/"},
		{"https://evil.example/x", "/"},
		{"//evil.example/x", "/"},
		{"/\\evil.example", "/"},
		{"board", "/"},
	}
	for _, tc := range tests {
		t.Run(tc.next, func(t *testing.T) {
			t.Parallel()
			rec := e.get(e.web, "/auth/github/start?next="+url.QueryEscape(tc.next))
			_, next, _, _, ok := parseOAuthCookie(oauthCookieOf(rec).Value)
			if !ok || next != tc.want {
				t.Errorf("next = %q (ok %t), want %q", next, ok, tc.want)
			}
		})
	}
}

func TestGitHubWebRoutesAre404WhenOff(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	mux := http.NewServeMux()
	newGitHubWeb(e.svc, config.Server{}, slog.New(slog.DiscardHandler)).register(mux)
	for _, path := range []string{"/auth/github/start", "/auth/github/callback?code=x&state=y"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rec.Code)
		}
	}
}

func TestGitHubCallbackRefusesBadState(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	e.gh.add(ghUser{ID: 1, Login: "mallory"}, "tok", "code")
	start := e.get(e.web, "/auth/github/start")
	good := oauthCookieOf(start)
	state, _, _, _, _ := parseOAuthCookie(good.Value)
	tests := []struct {
		name   string
		target string
		cookie *http.Cookie
	}{
		{"no cookie", "/auth/github/callback?code=code&state=" + state, nil},
		{"wrong state", "/auth/github/callback?code=code&state=other", good},
		{"missing state", "/auth/github/callback?code=code", good},
		{"garbled cookie", "/auth/github/callback?code=code&state=" + state, &http.Cookie{Name: oauthCookie, Value: "nonsense"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var cs []*http.Cookie
			if tc.cookie != nil {
				cs = append(cs, tc.cookie)
			}
			rec := e.get(e.web, tc.target, cs...)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
			if cleared := oauthCookieOf(rec); cleared == nil || cleared.MaxAge >= 0 {
				t.Errorf("oauth cookie = %v, want expired", cleared)
			}
			if cookieNamed(rec, handoffCookie) != nil || rec.Header().Get("Location") != "" {
				t.Errorf("handoff cookie %v location %q, want none", cookieNamed(rec, handoffCookie), rec.Header().Get("Location"))
			}
		})
	}
	if _, ok := e.userByLogin("mallory"); ok {
		t.Error("a refused callback created a user")
	}
}

func TestGitHubCallbackErrorRedirects(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"access denied", "error=access_denied", "/login?error=denied"},
		{"other github error", "error=redirect_uri_mismatch", "/login?error=github"},
		{"no code", "", "/login?error=github"},
		{"code GitHub refuses", "code=stale", "/login?error=github"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			start := e.get(e.web, "/auth/github/start")
			c := oauthCookieOf(start)
			state, _, _, _, _ := parseOAuthCookie(c.Value)
			rec := e.get(e.web, "/auth/github/callback?state="+state+"&"+tc.query, c)
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != tc.want {
				t.Errorf("answer = %d %q, want 302 %q", rec.Code, rec.Header().Get("Location"), tc.want)
			}
			if cleared := oauthCookieOf(rec); cleared == nil || cleared.MaxAge >= 0 {
				t.Errorf("oauth cookie = %+v, want cleared", cleared)
			}
			if cookieNamed(rec, handoffCookie) != nil {
				t.Error("handoff cookie set on an error")
			}
		})
	}
}

// A sign-in link that names an org is how a person asks to join it.
func TestGitHubWebFlowNewUserOfAnOrgsLinkWaitsForAnAdmin(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	e.gh.add(ghUser{ID: 1001, Login: "newbie", Avatar: "https://avatars/1001"}, "tok-newbie", "code-newbie")
	for range 2 {
		rec := e.signIn(t, "?next=/board&org="+e.org.ID.String(), "code-newbie")
		if got, want := rec.Header().Get("Location"), "/login?error=pending&login=newbie&org_name=Acme"; got != want {
			t.Fatalf("location = %q, want %q", got, want)
		}
		if cookieNamed(rec, handoffCookie) != nil {
			t.Fatal("pending user got a handoff cookie")
		}
	}
	m, ok := e.membershipOf("newbie")
	if !ok || m.Status != domain.RecordPending || m.Role != domain.RoleViewer || m.GrantedBy != nil {
		t.Fatalf("membership = %+v, want pending viewer without granted_by", m)
	}
	u, _ := e.userByLogin("newbie")
	if u.Name != "newbie" || u.AvatarURL != "https://avatars/1001" || u.Email != "" || u.Status != domain.RecordActive {
		t.Errorf("user = %+v, want name falling back to login, no email (none verified and primary)", u)
	}
	if n := countOf(e.eventTypes(), "membership.requested"); n != 1 {
		t.Errorf("membership.requested recorded %d times, want 1: %v", n, e.eventTypes())
	}
	ev := e.lastEvent()
	if ev.Type != domain.EventMembershipRequested || ev.Actor != u.Ref() || ev.Subject.ID != u.ID {
		t.Errorf("event = %+v, want requested by the user about the user's membership", ev)
	}

	granted := call[apitypes.Membership](e.testEnv, ghAdminSecret, http.MethodPut, "/members/user/"+u.ID.String(), apitypes.PutMembership{Role: "member"}, http.StatusOK)
	if granted.Status != "active" || granted.Role != "member" {
		t.Errorf("granted = %+v, want active member", granted)
	}
	rec := e.signIn(t, "?next=/board", "code-newbie")
	if c := cookieNamed(rec, handoffCookie); c == nil || rec.Header().Get("Location") != "/login?next=%2Fboard" {
		t.Errorf("after grant: location %q handoff %v", rec.Header().Get("Location"), c)
	}
}

func countOf(all []string, want string) int {
	n := 0
	for _, s := range all {
		if s == want {
			n++
		}
	}
	return n
}

// An invitation is written by the inviting org alone, so neither a plain
// sign-in nor a link that names the org accepts it: the person gets the
// choice and accepts by picking the org there.
func TestGitHubWebFlowInvitedUserAcceptsByPickingTheOrg(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "https://lorvik.example.com")
	e.gh.add(ghUser{ID: 1002, Login: "invitee", Name: "Ina Vitee", Avatar: "https://avatars/1002"}, "tok-invitee", "code-invitee")
	invited := e.invite(t, "invitee", "reviewer")
	if invited.Status != "pending" || invited.Role != "reviewer" || invited.GrantedBy != e.admin.ID.String() {
		t.Fatalf("invite = %+v", invited)
	}
	var signIn string
	for _, query := range []string{"?next=/board", "?next=/board&org=" + e.org.ID.String()} {
		rec := e.signIn(t, query, "code-invitee")
		if got, want := rec.Header().Get("Location"), "/login?choose=1&next=%2Fboard"; got != want {
			t.Fatalf("%s: location = %q, want %q", query, got, want)
		}
		c := cookieNamed(rec, handoffHostCookie)
		if c == nil {
			t.Fatalf("%s: no handoff cookie", query)
		}
		if c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.MaxAge != 60 || !c.Secure {
			t.Errorf("handoff flags = %+v, want script-readable strict /login 60s secure", c)
		}
		signIn = c.Value
		e.problem(signIn, http.MethodGet, "/me", nil, http.StatusForbidden)
		if m, _ := e.membershipOf("invitee"); m.Status != domain.RecordPending {
			t.Fatalf("%s: membership = %+v, want the invitation still waiting", query, m)
		}
		if n := countOf(e.eventTypes(), "membership.granted"); n != 0 {
			t.Fatalf("%s: membership.granted = %d, want none before the person picks the org", query, n)
		}
	}
	orgs := call[apitypes.SignInOrgs](e.testEnv, signIn, http.MethodGet, "/login/orgs", nil, http.StatusOK).Orgs
	if want := []apitypes.SignInOrg{{ID: e.org.ID.String(), Name: e.org.Name, Role: "reviewer", Status: "invited"}}; !slices.Equal(orgs, want) {
		t.Fatalf("orgs = %+v, want %+v", orgs, want)
	}
	c := &http.Cookie{Value: call[apitypes.TokenCreated](e.testEnv, signIn, http.MethodPost, "/login/orgs/"+e.org.ID.String()+"/token", apitypes.EnterOrg{}, http.StatusCreated).Secret}
	me := call[apitypes.Me](e.testEnv, c.Value, http.MethodGet, "/me", nil, http.StatusOK)
	if me.Role != "reviewer" || me.User == nil || me.User.Login != "invitee" {
		t.Errorf("me = %+v, want the invitee as reviewer", me)
	}
	m, _ := e.membershipOf("invitee")
	if m.Status != domain.RecordActive || m.Role != domain.RoleReviewer || m.GrantedBy == nil || *m.GrantedBy != e.admin.ID {
		t.Errorf("membership = %+v, want active reviewer granted by the inviter", m)
	}
	types := e.eventTypes()
	if !slices.Contains(types, "membership.invited") || countOf(types, "membership.granted") != 1 {
		t.Errorf("events = %v, want membership.invited then one membership.granted", types)
	}
	var granted domain.Event
	e.fs.mu.Lock()
	for _, ev := range e.fs.events {
		if ev.Type == domain.EventMembershipGranted {
			granted = ev
		}
	}
	e.fs.mu.Unlock()
	if granted.Actor != e.admin {
		t.Errorf("granted by %+v, want the inviter %+v", granted.Actor, e.admin)
	}
	if strings.Contains(string(granted.Payload), c.Value) {
		t.Error("token secret leaked into an event")
	}
	u, _ := e.userByLogin("invitee")
	e.fs.mu.Lock()
	var id domain.Identity
	for _, i := range e.fs.identities {
		if i.UserID == u.ID {
			id = i
		}
	}
	e.fs.mu.Unlock()
	if id.Provider != domain.ProviderGitHub || id.ProviderID != "1002" || id.LastLoginAt == nil {
		t.Errorf("identity = %+v, want github 1002 with last_login_at set by the sign-in", id)
	}
}

func TestGitHubWebFlowActiveUserGetsATokenAndKeepsItsOwnProfile(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	u, _ := e.fs.CreateUser(context.Background(), domain.User{ID: e.ids.New(), Login: "old-name", Name: "Old", AvatarURL: "https://avatars/own", Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	_, _ = e.fs.CreateIdentity(context.Background(), domain.Identity{ID: e.ids.New(), UserID: u.ID, Provider: domain.ProviderGitHub, ProviderID: "1003", Login: "old-name", CreatedAt: t0})
	_, _ = e.fs.PutMembership(context.Background(), domain.Membership{OrgID: e.org.ID, Actor: u.Ref(), Role: domain.RoleMember, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	e.gh.add(ghUser{ID: 1003, Login: "renamed", Name: "New Name", Avatar: "https://avatars/new"}, "tok-active", "code-active")
	before := len(e.eventTypes())

	rec := e.signIn(t, "", "code-active")
	c := cookieNamed(rec, handoffCookie)
	if c == nil || rec.Header().Get("Location") != "/login?next=%2F" {
		t.Fatalf("location %q handoff %v", rec.Header().Get("Location"), c)
	}
	me := call[apitypes.Me](e.testEnv, c.Value, http.MethodGet, "/me", nil, http.StatusOK)
	if me.Actor.ID != u.ID.String() || me.Role != "member" {
		t.Errorf("me = %+v, want the existing user as member", me)
	}
	got, _ := e.fs.GetUser(context.Background(), u.ID)
	if got.Name != "Old" || got.AvatarURL != "https://avatars/own" || got.Login != "old-name" || !got.UpdatedAt.Equal(t0) {
		t.Errorf("user = %+v, want the same user with its own name and avatar, untouched", got)
	}
	if id, err := e.fs.GetIdentity(context.Background(), domain.ProviderGitHub, "1003"); err != nil || id.Login != "renamed" || id.LastLoginAt == nil {
		t.Errorf("identity = %+v (%v), want its login refreshed and the sign-in recorded", id, err)
	}
	if len(e.eventTypes()) != before {
		t.Errorf("events = %v, want none added by a plain sign-in", e.eventTypes()[before:])
	}
	toks := call[apitypes.TokenList](e.testEnv, c.Value, http.MethodGet, "/tokens", nil, http.StatusOK).Items
	if len(toks) != 1 || toks[0].Name != "dashboard" {
		t.Errorf("tokens = %+v, want one named dashboard", toks)
	}
}

func TestGitHubWebFlowRemovedMemberAsksAgain(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	e.gh.add(ghUser{ID: 1004, Login: "returner"}, "tok-ret", "code-ret")
	orgLink := "?org=" + e.org.ID.String()
	e.signIn(t, orgLink, "code-ret")
	u, _ := e.userByLogin("returner")
	call[apitypes.Membership](e.testEnv, ghAdminSecret, http.MethodPut, "/members/user/"+u.ID.String(), apitypes.PutMembership{Role: "member"}, http.StatusOK)
	if rec := e.do(ghAdminSecret, http.MethodDelete, "/members/user/"+u.ID.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("remove = %d", rec.Code)
	}
	rec := e.signIn(t, orgLink, "code-ret")
	if rec.Header().Get("Location") != "/login?error=pending&login=returner&org_name=Acme" || cookieNamed(rec, handoffCookie) != nil {
		t.Errorf("location %q, want pending without a token", rec.Header().Get("Location"))
	}
	m, _ := e.membershipOf("returner")
	if m.Status != domain.RecordPending || m.Role != domain.RoleViewer || m.GrantedBy != nil {
		t.Errorf("membership = %+v, want a fresh pending viewer request", m)
	}
	if n := countOf(e.eventTypes(), "membership.requested"); n != 2 {
		t.Errorf("membership.requested = %d, want 2", n)
	}
}

func TestGitHubWebFlowOrgSelection(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	e.gh.add(ghUser{ID: 1005, Login: "multi"}, "tok-multi", "code-multi")
	ctx := context.Background()
	second, _ := e.fs.CreateOrg(ctx, domain.Org{ID: e.ids.New(), Name: "Beta", Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	closed, _ := e.fs.CreateOrg(ctx, domain.Org{ID: e.ids.New(), Name: "Gone", Status: domain.RecordDeleted, CreatedAt: t0, UpdatedAt: t0})
	for _, bad := range []string{"abc", "12345", closed.ID.String()} {
		rec := e.signIn(t, "?org="+bad, "code-multi")
		if got := rec.Header().Get("Location"); got != "/login?error=org" || cookieNamed(rec, handoffCookie) != nil {
			t.Errorf("org %q: %q, want /login?error=org and no handoff", bad, got)
		}
	}
	if _, ok := e.userByLogin("multi"); ok {
		t.Error("a sign-in naming no usable org created a user")
	}
	if got := e.signIn(t, "?org="+second.ID.String(), "code-multi").Header().Get("Location"); got != "/login?error=pending&login=multi&org_name=Beta" {
		t.Errorf("named org: %q, want pending in Beta", got)
	}
	u := mustUser(t, e, "multi")
	if m, _ := e.fs.GetMembership(ctx, second.ID, u.Ref()); m.Status != domain.RecordPending {
		t.Errorf("membership in the named org = %+v", m)
	}
	if _, err := e.fs.GetMembership(ctx, e.org.ID, u.Ref()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the request reached the org that was not named: %v", err)
	}
}

func mustUser(t *testing.T, e *ghEnv, login string) domain.User {
	t.Helper()
	u, ok := e.userByLogin(login)
	if !ok {
		t.Fatalf("no user %q", login)
	}
	return u
}

func TestLoginGitHub(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	active, _ := e.fs.CreateUser(context.Background(), domain.User{ID: e.ids.New(), Login: "dev", Name: "Dev", Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	_, _ = e.fs.CreateIdentity(context.Background(), domain.Identity{ID: e.ids.New(), UserID: active.ID, Provider: domain.ProviderGitHub, ProviderID: "2001", Login: "dev", CreatedAt: t0})
	_, _ = e.fs.PutMembership(context.Background(), domain.Membership{OrgID: e.org.ID, Actor: active.Ref(), Role: domain.RoleMember, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	e.gh.add(ghUser{ID: 2001, Login: "dev", Name: "Dev"}, "tok-dev", "")
	e.gh.add(ghUser{ID: 2002, Login: "stranger"}, "tok-stranger", "")
	e.gh.add(ghUser{ID: 2003, Login: "later"}, "tok-later", "")

	created := call[apitypes.TokenCreated](e.testEnv, "", http.MethodPost, "/login/github", apitypes.GitHubLogin{AccessToken: "tok-dev", TokenName: "laptop"}, http.StatusCreated)
	if created.Token.Name != "laptop" || !strings.HasPrefix(created.Secret, "lvk_u_") {
		t.Fatalf("created = %+v", created)
	}
	call[apitypes.Me](e.testEnv, created.Secret, http.MethodGet, "/me", nil, http.StatusOK)
	if named := call[apitypes.TokenCreated](e.testEnv, "", http.MethodPost, "/login/github", apitypes.GitHubLogin{AccessToken: "tok-dev"}, http.StatusCreated); named.Token.Name != "dev" {
		t.Errorf("default token name = %q, want the login", named.Token.Name)
	}

	for range 2 {
		p := e.problem("", http.MethodPost, "/login/github", apitypes.GitHubLogin{AccessToken: "tok-stranger", Org: e.org.ID.String()}, http.StatusForbidden)
		if p.Detail != "membership pending approval" || p.Hint != "ask an org admin to grant your role on the Access page" {
			t.Errorf("pending problem = %+v", p)
		}
	}
	if n := countOf(e.eventTypes(), "membership.requested"); n != 1 {
		t.Errorf("membership.requested = %d, want 1 for a repeated request", n)
	}

	if p := e.problem("", http.MethodPost, "/login/github", apitypes.GitHubLogin{AccessToken: "nope"}, http.StatusUnauthorized); p.Detail != "GitHub rejected the token" {
		t.Errorf("rejected detail = %q", p.Detail)
	}

	e.invite(t, "later", "member")
	if got := call[apitypes.TokenCreated](e.testEnv, "", http.MethodPost, "/login/github", apitypes.GitHubLogin{AccessToken: "tok-later"}, http.StatusCreated); got.Secret == "" {
		t.Error("invited user got no token")
	}

	// A second org on the server changes nothing for a person who is in one.
	_, _ = e.fs.CreateOrg(context.Background(), domain.Org{ID: e.ids.New(), Name: "Beta", Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	if got := call[apitypes.TokenCreated](e.testEnv, "", http.MethodPost, "/login/github", apitypes.GitHubLogin{AccessToken: "tok-dev"}, http.StatusCreated); got.Token.Scope != "write" {
		t.Errorf("a member of one org got scope %q, want its token", got.Token.Scope)
	}
}

func TestLoginGitHubRefusesATokenIssuedToAnotherApp(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	dev, _ := e.fs.CreateUser(context.Background(), domain.User{ID: e.ids.New(), Login: "dev", Name: "Dev", Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	_, _ = e.fs.CreateIdentity(context.Background(), domain.Identity{ID: e.ids.New(), UserID: dev.ID, Provider: domain.ProviderGitHub, ProviderID: "2101", Login: "dev", CreatedAt: t0})
	_, _ = e.fs.PutMembership(context.Background(), domain.Membership{OrgID: e.org.ID, Actor: dev.Ref(), Role: domain.RoleOwner, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	e.gh.add(ghUser{ID: 2101, Login: "dev"}, "tok-from-another-app", "")
	e.gh.foreign["tok-from-another-app"] = true

	foreign := e.do("", http.MethodPost, "/login/github", apitypes.GitHubLogin{AccessToken: "tok-from-another-app"})
	unknown := e.do("", http.MethodPost, "/login/github", apitypes.GitHubLogin{AccessToken: "no-such-token"})
	if foreign.Code != http.StatusUnauthorized {
		t.Fatalf("another app's token = %d, want 401: %s", foreign.Code, foreign.Body)
	}
	if foreign.Code != unknown.Code || foreign.Body.String() != unknown.Body.String() {
		t.Errorf("another app's token = %d %s, a token GitHub rejects = %d %s; want the same answer", foreign.Code, foreign.Body, unknown.Code, unknown.Body)
	}
	id, _ := e.fs.GetIdentity(context.Background(), domain.ProviderGitHub, "2101")
	if id.LastLoginAt != nil {
		t.Error("a refused token was recorded as a sign-in")
	}
	e.fs.mu.Lock()
	defer e.fs.mu.Unlock()
	for _, tok := range e.fs.tokens {
		if tok.Actor == dev.Ref() {
			t.Errorf("a refused token minted %+v", tok)
		}
	}
}

func TestGitHubTokenCheck(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		status   int
		body     string
		rejected bool
		wantErr  bool
	}{
		{"issued to this app", http.StatusOK, `{"app":{"client_id":"client-id"}}`, false, false},
		{"answer names another app", http.StatusOK, `{"app":{"client_id":"someone-else"}}`, true, true},
		{"answer names no app", http.StatusOK, `{}`, true, true},
		{"invalid token", http.StatusNotFound, `{"message":"Not Found"}`, true, true},
		{"endpoint throttled", http.StatusUnprocessableEntity, `{"message":"Validation Failed"}`, false, true},
		{"app credentials refused", http.StatusUnauthorized, `{"message":"Bad credentials"}`, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				id, secret, _ := r.BasicAuth()
				if r.Method != http.MethodPost || r.URL.Path != "/applications/client-id/token" || id != ghClientID || secret != ghClientSecret {
					t.Errorf("request = %s %s as %q, want POST /applications/client-id/token with the app's basic auth", r.Method, r.URL.Path, id)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)
			err := newGitHubClient(ghClientID, ghClientSecret, srv.URL, srv.URL, srv.Client(), time.Now, slog.New(slog.DiscardHandler)).issuedHere(context.Background(), "gho_supersecret")
			if (err != nil) != tc.wantErr || errors.Is(err, errGitHubRejected) != tc.rejected {
				t.Errorf("err = %v, want error %t, rejected %t", err, tc.wantErr, tc.rejected)
			}
			if err != nil && (strings.Contains(err.Error(), "gho_supersecret") || strings.Contains(err.Error(), ghClientSecret)) {
				t.Errorf("error leaks a secret: %v", err)
			}
		})
	}
}

func TestLoginGitHubIsNotFoundWhenOff(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.problem("", http.MethodPost, "/login/github", apitypes.GitHubLogin{AccessToken: "x"}, http.StatusNotFound)
	e.problem(userSecret, http.MethodPost, "/members/invite", apitypes.Invite{Login: "x", Role: "member"}, http.StatusNotFound)
}

func TestGitHubAuthAnswer(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	rec := e.get(e.api, "/api/v1/auth/github")
	var on apitypes.GitHubAuth
	if err := json.Unmarshal(rec.Body.Bytes(), &on); err != nil || rec.Code != http.StatusOK || !on.Enabled || on.ClientID != ghClientID {
		t.Errorf("on = %d %s", rec.Code, rec.Body)
	}
	off := httptest.NewRecorder()
	newAPI(Deps{Logger: slog.New(slog.DiscardHandler), Store: e.fs}, e.svc).ServeHTTP(off, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/auth/github", nil))
	if strings.Contains(off.Body.String(), "client_id") || !strings.Contains(off.Body.String(), `"enabled":false`) {
		t.Errorf("off = %d %s, want enabled false without client_id", off.Code, off.Body)
	}
}

func TestInviteMember(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	e.gh.add(ghUser{ID: 3001, Login: "friend", Name: "Fri End", Avatar: "https://avatars/3001"}, "tok-friend", "")
	known := e.seedUser("known", 0)
	_, _ = e.fs.CreateIdentity(context.Background(), domain.Identity{ID: e.ids.New(), UserID: known.ID, Provider: domain.ProviderGitHub, ProviderID: "3002", Login: "known", CreatedAt: t0})
	e.gh.add(ghUser{ID: 3002, Login: "known"}, "tok-known", "")
	e.gh.byName["Mixed"] = ghUser{ID: 3003, Login: "Mixed"}

	m := e.invite(t, "friend", "member")
	if m.Status != "pending" || m.Role != "member" || m.GrantedBy != e.admin.ID.String() {
		t.Fatalf("invite = %+v, want pending member granted_by the caller", m)
	}
	u, ok := e.userByLogin("friend")
	if !ok || u.Name != "Fri End" || u.AvatarURL != "https://avatars/3001" || u.Email != "" || m.Actor.ID != u.ID.String() {
		t.Errorf("user = %+v ok=%t", u, ok)
	}
	ev := e.lastEvent()
	if ev.Type != domain.EventMembershipInvited || ev.Actor != e.admin || ev.Subject.ID != u.ID {
		t.Errorf("event = %+v, want membership.invited by the admin", ev)
	}

	again := e.invite(t, "friend", "reviewer")
	if again.Role != "reviewer" || again.CreatedAt != m.CreatedAt {
		t.Errorf("re-invite = %+v, want the pending row overwritten with the new role", again)
	}

	reuse := e.invite(t, "known", "viewer")
	if reuse.Actor.ID != known.ID.String() {
		t.Errorf("existing identity: invite for %s, want the existing user %s", reuse.Actor.ID, known.ID)
	}
	e.fs.mu.Lock()
	identities := len(e.fs.identities)
	e.fs.mu.Unlock()
	if identities != 2 {
		t.Errorf("identities = %d, want only the new one added (existing + friend)", identities)
	}

	if p := e.problem(ghAdminSecret, http.MethodPost, "/members/invite", apitypes.Invite{Login: "ghost", Role: "member"}, http.StatusUnprocessableEntity); !hasField(p, "login") {
		t.Errorf("unknown login problem = %+v, want a login field error", p)
	}
	e.problem(ghAdminSecret, http.MethodPost, "/members/invite", apitypes.Invite{Login: "sam", Role: "member"}, http.StatusUnprocessableEntity)

	e.gh.add(ghUser{ID: 3004, Login: "sam"}, "tok-sam", "")
	_, _ = e.fs.CreateIdentity(context.Background(), domain.Identity{ID: e.ids.New(), UserID: e.member.ID, Provider: domain.ProviderGitHub, ProviderID: "3004", Login: "sam", CreatedAt: t0})
	if p := e.problem(ghAdminSecret, http.MethodPost, "/members/invite", apitypes.Invite{Login: "sam", Role: "viewer"}, http.StatusConflict); p.Detail != "already a member" {
		t.Errorf("active member detail = %q", p.Detail)
	}

	e.problem(ghAdminSecret, http.MethodPost, "/members/invite", apitypes.Invite{Login: "friend", Role: "admin"}, http.StatusForbidden)
	e.problem(memberSecret, http.MethodPost, "/members/invite", apitypes.Invite{Login: "friend", Role: "viewer"}, http.StatusForbidden)
	e.problem(agentSecret, http.MethodPost, "/members/invite", apitypes.Invite{Login: "friend", Role: "viewer"}, http.StatusForbidden)
	e.problem("", http.MethodPost, "/members/invite", apitypes.Invite{Login: "friend", Role: "viewer"}, http.StatusUnauthorized)
	if owner := call[apitypes.Membership](e.testEnv, userSecret, http.MethodPost, "/members/invite", apitypes.Invite{Login: "friend", Role: "admin"}, http.StatusOK); owner.Role != "admin" {
		t.Errorf("owner invite = %+v, want admin allowed", owner)
	}
}

func TestGitHubClientDoesNotLeakSecretsInErrors(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	e.srvDown()
	_, err := e.svc.github.profile(context.Background(), "gho_supersecret")
	if err == nil {
		t.Fatal("want an error from a dead GitHub")
	}
	if strings.Contains(err.Error(), "gho_supersecret") || strings.Contains(err.Error(), ghClientSecret) {
		t.Errorf("error leaks a secret: %v", err)
	}
	_, err = e.svc.github.exchange(context.Background(), "code", "http://x/cb")
	if err == nil || strings.Contains(err.Error(), ghClientSecret) {
		t.Errorf("exchange error = %v", err)
	}
}

func (e *ghEnv) srvDown() { e.gh.srv.Close() }

// link runs POST /me/github-link as the admin and the callback for code,
// returning the callback's answer.
func (e *ghEnv) link(t *testing.T, body any, code string) *httptest.ResponseRecorder {
	t.Helper()
	rec := e.do(ghAdminSecret, http.MethodPost, "/me/github-link", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("link = %d: %s", rec.Code, rec.Body)
	}
	var out apitypes.GitHubLink
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	loc, err := url.Parse(out.URL)
	if err != nil {
		t.Fatal(err)
	}
	return e.get(e.web, "/auth/github/callback?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(loc.Query().Get("state")), oauthCookieOf(rec))
}

func (e *ghEnv) userCount() int {
	e.fs.mu.Lock()
	defer e.fs.mu.Unlock()
	return len(e.fs.users)
}

func TestGitHubLinkAddsTheIdentityToTheSignedInUser(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body any
		want string
	}{
		{"default next is the Access page", nil, "/admin?linked=1"},
		{"next keeps its query", apitypes.GitHubLinkStart{Next: "/board?tab=x"}, "/board?linked=1&tab=x"},
		{"foreign next falls back to the root", apitypes.GitHubLinkStart{Next: "//evil.test"}, "/?linked=1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newGHEnv(t, "")
			e.gh.add(ghUser{ID: 1101, Login: "ada-gh"}, "tok-link", "code-link")
			users, events := e.userCount(), len(e.eventTypes())

			rec := e.link(t, tc.body, "code-link")
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != tc.want {
				t.Fatalf("callback = %d %q, want 302 %q", rec.Code, rec.Header().Get("Location"), tc.want)
			}
			if cookieNamed(rec, handoffCookie) != nil {
				t.Error("a link minted a handoff cookie")
			}
			id, err := e.fs.GetIdentity(context.Background(), domain.ProviderGitHub, "1101")
			if err != nil || id.UserID != e.admin.ID || id.Login != "ada-gh" || id.LastLoginAt == nil {
				t.Errorf("identity = %+v, %v; want github 1101 on the admin, used just now", id, err)
			}
			if got := e.userCount(); got != users {
				t.Errorf("users = %d, want %d: a link must not create a user", got, users)
			}
			if got := e.eventTypes()[events:]; !slices.Equal(got, []string{"identity.linked"}) {
				t.Fatalf("events = %v, want one identity.linked", got)
			}
			ev := e.lastEvent()
			var payload map[string]string
			_ = json.Unmarshal(ev.Payload, &payload)
			if ev.Actor != e.admin || ev.OrgID != e.org.ID || ev.Subject != membershipSubject(e.admin.ID) || payload["provider"] != "github" || payload["login"] != "ada-gh" {
				t.Errorf("event = %+v payload %v, want the admin linking github ada-gh in their org", ev, payload)
			}
			toks := call[apitypes.TokenList](e.testEnv, ghAdminSecret, http.MethodGet, "/tokens", nil, http.StatusOK).Items
			if len(toks) != 1 {
				t.Errorf("tokens = %+v, want only the caller's", toks)
			}
		})
	}
}

func TestGitHubLinkRefusesAnAccountLinkedToAnotherUser(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	other := e.seedUser("dev", domain.RoleMember)
	_, _ = e.fs.CreateIdentity(context.Background(), domain.Identity{ID: e.ids.New(), UserID: other.ID, Provider: domain.ProviderGitHub, ProviderID: "1102", Login: "dev", CreatedAt: t0})
	e.gh.add(ghUser{ID: 1102, Login: "dev"}, "tok-taken", "code-taken")

	rec := e.link(t, nil, "code-taken")
	if got, want := rec.Header().Get("Location"), "/admin?error=linked_elsewhere"; got != want {
		t.Fatalf("location = %q, want %q", got, want)
	}
	if cookieNamed(rec, handoffCookie) != nil {
		t.Error("a refused link minted a handoff cookie")
	}
	id, _ := e.fs.GetIdentity(context.Background(), domain.ProviderGitHub, "1102")
	if id.UserID != other.ID || id.LastLoginAt != nil {
		t.Errorf("identity = %+v, want untouched on the other user", id)
	}
}

func TestGitHubLinkRefreshesAnIdentityAlreadyOnTheUser(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	_, _ = e.fs.CreateIdentity(context.Background(), domain.Identity{ID: e.ids.New(), UserID: e.admin.ID, Provider: domain.ProviderGitHub, ProviderID: "1103", Login: "old", CreatedAt: t0})
	e.gh.add(ghUser{ID: 1103, Login: "renamed"}, "tok-own", "code-own")
	users := e.userCount()

	rec := e.link(t, nil, "code-own")
	if got, want := rec.Header().Get("Location"), "/admin?linked=1"; got != want {
		t.Fatalf("location = %q, want %q", got, want)
	}
	id, _ := e.fs.GetIdentity(context.Background(), domain.ProviderGitHub, "1103")
	if id.Login != "renamed" || id.LastLoginAt == nil || id.UserID != e.admin.ID || e.userCount() != users {
		t.Errorf("identity = %+v, users %d (was %d); want login refreshed on the same row", id, e.userCount(), users)
	}
	if slices.Contains(e.eventTypes(), "identity.linked") {
		t.Error("refreshing a link that already exists was recorded as a new link")
	}
}

// A second GitHub account on one user would be a way in that the profile does
// not show and, for a user with no password, that cannot be removed.
func TestGitHubLinkRefusesASecondAccountOnTheUser(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	_, _ = e.fs.CreateIdentity(context.Background(), domain.Identity{ID: e.ids.New(), UserID: e.admin.ID, Provider: domain.ProviderGitHub, ProviderID: "1104", Login: "ada-gh", CreatedAt: t0})
	e.gh.add(ghUser{ID: 1105, Login: "mallory"}, "tok-second", "code-second")
	events := len(e.eventTypes())

	rec := e.link(t, nil, "code-second")
	if got, want := rec.Header().Get("Location"), "/admin?error=already_linked"; got != want {
		t.Fatalf("location = %q, want %q", got, want)
	}
	if got := e.githubIdentitiesOf(e.admin.ID); len(got) != 1 || got[0].ProviderID != "1104" || got[0].LastLoginAt != nil {
		t.Errorf("admin identities = %+v, want only github 1104, untouched", got)
	}
	if _, err := e.fs.GetIdentity(context.Background(), domain.ProviderGitHub, "1105"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the second account was stored (err %v)", err)
	}
	if got := e.eventTypes()[events:]; len(got) != 0 {
		t.Errorf("events = %v, want none", got)
	}
}

func TestGitHubLinkIsWithdrawnWhenItsStarterNoLongerStands(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		undo func(e *ghEnv, tok domain.Token, starter domain.ActorRef)
	}{
		{"the token was revoked", func(e *ghEnv, tok domain.Token, _ domain.ActorRef) {
			tok.Status = domain.RecordRevoked
			if err := e.fs.UpdateToken(context.Background(), tok); err != nil {
				e.t.Fatal(err)
			}
		}},
		{"the user was removed from the org", func(e *ghEnv, _ domain.Token, starter domain.ActorRef) {
			e.setOrgRole(starter, domain.RoleMember, domain.RecordRemoved)
		}},
		{"the user was suspended", func(e *ghEnv, _ domain.Token, starter domain.ActorRef) {
			e.setUserStatus(starter, domain.RecordSuspended)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newGHEnv(t, "")
			e.gh.add(ghUser{ID: 1106, Login: "mallory"}, "tok-late", "code-late")
			eve := e.seedUser("eve", domain.RoleMember)
			tok := e.seedToken("lvk_u_eve-withdrawn-secret", eve, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
			state, c := e.startLink(t, "lvk_u_eve-withdrawn-secret")
			events := len(e.eventTypes())

			tc.undo(e, tok, eve)
			if got, want := e.callback("code-late", state, c).Header().Get("Location"), "/admin?error=expired"; got != want {
				t.Errorf("location = %q, want %q", got, want)
			}
			if got := e.githubIdentitiesOf(eve.ID); len(got) != 0 {
				t.Errorf("identities = %+v, want none: the link was started by a caller who no longer stands", got)
			}
			if got := e.eventTypes()[events:]; len(got) != 0 {
				t.Errorf("events = %v, want none", got)
			}
		})
	}
}

// A callback from a browser that did not start the link is refused without
// spending the link, so the browser that did start it can still finish.
func TestGitHubLinkCallbackFromAnotherBrowserLeavesTheLinkUsable(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	e.gh.add(ghUser{ID: 1107, Login: "ada-gh"}, "tok-ada", "code-ada")
	e.gh.add(ghUser{ID: 1108, Login: "mallory"}, "tok-mallory", "code-mallory")
	state, c := e.startLink(t, ghAdminSecret)
	other := oauthCookieOf(e.get(e.web, "/auth/github/start"))

	if rec := e.get(e.web, "/auth/github/callback?code=code-mallory&state="+url.QueryEscape(state)); rec.Code != http.StatusBadRequest {
		t.Errorf("callback without the cookie = %d, want 400", rec.Code)
	}
	if rec := e.callback("code-mallory", state, other); rec.Code != http.StatusBadRequest {
		t.Errorf("callback with another browser's cookie = %d, want 400", rec.Code)
	}
	if got := e.githubIdentitiesOf(e.admin.ID); len(got) != 0 {
		t.Fatalf("admin identities = %+v, want none yet", got)
	}
	if got, want := e.callback("code-ada", state, c).Header().Get("Location"), "/admin?linked=1"; got != want {
		t.Errorf("the starting browser's callback = %q, want %q", got, want)
	}
	if got := e.githubIdentitiesOf(e.admin.ID); len(got) != 1 || got[0].ProviderID != "1107" {
		t.Errorf("admin identities = %+v, want github 1107", got)
	}
}

// Over HTTPS only the __Host- cookie counts: a cookie under the plain name is
// one a sibling subdomain could have planted.
func TestGitHubCallbackOverHTTPSReadsOnlyTheHostCookie(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "https://lorvik.example.com")
	e.h = e.api // the API that knows the public URL, so the link start sees HTTPS too
	e.gh.add(ghUser{ID: 1109, Login: "ada-gh"}, "tok-ada", "code-ada")
	state, c := e.startLink(t, ghAdminSecret)
	if c.Name != oauthHostCookie {
		t.Fatalf("link cookie = %q, want %q", c.Name, oauthHostCookie)
	}

	planted := &http.Cookie{Name: oauthCookie, Value: c.Value}
	if rec := e.callback("code-ada", state, planted); rec.Code != http.StatusBadRequest {
		t.Errorf("callback with the plain-named cookie = %d, want 400", rec.Code)
	}
	if got, want := e.callback("code-ada", state, c).Header().Get("Location"), "/admin?linked=1"; got != want {
		t.Errorf("callback with the host cookie = %q, want %q", got, want)
	}
}

func TestLinkStatesKeepOneLivePendingLinkPerUser(t *testing.T) {
	t.Parallel()
	ada := domain.ActorRef{Kind: domain.ActorUser, ID: 1}
	eve := domain.ActorRef{Kind: domain.ActorUser, ID: 2}
	var l linkStates
	l.put("ada-old", pendingLink{actor: ada, expires: t0.Add(oauthLifetime)}, t0)
	l.put("eve", pendingLink{actor: eve, expires: t0.Add(time.Minute)}, t0)
	l.put("ada-new", pendingLink{actor: ada, expires: t0.Add(oauthLifetime)}, t0)
	if len(l.byState) != 2 {
		t.Errorf("held = %d, want 2: a user's new link replaces their earlier one", len(l.byState))
	}

	later := t0.Add(time.Minute)
	l.put("third", pendingLink{actor: domain.ActorRef{Kind: domain.ActorUser, ID: 3}, expires: later.Add(oauthLifetime)}, later)
	if _, held := l.byState["eve"]; held {
		t.Error("an expired link was kept after a later put")
	}
	if _, ok := l.take("ada-old", later); ok {
		t.Error("a replaced link was taken")
	}
	if p, ok := l.take("ada-new", later); !ok || p.actor != ada {
		t.Errorf("take = %+v, %t; want ada's link", p, ok)
	}
	if _, ok := l.take("ada-new", later); ok {
		t.Error("a link was taken twice")
	}
}

// startLink runs POST /me/github-link with secret and returns the state
// GitHub would send back and the oauth cookie.
func (e *ghEnv) startLink(t *testing.T, secret string) (string, *http.Cookie) {
	t.Helper()
	rec := e.do(secret, http.MethodPost, "/me/github-link", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("link = %d: %s", rec.Code, rec.Body)
	}
	var out apitypes.GitHubLink
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	loc, err := url.Parse(out.URL)
	if err != nil {
		t.Fatal(err)
	}
	return loc.Query().Get("state"), oauthCookieOf(rec)
}

func (e *ghEnv) callback(code, state string, c *http.Cookie) *httptest.ResponseRecorder {
	return e.get(e.web, "/auth/github/callback?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state), c)
}

func (e *testEnv) githubIdentitiesOf(userID domain.ID) []domain.Identity {
	ids, _ := e.fs.ListIdentities(context.Background(), userID)
	return slices.DeleteFunc(ids, func(i domain.Identity) bool { return i.Provider != domain.ProviderGitHub })
}

// The takeover the link cookie once allowed: a caller who writes the cookie
// themselves names the user their GitHub account is added to.
func TestGitHubLinkCannotBeForgedThroughTheCookie(t *testing.T) {
	t.Parallel()
	enc := base64.RawURLEncoding.EncodeToString
	tests := []struct {
		name   string
		cookie func(e *ghEnv, victim domain.ID) (state, value string)
		status int
		want   string
	}{
		{"the old format naming the victim", func(_ *ghEnv, victim domain.ID) (string, string) {
			return "forged", "forged." + enc([]byte("/admin")) + "." + enc(nil) + "." + enc([]byte(victim.String()))
		}, http.StatusBadRequest, ""},
		{"a link marker on a made-up state", func(*ghEnv, domain.ID) (string, string) {
			return "forged", "forged." + enc([]byte("/admin")) + "." + enc(nil) + "." + linkFlow
		}, http.StatusFound, "/admin?error=expired"},
		{"a link marker on the state of a real sign-in start", func(e *ghEnv, _ domain.ID) (string, string) {
			c := oauthCookieOf(e.get(e.web, "/auth/github/start?next=/admin"))
			state, _, _, _, _ := parseOAuthCookie(c.Value)
			return state, c.Value + linkFlow
		}, http.StatusFound, "/admin?error=expired"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newGHEnv(t, "")
			e.gh.add(ghUser{ID: 1201, Login: "mallory"}, "tok-mallory", "code-mallory")
			users, events := e.userCount(), len(e.eventTypes())
			state, value := tc.cookie(e, e.user.ID)

			rec := e.callback("code-mallory", state, &http.Cookie{Name: oauthCookie, Value: value})
			if rec.Code != tc.status || rec.Header().Get("Location") != tc.want {
				t.Errorf("callback = %d %q, want %d %q", rec.Code, rec.Header().Get("Location"), tc.status, tc.want)
			}
			if _, err := e.fs.GetIdentity(context.Background(), domain.ProviderGitHub, "1201"); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("the forged callback stored an identity (err %v)", err)
			}
			if got := e.githubIdentitiesOf(e.user.ID); len(got) != 0 {
				t.Errorf("the victim gained %+v", got)
			}
			if e.userCount() != users || len(e.eventTypes()) != events || cookieNamed(rec, handoffCookie) != nil {
				t.Errorf("users %d (was %d), events %v, handoff %v; want nothing changed", e.userCount(), users, e.eventTypes()[events:], cookieNamed(rec, handoffCookie))
			}
		})
	}
}

func TestGitHubLinkWithItsMarkerRemovedIsAPlainSignIn(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	e.gh.add(ghUser{ID: 1202, Login: "ada-gh"}, "tok-ada", "code-ada")
	state, c := e.startLink(t, ghAdminSecret)

	rec := e.callback("code-ada", state, &http.Cookie{Name: oauthCookie, Value: strings.TrimSuffix(c.Value, linkFlow)})
	if got, want := rec.Header().Get("Location"), "/login?choose=1&next=%2Fadmin"; got != want {
		t.Errorf("location = %q, want %q", got, want)
	}
	if got := e.githubIdentitiesOf(e.admin.ID); len(got) != 0 {
		t.Errorf("the admin gained %+v from a callback that was not a link", got)
	}
}

func TestGitHubLinkStateIsSpentByItsFirstCallback(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	e.gh.add(ghUser{ID: 1203, Login: "ada-gh"}, "tok-ada", "code-ada")
	e.gh.add(ghUser{ID: 1204, Login: "mallory"}, "tok-mallory", "code-mallory")
	state, c := e.startLink(t, ghAdminSecret)

	if got, want := e.callback("code-ada", state, c).Header().Get("Location"), "/admin?linked=1"; got != want {
		t.Fatalf("first callback = %q, want %q", got, want)
	}
	if got, want := e.callback("code-mallory", state, c).Header().Get("Location"), "/admin?error=expired"; got != want {
		t.Errorf("second callback = %q, want %q", got, want)
	}

	// A callback GitHub answers with an error spends the state too.
	state, c = e.startLink(t, ghAdminSecret)
	denied := e.get(e.web, "/auth/github/callback?error=access_denied&state="+url.QueryEscape(state), c)
	if got, want := denied.Header().Get("Location"), "/admin?error=denied"; got != want {
		t.Fatalf("denied callback = %q, want %q", got, want)
	}
	if got, want := e.callback("code-mallory", state, c).Header().Get("Location"), "/admin?error=expired"; got != want {
		t.Errorf("callback after a denial = %q, want %q", got, want)
	}
	if got := e.githubIdentitiesOf(e.admin.ID); len(got) != 1 || got[0].ProviderID != "1203" {
		t.Errorf("admin identities = %+v, want only github 1203", got)
	}
}

func TestGitHubLinkStateExpiresAndIsReplacedByANewerStart(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	e.gh.add(ghUser{ID: 1205, Login: "ada-gh"}, "tok-ada", "code-ada")
	e.gh.add(ghUser{ID: 1206, Login: "eve-gh"}, "tok-eve", "code-eve")
	eve := e.seedUser("eve", domain.RoleMember)
	e.seedToken("lvk_u_eve-link-secret", eve, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))

	old, oldCookie := e.startLink(t, ghAdminSecret)
	eveState, eveCookie := e.startLink(t, "lvk_u_eve-link-secret")
	fresh, freshCookie := e.startLink(t, ghAdminSecret)
	if got, want := e.callback("code-ada", old, oldCookie).Header().Get("Location"), "/admin?error=expired"; got != want {
		t.Errorf("replaced start = %q, want %q", got, want)
	}
	if got, want := e.callback("code-eve", eveState, eveCookie).Header().Get("Location"), "/admin?linked=1"; got != want {
		t.Errorf("another user's start = %q, want %q: one user's new start must not end another's", got, want)
	}
	e.advance(oauthLifetime)
	if got, want := e.callback("code-ada", fresh, freshCookie).Header().Get("Location"), "/admin?error=expired"; got != want {
		t.Errorf("start after %s = %q, want %q", oauthLifetime, got, want)
	}
	if got := e.githubIdentitiesOf(e.admin.ID); len(got) != 0 {
		t.Errorf("admin identities = %+v, want none", got)
	}
	if got := e.githubIdentitiesOf(eve.ID); len(got) != 1 || got[0].ProviderID != "1206" {
		t.Errorf("eve identities = %+v, want github 1206", got)
	}
}

func TestUnlinkGitHub(t *testing.T) {
	t.Parallel()
	const hash = "argon2id-hash"
	tests := []struct {
		name       string
		github     bool
		password   bool
		secret     string
		passwordOn bool
		want       int
		detail     string
	}{
		{"a password is the other way in", true, true, hash, true, http.StatusNoContent, ""},
		{"GitHub is the only identity", true, false, "", true, http.StatusConflict, "GitHub is your only way to sign in"},
		{"the password identity holds no password", true, true, "", true, http.StatusConflict, "GitHub is your only way to sign in"},
		{"the password cannot be used on this server", true, true, hash, false, http.StatusConflict, "GitHub is your only way to sign in"},
		{"nothing is linked", false, true, hash, true, http.StatusNotFound, "no GitHub account is linked"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// GitHub sign-in is off on this server: a link must still be removable.
			e := newEnv(t)
			e.svc.password = tc.passwordOn
			ctx := context.Background()
			if tc.github {
				_, _ = e.fs.CreateIdentity(ctx, domain.Identity{ID: e.ids.New(), UserID: e.user.ID, Provider: domain.ProviderGitHub, ProviderID: "1301", Login: "owner-gh", CreatedAt: t0})
				_, _ = e.fs.CreateIdentity(ctx, domain.Identity{ID: e.ids.New(), UserID: e.user.ID, Provider: domain.ProviderGitHub, ProviderID: "1302", Login: "owner-alt", CreatedAt: t0})
			}
			if tc.password {
				_, _ = e.fs.CreateIdentity(ctx, domain.Identity{ID: e.ids.New(), UserID: e.user.ID, Provider: domain.ProviderPassword, ProviderID: "owner", Secret: []byte(tc.secret), Login: "owner", CreatedAt: t0})
			}
			_, _ = e.fs.CreateIdentity(ctx, domain.Identity{ID: e.ids.New(), UserID: e.member.ID, Provider: domain.ProviderGitHub, ProviderID: "1303", Login: "member-gh", CreatedAt: t0})
			before, events := len(e.githubIdentitiesOf(e.user.ID)), len(e.eventTypes())

			rec := e.do(userSecret, http.MethodDelete, "/me/github-link", nil)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
			left := e.githubIdentitiesOf(e.user.ID)
			if tc.want != http.StatusNoContent {
				var p apitypes.Problem
				_ = json.Unmarshal(rec.Body.Bytes(), &p)
				if p.Detail != tc.detail || len(left) != before || len(e.eventTypes()) != events {
					t.Errorf("detail %q, identities %d (was %d), events %v; want %q and nothing changed", p.Detail, len(left), before, e.eventTypes()[events:], tc.detail)
				}
				return
			}
			if len(left) != 0 {
				t.Errorf("identities left = %+v, want every GitHub identity of the caller removed", left)
			}
			if got := e.eventTypes()[events:]; !slices.Equal(got, []string{"identity.unlinked", "identity.unlinked"}) {
				t.Errorf("events = %v, want one identity.unlinked per removed identity", got)
			}
			if ev := e.lastEvent(); ev.Actor != e.user || ev.Subject != membershipSubject(e.user.ID) || !strings.Contains(string(ev.Payload), `"login":"owner-alt"`) {
				t.Errorf("event = %+v %s, want the owner unlinking owner-alt", ev, ev.Payload)
			}
			if me := call[apitypes.Me](e, userSecret, http.MethodGet, "/me", nil, http.StatusOK); me.User == nil || me.User.GitHubLogin != "" {
				t.Errorf("me = %+v, want no github_login and the token still working", me.User)
			}
			if got := e.githubIdentitiesOf(e.member.ID); len(got) != 1 {
				t.Errorf("another user's identities = %+v, want untouched", got)
			}
		})
	}
}

func TestUnlinkGitHubRouteRefusals(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.svc.password = true
	ctx := context.Background()
	_, _ = e.fs.CreateIdentity(ctx, domain.Identity{ID: e.ids.New(), UserID: e.user.ID, Provider: domain.ProviderGitHub, ProviderID: "1304", Login: "owner-gh", CreatedAt: t0})
	_, _ = e.fs.CreateIdentity(ctx, domain.Identity{ID: e.ids.New(), UserID: e.user.ID, Provider: domain.ProviderPassword, ProviderID: "owner", Secret: []byte("hash"), Login: "owner", CreatedAt: t0})
	e.seedToken("lvk_u_readonly-unlink-secret", e.user, domain.ScopeRead, t0.Add(domain.DefaultTokenTTL))
	tests := []struct {
		name, secret string
		want         int
	}{
		{"no token", "", http.StatusUnauthorized},
		{"agent token", agentSecret, http.StatusForbidden},
		{"read-scoped token", "lvk_u_readonly-unlink-secret", http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if rec := e.do(tc.secret, http.MethodDelete, "/me/github-link", nil); rec.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
			if got := e.githubIdentitiesOf(e.user.ID); len(got) != 1 {
				t.Errorf("identities = %+v, want the link kept", got)
			}
		})
	}
}

func TestGitHubLinkRouteRefusals(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	agent := e.seedAgent("bot", domain.HarnessClaudeCode)
	e.seedToken("lvk_a_agent-link-secret", agent, domain.ScopeWrite, t0.Add(domain.AgentTokenTTL))
	admin := e.admin
	e.seedToken("lvk_u_readonly-link-secret", admin, domain.ScopeRead, t0.Add(domain.DefaultTokenTTL))
	tests := []struct {
		name, secret string
		want         int
	}{
		{"no token", "", http.StatusUnauthorized},
		{"agent token", "lvk_a_agent-link-secret", http.StatusForbidden},
		{"read-scoped token", "lvk_u_readonly-link-secret", http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := e.do(tc.secret, http.MethodPost, "/me/github-link", nil)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body)
			}
			if oauthCookieOf(rec) != nil {
				t.Error("a refused request set the oauth cookie")
			}
		})
	}
}

func TestGitHubLinkIsNotFoundWhenOff(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.seedToken(ghAdminSecret, e.user, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
	if rec := e.do(ghAdminSecret, http.MethodPost, "/me/github-link", nil); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404: %s", rec.Code, rec.Body)
	}
}

// Over HTTPS the handoff cookie is one only this host can set, so a sibling
// subdomain cannot plant a token that signs the visitor in to its account.
func TestGitHubHandoffOverHTTPSIsAHostCookie(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		base       string
		name, path string
		secure     bool
	}{
		{"https://lorvik.example.com", handoffHostCookie, "/", true},
		{"http://localhost:8080", handoffCookie, handoffPath, false},
	} {
		t.Run(tt.base, func(t *testing.T) {
			t.Parallel()
			e := newGHEnv(t, tt.base)
			e.gh.add(ghUser{ID: 1201, Login: "ana", Name: "Ana"}, "tok-ana", "code-ana")
			rec := e.signIn(t, "", "code-ana")
			c := cookieNamed(rec, tt.name)
			if c == nil {
				t.Fatalf("no %s cookie in %v", tt.name, rec.Result().Cookies())
			}
			if c.Path != tt.path || c.Secure != tt.secure || c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Domain != "" || c.MaxAge != int(handoffLifetime/time.Second) || c.Value == "" {
				t.Errorf("cookie = %+v, want path %s, secure %v, readable by the page, strict, host-only, %v", c, tt.path, tt.secure, handoffLifetime)
			}
		})
	}
}
