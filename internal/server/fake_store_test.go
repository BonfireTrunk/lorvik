package server

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// fakeStore is an in-memory Store with the same semantics as the Postgres
// one: caller-assigned ids, store-allocated numbers, versioned writes, keyset
// paging with capped remaining counts, and ErrNotFound for misses.
type fakeStore struct {
	mu sync.Mutex

	orgs       map[domain.ID]*domain.Org
	users      map[domain.ID]*domain.User
	identities []domain.Identity
	members    map[string]*domain.Membership
	pmembers   map[string]*domain.ProjectMember
	joinRules  map[[2]domain.ID]domain.AgentJoinRule
	// sessions and grants are keyed by agent id and base label, and by
	// project id too for grants.
	sessions  map[sessionKey]*domain.AgentSession
	grants    map[grantKey]*domain.AgentSessionGrant
	agents    map[domain.ID]*domain.Agent
	tokens    map[domain.ID]*domain.Token
	projects  map[domain.ID]*domain.Project
	cards     map[domain.ID]*domain.Card
	claims    map[domain.ID]*domain.Claim
	locks     map[domain.ID]*domain.Lock
	links     []domain.CardLink
	comments  []domain.Comment
	reads     map[string]domain.CardRead
	notes     map[domain.ID]*domain.Note
	revisions []domain.NoteRevision
	cardRevs  []domain.CardRevision
	cardPages map[domain.ID]string
	notePages map[domain.ID]string
	refs      []domain.RefLink
	events    []domain.Event
	// lastXid stands in for transaction ids: each appended event takes the
	// next one, so feed order is append order whatever the event ids.
	lastXid uint64

	requestKeys map[requestKeyID]domain.RequestKey

	// endingAnnounced is claims.ending_announced_for, by card.
	endingAnnounced map[domain.ID]time.Time

	// sessionNamings counts the calls to SetAgentSessionName.
	sessionNamings int
	// failNaming, when set, is returned by the next SetAgentSessionName.
	failNaming error
	// subscriptions are the card subscriptions, one row per call that was not a repeat.
	subscriptions []domain.CardSubscription
	// sessionLookups counts the calls to ListAgentSessionsByLabel.
	sessionLookups int
	// sessionTouches counts the calls to TouchAgentSession.
	sessionTouches int
	// trims records the keep of every call to TrimAgentSessions.
	trims []int

	// listeners receive the org id of every appended event.
	listeners []func(domain.ID)
	// onAppend, when set, runs before an event is appended; its error is
	// the append's.
	onAppend func(domain.Event) error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		orgs: map[domain.ID]*domain.Org{}, users: map[domain.ID]*domain.User{}, members: map[string]*domain.Membership{},
		pmembers: map[string]*domain.ProjectMember{}, joinRules: map[[2]domain.ID]domain.AgentJoinRule{},
		sessions: map[sessionKey]*domain.AgentSession{}, grants: map[grantKey]*domain.AgentSessionGrant{}, agents: map[domain.ID]*domain.Agent{}, tokens: map[domain.ID]*domain.Token{},
		projects: map[domain.ID]*domain.Project{}, cards: map[domain.ID]*domain.Card{}, claims: map[domain.ID]*domain.Claim{}, endingAnnounced: map[domain.ID]time.Time{}, locks: map[domain.ID]*domain.Lock{},
		reads: map[string]domain.CardRead{}, notes: map[domain.ID]*domain.Note{},
		cardPages: map[domain.ID]string{}, notePages: map[domain.ID]string{},
		requestKeys: map[requestKeyID]domain.RequestKey{},
	}
}

func notFound(what string) error { return fmt.Errorf("%s: %w", what, domain.ErrNotFound) }

func memberKey(scope domain.ID, a domain.ActorRef) string {
	return fmt.Sprintf("%d/%d/%d", scope, a.Kind, a.ID)
}

// WithTx runs fn against the same store: the fake has no transactions, so a
// failing fn leaves partial writes behind, which no test relies on.
// WithTx runs fn on the store itself and undoes nothing but the request keys
// a failed fn claimed: a refused keyed write must leave its key free, and the
// tests of that rule need the rollback the real store gives. It assumes no
// other keyed write runs at the same time.
func (f *fakeStore) WithTx(_ context.Context, fn func(Store) error) error {
	f.mu.Lock()
	before := maps.Clone(f.requestKeys)
	f.mu.Unlock()
	err := fn(f)
	if err != nil {
		f.mu.Lock()
		f.requestKeys = before
		f.mu.Unlock()
	}
	return err
}

func (f *fakeStore) WithSnapshot(_ context.Context, fn func(Store) error) error { return fn(f) }

// position resolves a cursor as the real store does: zero as the start, the
// largest id as past every event, and otherwise the position of the event it
// names. ok is false when no event of the org has that id, or a full
// position's xid differs from the event's. Callers hold f.mu.
func (f *fakeStore) position(orgID domain.ID, p query.EventPosition) (query.EventPosition, bool) {
	switch {
	case p.ID == 0:
		return p, true
	case p.ID == math.MaxInt64 && p.Xid == 0:
		return query.EventPosition{Xid: math.MaxUint64, ID: p.ID}, true
	}
	i := slices.IndexFunc(f.events, func(e domain.Event) bool { return e.OrgID == orgID && e.ID == p.ID })
	if i < 0 || (p.Xid != 0 && f.events[i].Xid != p.Xid) {
		return query.EventPosition{}, false
	}
	return positionOf(f.events[i]), true
}

func comparePositions(a, b query.EventPosition) int { return a.Compare(b) }

func (f *fakeStore) Ping(context.Context) error { return nil }

// Listen implements Notifier.
func (f *fakeStore) Listen(ctx context.Context, fn func(domain.ID)) error {
	f.mu.Lock()
	f.listeners = append(f.listeners, fn)
	f.mu.Unlock()
	<-ctx.Done()
	return nil
}

func (f *fakeStore) CreateOrg(_ context.Context, o domain.Org) (domain.Org, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orgs[o.ID] = &o
	return o, nil
}

func (f *fakeStore) GetOrg(_ context.Context, id domain.ID) (domain.Org, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if o, ok := f.orgs[id]; ok {
		return *o, nil
	}
	return domain.Org{}, notFound("org")
}

func (f *fakeStore) UpdateOrg(_ context.Context, o domain.Org) (domain.Org, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.orgs[o.ID]
	if !ok {
		return domain.Org{}, notFound("org")
	}
	cur.Name, cur.Status, cur.Settings, cur.UpdatedAt = o.Name, o.Status, o.Settings, o.UpdatedAt
	return *cur, nil
}

func (f *fakeStore) ListOrgs(context.Context) ([]domain.Org, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.Org, 0, len(f.orgs))
	for _, o := range f.orgs {
		out = append(out, *o)
	}
	slices.SortFunc(out, func(a, b domain.Org) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (f *fakeStore) CreateUser(_ context.Context, u domain.User) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[u.ID] = &u
	return u, nil
}

func (f *fakeStore) GetUser(_ context.Context, id domain.ID) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[id]; ok {
		return *u, nil
	}
	return domain.User{}, notFound("user")
}

func (f *fakeStore) LockUser(ctx context.Context, id domain.ID) (domain.User, error) {
	return f.GetUser(ctx, id)
}

func (f *fakeStore) GetUsers(_ context.Context, ids []domain.ID) ([]domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.User
	for _, id := range ids {
		if u, ok := f.users[id]; ok {
			out = append(out, *u)
		}
	}
	return out, nil
}

func (f *fakeStore) UpdateUser(_ context.Context, u domain.User) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.users[u.ID]
	if !ok {
		return domain.User{}, notFound("user")
	}
	cur.Login, cur.Name, cur.Email, cur.AvatarURL, cur.Locale, cur.Timezone, cur.Status, cur.AgentJoin, cur.UpdatedAt =
		u.Login, u.Name, u.Email, u.AvatarURL, u.Locale, u.Timezone, u.Status, u.AgentJoin, u.UpdatedAt
	return *cur, nil
}

func (f *fakeStore) CreateIdentity(_ context.Context, i domain.Identity) (domain.Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.identities = append(f.identities, i)
	return i, nil
}

func (f *fakeStore) GetIdentity(_ context.Context, provider domain.Provider, providerID string) (domain.Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, i := range f.identities {
		if i.Provider == provider && i.ProviderID == providerID {
			return i, nil
		}
	}
	return domain.Identity{}, notFound("identity")
}

func (f *fakeStore) ListIdentities(_ context.Context, userID domain.ID) ([]domain.Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Identity
	for _, i := range f.identities {
		if i.UserID == userID {
			out = append(out, i)
		}
	}
	return out, nil
}

func (f *fakeStore) UpdateIdentity(_ context.Context, i domain.Identity) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k := range f.identities {
		if f.identities[k].ID == i.ID {
			f.identities[k].Secret, f.identities[k].Login, f.identities[k].LastLoginAt = i.Secret, i.Login, i.LastLoginAt
			return nil
		}
	}
	return notFound("identity")
}

func (f *fakeStore) DeleteIdentity(_ context.Context, userID, id domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k := range f.identities {
		if f.identities[k].ID == id && f.identities[k].UserID == userID {
			f.identities = slices.Delete(f.identities, k, k+1)
			return nil
		}
	}
	return notFound("identity")
}

func (f *fakeStore) PutMembership(_ context.Context, m domain.Membership) (domain.Membership, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members[memberKey(m.OrgID, m.Actor)] = &m
	return m, nil
}

func (f *fakeStore) GetMembership(_ context.Context, orgID domain.ID, a domain.ActorRef) (domain.Membership, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.members[memberKey(orgID, a)]; ok {
		return *m, nil
	}
	return domain.Membership{}, notFound("membership")
}

func (f *fakeStore) LockMembership(ctx context.Context, orgID domain.ID, a domain.ActorRef) (domain.Membership, error) {
	return f.GetMembership(ctx, orgID, a)
}

func (f *fakeStore) ListMemberships(_ context.Context, orgID domain.ID, q query.MemberFilter) ([]domain.Membership, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Membership
	for _, m := range f.members {
		if m.OrgID == orgID && (q.All || m.Status.Alive()) {
			out = append(out, *m)
		}
	}
	slices.SortFunc(out, func(a, b domain.Membership) int {
		return compareMembers(a.Role, a.CreatedAt, a.Actor, b.Role, b.CreatedAt, b.Actor)
	})
	items, remaining := fakePage(out, q.Page.Limit, func(m domain.Membership) bool { return memberAfter(m.Role, m.CreatedAt, m.Actor, q.Cursor) })
	return items, remaining, nil
}

func (f *fakeStore) CountOrgPeople(_ context.Context, orgID domain.ID) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.members {
		if m.OrgID == orgID && m.Actor.Kind == domain.ActorUser && m.Status.Alive() && f.users[m.Actor.ID] != nil && f.users[m.Actor.ID].Status.Alive() {
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) CountOrgContents(_ context.Context, orgID domain.ID) (projects, cards int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.projects {
		if p.OrgID == orgID && p.Scope != domain.ProjectScopeGlobal {
			projects++
		}
	}
	for _, c := range f.cards {
		if p := f.projects[c.ProjectID]; p != nil && p.OrgID == orgID {
			cards++
		}
	}
	return projects, cards, nil
}

func (f *fakeStore) LockOrgOwners(_ context.Context, orgID domain.ID) ([]domain.Membership, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Membership
	for _, m := range f.members {
		if m.OrgID == orgID && m.Actor.Kind == domain.ActorUser && m.Role == domain.RoleOwner && m.Status.Alive() && f.users[m.Actor.ID] != nil && f.users[m.Actor.ID].Status.Alive() {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (f *fakeStore) ListMembershipsForActor(_ context.Context, a domain.ActorRef) ([]domain.Membership, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Membership
	for _, m := range f.members {
		if m.Actor == a && m.Status.Alive() {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (f *fakeStore) ListOpenMembershipsForActor(_ context.Context, a domain.ActorRef) ([]domain.Membership, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Membership
	for _, m := range f.members {
		if m.Actor == a && (m.Status.Alive() || m.Status == domain.RecordPending) {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (f *fakeStore) PutProjectMember(_ context.Context, m domain.ProjectMember) (domain.ProjectMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pmembers[memberKey(m.ProjectID, m.Actor)] = &m
	return m, nil
}

func (f *fakeStore) GetProjectMember(_ context.Context, projectID domain.ID, a domain.ActorRef) (domain.ProjectMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.pmembers[memberKey(projectID, a)]; ok {
		return *m, nil
	}
	return domain.ProjectMember{}, notFound("project member")
}

func (f *fakeStore) ListProjectMembers(_ context.Context, projectID domain.ID, q query.MemberFilter) ([]domain.ProjectMember, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.ProjectMember
	for _, m := range f.pmembers {
		if m.ProjectID == projectID && (q.All || m.Status.Alive()) {
			out = append(out, *m)
		}
	}
	slices.SortFunc(out, func(a, b domain.ProjectMember) int {
		return compareMembers(a.Role, a.CreatedAt, a.Actor, b.Role, b.CreatedAt, b.Actor)
	})
	items, remaining := fakePage(out, q.Page.Limit, func(m domain.ProjectMember) bool { return memberAfter(m.Role, m.CreatedAt, m.Actor, q.Cursor) })
	return items, remaining, nil
}

func (f *fakeStore) ListProjectMembersForActor(_ context.Context, orgID domain.ID, a domain.ActorRef) ([]domain.ProjectMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.ProjectMember
	for _, m := range f.pmembers {
		p, ok := f.projects[m.ProjectID]
		if ok && p.OrgID == orgID && m.Actor == a && m.Status.Alive() {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (f *fakeStore) ListAgentRequests(_ context.Context, orgID, ownerID domain.ID) ([]domain.ProjectMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.ProjectMember
	for _, m := range f.pmembers {
		p, ok := f.projects[m.ProjectID]
		if ok && p.OrgID == orgID && m.Actor.Kind == domain.ActorAgent && m.Status == domain.RecordPending && m.GrantedBy != nil && *m.GrantedBy == ownerID {
			out = append(out, *m)
		}
	}
	slices.SortFunc(out, func(a, b domain.ProjectMember) int {
		return cmp.Or(a.UpdatedAt.Compare(b.UpdatedAt), cmp.Compare(a.ProjectID, b.ProjectID), cmp.Compare(a.Actor.ID, b.Actor.ID))
	})
	return out, nil
}

type sessionKey struct {
	agent   domain.ID
	session string
}

type grantKey struct {
	project domain.ID
	sessionKey
}

func (f *fakeStore) TouchAgentSession(_ context.Context, agentID domain.ID, session string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessionTouches++
	k := sessionKey{agentID, session}
	if s, ok := f.sessions[k]; ok {
		if s.LastSeenAt.Before(now.Add(-time.Minute)) {
			s.LastSeenAt = now
		}
		return nil
	}
	f.sessions[k] = &domain.AgentSession{AgentID: agentID, Session: session, CreatedAt: now, LastSeenAt: now}
	return nil
}

func (f *fakeStore) SetAgentSessionName(_ context.Context, agentID domain.ID, session, name string, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessionNamings++
	if err := f.failNaming; err != nil {
		f.failNaming = nil
		return err
	}
	k := sessionKey{agentID, session}
	if s, ok := f.sessions[k]; ok {
		s.Name = name
		return nil
	}
	f.sessions[k] = &domain.AgentSession{AgentID: agentID, Session: session, Name: name, CreatedAt: now, LastSeenAt: now}
	return nil
}

func (f *fakeStore) TrimAgentSessions(_ context.Context, agentID domain.ID, keep int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trims = append(f.trims, keep)
	var mine []*domain.AgentSession
	for k, s := range f.sessions {
		if k.agent == agentID {
			mine = append(mine, s)
		}
	}
	slices.SortFunc(mine, func(a, b *domain.AgentSession) int {
		return cmp.Or(b.LastSeenAt.Compare(a.LastSeenAt), cmp.Compare(a.Session, b.Session))
	})
	for _, s := range mine[min(keep, len(mine)):] {
		granted := false
		for k, g := range f.grants {
			granted = granted || k.sessionKey == sessionKey{agentID, s.Session} && g.Status.Alive()
		}
		if !granted {
			delete(f.sessions, sessionKey{agentID, s.Session})
		}
	}
	return nil
}

func (f *fakeStore) ListAgentSessions(_ context.Context, agentID domain.ID, limit int) ([]domain.AgentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.AgentSession
	for k, s := range f.sessions {
		if k.agent == agentID {
			out = append(out, *s)
		}
	}
	slices.SortFunc(out, func(a, b domain.AgentSession) int {
		return cmp.Or(b.LastSeenAt.Compare(a.LastSeenAt), cmp.Compare(a.Session, b.Session))
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeStore) ListAgentSessionsByLabel(_ context.Context, agentID domain.ID, labels []string) ([]domain.AgentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessionLookups++
	var out []domain.AgentSession
	for k, s := range f.sessions {
		if k.agent == agentID && slices.Contains(labels, k.session) {
			out = append(out, *s)
		}
	}
	slices.SortFunc(out, func(a, b domain.AgentSession) int {
		return cmp.Or(b.LastSeenAt.Compare(a.LastSeenAt), cmp.Compare(a.Session, b.Session))
	})
	return out, nil
}

func (f *fakeStore) PutAgentSessionGrant(_ context.Context, g domain.AgentSessionGrant) (domain.AgentSessionGrant, error) {
	if err := g.Validate(); err != nil {
		return domain.AgentSessionGrant{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k := grantKey{g.ProjectID, sessionKey{g.AgentID, g.Session}}
	if old, ok := f.grants[k]; ok {
		g.CreatedAt = old.CreatedAt
	}
	f.grants[k] = &g
	return g, nil
}

func (f *fakeStore) GetAgentSessionGrant(_ context.Context, projectID, agentID domain.ID, session string) (domain.AgentSessionGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if g, ok := f.grants[grantKey{projectID, sessionKey{agentID, session}}]; ok {
		return *g, nil
	}
	return domain.AgentSessionGrant{}, notFound("agent session grant")
}

func (f *fakeStore) ListAgentSessionGrants(_ context.Context, agentID domain.ID) ([]domain.AgentSessionGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.AgentSessionGrant
	for k, g := range f.grants {
		if k.agent == agentID && g.Status.Alive() {
			out = append(out, *g)
		}
	}
	slices.SortFunc(out, func(a, b domain.AgentSessionGrant) int {
		return cmp.Or(cmp.Compare(a.ProjectID, b.ProjectID), cmp.Compare(a.Session, b.Session))
	})
	return out, nil
}

func (f *fakeStore) EndAgentSessionGrant(_ context.Context, projectID, agentID domain.ID, session string, before time.Time) (domain.AgentSessionGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.grants[grantKey{projectID, sessionKey{agentID, session}}]
	if !ok || !g.Status.Alive() || g.EndsAt == nil || g.EndsAt.After(before) {
		return domain.AgentSessionGrant{}, notFound("agent session grant")
	}
	g.Status, g.UpdatedAt = domain.RecordRemoved, before
	return *g, nil
}

func (f *fakeStore) ListEndedAgentSessionGrants(_ context.Context, before time.Time, limit int) ([]domain.AgentSessionGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.AgentSessionGrant
	for _, g := range f.grants {
		if g.Status.Alive() && g.EndsAt != nil && !g.EndsAt.After(before) {
			out = append(out, *g)
		}
	}
	slices.SortFunc(out, func(a, b domain.AgentSessionGrant) int { return a.EndsAt.Compare(*b.EndsAt) })
	return out[:min(len(out), limit)], nil
}

func (f *fakeStore) GetAgentJoinRule(_ context.Context, projectID, userID domain.ID) (domain.AgentJoinRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.joinRules[[2]domain.ID{projectID, userID}]; ok {
		return r, nil
	}
	return domain.AgentJoinRule{}, notFound("agent join rule")
}

func (f *fakeStore) PutAgentJoinRule(_ context.Context, r domain.AgentJoinRule) (domain.AgentJoinRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.joinRules[[2]domain.ID{r.ProjectID, r.UserID}] = r
	return r, nil
}

func (f *fakeStore) DeleteAgentJoinRule(_ context.Context, projectID, userID domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.joinRules, [2]domain.ID{projectID, userID})
	return nil
}

func (f *fakeStore) HasProjectMembership(ctx context.Context, orgID domain.ID, a domain.ActorRef) (bool, error) {
	pms, err := f.ListProjectMembersForActor(ctx, orgID, a)
	return len(pms) > 0, err
}

func (f *fakeStore) CreateAgent(_ context.Context, a domain.Agent) (domain.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agents[a.ID] = &a
	return a, nil
}

func (f *fakeStore) GetAgent(_ context.Context, id domain.ID) (domain.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.agents[id]; ok {
		return *a, nil
	}
	return domain.Agent{}, notFound("agent")
}

func (f *fakeStore) LockAgent(ctx context.Context, id domain.ID) (domain.Agent, error) {
	return f.GetAgent(ctx, id)
}

func (f *fakeStore) GetAgents(_ context.Context, ids []domain.ID) ([]domain.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Agent
	for _, id := range ids {
		if a, ok := f.agents[id]; ok {
			out = append(out, *a)
		}
	}
	return out, nil
}

func (f *fakeStore) ListAgents(_ context.Context, orgID domain.ID, all bool) ([]domain.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Agent
	for _, a := range f.agents {
		if a.OrgID == orgID && (all || a.Status >= domain.RecordPending) {
			out = append(out, *a)
		}
	}
	slices.SortFunc(out, func(a, b domain.Agent) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (f *fakeStore) ListAgentsPage(ctx context.Context, orgID domain.ID, q query.AgentFilter) ([]domain.Agent, int, error) {
	out, err := f.ListAgents(ctx, orgID, q.All)
	if err != nil {
		return nil, 0, err
	}
	out = slices.DeleteFunc(out, func(a domain.Agent) bool { return a.Status == domain.RecordForgotten })
	slices.SortFunc(out, func(a, b domain.Agent) int {
		return cmp.Or(cmp.Compare(b.Status, a.Status), a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	items, remaining := fakePage(out, q.Page.Limit, func(a domain.Agent) bool {
		if q.Cursor == nil {
			return true
		}
		return cmp.Or(cmp.Compare(q.Cursor.Status, a.Status), a.CreatedAt.Compare(q.Cursor.CreatedAt), cmp.Compare(a.ID, q.Cursor.ID)) > 0
	})
	return items, remaining, nil
}

func (f *fakeStore) UpdateAgent(_ context.Context, a domain.Agent) (domain.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.agents[a.ID]
	if !ok {
		return domain.Agent{}, notFound("agent")
	}
	cur.Name, cur.Harness, cur.Machine, cur.Status, cur.AgentJoin, cur.UpdatedAt = a.Name, a.Harness, a.Machine, a.Status, a.AgentJoin, a.UpdatedAt
	return *cur, nil
}

func (f *fakeStore) CreateToken(_ context.Context, t domain.Token) (domain.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[t.ID] = &t
	return t, nil
}

func (f *fakeStore) GetTokenByHash(_ context.Context, hash []byte) (domain.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tokens {
		if bytes.Equal(t.Hash, hash) {
			return *t, nil
		}
	}
	return domain.Token{}, notFound("token")
}

func (f *fakeStore) GetToken(_ context.Context, id domain.ID) (domain.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.tokens[id]; ok {
		return *t, nil
	}
	return domain.Token{}, notFound("token")
}

func (f *fakeStore) LockToken(ctx context.Context, id domain.ID) (domain.Token, error) {
	return f.GetToken(ctx, id)
}

func (f *fakeStore) ListTokens(_ context.Context, orgID domain.ID, a domain.ActorRef, pg query.Page, after *query.TokenCursor) ([]domain.Token, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Token
	for _, t := range f.tokens {
		if t.OrgID == orgID && t.Actor == a {
			out = append(out, *t)
		}
	}
	slices.SortFunc(out, func(a, b domain.Token) int {
		return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), cmp.Compare(b.ID, a.ID))
	})
	items, remaining := fakePage(out, pg.Limit, func(t domain.Token) bool {
		if after == nil {
			return true
		}
		return cmp.Or(after.CreatedAt.Compare(t.CreatedAt), cmp.Compare(after.ID, t.ID)) > 0
	})
	return items, remaining, nil
}

// renewToken stands in for a fresh agent token minted at now: the same
// secret, issued again for ttl.
func (f *fakeStore) renewToken(id domain.ID, now time.Time, ttl time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.tokens[id]; ok && t.Status == domain.RecordActive {
		t.CreatedAt, t.ExpiresAt = now, now.Add(ttl)
	}
}

func (f *fakeStore) UpdateToken(_ context.Context, t domain.Token) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.tokens[t.ID]
	if !ok {
		return notFound("token")
	}
	cur.Status, cur.ExpiresAt, cur.ReplacedBy = t.Status, t.ExpiresAt, t.ReplacedBy
	return nil
}

func (f *fakeStore) TouchToken(_ context.Context, id domain.ID, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.tokens[id]; ok {
		t.LastUsedAt = &at
	}
	return nil
}

func (f *fakeStore) RevokeTokensForActor(_ context.Context, orgID domain.ID, a domain.ActorRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tokens {
		if t.OrgID == orgID && t.Actor == a && t.Status.Alive() {
			t.Status = domain.RecordRevoked
		}
	}
	return nil
}

func (f *fakeStore) CreateProject(_ context.Context, p domain.Project) (domain.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, q := range f.projects {
		if q.OrgID == p.OrgID && q.Slug == p.Slug {
			return domain.Project{}, errors.New("duplicate project slug")
		}
	}
	p.NextCardNumber = 1
	f.projects[p.ID] = &p
	return p, nil
}

func (f *fakeStore) GetProject(_ context.Context, id domain.ID) (domain.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.projects[id]; ok {
		return *p, nil
	}
	return domain.Project{}, notFound("project")
}

func (f *fakeStore) GetProjectBySlug(_ context.Context, orgID domain.ID, slug string) (domain.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.projects {
		if p.OrgID == orgID && p.Slug == slug {
			return *p, nil
		}
	}
	return domain.Project{}, notFound("project " + slug)
}

func (f *fakeStore) ListProjects(_ context.Context, orgID domain.ID) ([]domain.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Project
	for _, p := range f.projects {
		if p.OrgID == orgID {
			out = append(out, *p)
		}
	}
	slices.SortFunc(out, func(a, b domain.Project) int {
		return cmp.Or(cmp.Compare(b.Scope, a.Scope), cmp.Compare(a.Slug, b.Slug))
	})
	return out, nil
}

func (f *fakeStore) UpdateProject(_ context.Context, p domain.Project) (domain.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.projects[p.ID]
	if !ok {
		return domain.Project{}, notFound("project")
	}
	cur.Name, cur.Description, cur.Visibility, cur.Concurrency, cur.Lease, cur.Status, cur.AgentJoin, cur.UpdatedAt = p.Name, p.Description, p.Visibility, p.Concurrency, p.Lease, p.Status, p.AgentJoin, p.UpdatedAt
	return *cur, nil
}

func (f *fakeStore) CountCards(_ context.Context, projectID domain.ID) (query.CardCounts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := query.CardCounts{ByStatus: map[domain.Status]int{}}
	for _, c := range f.cards {
		if c.ProjectID != projectID {
			continue
		}
		out.ByStatus[c.Status]++
		if c.Kind == domain.KindQuestion && c.Status.Open() {
			out.OpenQuestions++
		}
	}
	return out, nil
}

func (f *fakeStore) CountOpenQuestionsBy(_ context.Context, projectID domain.ID, actor domain.ActorRef) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.cards {
		if c.ProjectID == projectID && c.Kind == domain.KindQuestion && c.Status.Open() && c.CreatedBy == actor {
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) CreateCard(_ context.Context, c domain.Card) (domain.Card, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[c.ProjectID]
	if !ok {
		return domain.Card{}, notFound("project")
	}
	c.Number, c.Version = p.NextCardNumber, 1
	p.NextCardNumber++
	f.cards[c.ID] = &c
	return c, nil
}

func (f *fakeStore) GetCard(_ context.Context, id domain.ID) (domain.Card, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.cards[id]; ok {
		return *c, nil
	}
	return domain.Card{}, notFound("card")
}

func (f *fakeStore) GetCards(_ context.Context, ids []domain.ID) ([]domain.Card, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Card
	for _, id := range ids {
		if c, ok := f.cards[id]; ok {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (f *fakeStore) GetCardByNumber(_ context.Context, projectID domain.ID, number int64) (domain.Card, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.cards {
		if c.ProjectID == projectID && c.Number == number {
			return *c, nil
		}
	}
	return domain.Card{}, notFound("card")
}

// cardOrder is the board order: status, priority desc, number.
func cardOrder(a, b domain.Card) int {
	return cmp.Or(cmp.Compare(a.Status, b.Status), cmp.Compare(b.Priority, a.Priority), cmp.Compare(a.Number, b.Number))
}

// cardOrderFor is the comparison of a card list order. Ties break by number
// in the same direction as the sort.
func cardOrderFor(sort query.CardSort, desc bool) func(a, b domain.Card) int {
	if sort == query.SortBoard {
		return cardOrder
	}
	return func(a, b domain.Card) int {
		var c int
		switch sort {
		case query.SortCreated:
			c = a.CreatedAt.Compare(b.CreatedAt)
		case query.SortUpdated:
			c = a.UpdatedAt.Compare(b.UpdatedAt)
		case query.SortPriority:
			c = cmp.Compare(a.Priority, b.Priority)
		}
		c = cmp.Or(c, cmp.Compare(a.Number, b.Number))
		if desc {
			return -c
		}
		return c
	}
}

func textMatch(q string, fields ...string) bool {
	q = strings.ToLower(q)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

// pageOf applies keyset paging to sorted rows: at is the index of the cursor
// row (-1 for none), forward takes rows after it, backward the rows before
// it (returned in forward order). Remaining counts the rest, capped.
func pageOf[T any](rows []T, at int, p query.Page) ([]T, int) {
	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	if p.Backward {
		end := at
		if at < 0 {
			end = len(rows)
		}
		start := max(0, end-limit)
		return slices.Clone(rows[start:end]), min(start, query.RemainingCap)
	}
	start := min(at+1, len(rows))
	end := min(len(rows), start+limit)
	return slices.Clone(rows[start:end]), min(len(rows)-end, query.RemainingCap)
}

// cardMatches applies the filter's constraints other than scope and paging.
// The caller holds the lock.
func (f *fakeStore) cardMatches(c *domain.Card, fl query.CardFilter) bool {
	if len(fl.Statuses) > 0 && !slices.Contains(fl.Statuses, c.Status) {
		return false
	}
	if fl.Kind != 0 && c.Kind != fl.Kind {
		return false
	}
	if fl.ParentID != nil && (c.ParentID == nil || *c.ParentID != *fl.ParentID) {
		return false
	}
	if fl.NoStory && (c.ParentID != nil || c.Kind == domain.KindStory) {
		return false
	}
	if (fl.Lane != "" && c.Lane != fl.Lane) || (fl.NoLane && c.Lane != "") {
		return false
	}
	if fl.Open && !c.Status.Open() {
		return false
	}
	if fl.Relayed && !c.Relayed() {
		return false
	}
	if fl.AwaitsKeepBy != nil && (!c.AwaitsKeep() || c.Behalf.For != *fl.AwaitsKeepBy) {
		return false
	}
	if fl.Actor != nil {
		cl, claimed := f.claims[c.ID]
		if (!claimed || cl.Actor != *fl.Actor) && (c.Assignee == nil || *c.Assignee != *fl.Actor) {
			return false
		}
	}
	if len(fl.Priorities) > 0 && !slices.Contains(fl.Priorities, c.Priority) {
		return false
	}
	_, claimed := f.claims[c.ID]
	if fl.Unassigned && (c.Assignee != nil || claimed) {
		return false
	}
	if fl.Assignee != nil && (c.Assignee == nil || c.Assignee.ID != *fl.Assignee) &&
		(!claimed || f.claims[c.ID].Actor.ID != *fl.Assignee) {
		return false
	}
	if !fl.Created.Contains(c.CreatedAt) || !fl.Updated.Contains(c.UpdatedAt) {
		return false
	}
	if !fl.Closed.IsZero() && (c.ClosedAt == nil || !fl.Closed.Contains(*c.ClosedAt)) {
		return false
	}
	return fl.Query == "" || textMatch(fl.Query, c.Title, c.Body)
}

func (f *fakeStore) ListCards(_ context.Context, projectID domain.ID, fl query.CardFilter) ([]domain.Card, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []domain.Card
	for _, c := range f.cards {
		if c.ProjectID == projectID && f.cardMatches(c, fl) {
			rows = append(rows, *c)
		}
	}
	order := cardOrderFor(fl.Sort, fl.Desc)
	slices.SortFunc(rows, order)
	if fl.Query != "" {
		if fl.Cursor != nil {
			at := slices.IndexFunc(rows, func(c domain.Card) bool { return c.ID == fl.Cursor.ID })
			rows = rows[at+1:]
		}
		limit := cmp.Or(fl.Page.Limit, 50)
		end := min(len(rows), limit)
		return rows[:end], min(len(rows)-end, query.RemainingCap), nil
	}
	at := -1
	if fl.Cursor != nil {
		k := domain.Card{Status: fl.Cursor.Status, Priority: fl.Cursor.Priority, Number: fl.Cursor.Number,
			CreatedAt: fl.Cursor.CreatedAt, UpdatedAt: fl.Cursor.UpdatedAt}
		at, _ = slices.BinarySearchFunc(rows, k, order)
		if at < len(rows) && order(rows[at], k) != 0 {
			at-- // cursor row gone: continue from the slot it would occupy
		}
	}
	out, remaining := pageOf(rows, at, fl.Page)
	return out, remaining, nil
}

func (f *fakeStore) ListCardsIn(_ context.Context, projectIDs []domain.ID, fl query.CardFilter) ([]domain.Card, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []domain.Card
	for _, c := range f.cards {
		if slices.Contains(projectIDs, c.ProjectID) && f.cardMatches(c, fl) {
			rows = append(rows, *c)
		}
	}
	byIDDesc := func(a, b domain.Card) int { return cmp.Compare(b.ID, a.ID) }
	slices.SortFunc(rows, byIDDesc)
	if fl.Query != "" {
		if fl.Cursor != nil {
			at := slices.IndexFunc(rows, func(c domain.Card) bool { return c.ID == fl.Cursor.ID })
			rows = rows[at+1:]
		}
		limit := cmp.Or(fl.Page.Limit, 50)
		end := min(len(rows), limit)
		return rows[:end], min(len(rows)-end, query.RemainingCap), nil
	}
	at := -1
	if fl.Cursor != nil {
		k := domain.Card{ID: fl.Cursor.ID}
		at, _ = slices.BinarySearchFunc(rows, k, byIDDesc)
		if at < len(rows) && byIDDesc(rows[at], k) != 0 {
			at--
		}
	}
	out, remaining := pageOf(rows, at, fl.Page)
	return out, remaining, nil
}

func (f *fakeStore) ListUnfolded(_ context.Context, projectID domain.ID, page query.Page, cur *query.UnfoldedCursor) ([]domain.Card, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []domain.Card
	for _, c := range f.cards {
		if c.ProjectID == projectID && c.Status == domain.StatusDone && c.ParentID == nil && c.Fold == nil {
			rows = append(rows, *c)
		}
	}
	order := func(a, b domain.Card) int {
		return cmp.Or(b.ClosedAt.Compare(*a.ClosedAt), cmp.Compare(b.ID, a.ID))
	}
	slices.SortFunc(rows, order)
	at := -1
	if cur != nil {
		k := domain.Card{ID: cur.ID, ClosedAt: &cur.ClosedAt}
		at, _ = slices.BinarySearchFunc(rows, k, order)
		if at < len(rows) && order(rows[at], k) != 0 {
			at--
		}
	}
	out, remaining := pageOf(rows, at, page)
	return out, remaining, nil
}

func (f *fakeStore) blocked(id domain.ID) bool {
	for _, l := range f.links {
		if l.Kind == domain.LinkBlocks && l.ToCardID == id {
			if b, ok := f.cards[l.FromCardID]; ok && b.Status.Open() {
				return true
			}
		}
	}
	return false
}

// laneBusy reports whether another card in c's lane is working or a lock named
// like the lane is live at now. The caller holds the lock.
func (f *fakeStore) laneBusy(c *domain.Card, now time.Time) bool {
	if c.Lane == "" {
		return false
	}
	for _, l := range f.locks {
		if l.OrgID == c.OrgID && l.Name == c.Lane && !l.Expired(now) {
			return true
		}
	}
	for _, w := range f.cards {
		if w.ProjectID == c.ProjectID && w.Status == domain.StatusWorking && w.Lane == c.Lane {
			return true
		}
	}
	return false
}

func (f *fakeStore) CountOnBusyLanes(_ context.Context, projectID domain.ID, actor domain.ActorRef, now time.Time) ([]query.LaneCount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	counts := map[string]int{}
	for _, c := range f.cards {
		if c.ProjectID != projectID || c.Status != domain.StatusReady || c.Kind != domain.KindTask || !f.laneBusy(c, now) {
			continue
		}
		if _, claimed := f.claims[c.ID]; claimed || f.blocked(c.ID) || (c.Assignee != nil && *c.Assignee != actor) {
			continue
		}
		if c.ParentID != nil {
			if p, ok := f.cards[*c.ParentID]; ok && p.Status == domain.StatusDropped {
				continue
			}
		}
		counts[c.Lane]++
	}
	var out []query.LaneCount
	for _, lane := range slices.Sorted(maps.Keys(counts)) {
		out = append(out, query.LaneCount{Lane: lane, Cards: counts[lane]})
	}
	return out, nil
}

func (f *fakeStore) NextReady(_ context.Context, projectID domain.ID, actor domain.ActorRef, now time.Time) (domain.Card, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var best *domain.Card
	for _, c := range f.cards {
		if c.ProjectID != projectID || c.Status != domain.StatusReady || c.Kind != domain.KindTask {
			continue
		}
		if _, claimed := f.claims[c.ID]; claimed || f.blocked(c.ID) {
			continue
		}
		if c.Assignee != nil && *c.Assignee != actor {
			continue
		}
		if f.laneBusy(c, now) {
			continue
		}
		if c.ParentID != nil {
			if p, ok := f.cards[*c.ParentID]; ok && p.Status == domain.StatusDropped {
				continue
			}
		}
		if best == nil || c.Priority > best.Priority || (c.Priority == best.Priority && c.Number < best.Number) {
			best = c
		}
	}
	if best == nil {
		return domain.Card{}, notFound("ready card")
	}
	return *best, nil
}

func (f *fakeStore) SnapshotCardRevision(_ context.Context, cardID domain.ID, expected int64, revisionID domain.ID, by domain.ActorRef, at time.Time, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.cards[cardID]
	if !ok {
		return notFound("card")
	}
	if cur.Version != expected {
		return domain.ErrVersionConflict
	}
	f.cardRevs = append(f.cardRevs, domain.CardRevision{ID: revisionID, CardID: cardID, Version: cur.Version, UpdatedBy: by, CreatedAt: at, Title: cur.Title, Body: cur.Body, Page: f.cardPages[cardID]})
	return nil
}

// historyPage applies a history page to entries sorted newest first, as the
// store's LIMIT and before do.
func historyPage[T any](sorted []T, key func(T) int64, pg query.HistoryPage) []T {
	out := slices.DeleteFunc(sorted, func(t T) bool { return pg.Before != 0 && key(t) >= pg.Before })
	limit := pg.Limit
	if limit <= 0 {
		limit = 50
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (f *fakeStore) ListCardRevisions(_ context.Context, cardID domain.ID, pg query.HistoryPage) ([]domain.CardRevision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.CardRevision
	for _, r := range f.cardRevs {
		if r.CardID == cardID {
			r.Body, r.Page = "", ""
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b domain.CardRevision) int { return cmp.Compare(b.Version, a.Version) })
	return historyPage(out, func(r domain.CardRevision) int64 { return r.Version }, pg), nil
}

func (f *fakeStore) GetCardRevision(_ context.Context, cardID domain.ID, version int64) (domain.CardRevision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.cardRevs {
		if r.CardID == cardID && r.Version == version {
			return r, nil
		}
	}
	return domain.CardRevision{}, notFound("revision")
}

func (f *fakeStore) PutCardPage(_ context.Context, cardID domain.ID, html, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cardPages[cardID] = html
	return nil
}

func (f *fakeStore) DeleteCardPage(_ context.Context, cardID domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.cardPages, cardID)
	return nil
}

func (f *fakeStore) GetCardPage(_ context.Context, cardID domain.ID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if html, ok := f.cardPages[cardID]; ok {
		return html, nil
	}
	return "", notFound("card page")
}

func (f *fakeStore) PutNotePage(_ context.Context, noteID domain.ID, html, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notePages[noteID] = html
	return nil
}

func (f *fakeStore) DeleteNotePage(_ context.Context, noteID domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.notePages, noteID)
	return nil
}

func (f *fakeStore) GetNotePage(_ context.Context, noteID domain.ID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if html, ok := f.notePages[noteID]; ok {
		return html, nil
	}
	return "", notFound("note page")
}

func (f *fakeStore) UpdateCard(_ context.Context, c domain.Card, expected int64) (domain.Card, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.cards[c.ID]
	if !ok {
		return domain.Card{}, notFound("card")
	}
	if cur.Version != expected {
		return domain.Card{}, domain.ErrVersionConflict
	}
	c.Number, c.OrgID, c.ProjectID, c.Kind, c.CreatedBy, c.CreatedAt = cur.Number, cur.OrgID, cur.ProjectID, cur.Kind, cur.CreatedBy, cur.CreatedAt
	c.Version = expected + 1
	*cur = c
	return c, nil
}

func (f *fakeStore) ListOpenCardsAssignedTo(_ context.Context, orgID domain.ID, a domain.ActorRef) ([]domain.Card, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Card
	for _, c := range f.cards {
		if c.OrgID == orgID && c.Assignee != nil && *c.Assignee == a && c.Status.Open() {
			out = append(out, *c)
		}
	}
	slices.SortFunc(out, func(a, b domain.Card) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

func (f *fakeStore) ListFirstChildren(ctx context.Context, parentID domain.ID, limit int) ([]domain.Card, error) {
	all, err := f.ListChildren(ctx, parentID)
	if err != nil {
		return nil, err
	}
	return all[:min(limit, len(all))], nil
}

func (f *fakeStore) CountChildrenByStatus(ctx context.Context, parentID domain.ID) (map[domain.Status]int, error) {
	all, err := f.ListChildren(ctx, parentID)
	if err != nil {
		return nil, err
	}
	out := map[domain.Status]int{}
	for _, c := range all {
		out[c.Status]++
	}
	return out, nil
}

func (f *fakeStore) ListChildren(_ context.Context, parentID domain.ID) ([]domain.Card, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Card
	for _, c := range f.cards {
		if c.ParentID != nil && *c.ParentID == parentID {
			out = append(out, *c)
		}
	}
	slices.SortFunc(out, func(a, b domain.Card) int {
		return cmp.Or(cmp.Compare(b.Priority, a.Priority), cmp.Compare(a.Number, b.Number))
	})
	return out, nil
}

func (f *fakeStore) GetClaim(_ context.Context, cardID domain.ID) (domain.Claim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.claims[cardID]; ok {
		return *c, nil
	}
	return domain.Claim{}, notFound("claim")
}

func (f *fakeStore) GetClaims(_ context.Context, cardIDs []domain.ID) ([]domain.Claim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Claim
	for _, id := range cardIDs {
		if c, ok := f.claims[id]; ok {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (f *fakeStore) PutClaim(_ context.Context, c domain.Claim) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims[c.CardID] = &c
	return nil
}

func (f *fakeStore) DeleteClaim(_ context.Context, cardID domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.claims, cardID)
	delete(f.endingAnnounced, cardID)
	return nil
}

func (f *fakeStore) ListClaimsByActor(_ context.Context, orgID domain.ID, a domain.ActorRef) ([]domain.Claim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Claim
	for _, c := range f.claims {
		if c.OrgID == orgID && c.Actor == a {
			out = append(out, *c)
		}
	}
	slices.SortFunc(out, func(a, b domain.Claim) int { return cmp.Compare(a.CardID, b.CardID) })
	return out, nil
}

func (f *fakeStore) RenewClaims(_ context.Context, orgID domain.ID, a domain.ActorRef, session string, now time.Time) ([]domain.Claim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Claim
	for _, c := range f.claims {
		if c.OrgID != orgID || !c.OwnedBy(a, session) || c.Expired(now) {
			continue
		}
		project := f.projects[c.ProjectID].Lease
		if !c.ExpiresAt.Add(-c.LeaseOr(project) / 2).Before(now) {
			continue // first half of the lease, boundary included: untouched
		}
		c.Renew(now, project, 0)
		out = append(out, *c)
	}
	return out, nil
}

func (f *fakeStore) ListEndingClaims(_ context.Context, now time.Time, limit int) ([]domain.Claim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Claim
	for id, c := range f.claims {
		due := !c.LastCallAt(f.projects[c.ProjectID].Lease).After(now)
		if !c.Expired(now) && due && !f.endingAnnounced[id].Equal(c.ExpiresAt) {
			out = append(out, *c)
		}
	}
	slices.SortFunc(out, func(a, b domain.Claim) int { return a.ExpiresAt.Compare(b.ExpiresAt) })
	return out[:min(len(out), limit)], nil
}

func (f *fakeStore) MarkClaimEnding(_ context.Context, cardID domain.ID, expiresAt time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.claims[cardID]
	if !ok || !c.ExpiresAt.Equal(expiresAt) || f.endingAnnounced[cardID].Equal(expiresAt) {
		return false, nil
	}
	f.endingAnnounced[cardID] = expiresAt
	return true, nil
}

func (f *fakeStore) ListExpiredClaims(_ context.Context, before time.Time, limit int) ([]domain.Claim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Claim
	for _, c := range f.claims {
		if !c.ExpiresAt.After(before) && len(out) < limit {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (f *fakeStore) LockExpiredClaim(_ context.Context, cardID domain.ID, before time.Time) (domain.Claim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.claims[cardID]; ok && !c.ExpiresAt.After(before) {
		return *c, nil
	}
	return domain.Claim{}, notFound("expired claim")
}

func (f *fakeStore) GetLock(_ context.Context, orgID domain.ID, name string) (domain.Lock, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.locks {
		if l.OrgID == orgID && l.Name == name {
			return *l, nil
		}
	}
	return domain.Lock{}, notFound("lock")
}

func (f *fakeStore) InsertLock(_ context.Context, l domain.Lock) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, x := range f.locks {
		if x.OrgID == l.OrgID && x.Name == l.Name {
			return domain.ErrLockHeld
		}
	}
	f.locks[l.ID] = &l
	return nil
}

func (f *fakeStore) UpdateLockHolder(_ context.Context, l domain.Lock) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.locks[l.ID]
	if !ok {
		return notFound("lock")
	}
	cur.Holder, cur.Session, cur.Purpose, cur.CardID, cur.AcquiredAt, cur.ExpiresAt = l.Holder, l.Session, l.Purpose, l.CardID, l.AcquiredAt, l.ExpiresAt
	return nil
}

func (f *fakeStore) RenewLock(_ context.Context, id domain.ID, expiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.locks[id]
	if !ok {
		return notFound("lock")
	}
	cur.ExpiresAt = expiresAt
	return nil
}

func (f *fakeStore) DeleteLock(_ context.Context, id domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.locks[id]; !ok {
		return notFound("lock")
	}
	delete(f.locks, id)
	return nil
}

func (f *fakeStore) ListLocks(_ context.Context, orgID domain.ID) ([]domain.Lock, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Lock
	for _, l := range f.locks {
		if l.OrgID == orgID {
			out = append(out, *l)
		}
	}
	slices.SortFunc(out, func(a, b domain.Lock) int { return cmp.Compare(a.Name, b.Name) })
	return out, nil
}

func (f *fakeStore) ListLiveLocks(ctx context.Context, orgID domain.ID, now time.Time, pg query.Page, after string) ([]domain.Lock, int, error) {
	all, err := f.ListLocks(ctx, orgID)
	if err != nil {
		return nil, 0, err
	}
	live := slices.DeleteFunc(all, func(l domain.Lock) bool { return l.Expired(now) })
	items, remaining := fakePage(live, pg.Limit, func(l domain.Lock) bool { return l.Name > after })
	return items, remaining, nil
}

func (f *fakeStore) ListExpiredLocks(_ context.Context, before time.Time, limit int) ([]domain.Lock, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Lock
	for _, l := range f.locks {
		if !l.ExpiresAt.After(before) {
			out = append(out, *l)
		}
	}
	slices.SortFunc(out, func(a, b domain.Lock) int { return cmp.Or(a.ExpiresAt.Compare(b.ExpiresAt), cmp.Compare(a.ID, b.ID)) })
	return out[:min(len(out), limit)], nil
}

func (f *fakeStore) AddCardLink(_ context.Context, l domain.CardLink) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, x := range f.links {
		if x.FromCardID == l.FromCardID && x.ToCardID == l.ToCardID && x.Kind == l.Kind {
			return nil
		}
	}
	f.links = append(f.links, l)
	return nil
}

func (f *fakeStore) RemoveCardLink(_ context.Context, fromID, toID domain.ID, kind domain.CardLinkKind) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.links)
	f.links = slices.DeleteFunc(f.links, func(x domain.CardLink) bool {
		return x.FromCardID == fromID && x.ToCardID == toID && x.Kind == kind
	})
	if len(f.links) == n {
		return notFound("link")
	}
	return nil
}

func (f *fakeStore) ListCardLinks(_ context.Context, cardID domain.ID) ([]domain.CardLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.CardLink
	for _, l := range f.links {
		if l.FromCardID == cardID || l.ToCardID == cardID {
			out = append(out, l)
		}
	}
	return out, nil
}

func (f *fakeStore) ListBlockers(_ context.Context, cardID domain.ID) ([]domain.Card, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Card
	for _, l := range f.links {
		if l.Kind == domain.LinkBlocks && l.ToCardID == cardID {
			if b, ok := f.cards[l.FromCardID]; ok && b.Status.Open() {
				out = append(out, *b)
			}
		}
	}
	slices.SortFunc(out, func(a, b domain.Card) int { return cmp.Compare(a.Number, b.Number) })
	return out, nil
}

func (f *fakeStore) AddComment(_ context.Context, h domain.Comment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments = append(f.comments, h)
	return nil
}

func (f *fakeStore) GetComment(_ context.Context, id domain.ID) (domain.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.comments {
		if h.ID == id {
			return h, nil
		}
	}
	return domain.Comment{}, notFound("comment")
}

type requestKeyID struct {
	actor domain.ActorRef
	key   string
}

func (f *fakeStore) ClaimRequestKey(_ context.Context, k domain.RequestKey) (domain.RequestKey, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := requestKeyID{k.Actor, k.Key}
	if have, ok := f.requestKeys[id]; ok {
		return have, false, nil
	}
	f.requestKeys[id] = k
	return k, true, nil
}

func (f *fakeStore) LatestComment(ctx context.Context, cardID domain.ID) (domain.Comment, error) {
	hs, _ := f.ListComments(ctx, cardID, query.HistoryPage{Limit: 1})
	if len(hs) == 0 {
		return domain.Comment{}, notFound("comment")
	}
	return hs[0], nil
}

func (f *fakeStore) ListComments(_ context.Context, cardID domain.ID, pg query.HistoryPage) ([]domain.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Comment
	for _, h := range f.comments {
		if h.CardID == cardID {
			out = append(out, h)
		}
	}
	slices.SortFunc(out, func(a, b domain.Comment) int { return cmp.Compare(b.ID, a.ID) })
	return historyPage(out, func(c domain.Comment) int64 { return int64(c.ID) }, pg), nil
}

func (f *fakeStore) MarkCardSeen(_ context.Context, r domain.CardRead) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := memberKey(r.CardID, r.Actor)
	if cur, ok := f.reads[k]; ok && cur.SeenID > r.SeenID {
		r.SeenID = cur.SeenID
	}
	f.reads[k] = r
	return nil
}

func (f *fakeStore) ListReplies(_ context.Context, actor domain.ActorRef, projectID, before domain.ID, limit int32) ([]domain.Reply, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	newest := map[domain.ID]domain.Reply{}
	for _, r := range f.reads {
		if r.Actor != actor || r.ProjectID != projectID {
			continue
		}
		for _, h := range f.comments {
			if h.CardID != r.CardID || h.ID <= r.SeenID || h.Actor == actor {
				continue
			}
			cur := newest[h.CardID]
			cur.Unseen++
			if h.ID > cur.Comment.ID {
				cur.Comment = h
			}
			newest[h.CardID] = cur
		}
	}
	out := slices.Collect(maps.Values(newest))
	if before != 0 {
		out = slices.DeleteFunc(out, func(r domain.Reply) bool { return r.Comment.ID >= before })
	}
	slices.SortFunc(out, func(a, b domain.Reply) int { return cmp.Compare(b.Comment.ID, a.Comment.ID) })
	remaining := 0
	if int(limit) < len(out) {
		remaining = min(len(out)-int(limit), query.RemainingCap)
		out = out[:limit]
	}
	return out, remaining, nil
}

func (f *fakeStore) CreateNote(_ context.Context, n domain.Note) (domain.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n.Version = 1
	f.notes[n.ID] = &n
	return n, nil
}

func (f *fakeStore) GetNote(_ context.Context, id domain.ID) (domain.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.notes[id]; ok {
		return *n, nil
	}
	return domain.Note{}, notFound("note")
}

func (f *fakeStore) GetNotes(_ context.Context, ids []domain.ID) ([]domain.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Note
	for _, id := range ids {
		if n, ok := f.notes[id]; ok {
			out = append(out, *n)
		}
	}
	return out, nil
}

func (f *fakeStore) GetNoteBySlug(_ context.Context, projectID domain.ID, slug string) (domain.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, n := range f.notes {
		if n.ProjectID == projectID && n.Slug == slug {
			return *n, nil
		}
	}
	return domain.Note{}, notFound("note")
}

func noteOrder(a, b domain.Note) int {
	return cmp.Or(b.UpdatedAt.Compare(a.UpdatedAt), cmp.Compare(b.ID, a.ID))
}

func (f *fakeStore) ListNotes(_ context.Context, orgID domain.ID, fl query.NoteFilter) ([]domain.Note, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []domain.Note
	for _, n := range f.notes {
		if n.OrgID != orgID {
			continue
		}
		if (fl.ProjectID != nil && n.ProjectID != *fl.ProjectID) || (fl.ProjectIDs != nil && !slices.Contains(fl.ProjectIDs, n.ProjectID)) {
			continue
		}
		if !fl.Archived && n.Status != domain.RecordActive {
			continue
		}
		if fl.Author != nil && n.CreatedBy.ID != *fl.Author {
			continue
		}
		if !fl.Created.Contains(n.CreatedAt) || !fl.Updated.Contains(n.UpdatedAt) {
			continue
		}
		if fl.Query != "" && !textMatch(fl.Query, n.Title, n.Body) {
			continue
		}
		rows = append(rows, *n)
	}
	slices.SortFunc(rows, noteOrder)
	if fl.Query != "" {
		if fl.Cursor != nil {
			at := slices.IndexFunc(rows, func(n domain.Note) bool { return n.ID == fl.Cursor.ID })
			rows = rows[at+1:]
		}
		end := min(len(rows), cmp.Or(fl.Page.Limit, 50))
		return rows[:end], min(len(rows)-end, query.RemainingCap), nil
	}
	at := -1
	if fl.Cursor != nil {
		k := domain.Note{UpdatedAt: fl.Cursor.UpdatedAt, ID: fl.Cursor.ID}
		i, found := slices.BinarySearchFunc(rows, k, noteOrder)
		at = i
		if !found {
			at = i - 1
		}
	}
	out, remaining := pageOf(rows, at, fl.Page)
	return out, remaining, nil
}

func (f *fakeStore) UpdateNote(_ context.Context, n domain.Note, expected int64, revisionID domain.ID, _ bool) (domain.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.notes[n.ID]
	if !ok {
		return domain.Note{}, notFound("note")
	}
	if cur.Version != expected {
		return domain.Note{}, domain.ErrVersionConflict
	}
	f.revisions = append(f.revisions, domain.NoteRevision{ID: revisionID, NoteID: cur.ID, Version: cur.Version, UpdatedBy: cur.UpdatedBy, Title: cur.Title, Body: cur.Body, Page: f.notePages[cur.ID]})
	cur.PageBytes = n.PageBytes
	cur.Title, cur.Body, cur.Status, cur.UpdatedBy, cur.UpdatedAt = n.Title, n.Body, n.Status, n.UpdatedBy, n.UpdatedAt
	cur.Version = expected + 1
	return *cur, nil
}

func (f *fakeStore) ListNoteRevisions(_ context.Context, noteID domain.ID, pg query.HistoryPage) ([]domain.NoteRevision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.NoteRevision
	for _, r := range f.revisions {
		if r.NoteID == noteID {
			r.Body, r.Page = "", ""
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b domain.NoteRevision) int { return cmp.Compare(b.Version, a.Version) })
	return historyPage(out, func(r domain.NoteRevision) int64 { return r.Version }, pg), nil
}

func (f *fakeStore) GetNoteRevision(_ context.Context, noteID domain.ID, version int64) (domain.NoteRevision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.revisions {
		if r.NoteID == noteID && r.Version == version {
			return r, nil
		}
	}
	return domain.NoteRevision{}, notFound("revision")
}

func (f *fakeStore) ReplaceRefs(_ context.Context, sourceID domain.ID, links []domain.RefLink) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refs = slices.DeleteFunc(f.refs, func(r domain.RefLink) bool { return r.SourceID == sourceID })
	f.refs = append(f.refs, links...)
	return nil
}

func (f *fakeStore) Backlinks(_ context.Context, targetID domain.ID) ([]domain.RefLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.RefLink
	for _, r := range f.refs {
		if r.TargetID == targetID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) AppendEvent(_ context.Context, e domain.Event) error {
	if f.onAppend != nil {
		if err := f.onAppend(e); err != nil {
			return err
		}
	}
	f.mu.Lock()
	f.lastXid++
	e.Xid = f.lastXid
	f.events = append(f.events, e)
	listeners := slices.Clone(f.listeners)
	f.mu.Unlock()
	for _, fn := range listeners {
		fn(e.OrgID)
	}
	return nil
}

func (f *fakeStore) eventMatches(e domain.Event, orgID domain.ID, fl query.EventFilter) bool {
	if e.OrgID != orgID {
		return false
	}
	if fl.ProjectIDs != nil && e.ProjectID != nil && !slices.Contains(fl.ProjectIDs, *e.ProjectID) {
		return false
	}
	if fl.ExcludeActor != nil && e.Actor == *fl.ExcludeActor && e.Session == fl.ExcludeSession {
		return false
	}
	return fl.Limited.Reaches(e)
}

func (f *fakeStore) ListEvents(_ context.Context, orgID domain.ID, fl query.EventFilter) ([]domain.Event, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	limit := cmp.Or(fl.Limit, 50)
	after, aok := f.position(orgID, fl.After)
	before, bok := f.position(orgID, fl.Before)
	if !aok || !bok {
		return nil, 0, fmt.Errorf("list events: cursor: %w", domain.ErrNotFound)
	}
	var rows []domain.Event
	for _, e := range f.events {
		if !f.eventMatches(e, orgID, fl) {
			continue
		}
		if !before.IsZero() {
			if comparePositions(positionOf(e), before) < 0 {
				rows = append(rows, e)
			}
			continue
		}
		if comparePositions(positionOf(e), after) > 0 {
			rows = append(rows, e)
		}
	}
	slices.SortFunc(rows, func(a, b domain.Event) int { return comparePositions(positionOf(a), positionOf(b)) })
	if !before.IsZero() {
		start := max(0, len(rows)-limit)
		return rows[start:], min(start, query.RemainingCap), nil
	}
	end := min(len(rows), limit)
	return rows[:end], min(len(rows)-end, query.RemainingCap), nil
}

func (f *fakeStore) CountEventsAfter(_ context.Context, orgID domain.ID, fl query.EventFilter) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	after, ok := f.position(orgID, fl.After)
	if !ok {
		return 0, fmt.Errorf("count events: cursor: %w", domain.ErrNotFound)
	}
	n := 0
	for _, e := range f.events {
		if f.eventMatches(e, orgID, fl) && comparePositions(positionOf(e), after) > 0 {
			n++
			if fl.Limit > 0 && n >= fl.Limit {
				break
			}
		}
	}
	return n, nil
}

func (f *fakeStore) ListEventsForSubject(_ context.Context, orgID domain.ID, subj domain.Subject, limit int) ([]domain.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Event
	for i := len(f.events) - 1; i >= 0 && len(out) < limit; i-- {
		if e := f.events[i]; e.OrgID == orgID && e.Subject.Type == subj.Type && e.Subject.ID == subj.ID && !e.Type.Limited() {
			out = append(out, f.events[i])
		}
	}
	return out, nil
}

func (f *fakeStore) CardsCreatedInSession(_ context.Context, orgID domain.ID, cardIDs []domain.ID, actor domain.ActorRef, session string) ([]domain.ID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.ID
	for _, e := range f.events {
		if e.OrgID == orgID && e.Type == domain.EventCardCreated && e.Subject.Type == domain.SubjectCard &&
			e.Actor == actor && e.Session == session && slices.Contains(cardIDs, e.Subject.ID) {
			out = append(out, e.Subject.ID)
		}
	}
	return out, nil
}

func (f *fakeStore) CardCreatedSession(_ context.Context, orgID, cardID domain.ID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.events {
		if e.OrgID == orgID && e.Type == domain.EventCardCreated && e.Subject.Type == domain.SubjectCard && e.Subject.ID == cardID {
			return e.Session, nil
		}
	}
	return "", domain.ErrNotFound
}

func (f *fakeStore) CardParkedFrom(_ context.Context, orgID, cardID domain.ID) (domain.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range slices.Backward(f.events) {

		if e.OrgID != orgID || e.Type != domain.EventCardStatusChanged || e.Subject.Type != domain.SubjectCard || e.Subject.ID != cardID {
			continue
		}
		var p struct{ From, To string }
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return 0, err
		}
		if p.To == domain.StatusWaiting.String() {
			return domain.ParseStatus(p.From)
		}
	}
	return 0, domain.ErrNotFound
}

func (f *fakeStore) LatestEventPosition(_ context.Context, orgID domain.ID, fl query.EventFilter) (query.EventPosition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl.ExcludeActor = nil
	var latest query.EventPosition
	for _, e := range f.events {
		if f.eventMatches(e, orgID, fl) && comparePositions(positionOf(e), latest) > 0 {
			latest = positionOf(e)
		}
	}
	return latest, nil
}

func (f *fakeStore) Search(_ context.Context, orgID domain.ID, projectIDs []domain.ID, text string, limit int, after *query.SearchCursor) ([]query.SearchHit, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []query.SearchHit
	inScope := func(pid *domain.ID) bool {
		return projectIDs == nil || (pid != nil && slices.Contains(projectIDs, *pid))
	}
	for _, c := range f.cards {
		if c.OrgID == orgID && inScope(&c.ProjectID) && textMatch(text, c.Title, c.Body) {
			pid := c.ProjectID
			out = append(out, query.SearchHit{Type: domain.SubjectCard, ID: c.ID, Kind: c.Kind, Number: c.Number, ProjectID: &pid, Title: c.Title, Status: c.Status.String(), Rank: 1})
		}
	}
	for _, n := range f.notes {
		pid := n.ProjectID
		if n.OrgID == orgID && inScope(&pid) && textMatch(text, n.Title, n.Body) {
			out = append(out, query.SearchHit{Type: domain.SubjectNote, ID: n.ID, Slug: n.Slug, ProjectID: &pid, Title: n.Title, Rank: 1})
		}
	}
	order := func(a, b query.SearchCursor) int {
		return cmp.Or(cmp.Compare(b.Rank, a.Rank), cmp.Compare(a.Type, b.Type), cmp.Compare(a.ID, b.ID))
	}
	key := func(h query.SearchHit) query.SearchCursor {
		return query.SearchCursor{Rank: h.Rank, Type: h.Type, ID: h.ID}
	}
	slices.SortFunc(out, func(a, b query.SearchHit) int { return order(key(a), key(b)) })
	if after != nil {
		out = slices.DeleteFunc(out, func(h query.SearchHit) bool { return order(key(h), *after) <= 0 })
	}
	remaining := 0
	if len(out) > limit && limit > 0 {
		remaining = min(len(out)-limit, query.RemainingCap)
		out = out[:limit]
	}
	return out, remaining, nil
}

func (f *fakeStore) SearchRank(context.Context, domain.SubjectType, domain.ID, string) (float64, error) {
	return 1, nil
}

var _ Store = (*fakeStore)(nil)
var _ Notifier = (*fakeStore)(nil)

// fakePage cuts one page from rows already in list order: the first rows past
// the cursor, then how many follow, capped like the store's count.
func fakePage[T any](rows []T, limit int, past func(T) bool) ([]T, int) {
	start := slices.IndexFunc(rows, past)
	if start < 0 {
		return nil, 0
	}
	rows = rows[start:]
	if limit <= 0 {
		limit = apitypes.DefaultPageSize
	}
	n := min(limit, len(rows))
	return rows[:n], min(len(rows)-n, query.RemainingCap)
}

func compareMembers(ar domain.Role, ac time.Time, a domain.ActorRef, br domain.Role, bc time.Time, b domain.ActorRef) int {
	return cmp.Or(cmp.Compare(br, ar), ac.Compare(bc), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID))
}

func memberAfter(role domain.Role, created time.Time, a domain.ActorRef, c *query.MemberCursor) bool {
	return c == nil || compareMembers(role, created, a, c.Role, c.CreatedAt, c.Actor) > 0
}

func (f *fakeStore) ListProjectLinks(_ context.Context, projectID domain.ID, visible []domain.ID, limit int, after *query.LinkCursor) ([]query.LinkEdge, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []query.LinkEdge
	for _, l := range f.links {
		from, to := f.cards[l.FromCardID], f.cards[l.ToCardID]
		if from == nil || to == nil || (from.ProjectID != projectID && to.ProjectID != projectID) {
			continue
		}
		if visible != nil && (!slices.Contains(visible, from.ProjectID) || !slices.Contains(visible, to.ProjectID)) {
			continue
		}
		rows = append(rows, query.LinkEdge{
			FromID: from.ID, ToID: to.ID, FromNumber: from.Number, ToNumber: to.Number,
			FromProject: from.ProjectID, ToProject: to.ProjectID, Kind: l.Kind,
		})
	}
	order := func(a, b query.LinkEdge) int {
		return cmp.Or(cmp.Compare(a.FromID, b.FromID), cmp.Compare(a.ToID, b.ToID), cmp.Compare(a.Kind, b.Kind))
	}
	slices.SortFunc(rows, order)
	at := -1
	if after != nil {
		k := query.LinkEdge{FromID: after.From, ToID: after.To, Kind: after.Kind}
		at = sort.Search(len(rows), func(i int) bool { return order(rows[i], k) > 0 }) - 1
	}
	out, remaining := pageOf(rows, at, query.Page{Limit: limit})
	return out, remaining, nil
}

func (f *fakeStore) ListLiveClaims(_ context.Context, orgID domain.ID, projects []domain.ID, now time.Time, limit int, after *query.LiveClaimCursor) ([]query.LiveClaim, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []query.LiveClaim
	for _, c := range f.claims {
		card := f.cards[c.CardID]
		if c.OrgID != orgID || card == nil || c.Expired(now) || (projects != nil && !slices.Contains(projects, c.ProjectID)) {
			continue
		}
		rows = append(rows, query.LiveClaim{
			CardID: c.CardID, ProjectID: c.ProjectID, Number: card.Number, Title: card.Title, Kind: card.Kind, Status: card.Status,
			Actor: c.Actor, Session: c.Session, ClaimedAt: c.CreatedAt, ExpiresAt: c.ExpiresAt,
		})
	}
	order := func(a, b query.LiveClaim) int {
		return cmp.Or(cmp.Compare(a.Actor.Kind, b.Actor.Kind), cmp.Compare(a.Actor.ID, b.Actor.ID), cmp.Compare(a.Session, b.Session), cmp.Compare(a.CardID, b.CardID))
	}
	slices.SortFunc(rows, order)
	at := -1
	if after != nil {
		k := query.LiveClaim{Actor: after.Actor, Session: after.Session, CardID: after.Card}
		at = sort.Search(len(rows), func(i int) bool { return order(rows[i], k) > 0 }) - 1
	}
	out, remaining := pageOf(rows, at, query.Page{Limit: limit})
	return out, remaining, nil
}

func (f *fakeStore) ListProjectMentions(_ context.Context, projectID domain.ID, visible []domain.ID, limit int, after *query.MentionCursor) ([]query.MentionEdge, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []query.MentionEdge
	for _, r := range f.refs {
		card, note := f.cards[r.SourceID], f.notes[r.TargetID]
		if r.SourceType != domain.SubjectCard || r.TargetType != domain.SubjectNote || card == nil || note == nil || card.ProjectID != projectID {
			continue
		}
		if visible != nil && !slices.Contains(visible, note.ProjectID) {
			continue
		}
		rows = append(rows, query.MentionEdge{CardID: card.ID, CardNumber: card.Number, NoteID: note.ID, NoteSlug: note.Slug})
	}
	order := func(a, b query.MentionEdge) int {
		return cmp.Or(cmp.Compare(a.CardID, b.CardID), cmp.Compare(a.NoteID, b.NoteID))
	}
	slices.SortFunc(rows, order)
	at := -1
	if after != nil {
		k := query.MentionEdge{CardID: after.Card, NoteID: after.Note}
		at = sort.Search(len(rows), func(i int) bool { return order(rows[i], k) > 0 }) - 1
	}
	out, remaining := pageOf(rows, at, query.Page{Limit: limit})
	return out, remaining, nil
}

// groupOf is the map group of a card: its story, or the lane of a card
// outside any story. The caller holds the lock.
func (f *fakeStore) groupOf(c *domain.Card) query.MapGroupRef {
	switch {
	case c.ParentID != nil:
		return query.MapGroupRef{Kind: query.GroupStory, Number: f.cards[*c.ParentID].Number}
	case c.Kind == domain.KindStory:
		return query.MapGroupRef{Kind: query.GroupStory, Number: c.Number}
	}
	return query.MapGroupRef{Kind: query.GroupLane, Lane: c.Lane}
}

func (f *fakeStore) ListMapGroups(_ context.Context, projectID domain.ID, limit int, after *query.MapGroupRef) ([]query.MapGroup, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	byRef := map[query.MapGroupRef]*query.MapGroup{}
	group := func(ref query.MapGroupRef) *query.MapGroup {
		if byRef[ref] == nil {
			byRef[ref] = &query.MapGroup{Ref: ref, Counts: query.CardCounts{ByStatus: map[domain.Status]int{}}}
		}
		return byRef[ref]
	}
	for _, c := range f.cards {
		if c.ProjectID != projectID {
			continue
		}
		if c.Kind == domain.KindStory {
			g := group(query.MapGroupRef{Kind: query.GroupStory, Number: c.Number})
			g.Title, g.StoryStatus = c.Title, c.Status
		}
		if c.Kind == domain.KindStory && c.ParentID == nil {
			continue
		}
		g := group(f.groupOf(c))
		g.Counts.ByStatus[c.Status]++
		if c.Kind == domain.KindQuestion && c.Status.Open() {
			g.Counts.OpenQuestions++
		}
	}
	var rows []query.MapGroup
	for _, g := range byRef {
		rows = append(rows, *g)
	}
	slices.SortFunc(rows, func(a, b query.MapGroup) int {
		switch {
		case a.Ref.Less(b.Ref):
			return -1
		case b.Ref.Less(a.Ref):
			return 1
		}
		return 0
	})
	at := -1
	if after != nil {
		at = sort.Search(len(rows), func(i int) bool { return after.Less(rows[i].Ref) }) - 1
	}
	out, remaining := pageOf(rows, at, query.Page{Limit: limit})
	return out, remaining, nil
}

func (f *fakeStore) ListMapGroupLinks(_ context.Context, projectID domain.ID, limit int, after *query.MapGroupLinkCursor) ([]query.MapGroupLink, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	type key struct {
		from, to query.MapGroupRef
		kind     domain.CardLinkKind
	}
	counts := map[key]int{}
	for _, l := range f.links {
		from, to := f.cards[l.FromCardID], f.cards[l.ToCardID]
		if from == nil || to == nil || from.ProjectID != projectID || to.ProjectID != projectID {
			continue
		}
		if fg, tg := f.groupOf(from), f.groupOf(to); fg != tg {
			counts[key{fg, tg, l.Kind}]++
		}
	}
	var rows []query.MapGroupLink
	for k, n := range counts {
		rows = append(rows, query.MapGroupLink{From: k.from, To: k.to, Kind: k.kind, Count: n})
	}
	before := func(a, b query.MapGroupLinkCursor) bool {
		switch {
		case a.From != b.From:
			return a.From.Less(b.From)
		case a.To != b.To:
			return a.To.Less(b.To)
		}
		return a.Kind < b.Kind
	}
	cursorOf := func(l query.MapGroupLink) query.MapGroupLinkCursor {
		return query.MapGroupLinkCursor{From: l.From, To: l.To, Kind: l.Kind}
	}
	slices.SortFunc(rows, func(a, b query.MapGroupLink) int {
		switch {
		case before(cursorOf(a), cursorOf(b)):
			return -1
		case before(cursorOf(b), cursorOf(a)):
			return 1
		}
		return 0
	})
	at := -1
	if after != nil {
		at = sort.Search(len(rows), func(i int) bool { return before(*after, cursorOf(rows[i])) }) - 1
	}
	out, remaining := pageOf(rows, at, query.Page{Limit: limit})
	return out, remaining, nil
}

func (f *fakeStore) SubscribeCard(_ context.Context, w domain.CardSubscription) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.ContainsFunc(f.subscriptions, func(o domain.CardSubscription) bool {
		return o.CardID == w.CardID && o.AgentID == w.AgentID && o.Session == w.Session
	}) {
		f.subscriptions = append(f.subscriptions, w)
	}
	return nil
}

func (f *fakeStore) UnsubscribeCard(_ context.Context, cardID, agentID domain.ID, session string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subscriptions = slices.DeleteFunc(f.subscriptions, func(w domain.CardSubscription) bool {
		return w.CardID == cardID && w.AgentID == agentID && w.Session == session
	})
	return nil
}

func (f *fakeStore) ListCardSubscriptions(_ context.Context, agentID domain.ID, session string) ([]query.SubscribedCard, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []query.SubscribedCard
	for _, w := range f.subscriptions {
		c := f.cards[w.CardID]
		if w.AgentID != agentID || w.Session != session || c == nil || c.Status.Terminal() {
			continue
		}
		out = append(out, query.SubscribedCard{CardID: c.ID, ProjectID: c.ProjectID, ProjectSlug: f.projects[c.ProjectID].Slug, Number: c.Number, Title: c.Title, Status: c.Status, SubscribedAt: w.CreatedAt})
	}
	slices.SortFunc(out, func(a, b query.SubscribedCard) int {
		return cmp.Or(a.SubscribedAt.Compare(b.SubscribedAt), cmp.Compare(a.CardID, b.CardID))
	})
	return out, nil
}

func (f *fakeStore) SubscribedCardsAmong(_ context.Context, orgID domain.ID, cardIDs []domain.ID, agentID domain.ID, session string) ([]domain.ID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.ID
	for _, w := range f.subscriptions {
		if w.OrgID == orgID && w.AgentID == agentID && w.Session == session && slices.Contains(cardIDs, w.CardID) && !slices.Contains(out, w.CardID) {
			out = append(out, w.CardID)
		}
	}
	return out, nil
}

func (f *fakeStore) DeleteCardSubscriptionsOfLostChats(_ context.Context, seenSince time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	before := len(f.subscriptions)
	f.subscriptions = slices.DeleteFunc(f.subscriptions, func(w domain.CardSubscription) bool {
		s := f.sessions[sessionKey{w.AgentID, w.Session}]
		return s == nil || s.LastSeenAt.Before(seenSince)
	})
	return before - len(f.subscriptions), nil
}

func (f *fakeStore) DeleteCardSubscriptionsOfClosedCards(_ context.Context, closedBefore time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	before := len(f.subscriptions)
	f.subscriptions = slices.DeleteFunc(f.subscriptions, func(w domain.CardSubscription) bool {
		c := f.cards[w.CardID]
		if c == nil {
			return true
		}
		closedAt := c.UpdatedAt
		if c.ClosedAt != nil {
			closedAt = *c.ClosedAt
		}
		return c.Status.Terminal() && closedAt.Before(closedBefore)
	})
	return before - len(f.subscriptions), nil
}
