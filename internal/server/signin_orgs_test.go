package server

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// pat adds a GitHub account and the Lorvik user behind it, and returns the
// user and the GitHub token that signs in as them.
func (e *ghEnv) pat(id int64) (domain.User, string) {
	const login = "pat"
	ctx := context.Background()
	u, _ := e.fs.CreateUser(ctx, domain.User{ID: e.ids.New(), Login: login, Name: login, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	_, _ = e.fs.CreateIdentity(ctx, domain.Identity{ID: e.ids.New(), UserID: u.ID, Provider: domain.ProviderGitHub, ProviderID: strconv.FormatInt(id, 10), Login: login, CreatedAt: t0})
	e.gh.add(ghUser{ID: id, Login: login}, "tok-"+login, "code-"+login)
	return u, "tok-" + login
}

func (e *ghEnv) newOrg(name string, status domain.RecordStatus) domain.Org {
	org, _ := e.fs.CreateOrg(context.Background(), domain.Org{ID: e.ids.New(), Name: name, Status: status, CreatedAt: t0, UpdatedAt: t0})
	return org
}

// place puts the user in an org: as a member, invited by the admin, or with a
// request to join of their own.
func (e *ghEnv) place(u domain.User, org domain.Org, state string) {
	m := domain.Membership{OrgID: org.ID, Actor: u.Ref(), Role: domain.RoleMember, Status: domain.RecordPending, CreatedAt: t0, UpdatedAt: t0}
	switch state {
	case openMember:
		m.Status = domain.RecordActive
	case openInvited:
		m.GrantedBy = &e.admin.ID
	case openRequested:
		m.Role = domain.RoleViewer
	}
	_, _ = e.fs.PutMembership(context.Background(), m)
}

func (e *ghEnv) signInCLI(t *testing.T, in apitypes.GitHubLogin) apitypes.TokenCreated {
	t.Helper()
	return call[apitypes.TokenCreated](e.testEnv, "", http.MethodPost, "/login/github", in, http.StatusCreated)
}

func (e *ghEnv) orgOfToken(t *testing.T, secret string) string {
	t.Helper()
	return call[apitypes.Me](e.testEnv, secret, http.MethodGet, "/me", nil, http.StatusOK).Org.Name
}

func TestASignInThatNamesNoOrgSettlesByThePersonsOrgs(t *testing.T) {
	t.Parallel()
	type placed struct{ org, state string }
	for _, tc := range []struct {
		name    string
		in      []placed
		choose  bool
		wantOrg string // "" for a sign-in token
	}{
		{name: "no org at all"},
		{name: "member of one", in: []placed{{"Beta", openMember}}, wantOrg: "Beta"},
		{name: "invited to one", in: []placed{{"Beta", openInvited}}},
		{name: "member of two", in: []placed{{"Beta", openMember}, {"Gamma", openMember}}},
		{name: "member of one and invited to another", in: []placed{{"Beta", openMember}, {"Gamma", openInvited}}},
		{name: "a request to join does not count", in: []placed{{"Beta", openMember}, {"Gamma", openRequested}}, wantOrg: "Beta"},
		{name: "only a request to join", in: []placed{{"Gamma", openRequested}}},
		{name: "an org that is not active does not count", in: []placed{{"Beta", openMember}, {"Closed", openMember}}, wantOrg: "Beta"},
		{name: "asked to choose with one org", in: []placed{{"Beta", openMember}}, choose: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newGHEnv(t, "")
			u, gh := e.pat(3001)
			for _, p := range tc.in {
				status := domain.RecordActive
				if p.org == "Closed" {
					status = domain.RecordSuspended
				}
				e.place(u, e.newOrg(p.org, status), p.state)
			}
			got := e.signInCLI(t, apitypes.GitHubLogin{AccessToken: gh, Choose: tc.choose})
			if tc.wantOrg == "" {
				if got.Token.Scope != apitypes.ScopeSignIn || !got.Token.ExpiresAt.Equal(t0.Add(domain.SignInTokenTTL)) {
					t.Fatalf("token = %+v, want a sign-in token good for %s", got.Token, domain.SignInTokenTTL)
				}
				return
			}
			if got.Token.Scope != "write" {
				t.Fatalf("token scope = %q, want the org's token", got.Token.Scope)
			}
			if name := e.orgOfToken(t, got.Secret); name != tc.wantOrg {
				t.Errorf("signed in to %q, want %q", name, tc.wantOrg)
			}
		})
	}
}

func TestAnInvitationIsAcceptedOnlyByThePersonsOwnPick(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	u, gh := e.pat(3010)
	beta := e.newOrg("Beta", domain.RecordActive)
	e.place(u, beta, openInvited)
	waiting := func(when string) {
		t.Helper()
		if m, _ := e.fs.GetMembership(context.Background(), beta.ID, u.Ref()); m.Status != domain.RecordPending {
			t.Fatalf("%s: membership = %+v, want the invitation still waiting", when, m)
		}
	}

	// Signing in is not picking: the invitation is listed, not entered.
	if got := e.signInCLI(t, apitypes.GitHubLogin{AccessToken: gh}); got.Token.Scope != apitypes.ScopeSignIn {
		t.Fatalf("plain sign-in: scope %q, want a sign-in token", got.Token.Scope)
	}
	waiting("after a plain sign-in")
	// Nor is following a link that names the org: anyone can send one.
	if loc := e.signIn(t, "?org="+beta.ID.String(), "code-pat").Header().Get("Location"); loc != "/login?choose=1&next=%2F" {
		t.Fatalf("link that names the org: location %q, want the choice", loc)
	}
	waiting("after a link that names the org")
	// A link to an org the person is a member of still enters it, and leaves the invitation alone.
	gamma := e.newOrg("Gamma", domain.RecordActive)
	e.place(u, gamma, openMember)
	rec := e.signIn(t, "?org="+gamma.ID.String(), "code-pat")
	if loc := rec.Header().Get("Location"); loc != "/login?next=%2F" {
		t.Fatalf("link that names an org the person is a member of: location %q, want the plain handoff", loc)
	}
	if name := e.orgOfToken(t, cookieNamed(rec, handoffCookie).Value); name != "Gamma" {
		t.Errorf("handed over a token of %q, want Gamma", name)
	}
	waiting("after entering another org")

	// Typing the org for the CLI is.
	got := e.signInCLI(t, apitypes.GitHubLogin{AccessToken: gh, Org: beta.ID.String()})
	if name := e.orgOfToken(t, got.Secret); got.Token.Scope != "write" || name != "Beta" {
		t.Fatalf("sign-in that names the org: scope %q in %q, want Beta's token", got.Token.Scope, name)
	}
	if m, _ := e.fs.GetMembership(context.Background(), beta.ID, u.Ref()); m.Status != domain.RecordActive {
		t.Errorf("membership = %+v, want the invitation accepted", m)
	}
	if ev := e.lastEvent(); ev.Type != domain.EventMembershipGranted || ev.OrgID != beta.ID || ev.Actor != e.admin {
		t.Errorf("event = %+v, want membership granted in Beta by the inviter", ev)
	}
}

func TestASignInTokenOpensOnlyTheOrgRoutes(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	_, gh := e.pat(3002)
	signIn := e.signInCLI(t, apitypes.GitHubLogin{AccessToken: gh}).Secret

	for _, path := range []string{"/me", "/projects", "/org", "/tokens", "/events", "/actors?id=1"} {
		if p := e.problem(signIn, http.MethodGet, path, nil, http.StatusForbidden); p.Detail != "no org chosen yet" {
			t.Errorf("GET %s with a sign-in token: %+v", path, p)
		}
	}
	e.problem(signIn, http.MethodPost, "/tokens", apitypes.CreateToken{Name: "kept"}, http.StatusForbidden)
	if got := call[apitypes.SignInOrgs](e.testEnv, signIn, http.MethodGet, "/login/orgs", nil, http.StatusOK); len(got.Orgs) != 0 || got.Login != "pat" {
		t.Errorf("orgs of a person with none = %+v, want none and the login pat", got)
	}

	// The other way round: a token of one org never reaches these routes.
	const detail = "a token of one org does not open another"
	for _, secret := range []string{userSecret, agentSecret} {
		if p := e.problem(secret, http.MethodGet, "/login/orgs", nil, http.StatusForbidden); p.Detail != detail {
			t.Errorf("GET /login/orgs: %+v", p)
		}
		if p := e.problem(secret, http.MethodPost, "/login/orgs", apitypes.CreateOrg{Name: "Mine"}, http.StatusForbidden); p.Detail != detail {
			t.Errorf("POST /login/orgs: %+v", p)
		}
		if p := e.problem(secret, http.MethodPost, "/login/orgs/"+e.org.ID.String()+"/token", apitypes.EnterOrg{}, http.StatusForbidden); p.Detail != detail {
			t.Errorf("POST /login/orgs/{id}/token: %+v", p)
		}
	}
	e.problem("", http.MethodGet, "/login/orgs", nil, http.StatusUnauthorized)
	if orgs, _ := e.fs.ListOrgs(context.Background()); len(orgs) != 1 {
		t.Errorf("orgs = %d, want the refused calls to have created none", len(orgs))
	}

	e.advance(domain.SignInTokenTTL)
	if p := e.problem(signIn, http.MethodGet, "/login/orgs", nil, http.StatusUnauthorized); p.Detail != "this sign-in was used or has expired" {
		t.Errorf("after its ten minutes: %+v", p)
	}
}

func TestTheSignInScopeCannotBeAskedFor(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.problem(userSecret, http.MethodPost, "/tokens", apitypes.CreateToken{Name: "x", Scope: "signin"}, http.StatusUnprocessableEntity)
}

func TestCreatingAnOrgMakesThePersonItsOwner(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	u, gh := e.pat(3003)
	signIn := e.signInCLI(t, apitypes.GitHubLogin{AccessToken: gh}).Secret

	e.problem(signIn, http.MethodPost, "/login/orgs", apitypes.CreateOrg{Name: "   "}, http.StatusUnprocessableEntity)
	created := call[apitypes.SignInOrg](e.testEnv, signIn, http.MethodPost, "/login/orgs", apitypes.CreateOrg{Name: "  Pat's team "}, http.StatusCreated)
	if created.Name != "Pat's team" || created.Role != "owner" || created.Status != "member" {
		t.Fatalf("created = %+v, want the trimmed name, owner, member", created)
	}
	if got := call[apitypes.SignInOrgs](e.testEnv, signIn, http.MethodGet, "/login/orgs", nil, http.StatusOK); len(got.Orgs) != 1 || got.Orgs[0] != created {
		t.Errorf("orgs after creating = %+v, want only %+v", got.Orgs, created)
	}

	tok := call[apitypes.TokenCreated](e.testEnv, signIn, http.MethodPost, "/login/orgs/"+created.ID+"/token", apitypes.EnterOrg{TokenName: "dashboard"}, http.StatusCreated)
	if tok.Token.Scope != "write" || tok.Token.Name != "dashboard" {
		t.Errorf("token = %+v, want a write token named dashboard", tok.Token)
	}
	me := call[apitypes.Me](e.testEnv, tok.Secret, http.MethodGet, "/me", nil, http.StatusOK)
	if me.Org.Name != "Pat's team" || me.Role != "owner" || me.Actor.ID != u.ID.String() {
		t.Errorf("me = %+v, want pat as owner of the new org", me)
	}
	// The new org starts with its global project and sees nothing of Acme.
	projects := call[apitypes.ProjectList](e.testEnv, tok.Secret, http.MethodGet, "/projects", nil, http.StatusOK)
	if len(projects.Items) != 1 || projects.Items[0].Slug != domain.GlobalSlug {
		t.Errorf("projects of the new org = %+v, want only its global project", projects.Items)
	}
	e.problem(tok.Secret, http.MethodGet, "/projects/"+e.proj.Slug, nil, http.StatusNotFound)
}

func TestEnteringAnOrgEndsTheSignIn(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	u, gh := e.pat(3004)
	beta, gamma, delta := e.newOrg("Beta", domain.RecordActive), e.newOrg("Gamma", domain.RecordActive), e.newOrg("Delta", domain.RecordActive)
	closed := e.newOrg("Closed", domain.RecordSuspended)
	e.place(u, beta, openMember)
	e.place(u, gamma, openInvited)
	e.place(u, delta, openRequested)
	e.place(u, closed, openMember)
	signIn := e.signInCLI(t, apitypes.GitHubLogin{AccessToken: gh}).Secret

	got := call[apitypes.SignInOrgs](e.testEnv, signIn, http.MethodGet, "/login/orgs", nil, http.StatusOK)
	want := []apitypes.SignInOrg{
		{ID: beta.ID.String(), Name: "Beta", Role: "member", Status: "member"},
		{ID: delta.ID.String(), Name: "Delta", Role: "viewer", Status: "requested"},
		{ID: gamma.ID.String(), Name: "Gamma", Role: "member", Status: "invited"},
	}
	if !slices.Equal(got.Orgs, want) {
		t.Fatalf("orgs = %+v, want %+v", got.Orgs, want)
	}

	enter := func(id string) string { return "/login/orgs/" + id + "/token" }
	// An org the person has no place in answers the same whether it exists,
	// is closed, or was never there.
	for _, id := range []string{e.org.ID.String(), closed.ID.String(), "12345"} {
		if p := e.problem(signIn, http.MethodPost, enter(id), apitypes.EnterOrg{}, http.StatusForbidden); p.Detail != "not a member of that org" {
			t.Errorf("enter %s: %+v", id, p)
		}
	}
	if p := e.problem(signIn, http.MethodPost, enter(delta.ID.String()), apitypes.EnterOrg{}, http.StatusForbidden); p.Detail != "membership pending approval" {
		t.Errorf("enter an org asked to join: %+v", p)
	}
	if _, err := e.fs.GetMembership(context.Background(), e.org.ID, u.Ref()); err == nil {
		t.Error("a refused enter left a request to join")
	}

	// A refusal leaves the sign-in usable; entering ends it.
	tok := call[apitypes.TokenCreated](e.testEnv, signIn, http.MethodPost, enter(gamma.ID.String()), apitypes.EnterOrg{}, http.StatusCreated)
	if name := e.orgOfToken(t, tok.Secret); name != "Gamma" || tok.Token.Name != "login" {
		t.Errorf("entered %q with token %q, want Gamma and the default name", name, tok.Token.Name)
	}
	if m, _ := e.fs.GetMembership(context.Background(), gamma.ID, u.Ref()); m.Status != domain.RecordActive {
		t.Errorf("membership in Gamma = %+v, want the invitation accepted", m)
	}
	if ev := e.lastEvent(); ev.Type != domain.EventMembershipGranted || ev.OrgID != gamma.ID || ev.Actor != e.admin {
		t.Errorf("event = %+v, want membership granted in Gamma by the inviter", ev)
	}
	for _, path := range []string{"/login/orgs", enter(beta.ID.String())} {
		method := http.MethodGet
		if path != "/login/orgs" {
			method = http.MethodPost
		}
		if p := e.problem(signIn, method, path, nil, http.StatusUnauthorized); p.Detail != "this sign-in was used or has expired" {
			t.Errorf("%s %s after entering: %+v", method, path, p)
		}
	}
}

func TestGitHubWebFlowHandsOverASignInTokenToChoose(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	e.gh.add(ghUser{ID: 3005, Login: "newbie"}, "tok-newbie", "code-newbie")
	member, _ := e.pat(3006)
	e.place(member, e.org, openMember)

	for _, tc := range []struct {
		name, query, code, wantLocation string
		wantOrgs                        int
	}{
		{name: "a person with no org", query: "?next=/board", code: "code-newbie", wantLocation: "/login?choose=1&next=%2Fboard"},
		{name: "a person with one org who asks to choose", query: "?choose=1", code: "code-pat", wantLocation: "/login?choose=1&next=%2F", wantOrgs: 1},
	} {
		rec := e.signIn(t, tc.query, tc.code)
		if got := rec.Header().Get("Location"); got != tc.wantLocation {
			t.Fatalf("%s: location = %q, want %q", tc.name, got, tc.wantLocation)
		}
		c := cookieNamed(rec, handoffCookie)
		if c == nil {
			t.Fatalf("%s: no handoff cookie", tc.name)
		}
		secret, _ := url.QueryUnescape(c.Value)
		if got := call[apitypes.SignInOrgs](e.testEnv, secret, http.MethodGet, "/login/orgs", nil, http.StatusOK); len(got.Orgs) != tc.wantOrgs {
			t.Errorf("%s: orgs = %+v, want %d", tc.name, got.Orgs, tc.wantOrgs)
		}
		e.problem(secret, http.MethodGet, "/me", nil, http.StatusForbidden)
	}
	// A plain sign-in asks nobody to let the person in.
	if _, ok := e.membershipOf("newbie"); ok {
		t.Error("a plain sign-in left a request to join the server's first org")
	}
	if n := countOf(e.eventTypes(), "membership.requested"); n != 0 {
		t.Errorf("membership.requested = %d, want none", n)
	}
	if rec := e.signIn(t, "", "code-pat"); rec.Header().Get("Location") != "/login?next=%2F" {
		t.Errorf("a member of one org: location %q, want the plain handoff", rec.Header().Get("Location"))
	}
}

func TestPasswordSignInSettlesTheOrgLikeAnyOther(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.svc.password = true
	ctx := context.Background()
	hash, err := domain.HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := e.fs.CreateUser(ctx, domain.User{ID: e.ids.New(), Login: "pat", Name: "Pat", Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	_, _ = e.fs.CreateIdentity(ctx, domain.Identity{ID: e.ids.New(), UserID: u.ID, Provider: domain.ProviderPassword, ProviderID: domain.PasswordKey("pat"), Secret: hash, Login: "pat", CreatedAt: t0})
	in := apitypes.Login{Login: "pat", Password: "correct horse"}

	if got := call[apitypes.TokenCreated](e, "", http.MethodPost, "/login", in, http.StatusCreated); got.Token.Scope != "signin" {
		t.Fatalf("no org: scope %q, want a sign-in token", got.Token.Scope)
	}
	named := in
	for _, id := range []string{e.org.ID.String(), "12345"} {
		named.Org = id
		if p := e.problem("", http.MethodPost, "/login", named, http.StatusForbidden); p.Detail != "not a member of that org" {
			t.Errorf("named org %s, no member: %+v", id, p)
		}
	}
	if _, err := e.fs.GetMembership(ctx, e.org.ID, u.Ref()); err == nil {
		t.Error("a password sign-in left a request to join")
	}

	// A request to join waits for an admin; an invitation is accepted by the sign-in that names its org.
	named.Org = e.org.ID.String()
	row := domain.Membership{OrgID: e.org.ID, Actor: u.Ref(), Role: domain.RoleMember, Status: domain.RecordPending, CreatedAt: t0, UpdatedAt: t0}
	_, _ = e.fs.PutMembership(ctx, row)
	if p := e.problem("", http.MethodPost, "/login", named, http.StatusForbidden); p.Detail != "membership pending approval" {
		t.Errorf("named org, request waiting: %+v", p)
	}
	row.GrantedBy = &e.user.ID
	_, _ = e.fs.PutMembership(ctx, row)
	if got := call[apitypes.TokenCreated](e, "", http.MethodPost, "/login", named, http.StatusCreated); got.Token.Scope != "write" {
		t.Errorf("named org, invited: token %+v, want the org's write token", got.Token)
	}
	if m, _ := e.fs.GetMembership(ctx, e.org.ID, u.Ref()); m.Status != domain.RecordActive {
		t.Errorf("membership = %+v, want the invitation accepted", m)
	}

	got := call[apitypes.TokenCreated](e, "", http.MethodPost, "/login", in, http.StatusCreated)
	if got.Token.Scope != "write" || got.Token.Name != "login" {
		t.Errorf("one org: token %+v, want its write token named login", got.Token)
	}
	choose := in
	choose.Choose = true
	if got := call[apitypes.TokenCreated](e, "", http.MethodPost, "/login", choose, http.StatusCreated); got.Token.Scope != "signin" {
		t.Errorf("asked to choose: scope %q, want a sign-in token", got.Token.Scope)
	}
}
