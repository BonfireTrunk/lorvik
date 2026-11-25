package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
	"github.com/BonfireTrunk/lorvik/internal/store/sqlcgen"
)

// CreateOrg inserts an org with the caller's id.
func (s *Store) CreateOrg(ctx context.Context, o domain.Org) (domain.Org, error) {
	const op = "create org"
	settings, err := json.Marshal(o.Settings)
	if err != nil {
		return domain.Org{}, fmt.Errorf("%s: encode settings: %w", op, err)
	}
	row, err := s.q.CreateOrg(ctx, sqlcgen.CreateOrgParams{
		ID: int64(o.ID), Name: o.Name, Status: int16(o.Status), Settings: settings, CreatedAt: o.CreatedAt,
	})
	if err != nil {
		return domain.Org{}, wrap(op, err)
	}
	return orgFromRow(row)
}

// GetOrg returns one org.
func (s *Store) GetOrg(ctx context.Context, id domain.ID) (domain.Org, error) {
	row, err := s.q.GetOrg(ctx, int64(id))
	if err != nil {
		return domain.Org{}, wrap("get org", err)
	}
	return orgFromRow(row)
}

// UpdateOrg writes name, status and settings.
func (s *Store) UpdateOrg(ctx context.Context, o domain.Org) (domain.Org, error) {
	const op = "update org"
	settings, err := json.Marshal(o.Settings)
	if err != nil {
		return domain.Org{}, fmt.Errorf("%s: encode settings: %w", op, err)
	}
	row, err := s.q.UpdateOrg(ctx, sqlcgen.UpdateOrgParams{
		ID: int64(o.ID), Name: o.Name, Status: int16(o.Status), Settings: settings, UpdatedAt: o.UpdatedAt,
	})
	if err != nil {
		return domain.Org{}, wrap(op, err)
	}
	return orgFromRow(row)
}

// CreateUser inserts a user.
func (s *Store) CreateUser(ctx context.Context, u domain.User) (domain.User, error) {
	row, err := s.q.CreateUser(ctx, sqlcgen.CreateUserParams{
		ID: int64(u.ID), Login: u.Login, Name: u.Name, Email: u.Email, AvatarUrl: u.AvatarURL,
		Locale: optText(u.Locale), Timezone: optText(u.Timezone), Status: int16(u.Status), AgentJoin: int16(u.AgentJoin), CreatedAt: u.CreatedAt,
	})
	if err != nil {
		return domain.User{}, wrap("create user", err)
	}
	return userFromRow(row), nil
}

// GetUser returns one user.
func (s *Store) GetUser(ctx context.Context, id domain.ID) (domain.User, error) {
	row, err := s.q.GetUser(ctx, int64(id))
	if err != nil {
		return domain.User{}, wrap("get user", err)
	}
	return userFromRow(row), nil
}

// LockUser is GetUser with a row lock held to the end of the transaction.
func (s *Store) LockUser(ctx context.Context, id domain.ID) (domain.User, error) {
	row, err := s.q.LockUser(ctx, int64(id))
	if err != nil {
		return domain.User{}, wrap("lock user", err)
	}
	return userFromRow(row), nil
}

// GetUsers returns the users among ids that exist.
func (s *Store) GetUsers(ctx context.Context, in []domain.ID) ([]domain.User, error) {
	return getMany(ctx, in, "get users", s.q.GetUsers, userFromRow)
}

// UpdateUser writes the profile fields, status and the agent join switch.
func (s *Store) UpdateUser(ctx context.Context, u domain.User) (domain.User, error) {
	row, err := s.q.UpdateUser(ctx, sqlcgen.UpdateUserParams{
		ID: int64(u.ID), Login: u.Login, Name: u.Name, Email: u.Email, AvatarUrl: u.AvatarURL,
		Locale: optText(u.Locale), Timezone: optText(u.Timezone), Status: int16(u.Status), AgentJoin: int16(u.AgentJoin), UpdatedAt: u.UpdatedAt,
	})
	if err != nil {
		return domain.User{}, wrap("update user", err)
	}
	return userFromRow(row), nil
}

// CreateIdentity links a login method to a user.
func (s *Store) CreateIdentity(ctx context.Context, i domain.Identity) (domain.Identity, error) {
	row, err := s.q.CreateIdentity(ctx, sqlcgen.CreateIdentityParams{
		ID: int64(i.ID), UserID: int64(i.UserID), Provider: int16(i.Provider), ProviderID: i.ProviderID,
		Secret: i.Secret, Login: i.Login, LastLoginAt: i.LastLoginAt, CreatedAt: i.CreatedAt,
	})
	if err != nil {
		return domain.Identity{}, wrap("create identity", err)
	}
	return identityFromRow(row), nil
}

// GetIdentity is the login lookup.
func (s *Store) GetIdentity(ctx context.Context, provider domain.Provider, providerID string) (domain.Identity, error) {
	row, err := s.q.GetIdentity(ctx, sqlcgen.GetIdentityParams{Provider: int16(provider), ProviderID: providerID})
	if err != nil {
		return domain.Identity{}, wrap("get identity", err)
	}
	return identityFromRow(row), nil
}

// ListIdentities returns a user's login methods, oldest first.
func (s *Store) ListIdentities(ctx context.Context, userID domain.ID) ([]domain.Identity, error) {
	rows, err := s.q.ListIdentities(ctx, int64(userID))
	if err != nil {
		return nil, wrap("list identities", err)
	}
	return mapRows(rows, identityFromRow), nil
}

// UpdateIdentity writes secret, login and last_login_at.
func (s *Store) UpdateIdentity(ctx context.Context, i domain.Identity) error {
	return wrap("update identity", s.q.UpdateIdentity(ctx, sqlcgen.UpdateIdentityParams{
		ID: int64(i.ID), Secret: i.Secret, Login: i.Login, LastLoginAt: i.LastLoginAt,
	}))
}

// DeleteIdentity removes the user's identity, or returns ErrNotFound.
func (s *Store) DeleteIdentity(ctx context.Context, userID, id domain.ID) error {
	n, err := s.q.DeleteIdentity(ctx, sqlcgen.DeleteIdentityParams{ID: int64(id), UserID: int64(userID)})
	if err != nil {
		return wrap("delete identity", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// PutMembership inserts or replaces the actor's org membership.
func (s *Store) PutMembership(ctx context.Context, m domain.Membership) (domain.Membership, error) {
	row, err := s.q.PutMembership(ctx, sqlcgen.PutMembershipParams{
		OrgID: int64(m.OrgID), ActorKind: int16(m.Actor.Kind), ActorID: int64(m.Actor.ID),
		Role: int16(m.Role), Status: int16(m.Status), Rights: int16(m.Rights), GrantedByUserID: idPtr(m.GrantedBy), CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	})
	if err != nil {
		return domain.Membership{}, wrap("put membership", err)
	}
	return membershipFromRow(row), nil
}

// GetMembership returns the row whatever its status.
func (s *Store) GetMembership(ctx context.Context, orgID domain.ID, a domain.ActorRef) (domain.Membership, error) {
	row, err := s.q.GetMembership(ctx, sqlcgen.GetMembershipParams{OrgID: int64(orgID), ActorKind: int16(a.Kind), ActorID: int64(a.ID)})
	if err != nil {
		return domain.Membership{}, wrap("get membership", err)
	}
	return membershipFromRow(row), nil
}

// LockMembership is GetMembership with a row lock, for a transaction that
// decides on the row and writes it back.
func (s *Store) LockMembership(ctx context.Context, orgID domain.ID, a domain.ActorRef) (domain.Membership, error) {
	row, err := s.q.LockMembership(ctx, sqlcgen.LockMembershipParams{OrgID: int64(orgID), ActorKind: int16(a.Kind), ActorID: int64(a.ID)})
	if err != nil {
		return domain.Membership{}, wrap("lock membership", err)
	}
	return membershipFromRow(row), nil
}

// CountOrgPeople counts the org's people with an active membership on an
// active account.
func (s *Store) CountOrgPeople(ctx context.Context, orgID domain.ID) (int, error) {
	n, err := s.q.CountOrgPeople(ctx, int64(orgID))
	if err != nil {
		return 0, wrap("count org people", err)
	}
	return int(n), nil
}

// CountOrgContents counts the org's projects other than its global one, and
// the cards of all its projects.
func (s *Store) CountOrgContents(ctx context.Context, orgID domain.ID) (projects, cards int, err error) {
	row, err := s.q.CountOrgContents(ctx, sqlcgen.CountOrgContentsParams{OrgID: int64(orgID), GlobalScope: int16(domain.ProjectScopeGlobal)})
	if err != nil {
		return 0, 0, wrap("count org contents", err)
	}
	return int(row.Projects), int(row.Cards), nil
}

// LockOrgOwners returns the org's active owners, people with an active
// account only, with their membership rows locked to the end of the
// transaction.
func (s *Store) LockOrgOwners(ctx context.Context, orgID domain.ID) ([]domain.Membership, error) {
	rows, err := s.q.LockOrgOwners(ctx, sqlcgen.LockOrgOwnersParams{OrgID: int64(orgID), Role: int16(domain.RoleOwner)})
	if err != nil {
		return nil, wrap("lock org owners", err)
	}
	return mapRows(rows, membershipFromRow), nil
}

// ListMemberships returns one page of an org's members, highest role first;
// remaining counts the members after it, capped.
func (s *Store) ListMemberships(ctx context.Context, orgID domain.ID, f query.MemberFilter) ([]domain.Membership, int, error) {
	const op = "list memberships"
	q := newFilter()
	q.add("org_id = @org_id", "org_id", int64(orgID))
	if !f.All {
		q.add("status > 0")
	}
	rows, remaining, err := listKeyset(ctx, s.db, q, "*", "memberships", membershipKeyset, f.Page, memberCursorArgs(f.Cursor))
	if err != nil {
		return nil, 0, wrap(op, err)
	}
	return mapRows(rows, membershipFromRow), remaining, nil
}

// ListMembershipsForActor returns the alive memberships of one actor.
func (s *Store) ListMembershipsForActor(ctx context.Context, a domain.ActorRef) ([]domain.Membership, error) {
	rows, err := s.q.ListMembershipsForActor(ctx, sqlcgen.ListMembershipsForActorParams{ActorKind: int16(a.Kind), ActorID: int64(a.ID)})
	if err != nil {
		return nil, wrap("list memberships for actor", err)
	}
	return mapRows(rows, membershipFromRow), nil
}

// ListOpenMembershipsForActor returns the alive and the pending memberships
// of one actor.
func (s *Store) ListOpenMembershipsForActor(ctx context.Context, a domain.ActorRef) ([]domain.Membership, error) {
	rows, err := s.q.ListOpenMembershipsForActor(ctx, sqlcgen.ListOpenMembershipsForActorParams{ActorKind: int16(a.Kind), ActorID: int64(a.ID)})
	if err != nil {
		return nil, wrap("list open memberships for actor", err)
	}
	return mapRows(rows, membershipFromRow), nil
}

// PutProjectMember inserts or replaces the actor's project role.
func (s *Store) PutProjectMember(ctx context.Context, m domain.ProjectMember) (domain.ProjectMember, error) {
	row, err := s.q.PutProjectMember(ctx, sqlcgen.PutProjectMemberParams{
		ProjectID: int64(m.ProjectID), ActorKind: int16(m.Actor.Kind), ActorID: int64(m.Actor.ID),
		Role: int16(m.Role), Status: int16(m.Status), Rights: int16(m.Rights), GrantedByUserID: idPtr(m.GrantedBy), CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	})
	if err != nil {
		return domain.ProjectMember{}, wrap("put project member", err)
	}
	return projectMemberFromRow(row), nil
}

// GetProjectMember returns the row whatever its status.
func (s *Store) GetProjectMember(ctx context.Context, projectID domain.ID, a domain.ActorRef) (domain.ProjectMember, error) {
	row, err := s.q.GetProjectMember(ctx, sqlcgen.GetProjectMemberParams{ProjectID: int64(projectID), ActorKind: int16(a.Kind), ActorID: int64(a.ID)})
	if err != nil {
		return domain.ProjectMember{}, wrap("get project member", err)
	}
	return projectMemberFromRow(row), nil
}

// ListAgentRequests returns the pending requests of agents to join projects
// of the org that the owner decides, oldest first.
func (s *Store) ListAgentRequests(ctx context.Context, orgID, ownerID domain.ID) ([]domain.ProjectMember, error) {
	rows, err := s.q.ListAgentRequests(ctx, sqlcgen.ListAgentRequestsParams{OrgID: int64(orgID), OwnerID: int64(ownerID)})
	if err != nil {
		return nil, wrap("list agent requests", err)
	}
	return mapRows(rows, projectMemberFromRow), nil
}

// TouchAgentSession records that the session used the agent's token at now.
// A row seen less than a minute ago is left alone, so a busy session costs
// no write.
func (s *Store) TouchAgentSession(ctx context.Context, agentID domain.ID, session string, now time.Time) error {
	return wrap("touch agent session", s.q.TouchAgentSession(ctx, sqlcgen.TouchAgentSessionParams{
		AgentID: int64(agentID), Session: session, Now: now, StaleBefore: now.Add(-time.Minute),
	}))
}

// TrimAgentSessions deletes the agent's chats beyond the keep most recently
// seen, except those that hold a grant.
func (s *Store) TrimAgentSessions(ctx context.Context, agentID domain.ID, keep int) error {
	return wrap("trim agent sessions", s.q.TrimAgentSessions(ctx, sqlcgen.TrimAgentSessionsParams{AgentID: int64(agentID), KeepRows: int32(keep)}))
}

// SetAgentSessionName stores what people call the session; an empty name
// clears it. The column holds null for no name and never an empty string.
func (s *Store) SetAgentSessionName(ctx context.Context, agentID domain.ID, session, name string, now time.Time) error {
	return wrap("set agent session name", s.q.SetAgentSessionName(ctx, sqlcgen.SetAgentSessionNameParams{
		AgentID: int64(agentID), Session: session, Name: optText(name), Now: now,
	}))
}

// ListAgentSessions returns up to limit sessions seen using the agent's
// token, most recently seen first.
func (s *Store) ListAgentSessions(ctx context.Context, agentID domain.ID, limit int) ([]domain.AgentSession, error) {
	rows, err := s.q.ListAgentSessions(ctx, sqlcgen.ListAgentSessionsParams{AgentID: int64(agentID), RowLimit: int32(limit)})
	if err != nil {
		return nil, wrap("list agent sessions", err)
	}
	return mapRows(rows, agentSessionFromRow), nil
}

// ListAgentSessionsByLabel returns the named sessions the agent was seen in,
// most recently seen first; a label never seen is absent.
func (s *Store) ListAgentSessionsByLabel(ctx context.Context, agentID domain.ID, labels []string) ([]domain.AgentSession, error) {
	rows, err := s.q.ListAgentSessionsByLabel(ctx, sqlcgen.ListAgentSessionsByLabelParams{AgentID: int64(agentID), Sessions: labels})
	if err != nil {
		return nil, wrap("list agent sessions by label", err)
	}
	return mapRows(rows, agentSessionFromRow), nil
}

// PutAgentSessionGrant inserts or replaces the rights of one session of an
// agent in one project.
func (s *Store) PutAgentSessionGrant(ctx context.Context, g domain.AgentSessionGrant) (domain.AgentSessionGrant, error) {
	if err := g.Validate(); err != nil {
		return domain.AgentSessionGrant{}, err
	}
	row, err := s.q.PutAgentSessionGrant(ctx, sqlcgen.PutAgentSessionGrantParams{
		ProjectID: int64(g.ProjectID), AgentID: int64(g.AgentID), Session: g.Session, Rights: int16(g.Rights),
		GrantedByUserID: int64(g.GrantedBy), EndsAt: g.EndsAt, Instructions: optText(g.Instructions),
		Status: int16(g.Status), CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt,
	})
	if err != nil {
		return domain.AgentSessionGrant{}, wrap("put agent session grant", err)
	}
	return agentSessionGrantFromRow(row), nil
}

// GetAgentSessionGrant returns the row whatever its status.
func (s *Store) GetAgentSessionGrant(ctx context.Context, projectID, agentID domain.ID, session string) (domain.AgentSessionGrant, error) {
	row, err := s.q.GetAgentSessionGrant(ctx, sqlcgen.GetAgentSessionGrantParams{ProjectID: int64(projectID), AgentID: int64(agentID), Session: session})
	if err != nil {
		return domain.AgentSessionGrant{}, wrap("get agent session grant", err)
	}
	return agentSessionGrantFromRow(row), nil
}

// ListAgentSessionGrants returns the alive grants of one agent across
// projects.
func (s *Store) ListAgentSessionGrants(ctx context.Context, agentID domain.ID) ([]domain.AgentSessionGrant, error) {
	rows, err := s.q.ListAgentSessionGrants(ctx, int64(agentID))
	if err != nil {
		return nil, wrap("list agent session grants", err)
	}
	return mapRows(rows, agentSessionGrantFromRow), nil
}

// EndAgentSessionGrant takes back a grant that is alive and whose end is at
// or before the given time, and returns domain.ErrNotFound for any other.
func (s *Store) EndAgentSessionGrant(ctx context.Context, projectID, agentID domain.ID, session string, before time.Time) (domain.AgentSessionGrant, error) {
	row, err := s.q.EndAgentSessionGrant(ctx, sqlcgen.EndAgentSessionGrantParams{
		ProjectID: int64(projectID), AgentID: int64(agentID), Session: session,
		Status: int16(domain.RecordRemoved), UpdatedAt: before, Before: before,
	})
	if err != nil {
		return domain.AgentSessionGrant{}, wrap("end agent session grant", err)
	}
	return agentSessionGrantFromRow(row), nil
}

// ListEndedAgentSessionGrants returns alive grants whose end is at or before
// the given time, soonest first.
func (s *Store) ListEndedAgentSessionGrants(ctx context.Context, before time.Time, limit int) ([]domain.AgentSessionGrant, error) {
	rows, err := s.q.ListEndedAgentSessionGrants(ctx, sqlcgen.ListEndedAgentSessionGrantsParams{Before: before, MaxRows: int32(clampLimit(limit))})
	if err != nil {
		return nil, wrap("list ended agent session grants", err)
	}
	return mapRows(rows, agentSessionGrantFromRow), nil
}

// GetAgentJoinRule returns a user's own join rule for one project;
// ErrNotFound means follow.
func (s *Store) GetAgentJoinRule(ctx context.Context, projectID, userID domain.ID) (domain.AgentJoinRule, error) {
	row, err := s.q.GetAgentJoinRule(ctx, sqlcgen.GetAgentJoinRuleParams{ProjectID: int64(projectID), UserID: int64(userID)})
	if err != nil {
		return domain.AgentJoinRule{}, wrap("get agent join rule", err)
	}
	return agentJoinRuleFromRow(row), nil
}

// PutAgentJoinRule sets a user's join rule for one project.
func (s *Store) PutAgentJoinRule(ctx context.Context, r domain.AgentJoinRule) (domain.AgentJoinRule, error) {
	row, err := s.q.PutAgentJoinRule(ctx, sqlcgen.PutAgentJoinRuleParams{
		ProjectID: int64(r.ProjectID), UserID: int64(r.UserID), AgentJoin: int16(r.AgentJoin), UpdatedAt: r.UpdatedAt,
	})
	if err != nil {
		return domain.AgentJoinRule{}, wrap("put agent join rule", err)
	}
	return agentJoinRuleFromRow(row), nil
}

// DeleteAgentJoinRule puts a user back to follow for one project.
func (s *Store) DeleteAgentJoinRule(ctx context.Context, projectID, userID domain.ID) error {
	return wrap("delete agent join rule", s.q.DeleteAgentJoinRule(ctx, sqlcgen.DeleteAgentJoinRuleParams{ProjectID: int64(projectID), UserID: int64(userID)}))
}

// ListProjectMembers returns one page of a project's members, highest role
// first; remaining counts the members after it, capped.
func (s *Store) ListProjectMembers(ctx context.Context, projectID domain.ID, f query.MemberFilter) ([]domain.ProjectMember, int, error) {
	const op = "list project members"
	q := newFilter()
	q.add("project_id = @project_id", "project_id", int64(projectID))
	if !f.All {
		q.add("status > 0")
	}
	rows, remaining, err := listKeyset(ctx, s.db, q, "*", "project_members", projectMemberKeyset, f.Page, memberCursorArgs(f.Cursor))
	if err != nil {
		return nil, 0, wrap(op, err)
	}
	return mapRows(rows, projectMemberFromRow), remaining, nil
}

// ListProjectMembersForActor returns the actor's alive project roles in one org.
func (s *Store) ListProjectMembersForActor(ctx context.Context, orgID domain.ID, a domain.ActorRef) ([]domain.ProjectMember, error) {
	rows, err := s.q.ListProjectMembersForActor(ctx, sqlcgen.ListProjectMembersForActorParams{
		ActorKind: int16(a.Kind), ActorID: int64(a.ID), OrgID: int64(orgID),
	})
	if err != nil {
		return nil, wrap("list project members for actor", err)
	}
	return mapRows(rows, projectMemberFromRow), nil
}

// HasProjectMembership reports whether the actor has an alive role in any
// project of the org.
func (s *Store) HasProjectMembership(ctx context.Context, orgID domain.ID, a domain.ActorRef) (bool, error) {
	ok, err := s.q.HasProjectMembership(ctx, sqlcgen.HasProjectMembershipParams{
		ActorKind: int16(a.Kind), ActorID: int64(a.ID), OrgID: int64(orgID),
	})
	if err != nil {
		return false, wrap("has project membership", err)
	}
	return ok, nil
}

// CreateAgent inserts an agent.
func (s *Store) CreateAgent(ctx context.Context, a domain.Agent) (domain.Agent, error) {
	row, err := s.q.CreateAgent(ctx, sqlcgen.CreateAgentParams{
		ID: int64(a.ID), OrgID: int64(a.OrgID), Name: a.Name, Harness: int16(a.Harness), Machine: a.Machine,
		CreatedByUserID: int64(a.CreatedBy), Status: int16(a.Status), AgentJoin: int16(a.AgentJoin), CreatedAt: a.CreatedAt,
	})
	if err != nil {
		return domain.Agent{}, wrap("create agent", err)
	}
	return agentFromRow(row), nil
}

// GetAgent returns one agent.
func (s *Store) GetAgent(ctx context.Context, id domain.ID) (domain.Agent, error) {
	row, err := s.q.GetAgent(ctx, int64(id))
	if err != nil {
		return domain.Agent{}, wrap("get agent", err)
	}
	return agentFromRow(row), nil
}

// LockAgent is GetAgent with a row lock, for a transaction that decides on
// the agent's status and writes it back or acts on it.
func (s *Store) LockAgent(ctx context.Context, id domain.ID) (domain.Agent, error) {
	row, err := s.q.LockAgent(ctx, int64(id))
	if err != nil {
		return domain.Agent{}, wrap("lock agent", err)
	}
	return agentFromRow(row), nil
}

// GetAgents returns the agents among ids that exist.
func (s *Store) GetAgents(ctx context.Context, in []domain.ID) ([]domain.Agent, error) {
	return getMany(ctx, in, "get agents", s.q.GetAgents, agentFromRow)
}

// ListAgents returns an org's agents, active first; revoked and removed
// ones only with all.
func (s *Store) ListAgents(ctx context.Context, orgID domain.ID, all bool) ([]domain.Agent, error) {
	rows, err := s.q.ListAgents(ctx, sqlcgen.ListAgentsParams{OrgID: int64(orgID), IncludeInactive: all})
	if err != nil {
		return nil, wrap("list agents", err)
	}
	return mapRows(rows, agentFromRow), nil
}

// ListAgentsPage returns one page of an org's agents, for the list route;
// remaining counts the agents after it, capped. All adds the revoked ones; a
// removed agent is on no page.
func (s *Store) ListAgentsPage(ctx context.Context, orgID domain.ID, f query.AgentFilter) ([]domain.Agent, int, error) {
	const op = "list agents page"
	q := newFilter()
	q.add("org_id = @org_id", "org_id", int64(orgID))
	if f.All {
		q.add("status >= @revoked", "revoked", int16(domain.RecordRevoked))
	} else {
		q.add("status >= 0")
	}
	var cursor pgx.NamedArgs
	if f.Cursor != nil {
		cursor = pgx.NamedArgs{"c_status": int16(f.Cursor.Status), "c_created_at": f.Cursor.CreatedAt, "c_id": int64(f.Cursor.ID)}
	}
	rows, remaining, err := listKeyset(ctx, s.db, q, "*", "agents", agentKeyset, f.Page, cursor)
	if err != nil {
		return nil, 0, wrap(op, err)
	}
	return mapRows(rows, agentFromRow), remaining, nil
}

// UpdateAgent writes name, harness, machine, status and the join setting.
func (s *Store) UpdateAgent(ctx context.Context, a domain.Agent) (domain.Agent, error) {
	row, err := s.q.UpdateAgent(ctx, sqlcgen.UpdateAgentParams{
		ID: int64(a.ID), Name: a.Name, Harness: int16(a.Harness), Machine: a.Machine, Status: int16(a.Status),
		AgentJoin: int16(a.AgentJoin), UpdatedAt: a.UpdatedAt,
	})
	if err != nil {
		return domain.Agent{}, wrap("update agent", err)
	}
	return agentFromRow(row), nil
}

// CreateToken stores a token's hash; the secret never reaches the store.
func (s *Store) CreateToken(ctx context.Context, t domain.Token) (domain.Token, error) {
	row, err := s.q.CreateToken(ctx, sqlcgen.CreateTokenParams{
		ID: int64(t.ID), OrgID: int64(t.OrgID), ActorKind: int16(t.Actor.Kind), ActorID: int64(t.Actor.ID),
		Name: t.Name, Prefix: t.Prefix, Hash: t.Hash, Scope: int16(t.Scope), Status: int16(t.Status),
		ExpiresAt: t.ExpiresAt, CreatedAt: t.CreatedAt,
	})
	if err != nil {
		return domain.Token{}, wrap("create token", err)
	}
	return tokenFromRow(row), nil
}

// GetTokenByHash is the authentication lookup; dead tokens are returned too.
func (s *Store) GetTokenByHash(ctx context.Context, hash []byte) (domain.Token, error) {
	row, err := s.q.GetTokenByHash(ctx, hash)
	if err != nil {
		return domain.Token{}, wrap("get token by hash", err)
	}
	return tokenFromRow(row), nil
}

// GetToken returns one token by id.
func (s *Store) GetToken(ctx context.Context, id domain.ID) (domain.Token, error) {
	row, err := s.q.GetToken(ctx, int64(id))
	if err != nil {
		return domain.Token{}, wrap("get token", err)
	}
	return tokenFromRow(row), nil
}

// LockToken is GetToken with a row lock held to the end of the transaction.
func (s *Store) LockToken(ctx context.Context, id domain.ID) (domain.Token, error) {
	row, err := s.q.LockToken(ctx, int64(id))
	if err != nil {
		return domain.Token{}, wrap("lock token", err)
	}
	return tokenFromRow(row), nil
}

// ListTokens returns one page of the actor's tokens in the org, newest first;
// remaining counts the tokens after it, capped.
func (s *Store) ListTokens(ctx context.Context, orgID domain.ID, a domain.ActorRef, pg query.Page, after *query.TokenCursor) ([]domain.Token, int, error) {
	const op = "list tokens"
	q := newFilter()
	q.add("org_id = @org_id AND actor_kind = @actor_kind AND actor_id = @actor_id",
		"org_id", int64(orgID), "actor_kind", int16(a.Kind), "actor_id", int64(a.ID))
	var cursor pgx.NamedArgs
	if after != nil {
		cursor = pgx.NamedArgs{"c_created_at": after.CreatedAt, "c_id": int64(after.ID)}
	}
	rows, remaining, err := listKeyset(ctx, s.db, q, "*", "tokens", tokenKeyset, pg, cursor)
	if err != nil {
		return nil, 0, wrap(op, err)
	}
	return mapRows(rows, tokenFromRow), remaining, nil
}

// UpdateToken writes status, expires_at and replaced_by.
func (s *Store) UpdateToken(ctx context.Context, t domain.Token) error {
	return wrap("update token", s.q.UpdateToken(ctx, sqlcgen.UpdateTokenParams{
		ID: int64(t.ID), Status: int16(t.Status), ExpiresAt: t.ExpiresAt, ReplacedBy: idPtr(t.ReplacedBy),
	}))
}

// TouchToken records last use, at most once a minute per token.
func (s *Store) TouchToken(ctx context.Context, id domain.ID, at time.Time) error {
	return wrap("touch token", s.q.TouchToken(ctx, sqlcgen.TouchTokenParams{At: at, ID: int64(id)}))
}

// RevokeTokensForActor revokes every live token of the actor in the org.
func (s *Store) RevokeTokensForActor(ctx context.Context, orgID domain.ID, a domain.ActorRef) error {
	// A rotation that held a token's row lock when this update reached it has
	// committed a successor the update's snapshot cannot see. Each pass takes
	// a new snapshot, so the pass after one that revoked anything catches
	// such successors; a pass that revokes nothing ends it.
	for {
		n, err := s.q.RevokeTokensForActor(ctx, sqlcgen.RevokeTokensForActorParams{
			Status: int16(domain.RecordRevoked), OrgID: int64(orgID), ActorID: int64(a.ID), ActorKind: int16(a.Kind),
		})
		if err != nil || n == 0 {
			return wrap("revoke tokens", err)
		}
	}
}

// ListOrgs returns every org, oldest first.
func (s *Store) ListOrgs(ctx context.Context) ([]domain.Org, error) {
	rows, err := s.q.ListOrgs(ctx)
	if err != nil {
		return nil, wrap("list orgs", err)
	}
	out := make([]domain.Org, 0, len(rows))
	for _, r := range rows {
		o, err := orgFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}
