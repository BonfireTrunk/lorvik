package server

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

func toActor(a domain.ActorRef) apitypes.Actor {
	return apitypes.Actor{Kind: a.Kind.String(), ID: a.ID.String()}
}

func toActorPtr(a *domain.ActorRef) *apitypes.Actor {
	if a == nil {
		return nil
	}
	v := toActor(*a)
	return &v
}

func idStr(p *domain.ID) string {
	if p == nil {
		return ""
	}
	return p.String()
}

// recordStatus names a lifecycle status; gone is the record type's word for
// its terminal state (deleted, archived, revoked or removed).
func recordStatus(s domain.RecordStatus, gone string) string {
	switch s {
	case domain.RecordActive:
		return "active"
	case domain.RecordPending:
		return "pending"
	case domain.RecordRotated:
		return "rotated"
	case domain.RecordSuspended:
		return "suspended"
	default:
		return gone
	}
}

func toUser(u domain.User) apitypes.User {
	return apitypes.User{ID: u.ID.String(), Login: u.Login, Name: u.Name, Email: u.Email, AvatarURL: u.AvatarURL,
		Locale: u.Locale, Timezone: u.Timezone, Status: recordStatus(u.Status, "deleted"), CreatedAt: u.CreatedAt}
}

// toNamedUser is what an org sees of a person who is not a member of it:
// invited, asking to join, or removed. Enough to name them in a list and in
// past work, and nothing they did not make public by signing in.
func toNamedUser(u domain.User) apitypes.User {
	return apitypes.User{ID: u.ID.String(), Login: u.Login, Name: u.Name, AvatarURL: u.AvatarURL,
		Status: recordStatus(u.Status, "deleted"), CreatedAt: u.CreatedAt}
}

// agentStatus names an agent's status: revoked can be restored, removed
// cannot.
func agentStatus(s domain.RecordStatus) string {
	if s == domain.RecordForgotten {
		return "removed"
	}
	return recordStatus(s, "revoked")
}

func toAgent(a domain.Agent) apitypes.Agent {
	return apitypes.Agent{ID: a.ID.String(), Name: a.Name, Harness: a.Harness.String(), Machine: a.Machine,
		Status: agentStatus(a.Status), CreatedBy: a.CreatedBy.String(), AgentJoin: a.AgentJoin.String(), CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
}

func toToken(t domain.Token) apitypes.Token {
	return apitypes.Token{ID: t.ID.String(), Actor: toActor(t.Actor), Name: t.Name, Prefix: t.Prefix,
		Scope: t.Scope.String(), Status: recordStatus(t.Status, "revoked"), ExpiresAt: t.ExpiresAt, ReplacedBy: idStr(t.ReplacedBy),
		LastUsedAt: t.LastUsedAt, CreatedAt: t.CreatedAt}
}

// agentRights names the rights an agent holds; people carry none, so their
// rows leave the field out.
func agentRights(a domain.ActorRef, r domain.Rights) []string {
	if a.Kind != domain.ActorAgent {
		return nil
	}
	return r.Names()
}

func toMembership(m domain.Membership) apitypes.Membership {
	return apitypes.Membership{Actor: toActor(m.Actor), Role: m.Role.String(), Rights: agentRights(m.Actor, m.Rights), Status: recordStatus(m.Status, "removed"),
		GrantedBy: idStr(m.GrantedBy), CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
}

func toProjectMember(slug string, m domain.ProjectMember) apitypes.ProjectMember {
	return apitypes.ProjectMember{Project: slug, Actor: toActor(m.Actor), Role: m.Role.String(), Rights: agentRights(m.Actor, m.Rights), Status: recordStatus(m.Status, "removed"),
		GrantedBy: idStr(m.GrantedBy), CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
}

func toOrg(o domain.Org, withSettings bool) apitypes.Org {
	out := apitypes.Org{ID: o.ID.String(), Name: o.Name, Status: recordStatus(o.Status, "deleted"), CreatedAt: o.CreatedAt}
	if withSettings {
		out.Settings = &apitypes.OrgSettings{AgentApproval: o.Settings.AgentApproval, TokenTTLDefault: o.Settings.TokenTTLDefault,
			TokenTTLMax: o.Settings.TokenTTLMax, ProjectVisibilityDefault: o.Settings.ProjectVisibilityDefault}
	}
	return out
}

func toProject(p domain.Project, role domain.Role) apitypes.Project {
	out := apitypes.Project{ID: p.ID.String(), Slug: p.Slug, Name: p.Name, Description: p.Description,
		Visibility: p.Visibility.String(), Concurrency: p.Concurrency.String(), Scope: p.Scope.String(), Lease: p.Lease.String(), Status: recordStatus(p.Status, "archived"),
		CreatedBy: toActor(p.CreatedBy), AgentJoin: p.AgentJoin.String(), CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
	if role != 0 {
		out.Role = role.String()
	}
	return out
}

func toCounts(c query.CardCounts) *apitypes.StatusCounts {
	return &apitypes.StatusCounts{
		Backlog: c.ByStatus[domain.StatusBacklog], Ready: c.ByStatus[domain.StatusReady],
		Working: c.ByStatus[domain.StatusWorking], Waiting: c.ByStatus[domain.StatusWaiting],
		Done: c.ByStatus[domain.StatusDone], Dropped: c.ByStatus[domain.StatusDropped],
		OpenQuestions: c.OpenQuestions,
	}
}

// wireClaim converts a card's claim, naming the holder's chat when it has a
// name.
func (l *lookup) wireClaim(ctx context.Context, c *domain.Claim) (*apitypes.Claim, error) {
	if c == nil {
		return nil, nil
	}
	name, err := l.sessionName(ctx, c.Actor, c.Session)
	if err != nil {
		return nil, err
	}
	out := &apitypes.Claim{Actor: toActor(c.Actor), Session: c.Session, SessionName: name, ExpiresAt: c.ExpiresAt,
		Mine: c.HeldBy(l.viewer, l.session, l.now), Owned: c.OwnedBy(l.viewer, l.session)}
	if c.Lease != 0 {
		out.Lease = c.Lease.String()
	}
	return out, nil
}

// sessionName is what people call the chat behind an agent's session label,
// a subagent's included, or "" when it has no name. Each chat is read once.
func (l *lookup) sessionName(ctx context.Context, actor domain.ActorRef, session string) (string, error) {
	if actor.Kind != domain.ActorAgent || session == "" {
		return "", nil
	}
	if err := l.loadSessionNames(ctx, chatOf{actor, session}); err != nil {
		return "", err
	}
	return l.names[seenSession{actor.ID, domain.BaseSession(session)}], nil
}

// chatOf is a holder and the session label it used.
type chatOf struct {
	actor   domain.ActorRef
	session string
}

// loadSessionNames reads the names of the given chats that are not loaded
// yet, with one query per agent, so a page of claims or locks costs one
// round trip per holding agent and not one per chat.
func (l *lookup) loadSessionNames(ctx context.Context, chats ...chatOf) error {
	missing := map[domain.ID][]string{}
	for _, c := range chats {
		if c.actor.Kind != domain.ActorAgent || c.session == "" {
			continue
		}
		key := seenSession{c.actor.ID, domain.BaseSession(c.session)}
		if _, ok := l.names[key]; !ok {
			l.names[key] = ""
			missing[key.agent] = append(missing[key.agent], key.label)
		}
	}
	for agent, labels := range missing {
		rows, err := l.st.ListAgentSessionsByLabel(ctx, agent, labels)
		if err != nil {
			return fmt.Errorf("load chat names: %w", err)
		}
		for _, r := range rows {
			l.names[seenSession{agent, r.Session}] = r.Name
		}
	}
	return nil
}

func toComment(h domain.Comment, number int64) apitypes.Comment {
	return apitypes.Comment{ID: h.ID.String(), Card: number, Actor: toActor(h.Actor), Body: h.Body, CreatedAt: h.ID.Time()}
}

func toRevision(r domain.NoteRevision) apitypes.NoteRevision {
	return apitypes.NoteRevision{ID: r.ID.String(), Version: r.Version, UpdatedBy: toActor(r.UpdatedBy), Title: r.Title, Body: r.Body, Page: r.Page, CreatedAt: r.ID.Time()}
}

func toCardRevision(r domain.CardRevision) apitypes.CardRevision {
	return apitypes.CardRevision{ID: r.ID.String(), Version: r.Version, UpdatedBy: toActor(r.UpdatedBy), Title: r.Title, Body: r.Body, Page: r.Page, CreatedAt: r.CreatedAt}
}

func toEvent(e domain.Event, project *string) apitypes.Event {
	return apitypes.Event{ID: e.ID.String(), Position: eventPosition(positionOf(e)), Type: e.Type.String(), Project: project, Actor: toActor(e.Actor),
		Subject: apitypes.Subject{Type: e.Subject.Type.String(), ID: e.Subject.ID.String(), Number: e.Subject.Number},
		Payload: e.Payload, CreatedAt: e.CreatedAt()}
}

// lookup resolves ids to the slugs, numbers and claims the wire types use,
// caching within one request. The viewer and now decide which claims render
// as the caller's own.
type lookup struct {
	st       Store
	viewer   domain.ActorRef
	session  string
	now      time.Time
	projects map[domain.ID]string
	cards    map[domain.ID]int64
	claims   map[domain.ID]*domain.Claim
	names    map[seenSession]string
}

func newLookup(st Store, p Principal, now time.Time) *lookup {
	return &lookup{st: st, viewer: p.Actor, session: p.Session, now: now, projects: map[domain.ID]string{}, cards: map[domain.ID]int64{}, claims: map[domain.ID]*domain.Claim{}, names: map[seenSession]string{}}
}

// projectRoles lists the actor's roles in the org's projects, by slug. An
// agent's entries carry rights: those of its row, or whatever rightsOf says
// when it is given.
func (l *lookup) projectRoles(ctx context.Context, orgID domain.ID, actor domain.ActorRef, rightsOf func(domain.ProjectMember) (domain.Rights, error)) ([]apitypes.ProjectRole, error) {
	pms, err := l.st.ListProjectMembersForActor(ctx, orgID, actor)
	if err != nil {
		return nil, fmt.Errorf("list project roles: %w", err)
	}
	var out []apitypes.ProjectRole
	for _, pm := range pms {
		slug, err := l.projectSlug(ctx, pm.ProjectID)
		if err != nil {
			return nil, err
		}
		rights := pm.Rights
		if rightsOf != nil {
			if rights, err = rightsOf(pm); err != nil {
				return nil, err
			}
		}
		out = append(out, apitypes.ProjectRole{Project: slug, Role: pm.Role.String(), Rights: agentRights(actor, rights)})
	}
	return out, nil
}

func (l *lookup) projectSlug(ctx context.Context, id domain.ID) (string, error) {
	if s, ok := l.projects[id]; ok {
		return s, nil
	}
	p, err := l.st.GetProject(ctx, id)
	if err != nil {
		return "", fmt.Errorf("load project: %w", err)
	}
	l.projects[id] = p.Slug
	return p.Slug, nil
}

func (l *lookup) projectSlugPtr(ctx context.Context, id *domain.ID) (*string, error) {
	if id == nil {
		return nil, nil
	}
	s, err := l.projectSlug(ctx, *id)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (l *lookup) cardNumber(ctx context.Context, id domain.ID) (int64, error) {
	if n, ok := l.cards[id]; ok {
		return n, nil
	}
	c, err := l.st.GetCard(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("load card: %w", err)
	}
	l.cards[id] = c.Number
	return c.Number, nil
}

func (l *lookup) cardNumberPtr(ctx context.Context, id *domain.ID) (*int64, error) {
	if id == nil {
		return nil, nil
	}
	n, err := l.cardNumber(ctx, *id)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// loadClaims fetches the claims of several cards in one query.
func (l *lookup) loadClaims(ctx context.Context, cs []domain.Card) error {
	var ids []domain.ID
	for _, c := range cs {
		if _, ok := l.claims[c.ID]; !ok {
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	claims, err := l.st.GetClaims(ctx, ids)
	if err != nil {
		return fmt.Errorf("load claims: %w", err)
	}
	for _, id := range ids {
		l.claims[id] = nil
	}
	for _, cl := range claims {
		l.claims[cl.CardID] = &cl
	}
	return nil
}

func (l *lookup) claim(ctx context.Context, c domain.Card) (*domain.Claim, error) {
	if cl, ok := l.claims[c.ID]; ok {
		return cl, nil
	}
	if err := l.loadClaims(ctx, []domain.Card{c}); err != nil {
		return nil, err
	}
	return l.claims[c.ID], nil
}

func (l *lookup) card(ctx context.Context, c domain.Card) (apitypes.Card, error) {
	slug, err := l.projectSlug(ctx, c.ProjectID)
	if err != nil {
		return apitypes.Card{}, err
	}
	b, err := l.brief(ctx, c)
	if err != nil {
		return apitypes.Card{}, err
	}
	l.cards[c.ID] = c.Number
	out := cardFromBrief(b)
	out.Project, out.Body, out.CreatedBy, out.Version = slug, c.Body, toActor(c.CreatedBy), c.Version
	out.CreatedAt, out.ClosingNote = c.CreatedAt, c.ClosingNote
	if c.Kind == domain.KindStory {
		counts, err := l.st.CountChildrenByStatus(ctx, c.ID)
		if err != nil {
			return apitypes.Card{}, fmt.Errorf("count children of #%d: %w", c.Number, err)
		}
		out.Progress = &apitypes.Progress{}
		for status, n := range counts {
			out.Progress.Total += n
			if status.Terminal() {
				out.Progress.Done += n
			}
		}
	}
	if q := c.Question; q != nil {
		out.Question = &apitypes.Question{Choices: q.Choices, Answer: q.Answer,
			AnsweredBy: toActorPtr(q.AnsweredBy), AnsweredAt: q.AnsweredAt}
	}
	return out, nil
}

func (l *lookup) brief(ctx context.Context, c domain.Card) (apitypes.CardBrief, error) {
	parent, err := l.cardNumberPtr(ctx, c.ParentID)
	if err != nil {
		return apitypes.CardBrief{}, err
	}
	cl, err := l.claim(ctx, c)
	if err != nil {
		return apitypes.CardBrief{}, err
	}
	held, err := l.wireClaim(ctx, cl)
	if err != nil {
		return apitypes.CardBrief{}, err
	}
	decision, err := l.decisionInfo(ctx, c)
	if err != nil {
		return apitypes.CardBrief{}, err
	}
	b := apitypes.CardBrief{ID: c.ID.String(), Number: c.Number, Kind: c.Kind.String(), Status: c.Status.String(),
		Title: c.Title, Priority: c.Priority.String(), Parent: parent, Claim: held,
		Assignee: toActorPtr(c.Assignee), Branch: c.Branch, Lane: c.Lane, Decision: decision,
		Fold: toFold(c.Fold), PageBytes: c.PageBytes, Relayed: c.Relayed(), ForPerson: c.ForPerson(), UpdatedAt: c.UpdatedAt, ClosedAt: c.ClosedAt}
	if b.Behalf, err = l.behalf(ctx, c); err != nil {
		return apitypes.CardBrief{}, err
	}
	if c.DropReason != domain.DropNone {
		b.DropReason = c.DropReason.String()
	}
	return b, nil
}

// behalf is the card's standing as an agent settled it for a person, with
// the name of the chat the agent acted from; nil when a person settled it.
func (l *lookup) behalf(ctx context.Context, c domain.Card) (*apitypes.Behalf, error) {
	b := c.Behalf
	if b == nil {
		return nil, nil
	}
	by := domain.ActorRef{Kind: domain.ActorAgent, ID: b.By}
	name, err := l.sessionName(ctx, by, b.Session)
	if err != nil {
		return nil, err
	}
	return &apitypes.Behalf{For: toActor(domain.ActorRef{Kind: domain.ActorUser, ID: b.For}), By: toActor(by),
		Session: b.Session, SessionName: name, Reason: b.Reason, KeptAt: b.KeptAt}, nil
}

// cardFromBrief starts a full card from its brief, so the fields both shapes
// carry are mapped from the domain card in one place.
func cardFromBrief(b apitypes.CardBrief) apitypes.Card {
	return apitypes.Card{ID: b.ID, Project: b.Project, Number: b.Number, Kind: b.Kind, Status: b.Status,
		DropReason: b.DropReason, Title: b.Title, Parent: b.Parent, Priority: b.Priority, Assignee: b.Assignee,
		Branch: b.Branch, Lane: b.Lane, Claim: b.Claim, Decision: b.Decision, Fold: b.Fold, PageBytes: b.PageBytes, Relayed: b.Relayed, ForPerson: b.ForPerson, Behalf: b.Behalf,
		UpdatedAt: b.UpdatedAt, ClosedAt: b.ClosedAt}
}

func (l *lookup) briefs(ctx context.Context, cs []domain.Card) ([]apitypes.CardBrief, error) {
	if err := l.loadClaims(ctx, cs); err != nil {
		return nil, err
	}
	chats := make([]chatOf, 0, len(cs))
	for _, c := range cs {
		if cl := l.claims[c.ID]; cl != nil {
			chats = append(chats, chatOf{cl.Actor, cl.Session})
		}
	}
	if err := l.loadSessionNames(ctx, chats...); err != nil {
		return nil, err
	}
	out := make([]apitypes.CardBrief, 0, len(cs))
	for _, c := range cs {
		b, err := l.brief(ctx, c)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// decisionInfo fills what a decision card adds. The supersedes links give
// the chain: the card this one replaces, and the accepted card that replaced
// it. A proposal that has not been accepted yet is not a successor. Once the
// card is dropped as superseded the link alone answers, so a successor that
// was accepted and revoked since is still reported; the acceptance, kept on
// revoke, tells it from a rival that was only proposed.
func (l *lookup) decisionInfo(ctx context.Context, c domain.Card) (*apitypes.CardDecision, error) {
	if c.Kind != domain.KindDecision {
		return nil, nil
	}
	out := &apitypes.CardDecision{}
	if c.Acceptance != nil {
		at := c.Acceptance.At
		out.AcceptedBy, out.AcceptedAt = toActorPtr(&c.Acceptance.By), &at
	}
	links, err := l.st.ListCardLinks(ctx, c.ID)
	if err != nil {
		return nil, fmt.Errorf("list links of #%d: %w", c.Number, err)
	}
	for _, lk := range links {
		if lk.Kind != domain.LinkSupersedes {
			continue
		}
		if lk.FromCardID == c.ID {
			n, err := l.cardNumber(ctx, lk.ToCardID)
			if err != nil {
				return nil, err
			}
			out.Supersedes = &n
			continue
		}
		succ, err := l.st.GetCard(ctx, lk.FromCardID)
		if err != nil {
			return nil, fmt.Errorf("load successor of #%d: %w", c.Number, err)
		}
		accepted := succ.Status == domain.StatusDone ||
			(c.DropReason == domain.DropSuperseded && succ.Acceptance != nil)
		if accepted {
			out.SupersededBy = &succ.Number
		}
	}
	return out, nil
}

func toFold(f *domain.Fold) *apitypes.CardFold {
	if f == nil {
		return nil
	}
	return &apitypes.CardFold{Into: f.Into, At: f.At, By: toActor(f.By)}
}

func (l *lookup) note(ctx context.Context, n domain.Note) (apitypes.Note, error) {
	b, err := l.noteBrief(ctx, n)
	if err != nil {
		return apitypes.Note{}, err
	}
	out := noteFromBrief(b)
	out.Body, out.Version, out.CreatedBy, out.CreatedAt = n.Body, n.Version, toActor(n.CreatedBy), n.CreatedAt
	return out, nil
}

func (l *lookup) noteBrief(ctx context.Context, n domain.Note) (apitypes.NoteBrief, error) {
	proj, err := l.projectSlug(ctx, n.ProjectID)
	if err != nil {
		return apitypes.NoteBrief{}, err
	}
	return apitypes.NoteBrief{ID: n.ID.String(), Slug: n.Slug, Title: n.Title, Project: &proj, Status: recordStatus(n.Status, "archived"),
		PageBytes: n.PageBytes, UpdatedBy: toActor(n.UpdatedBy), UpdatedAt: n.UpdatedAt}, nil
}

// noteFromBrief starts a full note from its brief, as cardFromBrief does.
func noteFromBrief(b apitypes.NoteBrief) apitypes.Note {
	return apitypes.Note{ID: b.ID, Project: b.Project, Slug: b.Slug, Title: b.Title, Status: b.Status, PageBytes: b.PageBytes,
		UpdatedBy: b.UpdatedBy, UpdatedAt: b.UpdatedAt}
}

func (l *lookup) event(ctx context.Context, e domain.Event) (apitypes.Event, error) {
	proj, err := l.projectSlugPtr(ctx, e.ProjectID)
	if err != nil {
		return apitypes.Event{}, err
	}
	return toEvent(e, proj), nil
}

func (l *lookup) events(ctx context.Context, es []domain.Event) ([]apitypes.Event, error) {
	out := make([]apitypes.Event, 0, len(es))
	for _, e := range es {
		ae, err := l.event(ctx, e)
		if err != nil {
			return nil, err
		}
		out = append(out, ae)
	}
	return out, nil
}

// newestFirst sorts events by descending id.
func newestFirst(es []domain.Event) {
	slices.SortFunc(es, func(a, b domain.Event) int { return int(b.ID - a.ID) })
}
