package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// agentOf loads an agent of the caller's org.
func (s *Service) agentOf(ctx context.Context, st Store, p Principal, id domain.ID) (domain.Agent, error) {
	a, err := st.GetAgent(ctx, id)
	if err != nil || a.OrgID != p.OrgID {
		return domain.Agent{}, fmt.Errorf("agent %s: %w", id, domain.ErrNotFound)
	}
	return a, nil
}

// liveAgent is agentOf without the removed: a removed agent is gone to every
// change, so it is not found.
func (s *Service) liveAgent(ctx context.Context, st Store, p Principal, id domain.ID) (domain.Agent, error) {
	a, err := s.agentOf(ctx, st, p, id)
	if err != nil {
		return domain.Agent{}, err
	}
	return notRemoved(a)
}

func notRemoved(a domain.Agent) (domain.Agent, error) {
	if a.Status == domain.RecordForgotten {
		return domain.Agent{}, fmt.Errorf("agent %s: %w", a.ID, domain.ErrNotFound)
	}
	return a, nil
}

// lockAgent is liveAgent with the agent's row locked to the end of the
// transaction, for a change that reads its status and writes it back or
// acts on it: without the lock a restore that read "revoked" could write
// "active" over a removal committed in between.
func (s *Service) lockAgent(ctx context.Context, tx Store, p Principal, id domain.ID) (domain.Agent, error) {
	a, err := tx.LockAgent(ctx, id)
	if err != nil || a.OrgID != p.OrgID {
		return domain.Agent{}, fmt.Errorf("agent %s: %w", id, domain.ErrNotFound)
	}
	return notRemoved(a)
}

// manageableAgent is liveAgent plus the check that the caller registered the
// agent or may manage every agent.
func (s *Service) manageableAgent(ctx context.Context, st Store, p Principal, id domain.ID) (domain.Agent, error) {
	a, err := s.liveAgent(ctx, st, p, id)
	if err != nil {
		return domain.Agent{}, err
	}
	return a, manageable(p, a)
}

// lockManageableAgent is manageableAgent on a locked row (lockAgent).
func (s *Service) lockManageableAgent(ctx context.Context, tx Store, p Principal, id domain.ID) (domain.Agent, error) {
	a, err := s.lockAgent(ctx, tx, p, id)
	if err != nil {
		return domain.Agent{}, err
	}
	return a, manageable(p, a)
}

func manageable(p Principal, a domain.Agent) error {
	if a.CreatedBy != p.Actor.ID || p.IsAgent() {
		return p.requireOrg(domain.PermAgentManage)
	}
	return nil
}

// agentDetail fills the agent's roles, rights and the chats seen using it.
func (s *Service) agentDetail(ctx context.Context, st Store, p Principal, a domain.Agent) (apitypes.Agent, error) {
	out := toAgent(a)
	m, ok, err := orgMember(ctx, st, a.OrgID, a.Ref())
	if err != nil {
		return apitypes.Agent{}, err
	}
	if ok {
		out.Role, out.Rights = m.Role.String(), m.Rights.Names()
	}
	lk := s.lookup(st, p)
	if out.Projects, err = lk.projectRoles(ctx, a.OrgID, a.Ref(), nil); err != nil {
		return apitypes.Agent{}, err
	}
	if out.Sessions, err = agentSessions(ctx, st, lk, a.ID, s.now()); err != nil {
		return apitypes.Agent{}, err
	}
	if manageable(p, a) == nil {
		return out, nil
	}
	// Whoever cannot manage the agent reads it as any reader of the org: the
	// projects they cannot see are left out, and of a chat's grant they read
	// which rights it has, not what its giver told it.
	visible, _, err := s.visibleProjects(ctx, st, p)
	if err != nil {
		return apitypes.Agent{}, err
	}
	sees := make(map[string]bool, len(visible))
	for _, proj := range visible {
		sees[proj.Slug] = true
	}
	out.Projects = slices.DeleteFunc(out.Projects, func(r apitypes.ProjectRole) bool { return !sees[r.Project] })
	for i := range out.Sessions {
		grants := slices.DeleteFunc(out.Sessions[i].Grants, func(g apitypes.SessionGrant) bool { return !sees[g.Project] })
		for j := range grants {
			grants[j].Instructions = ""
		}
		out.Sessions[i].Grants = grants
	}
	return out, nil
}

// agentSessionLimit is how many chats an agent's detail lists.
const agentSessionLimit = 20

// agentSessions lists the chats seen using the agent, newest first, each
// with the rights its owner gave it: the most recent agentSessionLimit, and
// every chat that holds a grant however old, so a grant is never hidden. A
// granted chat never seen comes last, without a last-seen time. A grant past
// its end at now is no longer one.
func agentSessions(ctx context.Context, st Store, lk *lookup, agentID domain.ID, now time.Time) ([]apitypes.AgentSession, error) {
	seen, err := st.ListAgentSessions(ctx, agentID, agentSessionLimit)
	if err != nil {
		return nil, fmt.Errorf("list agent sessions: %w", err)
	}
	grants, err := st.ListAgentSessionGrants(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("list agent session grants: %w", err)
	}
	grants = slices.DeleteFunc(grants, func(g domain.AgentSessionGrant) bool { return !g.Live(now) })
	listed := map[string]bool{}
	for _, sess := range seen {
		listed[sess.Session] = true
	}
	var extra []string
	for _, g := range grants {
		if !listed[g.Session] {
			listed[g.Session] = true
			extra = append(extra, g.Session)
		}
	}
	if len(extra) > 0 {
		older, err := st.ListAgentSessionsByLabel(ctx, agentID, extra)
		if err != nil {
			return nil, fmt.Errorf("list granted agent sessions: %w", err)
		}
		seen = append(seen, older...)
		for _, sess := range older {
			extra = slices.DeleteFunc(extra, func(l string) bool { return l == sess.Session })
		}
	}
	slices.SortStableFunc(seen, func(a, b domain.AgentSession) int {
		return cmp.Or(b.LastSeenAt.Compare(a.LastSeenAt), cmp.Compare(a.Session, b.Session))
	})
	out := make([]apitypes.AgentSession, 0, len(seen)+len(extra))
	for _, sess := range seen {
		at := sess.LastSeenAt
		out = append(out, apitypes.AgentSession{Label: sess.Session, Name: sess.Name, LastSeenAt: &at})
	}
	slices.Sort(extra)
	for _, label := range extra {
		out = append(out, apitypes.AgentSession{Label: label})
	}
	for i := range out {
		for _, g := range grants {
			if g.Session != out[i].Label {
				continue
			}
			slug, err := lk.projectSlug(ctx, g.ProjectID)
			if err != nil {
				return nil, err
			}
			out[i].Grants = append(out[i].Grants, toSessionGrant(g, slug))
		}
	}
	return out, nil
}

func toSessionGrant(g domain.AgentSessionGrant, slug string) apitypes.SessionGrant {
	return apitypes.SessionGrant{Project: slug, Rights: g.Rights.Names(), EndsAt: g.EndsAt, Instructions: g.Instructions}
}

// effectiveRights gives projectRoles the rights an agent caller holds in
// each of its projects for this very request, its chat's grant included;
// nil for a person, whose rows carry none.
func (s *Service) effectiveRights(ctx context.Context, st Store, p Principal) func(domain.ProjectMember) (domain.Rights, error) {
	if !p.IsAgent() {
		return nil
	}
	return func(pm domain.ProjectMember) (domain.Rights, error) {
		a, err := s.accessByID(ctx, st, p, pm.ProjectID)
		if errors.Is(err, domain.ErrNotFound) {
			return 0, nil
		}
		return a.rights, err
	}
}

// parseAgentRights reads the rights asked for an agent: work when none are
// named.
func parseAgentRights(names []string) (domain.Rights, error) {
	if len(names) == 0 {
		return domain.RightWork, nil
	}
	return domain.ParseRights(names)
}

// capRights refuses rights beyond what the granter holds, naming the first
// one in the 422 on rights. A granter hands on only what their own role has.
func capRights(want, held domain.Rights, role domain.Role, where string) error {
	if over := want &^ held; over != 0 {
		return invalidField("rights", fmt.Sprintf("you are %s in %s and cannot give the %s right there", role, where, strings.Join(over.Names(), ", ")))
	}
	return nil
}

// agentGrantRights is the rights an agent's membership body asks for. Its
// role must be empty or viewer, since an agent's rights carry the rest. Work
// is the default only when the body names neither a role nor rights: viewer
// alone asks for no rights, which a grant does not accept.
func agentGrantRights(in apitypes.PutMembership) (domain.Rights, error) {
	if in.Role != "" && in.Role != domain.RoleViewer.String() {
		return 0, invalidField("role", "an agent's role is viewer; give it rights instead")
	}
	if in.Role != "" && len(in.Rights) == 0 {
		return 0, invalidField("rights", "name the agent's rights; viewer alone gives none")
	}
	return parseAgentRights(in.Rights)
}

// rightsWords says what rights allow, for messages.
func rightsWords(r domain.Rights) string {
	if r == 0 {
		return "no rights beyond reading"
	}
	return strings.Join(r.Names(), ", ")
}

const agentList = "agents"

// ListAgents lists a page of the org's agents; revoked ones only with all.
func (s *Service) ListAgents(ctx context.Context, all bool, token string, limit int) (apitypes.AgentList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.AgentList{}, err
	}
	if err := p.requireOrg(domain.PermRead); err != nil {
		return apitypes.AgentList{}, err
	}
	cur, err := decodeCursor(agentList, token)
	if err != nil {
		return apitypes.AgentList{}, err
	}
	f := query.AgentFilter{All: all, Page: query.Page{Limit: limit}}
	if cur != nil {
		f.Cursor = &query.AgentCursor{Status: domain.RecordStatus(cur.Status), CreatedAt: time.Unix(0, cur.CreatedAt).UTC(), ID: domain.ID(cur.ID)}
	}
	agents, remaining, err := s.store.ListAgentsPage(ctx, p.OrgID, f)
	if err != nil {
		return apitypes.AgentList{}, fmt.Errorf("list agents: %w", err)
	}
	out := make([]apitypes.Agent, 0, len(agents))
	for _, a := range agents {
		out = append(out, toAgent(a))
	}
	return apitypes.AgentList{Items: out, Page: forwardPage(agentList, agents, func(a domain.Agent) cursor {
		return cursor{Status: int16(a.Status), CreatedAt: a.CreatedAt.UnixNano(), ID: int64(a.ID)}
	}, remaining)}, nil
}

// GetAgent returns one agent with its roles.
func (s *Service) GetAgent(ctx context.Context, id domain.ID) (apitypes.Agent, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Agent{}, err
	}
	a, err := s.agentOf(ctx, s.store, p, id)
	if err != nil {
		return apitypes.Agent{}, err
	}
	return s.agentDetail(ctx, s.store, p, a)
}

// CreateAgent registers an agent, binds it to projects or the org, and mints
// its token. The secret is returned once and never stored.
func (s *Service) CreateAgent(ctx context.Context, in apitypes.CreateAgent) (apitypes.AgentCreated, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.AgentCreated{}, err
	}
	if err := p.requireOrg(domain.PermAgentRegister); err != nil {
		return apitypes.AgentCreated{}, err
	}
	rights, err := parseAgentRights(in.Rights)
	if err != nil {
		return apitypes.AgentCreated{}, err
	}
	harness := domain.HarnessOther
	if in.Harness != "" {
		if harness, err = domain.ParseHarness(in.Harness); err != nil {
			return apitypes.AgentCreated{}, invalidField("harness", err.Error())
		}
	}
	if in.OrgWide {
		if err := capRights(rights, domain.RightsHeldBy(p.OrgRole), p.OrgRole, "the org"); err != nil {
			return apitypes.AgentCreated{}, err
		}
	}
	if in.Auto && (len(in.Projects) > 0 || in.OrgWide) {
		return apitypes.AgentCreated{}, invalidField("auto", "an automatic registration takes no projects or org role; the agent asks to join each project")
	}
	org, err := s.store.GetOrg(ctx, p.OrgID)
	if err != nil {
		return apitypes.AgentCreated{}, fmt.Errorf("load org: %w", err)
	}
	ttl, err := agentTokenTTL(in.TokenExpiresIn, "token_expires_in")
	if err != nil {
		return apitypes.AgentCreated{}, err
	}
	// An admin registering by hand approves the agent by doing so; the CLI
	// registering by itself approves nothing.
	status := domain.RecordActive
	if org.Settings.AgentsNeedApproval() && (in.Auto || !p.canOrg(domain.PermAgentManage)) {
		status = domain.RecordPending
	}
	now := s.now()
	var out apitypes.AgentCreated
	err = s.store.WithTx(ctx, func(tx Store) error {
		existing, err := tx.ListAgents(ctx, p.OrgID, in.Auto)
		if err != nil {
			return fmt.Errorf("list agents: %w", err)
		}
		for _, a := range existing {
			switch {
			case a.Name != in.Name, a.Status == domain.RecordForgotten:
			case a.Status != domain.RecordRevoked:
				return fmt.Errorf("agent %q: %w", in.Name, errExists)
			case a.CreatedBy == p.Actor.ID:
				return withHint(fmt.Errorf("%w: %s", errAgentRevoked, in.Name), revokedHint(a.ID))
			}
		}
		agent := domain.Agent{ID: s.ids.New(), OrgID: p.OrgID, Name: in.Name, Harness: harness, Machine: in.Machine,
			CreatedBy: p.Actor.ID, Status: status, CreatedAt: now, UpdatedAt: now}
		if err := agent.Validate(); err != nil {
			return err
		}
		agent, err = tx.CreateAgent(ctx, agent)
		if ve, ok := errors.AsType[*domain.ValidationError](err); ok && ve.Field == "agents_name_key" {
			// A registration of the same name committed after the check above.
			return fmt.Errorf("agent %q: %w", in.Name, errExists)
		}
		if err != nil {
			return fmt.Errorf("create agent: %w", err)
		}
		if in.OrgWide {
			if _, err := putMembership(ctx, tx, domain.Membership{OrgID: p.OrgID, Actor: agent.Ref(), Role: domain.RoleViewer, Rights: rights, Status: domain.RecordActive,
				GrantedBy: &p.Actor.ID, CreatedAt: now, UpdatedAt: now}); err != nil {
				return fmt.Errorf("grant org role: %w", err)
			}
		}
		// The event goes to every reader of the org, so it names only the
		// projects every member can see.
		var named []string
		for _, slug := range in.Projects {
			proj, err := s.bindAgent(ctx, tx, p, agent, slug, rights, now)
			if err != nil {
				return err
			}
			if proj.Visibility != domain.VisibilityPrivate {
				named = append(named, slug)
			}
		}
		tok, secret, err := s.mintToken(ctx, tx, p.OrgID, agent.Ref(), agent.Name, domain.ScopeWrite, ttl, now)
		if err != nil {
			return err
		}
		detail, err := s.agentDetail(ctx, tx, p, agent)
		if err != nil {
			return err
		}
		out = apitypes.AgentCreated{Agent: detail, Token: secret, TokenExpiresAt: tok.ExpiresAt}
		return s.emitP(ctx, tx, p, nil, domain.EventAgentRegistered, agentSubject(agent.ID),
			payload(agent.Name, "harness", harness.String(), "status", agentStatus(status), "rights", rights.Names(), "projects", named, "org_wide", in.OrgWide, "auto", in.Auto))
	})
	if err != nil {
		return apitypes.AgentCreated{}, err
	}
	return out, nil
}

// bindAgent gives an agent rights in one project, which it returns; the
// caller must hold every one of them there.
func (s *Service) bindAgent(ctx context.Context, tx Store, p Principal, agent domain.Agent, slug string, rights domain.Rights, now time.Time) (domain.Project, error) {
	proj, a, err := s.project(ctx, tx, p, slug)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Project{}, invalidField("projects", fmt.Sprintf("no project %q", slug))
	}
	if err != nil {
		return domain.Project{}, err
	}
	if err := capRights(rights, domain.RightsHeldBy(a.role), a.role, slug); err != nil {
		return domain.Project{}, err
	}
	if _, err := putProjectMember(ctx, tx, domain.ProjectMember{ProjectID: proj.ID, Actor: agent.Ref(), Role: domain.RoleViewer, Rights: rights, Status: domain.RecordActive,
		GrantedBy: &p.Actor.ID, CreatedAt: now, UpdatedAt: now}); err != nil {
		return domain.Project{}, fmt.Errorf("bind agent to %s: %w", slug, err)
	}
	return proj, nil
}

// BindAgent gives an existing agent rights in a project.
func (s *Service) BindAgent(ctx context.Context, id domain.ID, slug string, in apitypes.PutMembership) (apitypes.Agent, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.Agent{}, err
	}
	if err := p.requireWritable(); err != nil {
		return apitypes.Agent{}, err
	}
	rights, err := agentGrantRights(in)
	if err != nil {
		return apitypes.Agent{}, err
	}
	var out apitypes.Agent
	err = s.store.WithTx(ctx, func(tx Store) error {
		agent, err := s.lockManageableAgent(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if _, err := s.bindAgent(ctx, tx, p, agent, slug, rights, s.now()); err != nil {
			return err
		}
		out, err = s.agentDetail(ctx, tx, p, agent)
		return err
	})
	return out, err
}

// UnbindAgent removes an agent from a project.
func (s *Service) UnbindAgent(ctx context.Context, id domain.ID, slug string) error {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return err
	}
	if err := p.requireWritable(); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx Store) error {
		agent, err := s.lockManageableAgent(ctx, tx, p, id)
		if err != nil {
			return err
		}
		proj, _, err := s.project(ctx, tx, p, slug)
		if err != nil {
			return err
		}
		pm, err := tx.GetProjectMember(ctx, proj.ID, agent.Ref())
		if err != nil {
			return fmt.Errorf("agent %s in %s: %w", agent.Name, slug, err)
		}
		pm.Status, pm.UpdatedAt = domain.RecordRemoved, s.now()
		if _, err := putProjectMember(ctx, tx, pm); err != nil {
			return err
		}
		if err := s.endSessionGrants(ctx, tx, p, agent, &proj.ID); err != nil {
			return err
		}
		return s.dropAssignments(ctx, tx, p, agent.Ref())
	})
}

// UpdateAgent changes an agent's own say over joining projects. Only a user
// changes it, the owner or an agent manager, so an agent never widens itself.
func (s *Service) UpdateAgent(ctx context.Context, id domain.ID, in apitypes.UpdateAgent) (apitypes.Agent, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.Agent{}, err
	}
	if err := p.requireWritable(); err != nil {
		return apitypes.Agent{}, err
	}
	var out apitypes.Agent
	err = s.store.WithTx(ctx, func(tx Store) error {
		agent, err := s.lockManageableAgent(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if in.AgentJoin != nil {
			if agent.AgentJoin, err = domain.ParseAgentJoin(*in.AgentJoin); err != nil {
				return err
			}
		}
		agent.UpdatedAt = s.now()
		if err := agent.Validate(); err != nil {
			return err
		}
		if agent, err = tx.UpdateAgent(ctx, agent); err != nil {
			return fmt.Errorf("update agent: %w", err)
		}
		if err := s.emitP(ctx, tx, p, nil, domain.EventAgentUpdated, agentSubject(agent.ID),
			payload(agent.Name, "agent_join", agent.AgentJoin.String())); err != nil {
			return err
		}
		out, err = s.agentDetail(ctx, tx, p, agent)
		return err
	})
	return out, err
}

// ApproveAgent activates a pending agent (admins).
func (s *Service) ApproveAgent(ctx context.Context, id domain.ID) (apitypes.Agent, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.Agent{}, err
	}
	if err := p.requireOrg(domain.PermAgentManage); err != nil {
		return apitypes.Agent{}, err
	}
	var out apitypes.Agent
	err = s.store.WithTx(ctx, func(tx Store) error {
		agent, err := s.lockAgent(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if agent.Status != domain.RecordPending {
			return fmt.Errorf("%w: agent is %s", domain.ErrInvalidTransition, agentStatus(agent.Status))
		}
		agent.Status, agent.UpdatedAt = domain.RecordActive, s.now()
		if agent, err = tx.UpdateAgent(ctx, agent); err != nil {
			return fmt.Errorf("approve agent: %w", err)
		}
		if out, err = s.agentDetail(ctx, tx, p, agent); err != nil {
			return err
		}
		return s.emitP(ctx, tx, p, nil, domain.EventAgentApproved, agentSubject(agent.ID), payload(agent.Name))
	})
	return out, err
}

// revokedHint tells a person how to get past a revoked agent. It names both
// ways out and leaves the choice to the owner: the caller may be the agent's
// own chat.
func revokedHint(id domain.ID) string {
	return fmt.Sprintf("its owner revoked this agent, and a revoked agent is never registered again by itself; the owner restores it on the dashboard's Access page or with `lorvik agent restore %s`, or removes it there so the next chat registers a new one", id)
}

// RevokeAgent stops an agent: its tokens, its assignments, its chats' grants
// and its waiting join requests go. Its project access stays on its rows, so
// RestoreAgent brings it back as it was.
func (s *Service) RevokeAgent(ctx context.Context, id domain.ID) error {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return err
	}
	if err := p.requireWritable(); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx Store) error {
		agent, err := s.lockManageableAgent(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if agent.Status == domain.RecordRevoked {
			return nil
		}
		if _, err := s.stopAgent(ctx, tx, p, agent, domain.RecordRevoked); err != nil {
			return err
		}
		return s.emitP(ctx, tx, p, nil, domain.EventAgentRevoked, agentSubject(agent.ID), payload(agent.Name))
	})
}

// stopAgent moves an agent to a gone status and takes away everything it
// could act through. A revoked agent holds none of that any more, so only
// its status moves.
func (s *Service) stopAgent(ctx context.Context, tx Store, p Principal, agent domain.Agent, to domain.RecordStatus) (domain.Agent, error) {
	was := agent.Status
	agent.Status, agent.UpdatedAt = to, s.now()
	agent, err := tx.UpdateAgent(ctx, agent)
	if err != nil {
		return domain.Agent{}, fmt.Errorf("stop agent: %w", err)
	}
	if was == domain.RecordRevoked {
		return agent, nil
	}
	if err := tx.RevokeTokensForActor(ctx, p.OrgID, agent.Ref()); err != nil {
		return domain.Agent{}, fmt.Errorf("revoke agent tokens: %w", err)
	}
	if err := s.dropAssignments(ctx, tx, p, agent.Ref()); err != nil {
		return domain.Agent{}, err
	}
	if err := s.endSessionGrants(ctx, tx, p, agent, nil); err != nil {
		return domain.Agent{}, err
	}
	// Its waiting requests to join projects go too: a stopped agent can never be let in.
	reqs, err := tx.ListAgentRequests(ctx, p.OrgID, agent.CreatedBy)
	if err != nil {
		return domain.Agent{}, fmt.Errorf("list agent requests: %w", err)
	}
	for _, pm := range reqs {
		if pm.Actor != agent.Ref() {
			continue
		}
		pm.Status, pm.UpdatedAt = domain.RecordRemoved, agent.UpdatedAt
		if _, err := tx.PutProjectMember(ctx, pm); err != nil {
			return domain.Agent{}, fmt.Errorf("drop request of %s: %w", agent.Name, err)
		}
	}
	return agent, nil
}

// RestoreAgent brings a revoked agent back with the project access it had.
// Its old tokens stay revoked, and so do its chats' grants and its
// assignments; a chat gets a new token from its owner's login. Whoever may
// register an agent and manage this one restores it, and where the org wants
// agents approved it waits for an agent manager like a new one.
func (s *Service) RestoreAgent(ctx context.Context, id domain.ID) (apitypes.Agent, error) {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return apitypes.Agent{}, err
	}
	if err := p.requireOrg(domain.PermAgentRegister); err != nil {
		return apitypes.Agent{}, err
	}
	org, err := s.store.GetOrg(ctx, p.OrgID)
	if err != nil {
		return apitypes.Agent{}, fmt.Errorf("load org: %w", err)
	}
	var out apitypes.Agent
	err = s.store.WithTx(ctx, func(tx Store) error {
		agent, err := s.lockManageableAgent(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if agent.Status != domain.RecordRevoked {
			return withHint(fmt.Errorf("%w: agent is %s", domain.ErrInvalidTransition, agentStatus(agent.Status)),
				"only a revoked agent is restored; list them with `lorvik agent ls --all`")
		}
		taken := withHint(fmt.Errorf("agent %q: %w", agent.Name, errExists), "another agent took this name since; revoke or remove that one first")
		alive, err := tx.ListAgents(ctx, p.OrgID, false)
		if err != nil {
			return fmt.Errorf("list agents: %w", err)
		}
		if slices.ContainsFunc(alive, func(a domain.Agent) bool { return a.Name == agent.Name }) {
			return taken
		}
		agent.Status, agent.UpdatedAt = domain.RecordActive, s.now()
		if org.Settings.AgentsNeedApproval() && !p.canOrg(domain.PermAgentManage) {
			agent.Status = domain.RecordPending
		}
		agent, err = tx.UpdateAgent(ctx, agent)
		if ve, ok := errors.AsType[*domain.ValidationError](err); ok && ve.Field == "agents_name_key" {
			// A registration of the same name committed after the check above.
			return taken
		}
		if err != nil {
			return fmt.Errorf("restore agent: %w", err)
		}
		if out, err = s.agentDetail(ctx, tx, p, agent); err != nil {
			return err
		}
		return s.emitP(ctx, tx, p, nil, domain.EventAgentRestored, agentSubject(agent.ID), payload(agent.Name, "status", agentStatus(agent.Status)))
	})
	return out, err
}

// RemoveAgent stops an agent for good and forgets it: it leaves every list
// and loses its project and org access, and its name is free, so the next
// chat of its harness registers a new agent. The row stays, so its past work
// keeps its name. It cannot be undone.
func (s *Service) RemoveAgent(ctx context.Context, id domain.ID) error {
	p, err := s.userPrincipal(ctx)
	if err != nil {
		return err
	}
	if err := p.requireWritable(); err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx Store) error {
		agent, err := s.lockManageableAgent(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if agent, err = s.stopAgent(ctx, tx, p, agent, domain.RecordForgotten); err != nil {
			return err
		}
		pms, err := tx.ListProjectMembersForActor(ctx, p.OrgID, agent.Ref())
		if err != nil {
			return fmt.Errorf("list project access of %s: %w", agent.Name, err)
		}
		for _, pm := range pms {
			pm.Status, pm.UpdatedAt = domain.RecordRemoved, agent.UpdatedAt
			if _, err := putProjectMember(ctx, tx, pm); err != nil {
				return fmt.Errorf("remove project access of %s: %w", agent.Name, err)
			}
		}
		m, ok, err := orgMember(ctx, tx, p.OrgID, agent.Ref())
		if err != nil {
			return err
		}
		if ok {
			m.Status, m.UpdatedAt = domain.RecordRemoved, agent.UpdatedAt
			if _, err := putMembership(ctx, tx, m); err != nil {
				return fmt.Errorf("remove org access of %s: %w", agent.Name, err)
			}
		}
		return s.emitP(ctx, tx, p, nil, domain.EventAgentRemoved, agentSubject(agent.ID), payload(agent.Name))
	})
}
