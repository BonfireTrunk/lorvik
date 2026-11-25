package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// JoinProject is an agent asking to join a project. With rights there it is
// already a member. Otherwise the says decide (domain.DecideAgentJoin): the
// agent's own setting, its owner's rule for the project, the project
// admin's setting and the owner's account switch. Allowed, it joins with the
// work right, which the owner's role there caps on every request, so the
// result reports what it holds now and a promoted owner lifts it; else a
// pending request waits for the owner on the dashboard. A project the owner cannot see is 404, and an
// agent denied or removed there is refused until its owner adds it back.
func (s *Service) JoinProject(ctx context.Context, slug string) (apitypes.JoinResult, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.JoinResult{}, err
	}
	if !p.IsAgent() {
		return apitypes.JoinResult{}, withHint(fmt.Errorf("%w: only an agent asks to join a project", domain.ErrForbidden),
			"ask a project admin to add you")
	}
	if err := p.requireWritable(); err != nil {
		return apitypes.JoinResult{}, err
	}
	var out apitypes.JoinResult
	err = s.store.WithTx(ctx, func(tx Store) error {
		proj, err := tx.GetProjectBySlug(ctx, p.OrgID, slug)
		if err != nil {
			return fmt.Errorf("project %q: %w", slug, err)
		}
		owner, ok, err := roleIn(ctx, tx, proj, domain.ActorRef{Kind: domain.ActorUser, ID: p.Owner}, p.OwnerOrgRole, true, 0)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("project %q: %w", slug, domain.ErrNotFound)
		}
		ag, err := tx.GetAgent(ctx, p.Actor.ID)
		if err != nil {
			return fmt.Errorf("load agent: %w", err)
		}
		a, err := s.access(ctx, tx, p, proj)
		switch {
		case err == nil:
			out = apitypes.JoinResult{Project: slug, Status: "member", Rights: a.rights.Names(),
				Message: fmt.Sprintf("%s is already in %s with %s", ag.Name, slug, rightsWords(a.rights))}
			return nil
		case !errors.Is(err, domain.ErrNotFound):
			return err
		}
		pm, err := tx.GetProjectMember(ctx, proj.ID, p.Actor)
		wasPending := err == nil && pm.Status == domain.RecordPending
		switch {
		case errors.Is(err, domain.ErrNotFound):
		case err != nil:
			return fmt.Errorf("load project role: %w", err)
		case pm.Status == domain.RecordRemoved:
			return withHint(fmt.Errorf("%w: %s was denied or removed in %s", domain.ErrForbidden, ag.Name, slug),
				"its owner can add it back on the dashboard's Access page")
		}
		ownerUser, err := tx.GetUser(ctx, p.Owner)
		if err != nil {
			return fmt.Errorf("load agent owner: %w", err)
		}
		rule, err := tx.GetAgentJoinRule(ctx, proj.ID, ownerUser.ID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("load agent join rule: %w", err)
		}
		allowed := domain.DecideAgentJoin(ag.AgentJoin, rule.AgentJoin, proj.AgentJoin, ownerUser.AgentJoin) == domain.AgentJoinAllow
		if !allowed && wasPending {
			out = waiting(ag, ownerUser, slug, allowedRights(owner.role))
			return nil
		}
		now := s.now()
		m := domain.ProjectMember{ProjectID: proj.ID, Actor: p.Actor, Role: domain.RoleViewer, Rights: domain.RightWork, Status: domain.RecordPending,
			GrantedBy: &ownerUser.ID, CreatedAt: now, UpdatedAt: now}
		if allowed {
			m.Status = domain.RecordActive
		}
		if _, err := putProjectMember(ctx, tx, m); err != nil {
			return fmt.Errorf("agent %s in %s: %w", ag.Name, slug, err)
		}
		held := allowedRights(owner.role)
		if !allowed {
			out = waiting(ag, ownerUser, slug, held)
			return s.emitP(ctx, tx, p, &proj.ID, domain.EventAgentJoinRequested, agentSubject(ag.ID),
				payload(ag.Name, "project", slug, "rights", held.Names(), "owner", ownerUser.ID.String()))
		}
		out = apitypes.JoinResult{Project: slug, Status: "joined", Rights: held.Names(),
			Message: fmt.Sprintf("%s joined %s with %s, allowed by %s's settings", ag.Name, slug, rightsWords(held), displayName(ownerUser))}
		return s.emitP(ctx, tx, p, &proj.ID, domain.EventAgentJoined, agentSubject(ag.ID),
			payload(ag.Name, "project", slug, "rights", held.Names(), "auto", true))
	})
	return out, err
}

func waiting(ag domain.Agent, owner domain.User, slug string, rights domain.Rights) apitypes.JoinResult {
	return apitypes.JoinResult{Project: slug, Status: "pending", Rights: rights.Names(),
		Message: fmt.Sprintf("waiting for %s to allow %s in %s on the dashboard", displayName(owner), ag.Name, slug)}
}

func displayName(u domain.User) string { return cmp.Or(u.Name, u.Login) }

// ListAgentRequests lists the pending requests of the caller's agents to
// join projects the caller can still see, oldest first.
func (s *Service) ListAgentRequests(ctx context.Context) (apitypes.AgentRequestList, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.AgentRequestList{}, err
	}
	pms, err := s.store.ListAgentRequests(ctx, p.OrgID, p.Actor.ID)
	if err != nil {
		return apitypes.AgentRequestList{}, fmt.Errorf("list agent requests: %w", err)
	}
	out := apitypes.AgentRequestList{Items: []apitypes.AgentRequest{}}
	for _, pm := range pms {
		ag, err := s.agentOf(ctx, s.store, p, pm.Actor.ID)
		if err != nil || ag.Status != domain.RecordActive {
			continue
		}
		proj, err := s.store.GetProject(ctx, pm.ProjectID)
		if err != nil {
			return apitypes.AgentRequestList{}, fmt.Errorf("load project: %w", err)
		}
		a, err := s.access(ctx, s.store, p, proj)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		} else if err != nil {
			return apitypes.AgentRequestList{}, err
		}
		out.Items = append(out.Items, apitypes.AgentRequest{Agent: toAgent(ag), Project: proj.Slug, ProjectName: proj.Name,
			Rights: allowedRights(a.role).Names(), RequestedAt: pm.UpdatedAt})
	}
	return out, nil
}

// allowedRights is what an agent that joined holds: the work right it is
// stored with, within what its owner's role there holds.
func allowedRights(role domain.Role) domain.Rights {
	return domain.RightWork & domain.RightsHeldBy(role)
}

// AllowAgentRequest lets the caller's agent into the project it asked to
// join, with the work right; reported capped by the caller's role there now.
func (s *Service) AllowAgentRequest(ctx context.Context, agentID domain.ID, slug string) (apitypes.Agent, error) {
	var out apitypes.Agent
	err := s.decideAgentRequest(ctx, agentID, slug, func(tx Store, p Principal, ag domain.Agent, proj domain.Project, pm domain.ProjectMember, role domain.Role) error {
		pm.Role, pm.Rights, pm.Status, pm.GrantedBy, pm.UpdatedAt = domain.RoleViewer, domain.RightWork, domain.RecordActive, &p.Actor.ID, s.now()
		if _, err := putProjectMember(ctx, tx, pm); err != nil {
			return fmt.Errorf("allow %s in %s: %w", ag.Name, slug, err)
		}
		if err := s.emitP(ctx, tx, p, &proj.ID, domain.EventAgentJoined, agentSubject(ag.ID),
			payload(ag.Name, "project", slug, "rights", allowedRights(role).Names())); err != nil {
			return err
		}
		var err error
		out, err = s.agentDetail(ctx, tx, p, ag)
		return err
	})
	return out, err
}

// DenyAgentRequest refuses the caller's agent the project it asked to join.
// The agent's later requests there are refused until the caller adds it.
func (s *Service) DenyAgentRequest(ctx context.Context, agentID domain.ID, slug string) error {
	return s.decideAgentRequest(ctx, agentID, slug, func(tx Store, p Principal, ag domain.Agent, proj domain.Project, pm domain.ProjectMember, _ domain.Role) error {
		pm.Status, pm.UpdatedAt = domain.RecordRemoved, s.now()
		if _, err := tx.PutProjectMember(ctx, pm); err != nil {
			return fmt.Errorf("deny %s in %s: %w", ag.Name, slug, err)
		}
		return s.emitP(ctx, tx, p, &proj.ID, domain.EventAgentJoinDenied, agentSubject(ag.ID), payload(ag.Name, "project", slug))
	})
}

// decideAgentRequest loads a pending request of one of the caller's agents
// in a project the caller can see, with the caller's role there, and hands
// it to decide inside a transaction. Anyone else's request is 404.
func (s *Service) decideAgentRequest(ctx context.Context, agentID domain.ID, slug string,
	decide func(tx Store, p Principal, ag domain.Agent, proj domain.Project, pm domain.ProjectMember, role domain.Role) error) error {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return err
	}
	if err := p.requireWritable(); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx Store) error {
		ag, err := s.agentOf(ctx, tx, p, agentID)
		if err != nil {
			return err
		}
		proj, a, err := s.project(ctx, tx, p, slug)
		if err != nil {
			return err
		}
		pm, err := tx.GetProjectMember(ctx, proj.ID, ag.Ref())
		if errors.Is(err, domain.ErrNotFound) || err == nil && (pm.Status != domain.RecordPending || ag.CreatedBy != p.Actor.ID) {
			return fmt.Errorf("request of agent %s to join %s: %w", ag.Name, slug, domain.ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("load project role: %w", err)
		}
		return decide(tx, p, ag, proj, pm, a.role)
	})
}
