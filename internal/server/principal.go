package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// Principal is the authenticated caller, attached to the request context by
// the auth middleware. OrgRole is the org-level role when the actor has an
// org membership; agents bound only to projects have none.
type Principal struct {
	Actor      domain.ActorRef
	OrgID      domain.ID
	OrgRole    domain.Role
	HasOrgRole bool
	// Owner is the user an agent acts for, zero for a human, and
	// OwnerOrgRole that user's org role. An agent never holds more than its
	// owner does now: its org role and OrgRights are capped here, its
	// project rights by access.
	Owner        domain.ID
	OwnerOrgRole domain.Role
	// OrgRights are an agent's org-wide rights, already capped by its
	// owner's org role; AgentName names it in refusals.
	OrgRights   domain.Rights
	AgentName   string
	TokenID     domain.ID
	TokenPrefix string
	Scope       domain.TokenScope
	ReadOnly    bool
	// Session is the validated X-Lorvik-Session label; empty when none sent.
	Session string
}

type principalKey struct{}

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the caller stored by the auth middleware.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// IsAgent reports whether the caller is a registered agent.
func (p Principal) IsAgent() bool { return p.Actor.Kind == domain.ActorAgent }

// canOrg reports whether the caller's org standing grants perm: a person's
// role, an agent's org-wide rights.
func (p Principal) canOrg(perm domain.Permission) bool {
	switch {
	case !p.HasOrgRole:
		return false
	case p.IsAgent():
		return perm == domain.PermRead || p.OrgRights.Grants(perm)
	}
	return p.OrgRole.Can(perm)
}

// seesPendingMembers reports whether the caller may see who is invited or
// asking to join: those who manage the org's members.
func (p Principal) seesPendingMembers() bool { return p.canOrg(domain.PermMemberManage) }

// limitedReach is the caller's reach into audience-limited events: a person
// who manages the org's members gets all of them, any other person those
// about their own account, an agent none, whoever owns it.
func (p Principal) limitedReach() query.LimitedReach {
	switch {
	case p.IsAgent():
		return query.LimitedReach{}
	case p.canOrg(domain.PermMemberManage):
		return query.LimitedReach{All: true}
	}
	return query.LimitedReach{User: p.Actor.ID}
}

// requireOrg checks an org-level permission.
func (p Principal) requireOrg(perm domain.Permission) error {
	if perm.Mutating() {
		if err := p.requireWritable(); err != nil {
			return err
		}
	}
	if !p.canOrg(perm) {
		return p.orgRefusal(perm)
	}
	return nil
}

// orgRefusal is the 403 for a missing org-level permission: an agent hears
// the right it lacks, a person the role.
func (p Principal) orgRefusal(perm domain.Permission) error {
	if right := domain.RightFor(perm); p.IsAgent() && right != 0 {
		return p.lacksRight(right, "this org")
	}
	return forbidden(perm, p.OrgRole, p.HasOrgRole)
}

// lacksRight is the 403 for an agent missing a right, naming the right and
// the chat the owner can also give it to; instead adds another way forward.
func (p Principal) lacksRight(right domain.Rights, where string, instead ...string) error {
	var hint strings.Builder
	hint.WriteString("its owner can give it on the Access page")
	if p.Session != "" {
		hint.WriteString(", to the agent or to this chat (" + domain.BaseSession(p.Session) + ")")
	}
	for _, alt := range instead {
		hint.WriteString("; or " + alt)
	}
	return withHint(fmt.Errorf("%w: agent %s lacks the %s right in %s", domain.ErrForbidden, p.AgentName, strings.Join(right.Names(), ", "), where), hint.String())
}

// userGate refuses a user who may not act: suspended is 403 (an admin can
// reinstate them), deleted is 401 (the credential will never work again).
// Every door a human comes through (bearer, password, GitHub sign-in and
// link) answers the same way.
func userGate(u domain.User) *problemError {
	switch {
	case u.Status.Alive():
		return nil
	case u.Status == domain.RecordSuspended:
		return newProblem(http.StatusForbidden, "user is suspended", "ask an org admin to reinstate this user")
	default:
		return newProblem(http.StatusUnauthorized, "user is deleted", "this user was deleted; sign in with another account")
	}
}

// requireWritable refuses a read-only token. It runs before any lookup, so a
// read-only caller learns that first rather than a 404 or 403 that depends on
// what it tried to change.
func (p Principal) requireWritable() error {
	if p.ReadOnly {
		return readOnlyErr()
	}
	return nil
}

func readOnlyErr() error {
	return withHint(fmt.Errorf("%w: this token is read-only", domain.ErrForbidden),
		"create a write token with `lorvik token create` or ask an admin for one")
}

func forbidden(perm domain.Permission, role domain.Role, has bool) error {
	if !has {
		return withHint(fmt.Errorf("%w: no role here", domain.ErrForbidden),
			"ask an admin to add you to the org or project")
	}
	return withHint(fmt.Errorf("%w: %s cannot %s", domain.ErrForbidden, role, perm),
		"ask an admin for a higher role")
}

// access is the caller's effective standing in one project, resolved from
// the org membership and the project membership. A person acts with a role;
// an agent reads, and does what its rights allow. Private projects are
// invisible without a project role, so ErrNotFound hides their existence.
type access struct {
	role   domain.Role
	rights domain.Rights
	agent  bool
	slug   string
}

// held is what one actor holds in a project: the role it acts with and, for
// an agent, the rights its rows give it.
type held struct {
	role   domain.Role
	rights domain.Rights
}

// underOwner caps an agent's holding by its owner's in the same project: the
// lower role, and only the rights the owner's role has, plus what a grant to
// the agent's chat adds.
func (h held) underOwner(owner held, grant domain.Rights) held {
	return held{role: min(h.role, owner.role), rights: (h.rights | grant) & domain.RightsHeldBy(owner.role)}
}

// rightNames are an agent caller's rights here, for the wire; nil for a
// person, whose role says it all.
func (a access) rightNames() []string {
	if !a.agent {
		return nil
	}
	return a.rights.Names()
}

// can reports whether the caller holds perm here. This is the one place a
// role or rights are compared for authorisation in a project.
func (a access) can(perm domain.Permission) bool {
	if a.agent {
		return perm == domain.PermRead || a.rights.Grants(perm)
	}
	return a.role.Can(perm)
}

// An agent's standing is the lower of its own and its owner's, and a project
// its owner cannot see does not exist for it.
func (s *Service) access(ctx context.Context, st Store, p Principal, proj domain.Project) (access, error) {
	h, ok, err := roleIn(ctx, st, proj, p.Actor, p.OrgRole, p.HasOrgRole, p.OrgRights)
	if err == nil && ok && p.IsAgent() {
		var owner held
		owner, ok, err = roleIn(ctx, st, proj, domain.ActorRef{Kind: domain.ActorUser, ID: p.Owner}, p.OwnerOrgRole, true, 0)
		if err == nil && ok {
			var grant domain.AgentSessionGrant
			if grant, _, err = sessionGrant(ctx, st, proj.ID, p, s.now()); err == nil {
				h = h.underOwner(owner, grant.Rights)
			}
		}
	}
	if err != nil {
		return access{}, err
	}
	if !ok {
		return access{}, fmt.Errorf("project %q: %w", proj.Slug, domain.ErrNotFound)
	}
	return access{role: h.role, rights: h.rights, agent: p.IsAgent(), slug: proj.Slug}, nil
}

// sessionGrant is what the agent's owner gave the caller's chat in the
// project; ok is false, and the grant empty, for a person, a request without
// a session, or a chat with no grant that is live at now.
func sessionGrant(ctx context.Context, st Store, projectID domain.ID, p Principal, now time.Time) (g domain.AgentSessionGrant, ok bool, err error) {
	if !p.IsAgent() || p.Session == "" {
		return domain.AgentSessionGrant{}, false, nil
	}
	g, err = st.GetAgentSessionGrant(ctx, projectID, p.Actor.ID, domain.BaseSession(p.Session))
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return domain.AgentSessionGrant{}, false, nil
	case err != nil:
		return domain.AgentSessionGrant{}, false, fmt.Errorf("load session grant: %w", err)
	case !g.Live(now):
		return domain.AgentSessionGrant{}, false, nil
	}
	return g, true, nil
}

// roleIn is one actor's standing in a project from their org role and
// project membership; false when the project is not visible to them. An
// agent's rights are those of its project row, plus its org-wide rights where
// the project lets the org role apply.
func roleIn(ctx context.Context, st Store, proj domain.Project, actor domain.ActorRef, orgRole domain.Role, hasOrgRole bool, orgRights domain.Rights) (held, bool, error) {
	pm, err := st.GetProjectMember(ctx, proj.ID, actor)
	hasProject := err == nil && pm.Status.Alive()
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return held{}, false, fmt.Errorf("load project role: %w", err)
	}
	// Only the global project asks whether the actor belongs to any project,
	// and only an actor with no org role needs that answer.
	hasAny := false
	if proj.Scope == domain.ProjectScopeGlobal && !hasOrgRole {
		if hasAny, err = st.HasProjectMembership(ctx, proj.OrgID, actor); err != nil {
			return held{}, false, fmt.Errorf("load project membership: %w", err)
		}
	}
	standing := domain.Standing{OrgRole: orgRole, HasOrgRole: hasOrgRole, ProjectRole: pm.Role, HasProjectRole: hasProject, InAnyProject: hasAny}
	role, ok := proj.EffectiveRole(standing)
	h := held{role: role}
	if hasProject {
		h.rights = pm.Rights
	}
	if proj.UsesOrgRole(standing) {
		h.rights |= orgRights
	}
	return h, ok, nil
}

// mayOverride reports whether the caller may act on a card another actor
// holds: release their claim, take it over, or move the card under them.
// Humans with triage may; agents never may, whatever their rights.
func (a access) mayOverride(p Principal) bool {
	return !p.IsAgent() && a.can(domain.PermCardTriage)
}

// require checks a project-level permission.
func (a access) require(p Principal, perm domain.Permission) error {
	if perm.Mutating() {
		if err := p.requireWritable(); err != nil {
			return err
		}
	}
	if !a.can(perm) {
		return a.refusal(p, perm)
	}
	return nil
}

// refusal is the 403 for a missing project permission: an agent hears the
// right it lacks and the chat, a person the role.
func (a access) refusal(p Principal, perm domain.Permission) error {
	if right := domain.RightFor(perm); a.agent && right != 0 {
		return p.lacksRight(right, a.slug)
	}
	return forbidden(perm, a.role, true)
}

// project resolves a slug and checks the caller may see it.
func (s *Service) project(ctx context.Context, st Store, p Principal, slug string) (domain.Project, access, error) {
	proj, err := st.GetProjectBySlug(ctx, p.OrgID, slug)
	if err != nil {
		return domain.Project{}, access{}, fmt.Errorf("project %q: %w", slug, err)
	}
	a, err := s.access(ctx, st, p, proj)
	if err != nil {
		return domain.Project{}, access{}, err
	}
	return proj, a, nil
}

// accessByID is the caller's access to a project whose id is already in
// hand (a note's or a membership's project).
func (s *Service) accessByID(ctx context.Context, st Store, p Principal, id domain.ID) (access, error) {
	proj, err := st.GetProject(ctx, id)
	if err != nil {
		return access{}, fmt.Errorf("project %s: %w", id, err)
	}
	if proj.OrgID != p.OrgID {
		return access{}, fmt.Errorf("project %s: %w", id, domain.ErrNotFound)
	}
	return s.access(ctx, st, p, proj)
}

// visibleProjects lists the projects the caller may see, with the caller's
// standing in each; for an agent, the projects its owner sees too, at the
// lower of the two roles and within the owner's rights.
func (s *Service) visibleProjects(ctx context.Context, st Store, p Principal) ([]domain.Project, map[domain.ID]access, error) {
	all, err := st.ListProjects(ctx, p.OrgID)
	if err != nil {
		return nil, nil, fmt.Errorf("list projects: %w", err)
	}
	mine, err := projectStandings(ctx, st, p.OrgID, p.Actor, p.OrgRole, p.HasOrgRole, p.OrgRights)
	if err != nil {
		return nil, nil, err
	}
	var owners func(domain.Project) (held, bool)
	var grants map[domain.ID]domain.Rights
	if p.IsAgent() {
		if owners, err = projectStandings(ctx, st, p.OrgID, domain.ActorRef{Kind: domain.ActorUser, ID: p.Owner}, p.OwnerOrgRole, true, 0); err != nil {
			return nil, nil, err
		}
		if grants, err = sessionGrants(ctx, st, p, s.now()); err != nil {
			return nil, nil, err
		}
	}
	accesses := make(map[domain.ID]access, len(all))
	out := all[:0]
	for _, proj := range all {
		h, ok := mine(proj)
		if ok && owners != nil {
			var owner held
			owner, ok = owners(proj)
			h = h.underOwner(owner, grants[proj.ID])
		}
		if !ok {
			continue
		}
		accesses[proj.ID] = access{role: h.role, rights: h.rights, agent: p.IsAgent(), slug: proj.Slug}
		out = append(out, proj)
	}
	return out, accesses, nil
}

// sessionGrants is the rights of sessionGrant for every project at once.
func sessionGrants(ctx context.Context, st Store, p Principal, now time.Time) (map[domain.ID]domain.Rights, error) {
	if p.Session == "" {
		return nil, nil
	}
	gs, err := st.ListAgentSessionGrants(ctx, p.Actor.ID)
	if err != nil {
		return nil, fmt.Errorf("list session grants: %w", err)
	}
	base := domain.BaseSession(p.Session)
	out := map[domain.ID]domain.Rights{}
	for _, g := range gs {
		if g.Session == base && g.Live(now) {
			out[g.ProjectID] = g.Rights
		}
	}
	return out, nil
}

// projectStandings loads one actor's project rows in the org once and
// answers their standing in any of its projects.
func projectStandings(ctx context.Context, st Store, orgID domain.ID, actor domain.ActorRef, orgRole domain.Role, hasOrgRole bool, orgRights domain.Rights) (func(domain.Project) (held, bool), error) {
	pms, err := st.ListProjectMembersForActor(ctx, orgID, actor)
	if err != nil {
		return nil, fmt.Errorf("list project roles: %w", err)
	}
	rows := make(map[domain.ID]domain.ProjectMember, len(pms))
	for _, pm := range pms {
		rows[pm.ProjectID] = pm
	}
	return func(proj domain.Project) (held, bool) {
		pm, has := rows[proj.ID]
		standing := domain.Standing{OrgRole: orgRole, HasOrgRole: hasOrgRole, ProjectRole: pm.Role, HasProjectRole: has, InAnyProject: len(pms) > 0}
		role, ok := proj.EffectiveRole(standing)
		h := held{role: role}
		if has {
			h.rights = pm.Rights
		}
		if proj.UsesOrgRole(standing) {
			h.rights |= orgRights
		}
		return h, ok
	}, nil
}

// ownerGap says why an agent's owner cannot lend it any rights.
type ownerGap int8

const (
	ownerActive    ownerGap = iota
	ownerSuspended          // an admin can reinstate them
	ownerGone               // deleted, or no such user
	ownerNotMember          // no live membership in the agent's org
)

// agentOwner loads an agent's owner and their org role. An agent acts only
// while its owner is an active user with a live membership in its org.
func agentOwner(ctx context.Context, st Store, ag domain.Agent) (domain.ID, domain.Role, ownerGap, error) {
	u, err := st.GetUser(ctx, ag.CreatedBy)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return 0, 0, ownerGone, nil
	case err != nil:
		return 0, 0, 0, fmt.Errorf("load agent owner: %w", err)
	case u.Status == domain.RecordSuspended:
		return 0, 0, ownerSuspended, nil
	case !u.Status.Alive():
		return 0, 0, ownerGone, nil
	}
	role, ok, err := orgRole(ctx, st, ag.OrgID, u.Ref())
	switch {
	case err != nil:
		return 0, 0, 0, err
	case !ok:
		return 0, 0, ownerNotMember, nil
	}
	return u.ID, role, ownerActive, nil
}

// orgRole is the actor's role in the org: ok only for a live membership. A
// pending or removed member holds no role; the bearer path gates pending
// members itself.
func orgRole(ctx context.Context, st Store, orgID domain.ID, actor domain.ActorRef) (domain.Role, bool, error) {
	m, ok, err := orgMember(ctx, st, orgID, actor)
	return m.Role, ok, err
}

// orgMember is the actor's org membership: ok only when it is live.
func orgMember(ctx context.Context, st Store, orgID domain.ID, actor domain.ActorRef) (domain.Membership, bool, error) {
	m, err := st.GetMembership(ctx, orgID, actor)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return domain.Membership{}, false, nil
	case err != nil:
		return domain.Membership{}, false, fmt.Errorf("load membership: %w", err)
	}
	return m, m.Status.Alive(), nil
}

// sight answers, for one reader, which projects they can read, loading each
// project at most once.
type sight struct {
	s    *Service
	st   Store
	p    Principal
	seen map[domain.ID]bool
}

func (s *Service) sightOf(st Store, p Principal) *sight {
	return &sight{s: s, st: st, p: p, seen: map[domain.ID]bool{}}
}

// sees reports whether the reader can read the project.
func (v *sight) sees(ctx context.Context, projectID domain.ID) (bool, error) {
	if v.p.HasOrgRole && v.p.OrgRole.SeesEveryProject() {
		return true, nil
	}
	if ok, known := v.seen[projectID]; known {
		return ok, nil
	}
	proj, err := v.st.GetProject(ctx, projectID)
	if err == nil {
		_, err = v.s.access(ctx, v.st, v.p, proj)
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		v.seen[projectID] = false
	case err != nil:
		return false, err
	default:
		v.seen[projectID] = true
	}
	return v.seen[projectID], nil
}

// filterSeen keeps the items whose project the reader can read; an item
// with no project (an org-level event) is kept.
func filterSeen[T any](ctx context.Context, v *sight, items []T, project func(T) *domain.ID) ([]T, error) {
	out := items[:0]
	for _, it := range items {
		if id := project(it); id != nil {
			ok, err := v.sees(ctx, *id)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		out = append(out, it)
	}
	return out, nil
}
