package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

func TestAuthenticateChecksTheUserStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		status     domain.RecordStatus
		wantStatus int
		wantDetail string
	}{
		{"active user passes", domain.RecordActive, http.StatusOK, ""},
		{"suspended user is forbidden", domain.RecordSuspended, http.StatusForbidden, "user is suspended"},
		{"deleted user is unauthorized", domain.RecordDeleted, http.StatusUnauthorized, "user is deleted"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			u, err := e.fs.GetUser(context.Background(), e.member.ID)
			if err != nil {
				t.Fatal(err)
			}
			u.Status = tc.status
			if _, err := e.fs.UpdateUser(context.Background(), u); err != nil {
				t.Fatal(err)
			}
			if tc.wantDetail == "" {
				if rec := e.do(memberSecret, http.MethodGet, "/projects/lorvik", nil); rec.Code != tc.wantStatus {
					t.Fatalf("GET = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body)
				}
				return
			}
			if p := e.problem(memberSecret, http.MethodGet, "/projects/lorvik", nil, tc.wantStatus); p.Detail != tc.wantDetail {
				t.Errorf("detail = %q, want %q", p.Detail, tc.wantDetail)
			}
		})
	}
}

// An agent acts only while its owner could: suspending, deleting or removing
// the owner from the org stops the agent on its next request.
func TestAgentStopsWhenItsOwnerCannotAct(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		change     func(e *testEnv)
		wantStatus int
		wantDetail string
	}{
		{"owner suspended", func(e *testEnv) { e.setUserStatus(e.user, domain.RecordSuspended) }, http.StatusForbidden, "agent's owner is suspended"},
		{"owner deleted", func(e *testEnv) { e.setUserStatus(e.user, domain.RecordDeleted) }, http.StatusUnauthorized, "agent's owner is deleted"},
		{"owner removed from the org", func(e *testEnv) { e.setOrgRole(e.user, domain.RoleOwner, domain.RecordRemoved) }, http.StatusForbidden, "agent's owner is not a member of this org"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			tc.change(e)
			for _, secret := range []string{agentSecret, agent2Secret} {
				if p := e.problem(secret, http.MethodGet, "/projects/lorvik", nil, tc.wantStatus); p.Detail != tc.wantDetail {
					t.Errorf("detail = %q, want %q", p.Detail, tc.wantDetail)
				}
			}
		})
	}
}

// An agent holds the lower of its own role and its owner's: an org-wide
// member agent whose owner drops to viewer may read and no longer write.
func TestAgentActsAtTheLowerOfItsRoleAndItsOwners(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.setOrgRole(e.user, domain.RoleViewer, domain.RecordActive)
	if rec := e.do(agentSecret, http.MethodGet, "/projects/lorvik", nil); rec.Code != http.StatusOK {
		t.Fatalf("agent GET = %d, want 200: %s", rec.Code, rec.Body)
	}
	for _, secret := range []string{agentSecret, agent2Secret} {
		e.problem(secret, http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Title: "x"}, http.StatusForbidden)
	}
	for _, p := range call[apitypes.ProjectList](e, agent2Secret, http.MethodGet, "/projects", nil, http.StatusOK).Items {
		if p.Slug == "lorvik" && p.Role != "viewer" {
			t.Errorf("listed role of the project-bound agent = %q, want viewer, its owner's", p.Role)
		}
	}
	if me := call[apitypes.Me](e, agentSecret, http.MethodGet, "/me", nil, http.StatusOK); me.Role != "viewer" {
		t.Errorf("agent org role = %q, want viewer, its owner's", me.Role)
	}
}

// A project the owner cannot see does not exist for the owner's agent, even
// when the agent was granted a role there; once the owner joins, it does.
func TestAgentSeesOnlyProjectsItsOwnerSees(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	secret, bot := e.memberAgentInPrivateProject("vault")
	e.problem(secret, http.MethodGet, "/projects/vault", nil, http.StatusNotFound)
	for _, p := range call[apitypes.ProjectList](e, secret, http.MethodGet, "/projects", nil, http.StatusOK).Items {
		if p.Slug == "vault" {
			t.Errorf("project list shows vault, which the owner cannot see")
		}
	}
	vault, _ := e.fs.GetProjectBySlug(ctx, e.org.ID, "vault")
	_, _ = e.fs.PutProjectMember(ctx, domain.ProjectMember{ProjectID: vault.ID, Actor: e.member, Role: domain.RoleViewer, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	if rec := e.do(secret, http.MethodGet, "/projects/vault", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET vault after the owner joined = %d, want 200: %s", rec.Code, rec.Body)
	}
	e.problem(secret, http.MethodPost, "/projects/vault/cards", apitypes.CreateCard{Title: "x"}, http.StatusForbidden)
	if ok, err := e.svc.assignable(ctx, e.fs, e.org.ID, bot, vault); err != nil || !ok {
		t.Errorf("assignable while the owner is active = %v, %v; want true", ok, err)
	}
	e.setUserStatus(e.member, domain.RecordSuspended)
	if ok, err := e.svc.assignable(ctx, e.fs, e.org.ID, bot, vault); err != nil || ok {
		t.Errorf("assignable with the owner suspended = %v, %v; want false", ok, err)
	}
}

func (e *testEnv) setUserStatus(u domain.ActorRef, status domain.RecordStatus) {
	e.t.Helper()
	got, err := e.fs.GetUser(context.Background(), u.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	got.Status = status
	if _, err := e.fs.UpdateUser(context.Background(), got); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) setOrgRole(actor domain.ActorRef, role domain.Role, status domain.RecordStatus) {
	e.t.Helper()
	if _, err := e.fs.PutMembership(context.Background(), domain.Membership{OrgID: e.org.ID, Actor: actor, Role: role, Status: status, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		e.t.Fatal(err)
	}
}

// memberAgentInPrivateProject creates a private project the member sam is
// not in, and an agent owned by sam with the work right there; it returns
// the agent's secret and ref.
func (e *testEnv) memberAgentInPrivateProject(slug string) (string, domain.ActorRef) {
	e.t.Helper()
	ctx := context.Background()
	proj, err := e.fs.CreateProject(ctx, domain.Project{ID: e.ids.New(), OrgID: e.org.ID, Slug: slug, Name: slug, Visibility: domain.VisibilityPrivate, Concurrency: domain.ConcurrencyParallel,
		Scope: domain.ProjectScopeProject, Lease: 30 * time.Minute, CreatedBy: e.user, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		e.t.Fatal(err)
	}
	a, err := e.fs.CreateAgent(ctx, domain.Agent{ID: e.ids.New(), OrgID: e.org.ID, Name: "sam-bot", Harness: domain.HarnessCodex, CreatedBy: e.member.ID,
		Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		e.t.Fatal(err)
	}
	_, _ = e.fs.PutProjectMember(ctx, domain.ProjectMember{ProjectID: proj.ID, Actor: a.Ref(), Role: domain.RoleViewer, Rights: domain.RightWork, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	const secret = "lvk_a_sam-bot-secret"
	e.seedToken(secret, a.Ref(), domain.ScopeWrite, t0.Add(domain.AgentTokenTTL))
	return secret, a.Ref()
}

// Every door a human signs in through refuses an inactive user the way the
// bearer middleware does, so a deleted user is 401 everywhere and a
// suspended one 403, each with its hint.
func TestSignInDoorsGateTheUserLikeTheBearer(t *testing.T) {
	t.Parallel()
	statuses := []struct {
		status     domain.RecordStatus
		wantStatus int
		wantDetail string
		wantHint   string
	}{
		{domain.RecordSuspended, http.StatusForbidden, "user is suspended", "ask an org admin to reinstate this user"},
		{domain.RecordDeleted, http.StatusUnauthorized, "user is deleted", "this user was deleted; sign in with another account"},
	}
	for _, st := range statuses {
		t.Run("password "+st.wantDetail, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.svc.password = true
			hash, err := domain.HashPassword("correct horse")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = e.fs.CreateIdentity(context.Background(), domain.Identity{ID: e.ids.New(), UserID: e.user.ID, Provider: domain.ProviderPassword, ProviderID: "ada", Secret: hash, Login: "ada"})
			u, _ := e.fs.GetUser(context.Background(), e.user.ID)
			u.Status = st.status
			_, _ = e.fs.UpdateUser(context.Background(), u)
			p := e.problem("", http.MethodPost, "/login", apitypes.Login{Login: "ada", Password: "correct horse"}, st.wantStatus)
			if p.Detail != st.wantDetail || p.Hint != st.wantHint {
				t.Errorf("problem = %q / %q, want %q / %q", p.Detail, p.Hint, st.wantDetail, st.wantHint)
			}
		})
		t.Run("github "+st.wantDetail, func(t *testing.T) {
			t.Parallel()
			e := newGHEnv(t, "")
			u, _ := e.fs.CreateUser(context.Background(), domain.User{ID: e.ids.New(), Login: "dev", Name: "Dev", Status: st.status, CreatedAt: t0, UpdatedAt: t0})
			_, _ = e.fs.CreateIdentity(context.Background(), domain.Identity{ID: e.ids.New(), UserID: u.ID, Provider: domain.ProviderGitHub, ProviderID: "2001", Login: "dev", CreatedAt: t0})
			e.gh.add(ghUser{ID: 2001, Login: "dev", Name: "Dev"}, "tok-dev", "")
			p := e.problem("", http.MethodPost, "/login/github", apitypes.GitHubLogin{AccessToken: "tok-dev"}, st.wantStatus)
			if p.Detail != st.wantDetail || p.Hint != st.wantHint {
				t.Errorf("problem = %q / %q, want %q / %q", p.Detail, p.Hint, st.wantDetail, st.wantHint)
			}
		})
	}
}
