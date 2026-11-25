package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// sessionAgent is agentOf for a chat grant: the agent's owner or a caller who
// manages agents sees it, anyone else gets a 404 so the agent stays hidden.
func (s *Service) sessionAgent(ctx context.Context, st Store, p Principal, id domain.ID) (domain.Agent, error) {
	a, err := s.liveAgent(ctx, st, p, id)
	if err != nil {
		return domain.Agent{}, err
	}
	if a.CreatedBy != p.Actor.ID && !p.canOrg(domain.PermAgentManage) {
		return domain.Agent{}, fmt.Errorf("agent %s: %w", id, domain.ErrNotFound)
	}
	return a, nil
}

// baseLabel checks that label names a chat itself: subagents share their
// chat's grant, so a grant is never made to a -c<n> label.
func baseLabel(label string) error {
	if !apitypes.ValidSession(label) || label != domain.BaseSession(label) {
		return withHint(invalidField("label", fmt.Sprintf("%q is not the label of a chat", label)),
			"grant the chat's own label, which covers its -c<n> subagents")
	}
	return nil
}

// PutAgentSessionGrant gives one chat of an agent rights in a project, on top
// of the agent's own, within what the caller holds there, until taken back
// or until the end the caller sets, with the caller's instructions to the
// chat. Only the agent's owner or an agent manager may, and only in a project
// both the caller and the agent can see.
func (s *Service) PutAgentSessionGrant(ctx context.Context, id domain.ID, label, slug string, in apitypes.AgentSessionGrantBody) (apitypes.Agent, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.Agent{}, err
	}
	if err := p.requireWritable(); err != nil {
		return apitypes.Agent{}, err
	}
	if err := baseLabel(label); err != nil {
		return apitypes.Agent{}, err
	}
	rights, err := domain.ParseRights(in.Rights)
	if err != nil {
		return apitypes.Agent{}, err
	}
	if in.EndsAt != nil && !in.EndsAt.After(s.now()) {
		return apitypes.Agent{}, invalidField("ends_at", "must be in the future")
	}
	var out apitypes.Agent
	err = s.store.WithTx(ctx, func(tx Store) error {
		agent, err := s.sessionAgent(ctx, tx, p, id)
		if err != nil {
			return err
		}
		proj, a, err := s.project(ctx, tx, p, slug)
		if err != nil {
			return err
		}
		sees, err := s.assignable(ctx, tx, p.OrgID, agent.Ref(), proj)
		if err != nil {
			return err
		}
		if !sees {
			return invalidField("project", fmt.Sprintf("agent %s is not in %s", agent.Name, slug))
		}
		if err := capRights(rights, domain.RightsHeldBy(a.role), a.role, slug); err != nil {
			return err
		}
		now := s.now()
		g := domain.AgentSessionGrant{ProjectID: proj.ID, AgentID: agent.ID, Session: label, Rights: rights, GrantedBy: p.Actor.ID,
			EndsAt: in.EndsAt, Instructions: strings.TrimSpace(in.Instructions),
			Status: domain.RecordActive, CreatedAt: now, UpdatedAt: now}
		if old, gerr := tx.GetAgentSessionGrant(ctx, proj.ID, agent.ID, label); gerr == nil {
			g.CreatedAt = old.CreatedAt
		} else if !errors.Is(gerr, domain.ErrNotFound) {
			return fmt.Errorf("load session grant: %w", gerr)
		}
		if _, err := tx.PutAgentSessionGrant(ctx, g); err != nil {
			return fmt.Errorf("grant chat %s of %s in %s: %w", label, agent.Name, slug, err)
		}
		pl := payload(agent.Name, "project", slug, "session", label, "rights", rights.Names())
		if g.EndsAt != nil {
			pl["ends_at"] = *g.EndsAt
		}
		if err := s.emitP(ctx, tx, p, &proj.ID, domain.EventAgentSessionRights, agentSubject(agent.ID), pl); err != nil {
			return err
		}
		out, err = s.agentDetail(ctx, tx, p, agent)
		return err
	})
	return out, err
}

// DeleteAgentSessionGrant takes back what PutAgentSessionGrant gave one chat
// in a project; a chat with no live grant there is a 404.
func (s *Service) DeleteAgentSessionGrant(ctx context.Context, id domain.ID, label, slug string) error {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return err
	}
	if err := p.requireWritable(); err != nil {
		return err
	}
	if err := baseLabel(label); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx Store) error {
		agent, err := s.sessionAgent(ctx, tx, p, id)
		if err != nil {
			return err
		}
		proj, _, err := s.project(ctx, tx, p, slug)
		if err != nil {
			return err
		}
		g, err := tx.GetAgentSessionGrant(ctx, proj.ID, agent.ID, label)
		if err == nil && !g.Status.Alive() {
			err = domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("grant of chat %s of %s in %s: %w", label, agent.Name, slug, err)
		}
		return s.removeSessionGrant(ctx, tx, p, agent, slug, g)
	})
}

// removeSessionGrant takes back one chat grant and records it with empty
// rights, as the event of a grant does.
func (s *Service) removeSessionGrant(ctx context.Context, tx Store, p Principal, agent domain.Agent, slug string, g domain.AgentSessionGrant) error {
	g.Status, g.UpdatedAt = domain.RecordRemoved, s.now()
	if _, err := tx.PutAgentSessionGrant(ctx, g); err != nil {
		return fmt.Errorf("take back chat %s of %s in %s: %w", g.Session, agent.Name, slug, err)
	}
	return s.emitP(ctx, tx, p, &g.ProjectID, domain.EventAgentSessionRights, agentSubject(agent.ID),
		payload(agent.Name, "project", slug, "session", g.Session, "rights", []string{}))
}

// endSessionGrants takes back the agent's chat grants in one project, or in
// every project when only is nil. A chat's grant rides on the agent's place
// in the project, so it ends with it and a later binding starts clean.
func (s *Service) endSessionGrants(ctx context.Context, tx Store, p Principal, agent domain.Agent, only *domain.ID) error {
	grants, err := tx.ListAgentSessionGrants(ctx, agent.ID)
	if err != nil {
		return fmt.Errorf("list session grants of %s: %w", agent.Name, err)
	}
	for _, g := range grants {
		if only != nil && g.ProjectID != *only {
			continue
		}
		proj, err := tx.GetProject(ctx, g.ProjectID)
		if err != nil {
			return fmt.Errorf("load project of chat %s: %w", g.Session, err)
		}
		if err := s.removeSessionGrant(ctx, tx, p, agent, proj.Slug, g); err != nil {
			return err
		}
	}
	return nil
}

// SetSessionName names the calling agent's chat, or clears its name when the
// name is empty. It is for a chat whose client finds no name to send: one
// the client sends with a later request replaces it.
func (s *Service) SetSessionName(ctx context.Context, in apitypes.PutSessionName) (apitypes.AgentSession, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.AgentSession{}, err
	}
	if err := p.requireWritable(); err != nil {
		return apitypes.AgentSession{}, err
	}
	if !p.IsAgent() {
		return apitypes.AgentSession{}, withHint(fmt.Errorf("%w: only an agent's chat carries a name", domain.ErrForbidden),
			"a person is shown by their own name")
	}
	if p.Session == "" {
		return apitypes.AgentSession{}, withHint(invalidField("session", "this request names no chat"),
			"send "+apitypes.HeaderSession+"; the lorvik CLI does it for you")
	}
	label, name := domain.BaseSession(p.Session), domain.CleanSessionName(in.Name)
	if err := s.store.SetAgentSessionName(ctx, p.Actor.ID, label, name, s.now()); err != nil {
		return apitypes.AgentSession{}, fmt.Errorf("name chat: %w", err)
	}
	return apitypes.AgentSession{Label: label, Name: name}, nil
}
