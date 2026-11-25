package lorvikd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/server"
)

// bootstrapStore implements only what bootstrap calls; any other method panics
// through the nil embedded interface.
type bootstrapStore struct {
	server.Store
	orgs       []domain.Org
	users      []domain.User
	identities []domain.Identity
	members    []domain.Membership
	tokens     []domain.Token
	projects   []domain.Project
}

func (s *bootstrapStore) WithTx(_ context.Context, fn func(server.Store) error) error { return fn(s) }

func (s *bootstrapStore) ListOrgs(context.Context) ([]domain.Org, error) { return s.orgs, nil }

func (s *bootstrapStore) CreateOrg(_ context.Context, o domain.Org) (domain.Org, error) {
	s.orgs = append(s.orgs, o)
	return o, nil
}

func (s *bootstrapStore) CreateUser(_ context.Context, u domain.User) (domain.User, error) {
	s.users = append(s.users, u)
	return u, nil
}

func (s *bootstrapStore) CreateIdentity(_ context.Context, i domain.Identity) (domain.Identity, error) {
	s.identities = append(s.identities, i)
	return i, nil
}

func (s *bootstrapStore) PutMembership(_ context.Context, m domain.Membership) (domain.Membership, error) {
	s.members = append(s.members, m)
	return m, nil
}

func (s *bootstrapStore) CreateProject(_ context.Context, p domain.Project) (domain.Project, error) {
	s.projects = append(s.projects, p)
	return p, nil
}

func (s *bootstrapStore) CreateToken(_ context.Context, t domain.Token) (domain.Token, error) {
	s.tokens = append(s.tokens, t)
	return t, nil
}

func newTestCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	return cmd
}

func TestBootstrapCreatesOwnerMembershipAndToken(t *testing.T) {
	t.Parallel()
	st := &bootstrapStore{}
	ids, _ := domain.NewIDGen(1, time.Now)
	o := bootstrapOpts{OrgName: "Acme", Login: "Ada"}
	secret, err := bootstrap(newTestCmd(t), st, ids, o, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, domain.TokenPrefix) {
		t.Errorf("secret = %q, want %s prefix", secret, domain.TokenPrefix)
	}
	if len(st.orgs) != 1 || st.orgs[0].ID == 0 || st.orgs[0].Status != domain.RecordActive {
		t.Fatalf("orgs = %+v, want one active org with an id", st.orgs)
	}
	org := st.orgs[0]
	if len(st.users) != 1 || st.users[0].Login != "Ada" || st.users[0].Name != "Ada" || st.users[0].ID == 0 {
		t.Fatalf("users = %+v, want one user named after the login", st.users)
	}
	u := st.users[0]
	if len(st.identities) != 0 {
		t.Errorf("identities = %+v, want none without a password", st.identities)
	}
	if len(st.members) != 1 || st.members[0].Role != domain.RoleOwner || st.members[0].OrgID != org.ID || st.members[0].Actor != u.Ref() || st.members[0].Status != domain.RecordActive {
		t.Errorf("memberships = %+v, want the user as active owner of the new org", st.members)
	}
	if len(st.projects) != 1 {
		t.Fatalf("projects = %+v, want only the global project", st.projects)
	}
	g := st.projects[0]
	if g.OrgID != org.ID || g.Slug != domain.GlobalSlug || g.Name != "Global" || g.Scope != domain.ProjectScopeGlobal ||
		g.Visibility != domain.VisibilityOrg || g.Concurrency != domain.ConcurrencyParallel || g.Lease != domain.DefaultLeaseDuration ||
		g.CreatedBy != u.Ref() || g.Status != domain.RecordActive {
		t.Errorf("global project = %+v, want the org's active global project created by the bootstrap user", g)
	}
	if len(st.tokens) != 1 {
		t.Fatalf("tokens = %+v, want one", st.tokens)
	}
	tok := st.tokens[0]
	if tok.OrgID != org.ID || tok.Actor != u.Ref() || !bytes.Equal(tok.Hash, domain.HashToken(secret)) || tok.Scope != domain.ScopeWrite {
		t.Errorf("token = %+v, want an org-scoped write token for the user matching the secret", tok)
	}
	if ttl := time.Until(tok.ExpiresAt); ttl < domain.DefaultTokenTTL-time.Minute || ttl > domain.DefaultTokenTTL {
		t.Errorf("token expires in %s, want about %s", ttl, domain.DefaultTokenTTL)
	}
}

func TestBootstrapStoresPasswordIdentity(t *testing.T) {
	t.Parallel()
	st := &bootstrapStore{}
	ids, _ := domain.NewIDGen(1, time.Now)
	o := bootstrapOpts{OrgName: "Acme", Login: "Ada", Password: "correct horse"}
	if _, err := bootstrap(newTestCmd(t), st, ids, o, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(st.identities) != 1 {
		t.Fatalf("identities = %+v, want one", st.identities)
	}
	id := st.identities[0]
	if id.Provider != domain.ProviderPassword || id.ProviderID != "ada" || id.UserID != st.users[0].ID {
		t.Errorf("identity = %+v, want a password identity keyed by the lowercased login", id)
	}
	if ok, err := domain.VerifyPassword(id.Secret, "correct horse"); err != nil || !ok {
		t.Errorf("VerifyPassword = %v, %v; want the bootstrap password to verify", ok, err)
	}
}

func TestBootstrapRejectsBadInput(t *testing.T) {
	t.Parallel()
	ids, _ := domain.NewIDGen(1, time.Now)
	tests := []struct {
		name string
		opts bootstrapOpts
		want string
	}{
		{"empty login", bootstrapOpts{OrgName: "Acme"}, "--user"},
		{"short password", bootstrapOpts{OrgName: "Acme", Login: "v", Password: "short"}, "password"},
		{"bad timezone", bootstrapOpts{OrgName: "Acme", Login: "v", Timezone: "Mars/Olympus"}, "timezone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st := &bootstrapStore{}
			_, err := bootstrap(newTestCmd(t), st, ids, tt.opts, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want mentioning %q", err, tt.want)
			}
			if len(st.orgs) != 0 || len(st.users) != 0 {
				t.Errorf("created orgs=%d users=%d, want nothing on a rejected input", len(st.orgs), len(st.users))
			}
		})
	}
}

func TestBootstrapRefusesWhenAnOrgExists(t *testing.T) {
	t.Parallel()
	st := &bootstrapStore{orgs: []domain.Org{{ID: 7, Name: "Acme"}}}
	ids, _ := domain.NewIDGen(1, time.Now)
	o := bootstrapOpts{OrgName: "Other", Login: "ada"}
	_, err := bootstrap(newTestCmd(t), st, ids, o, &bytes.Buffer{})
	if err == nil || errors.Is(err, domain.ErrNotFound) || len(st.users) != 0 || len(st.orgs) != 1 {
		t.Fatalf("err = %v, users = %d, orgs = %d; want a refusal and nothing created", err, len(st.users), len(st.orgs))
	}
}
