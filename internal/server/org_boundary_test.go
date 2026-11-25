package server

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// Anyone may create an org, so an org's admin is a stranger to the people of
// every other org. These tests pin what such an admin cannot do to them.

// stranger signs in with a GitHub account nobody invited, creates an org and
// returns its owner's token and the org's id.
func (e *ghEnv) stranger(t *testing.T) (string, domain.ID) {
	t.Helper()
	e.gh.add(ghUser{ID: 9001, Login: "mallory"}, "tok-mallory", "code-mallory")
	signIn := e.signInCLI(t, apitypes.GitHubLogin{AccessToken: "tok-mallory"}).Secret
	org := call[apitypes.SignInOrg](e.testEnv, signIn, http.MethodPost, "/login/orgs", apitypes.CreateOrg{Name: "Other"}, http.StatusCreated)
	tok := call[apitypes.TokenCreated](e.testEnv, signIn, http.MethodPost, "/login/orgs/"+org.ID+"/token", apitypes.EnterOrg{}, http.StatusCreated)
	id, err := domain.ParseID(org.ID)
	if err != nil {
		t.Fatal(err)
	}
	return tok.Secret, id
}

// private gives the user what GitHub hands over at sign-in and does not
// publish.
func (e *ghEnv) private(u domain.User) {
	u.Email, u.Locale, u.Timezone = "pat@private.example", "en-GB", "Europe/Berlin"
	_, _ = e.fs.UpdateUser(context.Background(), u)
}

func TestAnOrgSeesOnlyTheNameOfAPersonWhoIsNotItsMember(t *testing.T) {
	t.Parallel()
	const removed = "removed"
	for _, tc := range []struct {
		state    string
		wantFull bool
	}{
		{state: openMember, wantFull: true},
		{state: openInvited},
		{state: openRequested},
		{state: removed},
	} {
		t.Run(tc.state, func(t *testing.T) {
			t.Parallel()
			e := newGHEnv(t, "")
			u, _ := e.pat(3101)
			e.private(u)
			e.place(u, e.org, openMember)
			secret, other := e.stranger(t)
			otherOrg, _ := e.fs.GetOrg(context.Background(), other)
			if tc.state == removed {
				e.place(u, otherOrg, openMember)
				m, _ := e.fs.GetMembership(context.Background(), other, u.Ref())
				m.Status = domain.RecordRemoved
				_, _ = e.fs.PutMembership(context.Background(), m)
			} else {
				e.place(u, otherOrg, tc.state)
			}

			got := call[apitypes.Actors](e.testEnv, secret, http.MethodGet, "/actors?id="+u.ID.String(), nil, http.StatusOK)
			if len(got.Users) != 1 || got.Users[0].Login != "pat" || got.Users[0].Name != "pat" {
				t.Fatalf("actors = %+v, want pat named", got.Users)
			}
			private := got.Users[0].Email != "" || got.Users[0].Locale != "" || got.Users[0].Timezone != ""
			if private != tc.wantFull {
				t.Errorf("user = %+v, private fields shown = %t, want %t", got.Users[0], private, tc.wantFull)
			}
		})
	}
}

func TestAnInvitationOpensNothingOfThePersonToTheInviter(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	u, _ := e.pat(3102)
	e.private(u)
	e.place(u, e.org, openMember)
	secret, _ := e.stranger(t)

	invited := call[apitypes.Membership](e.testEnv, secret, http.MethodPost, "/members/invite", apitypes.Invite{Login: "pat", Role: "viewer"}, http.StatusOK)
	got := call[apitypes.Actors](e.testEnv, secret, http.MethodGet, "/actors?id="+invited.Actor.ID, nil, http.StatusOK)
	if len(got.Users) != 1 || got.Users[0].Email != "" || got.Users[0].Timezone != "" || got.Users[0].Locale != "" {
		t.Errorf("actors = %+v, want the invited person named and nothing private", got.Users)
	}
}

func TestAnOrgCannotMakeAMemberOfSomeoneWhoDidNotAsk(t *testing.T) {
	t.Parallel()
	const none, removed, missing = "no place in the org", "removed", "no such user"
	for _, state := range []string{none, openInvited, removed, missing} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			e := newGHEnv(t, "")
			u, gh := e.pat(3103)
			e.place(u, e.org, openMember)
			secret, other := e.stranger(t)
			otherOrg, _ := e.fs.GetOrg(context.Background(), other)
			id := u.ID.String()
			switch state {
			case openInvited:
				e.place(u, otherOrg, openInvited)
			case removed:
				e.place(u, otherOrg, openMember)
				m, _ := e.fs.GetMembership(context.Background(), other, u.Ref())
				m.Status = domain.RecordRemoved
				_, _ = e.fs.PutMembership(context.Background(), m)
			case missing:
				id = "12345"
			}
			before, hadRow := e.fs.GetMembership(context.Background(), other, u.Ref())
			events := len(e.eventTypes())

			p := e.problem(secret, http.MethodPut, "/members/user/"+id, apitypes.PutMembership{Role: "member"}, http.StatusUnprocessableEntity)
			// One answer for every case, so the route does not tell which ids are users of the server.
			if !hasField(p, "id") || p.Errors[0].Message != "no such member of this org" {
				t.Errorf("problem = %+v, want id: no such member of this org", p)
			}
			after, hasRow := e.fs.GetMembership(context.Background(), other, u.Ref())
			if (hadRow == nil) != (hasRow == nil) || after.Status != before.Status || after.Role != before.Role {
				t.Errorf("membership = %+v (err %v), want it left as %+v (err %v)", after, hasRow, before, hadRow)
			}
			if n := len(e.eventTypes()); n != events {
				t.Errorf("events = %d, want the refusal to record none beyond %d", n, events)
			}
			// The person's own sign-in is what it was: straight into their one org, unless they hold an invitation.
			got := e.signInCLI(t, apitypes.GitHubLogin{AccessToken: gh})
			if wantChoice := state == openInvited; (got.Token.Scope == apitypes.ScopeSignIn) != wantChoice {
				t.Errorf("sign-in scope = %q, want a choice: %t", got.Token.Scope, wantChoice)
			}
		})
	}
}

func TestPutMemberGrantsARequestToJoin(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	u, _ := e.pat(3104)
	e.place(u, e.org, openRequested)

	got := call[apitypes.Membership](e.testEnv, ghAdminSecret, http.MethodPut, "/members/user/"+u.ID.String(), apitypes.PutMembership{Role: "member"}, http.StatusOK)
	if got.Status != "active" || got.Role != "member" || got.GrantedBy != e.admin.ID.String() {
		t.Errorf("put = %+v, want active member granted by the admin", got)
	}
	if e.lastEvent().Type != domain.EventMembershipGranted {
		t.Errorf("last event = %v, want membership.granted", e.lastEvent().Type)
	}
	list := call[apitypes.MemberList](e.testEnv, ghAdminSecret, http.MethodGet, "/members", nil, http.StatusOK).Items
	if !slices.ContainsFunc(list, func(m apitypes.Membership) bool { return m.Actor.ID == u.ID.String() && m.Status == "active" }) {
		t.Errorf("members = %+v, want the row listed as active", list)
	}
}

func TestAProjectRoleGoesOnlyToAMemberOfTheOrg(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	u, _ := e.pat(3105)
	path := "/projects/" + e.proj.Slug + "/members/user/"

	for _, state := range []string{"", openInvited, openRequested} {
		if state != "" {
			e.place(u, e.org, state)
		}
		p := e.problem(userSecret, http.MethodPut, path+u.ID.String(), apitypes.PutMembership{Role: "member"}, http.StatusUnprocessableEntity)
		if !hasField(p, "id") {
			t.Errorf("state %q: problem = %+v, want the id refused", state, p)
		}
		if _, err := e.fs.GetProjectMember(context.Background(), e.proj.ID, u.Ref()); err == nil {
			t.Fatalf("state %q: a project role was written for someone who is not a member of the org", state)
		}
	}
	e.place(u, e.org, openMember)
	call[apitypes.ProjectMember](e.testEnv, userSecret, http.MethodPut, path+u.ID.String(), apitypes.PutMembership{Role: "member"}, http.StatusOK)
}

// removingStore removes the person from an org between the unlocked list of
// their orgs and the locked read of their row, as an admin removing them at
// that moment would.
type removingStore struct {
	Store
	fs  *fakeStore
	org domain.ID
}

func (r *removingStore) WithTx(_ context.Context, fn func(Store) error) error { return fn(r) }

func (r *removingStore) ListOpenMembershipsForActor(ctx context.Context, a domain.ActorRef) ([]domain.Membership, error) {
	ms, err := r.Store.ListOpenMembershipsForActor(ctx, a)
	for _, m := range ms {
		if m.OrgID == r.org {
			m.Status = domain.RecordRemoved
			_, _ = r.fs.PutMembership(ctx, m)
		}
	}
	return ms, err
}

func TestASignInThatNamesNoOrgLeavesNoRequestWhenThePersonIsRemovedMeanwhile(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	u, gh := e.pat(3106)
	e.place(u, e.org, openMember)
	e.svc.store = &removingStore{Store: e.fs, fs: e.fs, org: e.org.ID}

	if p := e.problem("", http.MethodPost, "/login/github", apitypes.GitHubLogin{AccessToken: gh}, http.StatusForbidden); p.Detail != "not a member of that org" {
		t.Errorf("problem = %+v, want not a member", p)
	}
	if m, _ := e.fs.GetMembership(context.Background(), e.org.ID, u.Ref()); m.Status != domain.RecordRemoved {
		t.Errorf("membership = %+v, want it left removed, not turned into a request to join", m)
	}
	if n := countOf(e.eventTypes(), "membership.requested"); n != 0 {
		t.Errorf("membership.requested = %d, want none", n)
	}
}

func TestAnOrgNameHoldsNoControlCharacters(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	_, gh := e.pat(3107)
	signIn := e.signInCLI(t, apitypes.GitHubLogin{AccessToken: gh}).Secret

	for _, name := range []string{"Acme  owner\n  2  Other", "Acme\x1b[2K", "Acme\ttab", "Ac\u0085me"} {
		if p := e.problem(signIn, http.MethodPost, "/login/orgs", apitypes.CreateOrg{Name: name}, http.StatusUnprocessableEntity); !hasField(p, "name") {
			t.Errorf("create %q: %+v, want the name refused", name, p)
		}
		if p := e.problem(userSecret, http.MethodPatch, "/org", apitypes.UpdateOrg{Name: &name}, http.StatusUnprocessableEntity); !hasField(p, "name") {
			t.Errorf("rename to %q: %+v, want the name refused", name, p)
		}
	}
	if orgs, _ := e.fs.ListOrgs(context.Background()); len(orgs) != 1 || orgs[0].Name != "Acme" {
		t.Errorf("orgs = %+v, want only Acme under its own name", orgs)
	}
	// Letters of any script and the marks inside them are a name like any other.
	for _, name := range []string{"Ünïcödé & Söhne", "株式会社テスト"} {
		if got := call[apitypes.SignInOrg](e.testEnv, signIn, http.MethodPost, "/login/orgs", apitypes.CreateOrg{Name: name}, http.StatusCreated); got.Name != name {
			t.Errorf("created %q, want %q", got.Name, name)
		}
	}
}

func TestASignInTokenStopsWhenItsPersonIsSuspended(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	u, gh := e.pat(3108)
	signIn := e.signInCLI(t, apitypes.GitHubLogin{AccessToken: gh}).Secret
	call[apitypes.SignInOrgs](e.testEnv, signIn, http.MethodGet, "/login/orgs", nil, http.StatusOK)

	u.Status = domain.RecordSuspended
	_, _ = e.fs.UpdateUser(context.Background(), u)
	e.problem(signIn, http.MethodGet, "/login/orgs", nil, http.StatusForbidden)
	e.problem(signIn, http.MethodPost, "/login/orgs", apitypes.CreateOrg{Name: "Mine"}, http.StatusForbidden)
	e.problem(signIn, http.MethodPost, "/login/orgs/"+e.org.ID.String()+"/token", apitypes.EnterOrg{}, http.StatusForbidden)
	if orgs, _ := e.fs.ListOrgs(context.Background()); len(orgs) != 1 {
		t.Errorf("orgs = %d, want a suspended person to have created none", len(orgs))
	}
}

func TestActorsShowsAPendingPersonOnlyToWhoManagesMembersAndToThemself(t *testing.T) {
	t.Parallel()
	for _, pending := range []string{openInvited, openRequested} {
		for _, tc := range []struct {
			caller string
			want   bool
		}{
			{caller: "admin", want: true},
			{caller: "member"},
			{caller: "agent"},
			{caller: "the pending person", want: true},
		} {
			t.Run(pending+"/"+tc.caller, func(t *testing.T) {
				t.Parallel()
				e := newGHEnv(t, "")
				u, _ := e.pat(3104)
				e.place(u, e.org, pending)
				p := Principal{OrgID: e.org.ID}
				switch tc.caller {
				case "admin":
					p.Actor, p.OrgRole, p.HasOrgRole = e.admin, domain.RoleAdmin, true
				case "member":
					p.Actor, p.OrgRole, p.HasOrgRole = e.member, domain.RoleMember, true
				case "agent":
					p.Actor, p.OrgRole, p.HasOrgRole, p.OrgRights = e.agent, domain.RoleViewer, true, domain.RightWork|domain.RightReady
				case "the pending person":
					p.Actor = u.Ref()
				}
				got, err := e.svc.Actors(WithPrincipal(t.Context(), p), []domain.ID{u.ID})
				if err != nil {
					t.Fatal(err)
				}
				if shown := len(got.Users) == 1 && got.Users[0].Login == "pat"; shown != tc.want || !tc.want && len(got.Users) != 0 {
					t.Errorf("actors = %+v, want pat shown = %t", got.Users, tc.want)
				}
			})
		}
	}
}
