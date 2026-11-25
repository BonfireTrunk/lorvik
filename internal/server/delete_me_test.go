package server

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

const samAgentSecret = "lvk_a_sams-agent-secret"

// deleteMe deletes the caller's account and fails the test unless it is gone.
func deleteMe(t *testing.T, e *testEnv, secret, login string) {
	t.Helper()
	if rec := e.do(secret, http.MethodDelete, "/me", apitypes.DeleteMe{Confirm: login}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete me = %d: %s", rec.Code, rec.Body)
	}
}

// seedSamsWorld gives the member sam a GitHub and a password sign-in, an
// agent of their own with a token, and a card assigned to them.
func seedSamsWorld(t *testing.T, e *testEnv) (domain.ActorRef, apitypes.Card) {
	t.Helper()
	ctx := context.Background()
	u, _ := e.fs.GetUser(ctx, e.member.ID)
	u.Email, u.AvatarURL, u.Locale, u.Timezone = "sam@example.test", "https://avatars.example.test/sam", "en-GB", "Europe/London"
	_, _ = e.fs.UpdateUser(ctx, u)
	_, _ = e.fs.CreateIdentity(ctx, domain.Identity{ID: e.ids.New(), UserID: e.member.ID, Provider: domain.ProviderGitHub, ProviderID: "4101", Login: "sam-gh", CreatedAt: t0})
	_, _ = e.fs.CreateIdentity(ctx, domain.Identity{ID: e.ids.New(), UserID: e.member.ID, Provider: domain.ProviderPassword, ProviderID: "sam", Secret: []byte("hash"), Login: "sam", CreatedAt: t0})
	a, _ := e.fs.CreateAgent(ctx, domain.Agent{ID: e.ids.New(), OrgID: e.org.ID, Name: "sams-claude", Harness: domain.HarnessClaudeCode, CreatedBy: e.member.ID,
		Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	_, _ = e.fs.PutMembership(ctx, domain.Membership{OrgID: e.org.ID, Actor: a.Ref(), Role: domain.RoleViewer, Rights: domain.RightWork, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	e.seedToken(samAgentSecret, a.Ref(), domain.ScopeWrite, t0.Add(domain.AgentTokenTTL))
	card := e.newCard(userSecret, apitypes.CreateCard{Title: "sam's", Status: "ready", Assignee: &apitypes.Actor{Kind: "user", ID: e.member.ID.String()}})
	return a.Ref(), card
}

func TestDeletingYourAccountEndsEverythingItHeld(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	agent, card := seedSamsWorld(t, e)
	events := len(e.eventTypes())

	deleteMe(t, e, memberSecret, "sam")
	if rec := e.do(memberSecret, http.MethodGet, "/me", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the deleted person's token: %d %s, want 401", rec.Code, rec.Body)
	}

	u, _ := e.fs.GetUser(ctx, e.member.ID)
	if u.Status != domain.RecordDeleted || u.Login != "sam" || u.Name != "sam" || u.Email != "" || u.AvatarURL != "" || u.Locale != "" || u.Timezone != "" {
		t.Errorf("user = %+v, want deleted with only login and name kept", u)
	}
	if m, _ := e.fs.GetMembership(ctx, e.org.ID, e.member); m.Status != domain.RecordRemoved {
		t.Errorf("membership = %+v, want removed", m)
	}
	if ids, _ := e.fs.ListIdentities(ctx, e.member.ID); len(ids) != 0 {
		t.Errorf("identities = %+v, want none", ids)
	}
	if a, _ := e.fs.GetAgent(ctx, agent.ID); a.Status != domain.RecordRevoked {
		t.Errorf("their agent = %+v, want revoked", a)
	}
	e.problem(samAgentSecret, http.MethodGet, "/me", nil, http.StatusUnauthorized)
	if got := e.show(userSecret, card.Number); got.Assignee != nil {
		t.Errorf("their card's assignee = %+v, want cleared", got.Assignee)
	}
	if a, _ := e.fs.GetAgent(ctx, e.agent.ID); a.Status != domain.RecordActive {
		t.Errorf("the owner's agent = %+v, want untouched", a)
	}
	call[apitypes.Me](e, userSecret, http.MethodGet, "/me", nil, http.StatusOK)

	got := e.eventTypes()[events:]
	for _, want := range []string{"card.updated", "agent.revoked", "user.deleted"} {
		if !slices.Contains(got, want) {
			t.Errorf("events = %v, want %s among them", got, want)
		}
	}
	if ev := e.lastEvent(); ev.Type != domain.EventUserDeleted || ev.Actor != e.member || ev.OrgID != e.org.ID || ev.Subject != membershipSubject(e.member.ID) {
		t.Errorf("last event = %+v, want sam deleting their account in Acme", ev)
	}
}

func TestDeletingYourAccountRefusals(t *testing.T) {
	t.Parallel()
	const readSecret = "lvk_u_member-read-secret"
	tests := []struct {
		name   string
		secret string
		body   apitypes.DeleteMe
		want   int
		detail string
	}{
		{"the login is mistyped", memberSecret, apitypes.DeleteMe{Confirm: "Sam"}, http.StatusUnprocessableEntity, ""},
		{"nothing is typed", memberSecret, apitypes.DeleteMe{}, http.StatusUnprocessableEntity, ""},
		{"an agent", agentSecret, apitypes.DeleteMe{Confirm: "claude-code"}, http.StatusForbidden, ""},
		{"a read-only token", readSecret, apitypes.DeleteMe{Confirm: "sam"}, http.StatusForbidden, ""},
		{"the org's only owner", userSecret, apitypes.DeleteMe{Confirm: "ada"}, http.StatusConflict, "you are the only owner of Acme"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.seedToken(readSecret, e.member, domain.ScopeRead, t0.Add(domain.DefaultTokenTTL))
			events := len(e.eventTypes())
			p := e.problem(tc.secret, http.MethodDelete, "/me", tc.body, tc.want)
			if tc.want == http.StatusUnprocessableEntity && !hasField(p, "confirm") {
				t.Errorf("problem = %+v, want it to name confirm", p)
			}
			if tc.detail != "" && p.Detail != tc.detail {
				t.Errorf("detail = %q, want %q", p.Detail, tc.detail)
			}
			for _, who := range []domain.ActorRef{e.user, e.member} {
				if u, _ := e.fs.GetUser(context.Background(), who.ID); u.Status != domain.RecordActive {
					t.Errorf("user %s = %+v, want untouched", u.Login, u)
				}
			}
			if got := e.eventTypes()[events:]; len(got) != 0 {
				t.Errorf("events = %v, want none", got)
			}
		})
	}
}

func TestAnOwnerWithAnotherOwnerBesideThemMayDelete(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.seedUser("ruth", domain.RoleOwner)
	deleteMe(t, e, userSecret, "ada")
	if u, _ := e.fs.GetUser(context.Background(), e.user.ID); u.Status != domain.RecordDeleted {
		t.Errorf("user = %+v, want deleted", u)
	}
	// The agents ada created go with them.
	for _, a := range []domain.ActorRef{e.agent, e.agent2} {
		if got, _ := e.fs.GetAgent(context.Background(), a.ID); got.Status != domain.RecordRevoked {
			t.Errorf("agent %s = %+v, want revoked", got.Name, got)
		}
	}
}

// #348: a GitHub sign-in made an empty person who holds the GitHub account,
// so the account it was meant for cannot link it. Deleting the empty person
// from the Create-an-org screen frees it.
func TestDeletingTheEmptyPersonAGitHubSignInMadeFreesTheGitHubAccount(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	e.gh.add(ghUser{ID: 4200, Login: "ada-gh"}, "tok-ada-gh", "code-ada-gh")
	signIn := e.signInCLI(t, apitypes.GitHubLogin{AccessToken: "tok-ada-gh"}).Secret
	id, err := e.fs.GetIdentity(context.Background(), domain.ProviderGitHub, "4200")
	if err != nil || id.UserID == e.admin.ID {
		t.Fatalf("identity = %+v, %v; want it on a new person", id, err)
	}
	if got, want := e.link(t, nil, "code-ada-gh").Header().Get("Location"), "/admin?error=linked_elsewhere"; got != want {
		t.Fatalf("link before = %q, want %q", got, want)
	}
	empty, _ := e.fs.GetUser(context.Background(), id.UserID)

	// The sign-in token opens nothing new beside the delete.
	e.problem(signIn, http.MethodPatch, "/me", apitypes.UpdateUser{}, http.StatusForbidden)
	deleteMe(t, e.testEnv, signIn, empty.Login)
	if u, _ := e.fs.GetUser(context.Background(), empty.ID); u.Status != domain.RecordDeleted {
		t.Fatalf("empty person = %+v, want deleted", u)
	}
	if p := e.problem(signIn, http.MethodGet, "/login/orgs", nil, http.StatusUnauthorized); p.Detail != "this sign-in was used or has expired" {
		t.Errorf("the sign-in token after the delete: %+v", p)
	}

	if got, want := e.link(t, nil, "code-ada-gh").Header().Get("Location"), "/admin?linked=1"; got != want {
		t.Fatalf("link after = %q, want %q", got, want)
	}
	if id, _ := e.fs.GetIdentity(context.Background(), domain.ProviderGitHub, "4200"); id.UserID != e.admin.ID {
		t.Errorf("identity = %+v, want it on the admin", id)
	}
}

func TestAPendingInvitationIsWithdrawnQuietly(t *testing.T) {
	t.Parallel()
	e := newGHEnv(t, "")
	u, gh := e.pat(4300)
	beta := e.newOrg("Beta", domain.RecordActive)
	e.place(u, beta, openInvited)
	signIn := e.signInCLI(t, apitypes.GitHubLogin{AccessToken: gh}).Secret
	events := len(e.eventTypes())

	deleteMe(t, e.testEnv, signIn, "pat")
	if m, _ := e.fs.GetMembership(context.Background(), beta.ID, u.Ref()); m.Status != domain.RecordRemoved {
		t.Errorf("invitation = %+v, want removed", m)
	}
	if got := e.eventTypes()[events:]; len(got) != 0 {
		t.Errorf("events = %v, want none: members never saw the invitation", got)
	}
}

func TestASuspendedCoOwnerIsNoOwnerToLeaveTheOrgTo(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ruth := e.seedUser("ruth", domain.RoleOwner)
	u, _ := e.fs.GetUser(context.Background(), ruth.ID)
	u.Status = domain.RecordSuspended
	_, _ = e.fs.UpdateUser(context.Background(), u)
	if p := e.problem(userSecret, http.MethodDelete, "/me", apitypes.DeleteMe{Confirm: "ada"}, http.StatusConflict); p.Detail != "you are the only owner of Acme" {
		t.Errorf("problem = %+v, want the only-owner refusal", p)
	}
}

func TestTheLastOwnerCannotBeMadeLessThanOwner(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := "/members/user/" + e.user.ID.String()
	if p := e.problem(userSecret, http.MethodPut, path, apitypes.PutMembership{Role: "admin"}, http.StatusConflict); p.Detail != "the org would be left without an owner" {
		t.Errorf("problem = %+v, want the last-owner refusal", p)
	}
	if m, _ := e.fs.GetMembership(context.Background(), e.org.ID, e.user); m.Role != domain.RoleOwner {
		t.Fatalf("membership = %+v, want still owner", m)
	}
	e.seedUser("ruth", domain.RoleOwner)
	if got := call[apitypes.Membership](e, userSecret, http.MethodPut, path, apitypes.PutMembership{Role: "admin"}, http.StatusOK); got.Role != "admin" {
		t.Errorf("with another owner: role %q, want admin", got.Role)
	}
}

// #422 A: an org whose only person deletes their account closes with it.
func TestTheOnlyPersonInAnOrgClosesItWithTheirAccount(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	m, _ := e.fs.GetMembership(ctx, e.org.ID, e.member)
	m.Status = domain.RecordRemoved
	_, _ = e.fs.PutMembership(ctx, m)
	// An agent a former member made is the org's too, and goes with it.
	left, _ := e.fs.CreateAgent(ctx, domain.Agent{ID: e.ids.New(), OrgID: e.org.ID, Name: "left-behind", Harness: domain.HarnessCodex, CreatedBy: e.member.ID,
		Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	e.readyCard(userSecret, "one")
	e.readyCard(userSecret, "two")

	got := call[apitypes.DeletionPreview](e, userSecret, http.MethodGet, "/me/deletion", nil, http.StatusOK)
	if want := []apitypes.ClosingOrg{{Name: "Acme", Projects: 1, Cards: 2}}; !slices.Equal(got.Closes, want) || len(got.BlockedBy) != 0 {
		t.Fatalf("preview = %+v, want Acme closing with 1 project besides its global one, and 2 cards", got)
	}
	deleteMe(t, e, userSecret, "ada")
	if org, _ := e.fs.GetOrg(ctx, e.org.ID); org.Status != domain.RecordDeleted {
		t.Errorf("org = %+v, want closed", org)
	}
	for _, a := range []domain.ID{e.agent.ID, e.agent2.ID, left.ID} {
		if got, _ := e.fs.GetAgent(ctx, a); got.Status != domain.RecordRevoked {
			t.Errorf("agent %s = %+v, want revoked with the org", got.Name, got)
		}
	}
}

func TestTheDeletionPreviewNamesWhatRefuses(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if got := call[apitypes.DeletionPreview](e, userSecret, http.MethodGet, "/me/deletion", nil, http.StatusOK); !slices.Equal(got.BlockedBy, []string{"Acme"}) || len(got.Closes) != 0 {
		t.Errorf("only owner beside a member: preview = %+v, want Acme blocking", got)
	}
	if got := call[apitypes.DeletionPreview](e, memberSecret, http.MethodGet, "/me/deletion", nil, http.StatusOK); len(got.BlockedBy)+len(got.Closes) != 0 {
		t.Errorf("a member: preview = %+v, want nothing closed or refused", got)
	}
	e.problem(agentSecret, http.MethodGet, "/me/deletion", nil, http.StatusForbidden)
	// A suspended member is no active person: the org would close.
	u, _ := e.fs.GetUser(context.Background(), e.member.ID)
	u.Status = domain.RecordSuspended
	_, _ = e.fs.UpdateUser(context.Background(), u)
	if got := call[apitypes.DeletionPreview](e, userSecret, http.MethodGet, "/me/deletion", nil, http.StatusOK); len(got.Closes) != 1 || len(got.BlockedBy) != 0 {
		t.Errorf("beside a suspended member: preview = %+v, want Acme closing", got)
	}
}

// A token in a closed org stops even if nothing revoked it, as when an
// invitation was accepted while the org closed.
func TestATokenInAClosedOrgIsRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	org, _ := e.fs.GetOrg(context.Background(), e.org.ID)
	org.Status = domain.RecordDeleted
	_, _ = e.fs.UpdateOrg(context.Background(), org)
	for _, secret := range []string{memberSecret, agentSecret} {
		if p := e.problem(secret, http.MethodGet, "/me", nil, http.StatusUnauthorized); p.Detail != "org closed" {
			t.Errorf("token in a closed org: %+v, want org closed", p)
		}
	}
}

// An org the person only leaves does not hide one they would close.
func TestTheDeletionPreviewLooksAtEveryOrg(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	lab, _ := e.fs.CreateOrg(ctx, domain.Org{ID: e.ids.New(), Name: "Sam's lab", Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	_, _ = e.fs.PutMembership(ctx, domain.Membership{OrgID: lab.ID, Actor: e.member, Role: domain.RoleOwner, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	got := call[apitypes.DeletionPreview](e, memberSecret, http.MethodGet, "/me/deletion", nil, http.StatusOK)
	if want := []apitypes.ClosingOrg{{Name: "Sam's lab"}}; !slices.Equal(got.Closes, want) || len(got.BlockedBy) != 0 {
		t.Errorf("preview = %+v, want Sam's lab closing after Acme is only left", got)
	}
}
