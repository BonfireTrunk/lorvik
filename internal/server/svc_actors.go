package server

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// Me describes the caller: identity, org, roles and the token in use.
func (s *Service) Me(ctx context.Context) (apitypes.Me, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Me{}, err
	}
	org, err := s.store.GetOrg(ctx, p.OrgID)
	if err != nil {
		return apitypes.Me{}, fmt.Errorf("load org: %w", err)
	}
	tok, err := s.store.GetToken(ctx, p.TokenID)
	if err != nil {
		return apitypes.Me{}, fmt.Errorf("load token: %w", err)
	}
	out := apitypes.Me{Actor: toActor(p.Actor), Org: toOrg(org, p.canOrg(domain.PermOrgManage) || p.canOrg(domain.PermMemberManage)),
		TokenPrefix: p.TokenPrefix, TokenExpiry: tok.ExpiresAt}
	if p.HasOrgRole {
		out.Role = p.OrgRole.String()
	}
	if out.Projects, err = s.lookup(s.store, p).projectRoles(ctx, p.OrgID, p.Actor, s.effectiveRights(ctx, s.store, p)); err != nil {
		return apitypes.Me{}, err
	}
	if p.IsAgent() {
		a, err := s.store.GetAgent(ctx, p.Actor.ID)
		if err != nil {
			return apitypes.Me{}, fmt.Errorf("load agent: %w", err)
		}
		ag := toAgent(a)
		out.Agent = &ag
		if out.SessionName, err = s.lookup(s.store, p).sessionName(ctx, p.Actor, p.Session); err != nil {
			return apitypes.Me{}, err
		}
	} else {
		u, err := s.store.GetUser(ctx, p.Actor.ID)
		if err != nil {
			return apitypes.Me{}, fmt.Errorf("load user: %w", err)
		}
		us := toSelf(u)
		ids, err := s.store.ListIdentities(ctx, u.ID)
		if err != nil {
			return apitypes.Me{}, fmt.Errorf("load identities: %w", err)
		}
		for _, id := range ids {
			if id.Provider == domain.ProviderGitHub {
				us.GitHubLogin = id.Login
			}
		}
		out.User = &us
	}
	return out, nil
}

// toSelf is toUser for the user themselves, with their own settings.
func toSelf(u domain.User) apitypes.User {
	out := toUser(u)
	out.AgentJoin = u.AgentJoin.String()
	return out
}

// UpdateMe edits the calling user's profile.
func (s *Service) UpdateMe(ctx context.Context, in apitypes.UpdateUser) (apitypes.User, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.User{}, err
	}
	if err := p.requireWritable(); err != nil {
		return apitypes.User{}, err
	}
	u, err := s.store.GetUser(ctx, p.Actor.ID)
	if err != nil {
		return apitypes.User{}, fmt.Errorf("load user: %w", err)
	}
	if in.Name != nil {
		u.Name = strings.TrimSpace(*in.Name)
	}
	if in.Email != nil {
		u.Email = strings.TrimSpace(*in.Email)
	}
	if in.Locale != nil {
		u.Locale = *in.Locale
	}
	if in.Timezone != nil {
		u.Timezone = *in.Timezone
	}
	was := u.AgentJoin
	if in.AgentJoin != nil {
		if u.AgentJoin, err = domain.ParseAgentJoin(*in.AgentJoin); err != nil {
			return apitypes.User{}, err
		}
	}
	u.UpdatedAt = s.now()
	if err := u.Validate(); err != nil {
		return apitypes.User{}, err
	}
	var updated domain.User
	err = s.store.WithTx(ctx, func(tx Store) error {
		if updated, err = tx.UpdateUser(ctx, u); err != nil {
			return fmt.Errorf("update user: %w", err)
		}
		if u.AgentJoin == was {
			return nil
		}
		return s.emitP(ctx, tx, p, nil, domain.EventAgentJoinSetting, membershipSubject(p.Actor.ID),
			payload("", "agent_join", u.AgentJoin.String()))
	})
	if err != nil {
		return apitypes.User{}, err
	}
	return toSelf(updated), nil
}

// GetOrg returns the caller's org; settings are included for admins.
func (s *Service) GetOrg(ctx context.Context) (apitypes.Org, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Org{}, err
	}
	org, err := s.store.GetOrg(ctx, p.OrgID)
	if err != nil {
		return apitypes.Org{}, fmt.Errorf("load org: %w", err)
	}
	return toOrg(org, p.canOrg(domain.PermMemberManage)), nil
}

// UpdateOrg edits the org's name and settings (owners).
func (s *Service) UpdateOrg(ctx context.Context, in apitypes.UpdateOrg) (apitypes.Org, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.Org{}, err
	}
	if err := p.requireOrg(domain.PermOrgManage); err != nil {
		return apitypes.Org{}, err
	}
	org, err := s.store.GetOrg(ctx, p.OrgID)
	if err != nil {
		return apitypes.Org{}, fmt.Errorf("load org: %w", err)
	}
	if in.Name != nil {
		if org.Name, err = orgName(*in.Name); err != nil {
			return apitypes.Org{}, err
		}
	}
	if in.Settings != nil {
		org.Settings = domain.OrgSettings{AgentApproval: in.Settings.AgentApproval, TokenTTLDefault: in.Settings.TokenTTLDefault,
			TokenTTLMax: in.Settings.TokenTTLMax, ProjectVisibilityDefault: in.Settings.ProjectVisibilityDefault}
		if err := org.Settings.Validate(); err != nil {
			return apitypes.Org{}, err
		}
	}
	org.UpdatedAt = s.now()
	updated, err := s.store.UpdateOrg(ctx, org)
	if err != nil {
		return apitypes.Org{}, fmt.Errorf("update org: %w", err)
	}
	return toOrg(updated, true), nil
}

// Actors resolves ids to the users and agents of the caller's org, for
// clients that cache names.
func (s *Service) Actors(ctx context.Context, ids []domain.ID) (apitypes.Actors, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Actors{}, err
	}
	out := apitypes.Actors{Users: []apitypes.User{}, Agents: []apitypes.Agent{}}
	if len(ids) == 0 {
		return out, nil
	}
	users, err := s.store.GetUsers(ctx, ids)
	if err != nil {
		return apitypes.Actors{}, fmt.Errorf("load users: %w", err)
	}
	for _, u := range users {
		// Only users with a record in this org are visible to it.
		m, err := s.store.GetMembership(ctx, p.OrgID, u.Ref())
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return apitypes.Actors{}, fmt.Errorf("check membership: %w", err)
		}
		if m.Status == domain.RecordPending && p.Actor != u.Ref() && !p.seesPendingMembers() {
			continue
		}
		// An org writes an invitation alone, and a removed row outlives the membership: neither may open the person's record.
		if !m.Status.Alive() {
			out.Users = append(out.Users, toNamedUser(u))
			continue
		}
		out.Users = append(out.Users, toUser(u))
	}
	agents, err := s.store.GetAgents(ctx, ids)
	if err != nil {
		return apitypes.Actors{}, fmt.Errorf("load agents: %w", err)
	}
	for _, a := range agents {
		if a.OrgID == p.OrgID {
			out.Agents = append(out.Agents, toAgent(a))
		}
	}
	return out, nil
}

// ListMembers lists the org's members, highest role first.
func (s *Service) ListMembers(ctx context.Context, all bool, token string, limit int) (apitypes.MemberList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.MemberList{}, err
	}
	if err := p.requireOrg(domain.PermRead); err != nil {
		return apitypes.MemberList{}, err
	}
	cur, err := decodeCursor(memberList, token)
	if err != nil {
		return apitypes.MemberList{}, err
	}
	ms, remaining, err := s.store.ListMemberships(ctx, p.OrgID, query.MemberFilter{
		All: all && p.seesPendingMembers(), Page: query.Page{Limit: limit}, Cursor: cur.memberCursor(),
	})
	if err != nil {
		return apitypes.MemberList{}, fmt.Errorf("list members: %w", err)
	}
	out := make([]apitypes.Membership, 0, len(ms))
	for _, m := range ms {
		out = append(out, toMembership(m))
	}
	return apitypes.MemberList{Items: out, Page: forwardPage(memberList, ms, func(m domain.Membership) cursor {
		return memberCursorKey(m.Role, m.CreatedAt, m.Actor)
	}, remaining)}, nil
}

const memberList = "members"

// putMembership writes an org membership once the domain accepts it; the
// domain owns the role rules, the agent cap among them, so no caller restates
// them.
func putMembership(ctx context.Context, tx Store, m domain.Membership) (domain.Membership, error) {
	if err := m.Validate(); err != nil {
		return domain.Membership{}, err
	}
	return tx.PutMembership(ctx, m)
}

// putProjectMember is putMembership for a project role.
func putProjectMember(ctx context.Context, tx Store, m domain.ProjectMember) (domain.ProjectMember, error) {
	if err := m.Validate(); err != nil {
		return domain.ProjectMember{}, err
	}
	return tx.PutProjectMember(ctx, m)
}

// grantOf reads a membership body for target: a person takes a role and no
// rights; an agent takes rights, within what the granter's role holds, and
// holds only the viewer role. where names the place for the error.
func grantOf(target domain.ActorRef, in apitypes.PutMembership, granter domain.Role, where string) (domain.Role, domain.Rights, error) {
	if target.Kind == domain.ActorAgent {
		rights, err := agentGrantRights(in)
		if err == nil {
			err = capRights(rights, domain.RightsHeldBy(granter), granter, where)
		}
		return domain.RoleViewer, rights, err
	}
	role, err := domain.ParseRole(in.Role)
	if err != nil {
		return 0, 0, invalidField("role", err.Error())
	}
	if len(in.Rights) > 0 {
		return 0, 0, invalidField("rights", "only agents carry rights; people have a role")
	}
	return role, 0, nil
}

// grantPayload is the event payload of a membership grant: the role for a
// person, the rights for an agent; extra adds the project.
func grantPayload(target domain.ActorRef, role domain.Role, rights domain.Rights, extra map[string]any) map[string]any {
	out := map[string]any{"actor": toActor(target), "role": role.String()}
	if target.Kind == domain.ActorAgent {
		out["rights"] = rights.Names()
	}
	maps.Copy(out, extra)
	return out
}

// grantable checks that the caller may hand out role.
func grantable(p Principal, role domain.Role) error {
	if !role.Valid() {
		return invalidField("role", "unknown role")
	}
	if !p.HasOrgRole || !p.OrgRole.CanGrant(role) {
		return withHint(fmt.Errorf("%w: %s cannot grant %s", domain.ErrForbidden, p.OrgRole, role), "only owners grant admin and owner")
	}
	return nil
}

// actorInOrg checks that the actor has a place in the org: a user is a
// member of it, an agent belongs to it and was not removed. A user who exists
// on the server but is no member answers the same as one who does not exist.
func (s *Service) actorInOrg(ctx context.Context, st Store, orgID domain.ID, a domain.ActorRef) error {
	switch a.Kind {
	case domain.ActorUser:
		m, err := st.GetMembership(ctx, orgID, a)
		if errors.Is(err, domain.ErrNotFound) || err == nil && !m.Status.Alive() {
			return noSuchMember()
		}
		if err != nil {
			return fmt.Errorf("load membership: %w", err)
		}
	case domain.ActorAgent:
		ag, err := st.GetAgent(ctx, a.ID)
		if err != nil || ag.OrgID != orgID || ag.Status == domain.RecordForgotten {
			return invalidField("id", "no such agent in this org")
		}
	}
	return nil
}

func noSuchMember() error {
	return withHint(invalidField("id", "no such member of this org"), "a person joins by an invitation (POST /members/invite) or by asking through the org's sign-in link")
}

// PutMember grants or changes an actor's org role (admins; owners for
// admin and owner). A person gets a role only where they have a place
// already: as a member, or with a request to join waiting. An org cannot make
// a member of someone who did not ask, and cannot accept its own invitation
// for them.
func (s *Service) PutMember(ctx context.Context, target domain.ActorRef, in apitypes.PutMembership) (apitypes.Membership, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.Membership{}, err
	}
	if err := p.requireOrg(domain.PermMemberManage); err != nil {
		return apitypes.Membership{}, err
	}
	role, rights, err := grantOf(target, in, p.OrgRole, "the org")
	if err != nil {
		return apitypes.Membership{}, err
	}
	if err := grantable(p, role); err != nil {
		return apitypes.Membership{}, err
	}
	var out domain.Membership
	err = s.store.WithTx(ctx, func(tx Store) error {
		now := s.now()
		// Owners before the member, in the order deleting an account takes them.
		var owners []domain.Membership
		if target.Kind == domain.ActorUser {
			var err error
			if owners, err = tx.LockOrgOwners(ctx, p.OrgID); err != nil {
				return err
			}
		}
		existing, gerr := tx.LockMembership(ctx, p.OrgID, target)
		if gerr != nil && !errors.Is(gerr, domain.ErrNotFound) {
			return fmt.Errorf("load membership: %w", gerr)
		}
		if target.Kind == domain.ActorUser {
			asked := gerr == nil && existing.Status == domain.RecordPending && existing.GrantedBy == nil
			if gerr != nil || !existing.Status.Alive() && !asked {
				return noSuchMember()
			}
		} else if err := s.actorInOrg(ctx, tx, p.OrgID, target); err != nil {
			return err
		}
		if gerr == nil && existing.Status.Alive() && existing.Role >= domain.RoleAdmin && !p.OrgRole.CanGrant(existing.Role) {
			return withHint(fmt.Errorf("%w: only an owner can change an %s", domain.ErrForbidden, existing.Role), "ask an owner")
		}
		if gerr == nil && existing.Status.Alive() && existing.Role == domain.RoleOwner && role != domain.RoleOwner && lastOwner(owners, target) {
			return newProblem(http.StatusConflict, "the org would be left without an owner", "make another member owner first")
		}
		typ := domain.EventMembershipGranted
		if gerr == nil && existing.Status.Alive() {
			typ = domain.EventMembershipChanged
		}
		m := domain.Membership{OrgID: p.OrgID, Actor: target, Role: role, Rights: rights, Status: domain.RecordActive, GrantedBy: &p.Actor.ID, CreatedAt: now, UpdatedAt: now}
		if gerr == nil {
			m.CreatedAt = existing.CreatedAt
		}
		saved, err := putMembership(ctx, tx, m)
		if err != nil {
			return fmt.Errorf("put membership: %w", err)
		}
		out = saved
		return s.emitP(ctx, tx, p, nil, typ, membershipSubject(target.ID), grantPayload(target, role, rights, nil))
	})
	if err != nil {
		return apitypes.Membership{}, err
	}
	return toMembership(out), nil
}

// lastOwner reports whether the org has no owner but target.
func lastOwner(owners []domain.Membership, target domain.ActorRef) bool {
	return !slices.ContainsFunc(owners, func(m domain.Membership) bool { return m.Actor != target })
}

// RemoveMember ends an actor's org membership and revokes their tokens.
func (s *Service) RemoveMember(ctx context.Context, target domain.ActorRef) error {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return err
	}
	if err := p.requireOrg(domain.PermMemberManage); err != nil {
		return err
	}
	if target == p.Actor {
		return withHint(fmt.Errorf("%w: you cannot remove yourself", domain.ErrForbidden), "ask another admin or owner")
	}
	return s.store.WithTx(ctx, func(tx Store) error {
		m, err := tx.LockMembership(ctx, p.OrgID, target)
		if err != nil {
			return fmt.Errorf("membership: %w", err)
		}
		if !p.OrgRole.CanGrant(m.Role) {
			return withHint(fmt.Errorf("%w: only an owner can remove an %s", domain.ErrForbidden, m.Role), "ask an owner")
		}
		if err := s.endMembership(ctx, tx, p, m); err != nil {
			return err
		}
		return s.emitP(ctx, tx, p, nil, domain.EventMembershipRemoved, membershipSubject(target.ID),
			map[string]any{"actor": toActor(target)})
	})
}

// endMembership ends m, a place in p's org: the row is marked removed, the
// tokens the actor holds in the org are revoked, an agent's chats lose their
// grants and the actor's assignments that no longer hold are dropped.
func (s *Service) endMembership(ctx context.Context, tx Store, p Principal, m domain.Membership) error {
	m.Status, m.UpdatedAt = domain.RecordRemoved, s.now()
	if _, err := putMembership(ctx, tx, m); err != nil {
		return fmt.Errorf("remove membership: %w", err)
	}
	if err := tx.RevokeTokensForActor(ctx, p.OrgID, m.Actor); err != nil {
		return fmt.Errorf("revoke tokens: %w", err)
	}
	if m.Actor.Kind == domain.ActorAgent {
		agent, err := tx.GetAgent(ctx, m.Actor.ID)
		if err != nil {
			return fmt.Errorf("load agent: %w", err)
		}
		if err := s.endSessionGrants(ctx, tx, p, agent, nil); err != nil {
			return err
		}
	}
	return s.dropAssignments(ctx, tx, p, m.Actor)
}
