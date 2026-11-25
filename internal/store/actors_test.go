//go:build integration

package store

import (
	"context"
	"errors"
	"maps"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

func TestOrgSettingsRoundTrip(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.org.Settings = domain.OrgSettings{AgentApproval: "admin", TokenTTLDefault: "48h"}
	e.org.Name = "Renamed"
	updated, err := e.st.UpdateOrg(ctx, e.org)
	if err != nil || updated.Name != "Renamed" || updated.Settings.AgentApproval != "admin" || updated.Settings.TokenTTLDefault != "48h" {
		t.Fatalf("updated = %+v, %v", updated, err)
	}
	got, err := e.st.GetOrg(ctx, e.org.ID)
	if err != nil || got.Settings != updated.Settings {
		t.Errorf("get = %+v, %v", got, err)
	}
	orgs, err := e.st.ListOrgs(ctx)
	if err != nil || len(orgs) == 0 {
		t.Errorf("ListOrgs = %d, %v", len(orgs), err)
	}
}

func TestMembershipsUpsertAndListAlive(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	m := domain.Membership{OrgID: e.org.ID, Actor: e.user, Role: domain.RoleMember, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0}
	if _, err := e.st.PutMembership(ctx, m); err != nil {
		t.Fatal(err)
	}
	m.Role, m.GrantedBy, m.UpdatedAt = domain.RoleAdmin, &e.user.ID, t0.Add(time.Hour)
	if _, err := e.st.PutMembership(ctx, m); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := e.st.GetMembership(ctx, e.org.ID, e.user)
	if err != nil || got.Role != domain.RoleAdmin || got.GrantedBy == nil {
		t.Errorf("membership = %+v, %v; want admin after upsert", got, err)
	}
	if !got.CreatedAt.Equal(t0) || !got.UpdatedAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("membership times = %v, %v; want created %v kept and updated %v", got.CreatedAt, got.UpdatedAt, t0, t0.Add(time.Hour))
	}
	gone := domain.Membership{OrgID: e.org.ID, Actor: e.agent, Role: domain.RoleMember, Status: domain.RecordRemoved, CreatedAt: t0, UpdatedAt: t0}
	if _, err := e.st.PutMembership(ctx, gone); err != nil {
		t.Fatal(err)
	}
	alive, _, err := e.st.ListMemberships(ctx, e.org.ID, query.MemberFilter{})
	if err != nil || len(alive) != 1 || alive[0].Actor != e.user {
		t.Errorf("alive memberships = %+v, %v; want the user only", alive, err)
	}
	all, _, err := e.st.ListMemberships(ctx, e.org.ID, query.MemberFilter{All: true})
	if err != nil || len(all) != 2 {
		t.Errorf("all memberships = %d, %v; want 2", len(all), err)
	}
	forActor, err := e.st.ListMembershipsForActor(ctx, e.agent)
	if err != nil || len(forActor) != 0 {
		t.Errorf("removed agent memberships = %+v, want none", forActor)
	}
	if _, err := e.st.GetMembership(ctx, e.org.ID, domain.ActorRef{Kind: domain.ActorUser, ID: 1}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown membership err = %v", err)
	}
}

func TestOpenMembershipsForActorAreTheAliveAndThePendingOnes(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	want := map[domain.ID]domain.RecordStatus{}
	for i, status := range []domain.RecordStatus{domain.RecordActive, domain.RecordPending, domain.RecordRemoved} {
		org, err := e.st.CreateOrg(ctx, domain.Org{ID: gen.New(), Name: "Open", Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
		if err != nil {
			t.Fatal(err)
		}
		at := t0.Add(time.Duration(i) * time.Minute)
		if _, err := e.st.PutMembership(ctx, domain.Membership{OrgID: org.ID, Actor: e.agent, Role: domain.RoleMember, Status: status, CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
		if status != domain.RecordRemoved {
			want[org.ID] = status
		}
	}
	got, err := e.st.ListOpenMembershipsForActor(ctx, e.agent)
	if err != nil {
		t.Fatal(err)
	}
	open := map[domain.ID]domain.RecordStatus{}
	for _, m := range got {
		open[m.OrgID] = m.Status
	}
	if !maps.Equal(open, want) {
		t.Errorf("open memberships = %v, want %v (the removed one left out)", open, want)
	}
	if other, err := e.st.ListOpenMembershipsForActor(ctx, e.user); err != nil || len(other) != 0 {
		t.Errorf("another actor's open memberships = %+v, %v; want none", other, err)
	}
}

func TestProjectMembersForActorAreScopedToTheOrg(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	other := e.newProject(t, "other")
	for _, p := range []domain.Project{e.project, other} {
		pm := domain.ProjectMember{ProjectID: p.ID, Actor: e.agent, Role: domain.RoleReviewer, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0}
		if _, err := e.st.PutProjectMember(ctx, pm); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.st.ListProjectMembersForActor(ctx, e.org.ID, e.agent)
	if err != nil || len(got) != 2 {
		t.Errorf("project roles = %+v, %v; want 2", got, err)
	}
	members, _, err := e.st.ListProjectMembers(ctx, other.ID, query.MemberFilter{})
	if err != nil || len(members) != 1 || members[0].Role != domain.RoleReviewer {
		t.Errorf("members of other = %+v, %v", members, err)
	}
	pm, err := e.st.GetProjectMember(ctx, e.project.ID, e.agent)
	if err != nil || pm.Role != domain.RoleReviewer {
		t.Errorf("get project member = %+v, %v", pm, err)
	}
}

func TestHasProjectMembershipIsPerOrgAndAliveOnly(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	has, err := e.st.HasProjectMembership(ctx, e.org.ID, e.agent)
	if err != nil || has {
		t.Fatalf("before any grant = %v, %v; want false", has, err)
	}
	pm := domain.ProjectMember{ProjectID: e.project.ID, Actor: e.agent, Role: domain.RoleViewer, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0}
	if _, err := e.st.PutProjectMember(ctx, pm); err != nil {
		t.Fatal(err)
	}
	if has, err = e.st.HasProjectMembership(ctx, e.org.ID, e.agent); err != nil || !has {
		t.Errorf("with an alive role = %v, %v; want true", has, err)
	}
	if has, err = e.st.HasProjectMembership(ctx, e.org.ID+1, e.agent); err != nil || has {
		t.Errorf("in another org = %v, %v; want false", has, err)
	}
	if has, err = e.st.HasProjectMembership(ctx, e.org.ID, e.user); err != nil || has {
		t.Errorf("for an actor without a role = %v, %v; want false", has, err)
	}
	pm.Status, pm.UpdatedAt = domain.RecordRemoved, t0.Add(time.Hour)
	if got, err := e.st.PutProjectMember(ctx, pm); err != nil {
		t.Fatal(err)
	} else if !got.CreatedAt.Equal(t0) || !got.UpdatedAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("project member times = %v, %v; want created %v kept and updated %v", got.CreatedAt, got.UpdatedAt, t0, t0.Add(time.Hour))
	}
	if has, err = e.st.HasProjectMembership(ctx, e.org.ID, e.agent); err != nil || has {
		t.Errorf("after removal = %v, %v; want false", has, err)
	}
}

func TestIdentitiesLookupByProvider(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	// Provider ids are unique across the database, so key it by the user id.
	pid := "tester-" + e.user.ID.String()
	id := domain.Identity{ID: gen.New(), UserID: e.user.ID, Provider: domain.ProviderPassword, ProviderID: pid, Secret: []byte("hash"), Login: "tester", CreatedAt: t0}
	if _, err := e.st.CreateIdentity(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, err := e.st.GetIdentity(ctx, domain.ProviderPassword, pid)
	if err != nil || got.UserID != e.user.ID || string(got.Secret) != "hash" {
		t.Errorf("identity = %+v, %v", got, err)
	}
	if _, err := e.st.GetIdentity(ctx, domain.ProviderGitHub, pid); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("other provider err = %v", err)
	}
	now := t0.Add(time.Hour)
	got.LastLoginAt = &now
	if err := e.st.UpdateIdentity(ctx, got); err != nil {
		t.Fatal(err)
	}
	list, err := e.st.ListIdentities(ctx, e.user.ID)
	if err != nil || len(list) != 1 || list[0].LastLoginAt == nil {
		t.Errorf("identities = %+v, %v", list, err)
	}
}

func TestDeleteIdentityRemovesOnlyThatRow(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	// Provider ids are unique across the database, so key them by the user id.
	keep := domain.Identity{ID: gen.New(), UserID: e.user.ID, Provider: domain.ProviderPassword, ProviderID: "keep-" + e.user.ID.String(), Secret: []byte("hash"), Login: "tester", CreatedAt: t0}
	gone := domain.Identity{ID: gen.New(), UserID: e.user.ID, Provider: domain.ProviderGitHub, ProviderID: "gone-" + e.user.ID.String(), Login: "tester-gh", CreatedAt: t0}
	for _, id := range []domain.Identity{keep, gone} {
		if _, err := e.st.CreateIdentity(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	// The id alone is not enough: the row goes only for the user it belongs to.
	if err := e.st.DeleteIdentity(ctx, gen.New(), gone.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete under another user err = %v, want not found", err)
	}
	if _, err := e.st.GetIdentity(ctx, gone.Provider, gone.ProviderID); err != nil {
		t.Fatalf("identity after a delete under another user: %v", err)
	}
	if err := e.st.DeleteIdentity(ctx, e.user.ID, gone.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.GetIdentity(ctx, gone.Provider, gone.ProviderID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deleted identity err = %v, want not found", err)
	}
	list, err := e.st.ListIdentities(ctx, e.user.ID)
	if err != nil || len(list) != 1 || list[0].ID != keep.ID {
		t.Errorf("identities = %+v, %v; want only the password identity", list, err)
	}
	if err := e.st.DeleteIdentity(ctx, e.user.ID, gone.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second delete err = %v, want not found", err)
	}
}

func (e *env) token(t *testing.T, actor domain.ActorRef, secret string, expires time.Time) domain.Token {
	t.Helper()
	tok, err := e.st.CreateToken(t.Context(), domain.Token{ID: gen.New(), OrgID: e.org.ID, Actor: actor, Name: "t", Prefix: secret[:domain.TokenPrefixLen],
		Hash: domain.HashToken(secret), Scope: domain.ScopeWrite, Status: domain.RecordActive, ExpiresAt: expires, CreatedAt: t0})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	return tok
}

func TestTokensLookupTouchAndRevoke(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	secret, _, _ := domain.NewTokenSecret(domain.ActorAgent)
	tok := e.token(t, e.agent, secret, t0.Add(time.Hour))
	other, _, _ := domain.NewTokenSecret(domain.ActorAgent)
	e.token(t, e.agent, other, t0.Add(time.Hour))
	got, err := e.st.GetTokenByHash(ctx, domain.HashToken(secret))
	if err != nil || got.ID != tok.ID || got.LastUsedAt != nil {
		t.Fatalf("by hash = %+v, %v", got, err)
	}
	if _, err := e.st.GetTokenByHash(ctx, domain.HashToken("lvk_a_nope")); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown hash err = %v", err)
	}
	if err := e.st.TouchToken(ctx, tok.ID, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ = e.st.GetToken(ctx, tok.ID)
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("last used = %v, want t0+1m", got.LastUsedAt)
	}
	if err := e.st.TouchToken(ctx, tok.ID, t0.Add(90*time.Second)); err != nil {
		t.Fatal(err)
	}
	got, _ = e.st.GetToken(ctx, tok.ID)
	if !got.LastUsedAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("last used after a touch 30s later = %v, want coalesced to t0+1m", got.LastUsedAt)
	}
	succ := gen.New()
	tok.Status, tok.ExpiresAt, tok.ReplacedBy = domain.RecordRotated, t0.Add(24*time.Hour), &succ
	if err := e.st.UpdateToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	got, _ = e.st.GetToken(ctx, tok.ID)
	if got.Status != domain.RecordRotated || got.ReplacedBy == nil || *got.ReplacedBy != succ {
		t.Errorf("after update = %+v", got)
	}
	if err := e.st.RevokeTokensForActor(ctx, e.org.ID, e.agent); err != nil {
		t.Fatal(err)
	}
	list, _, err := e.st.ListTokens(ctx, e.org.ID, e.agent, query.Page{}, nil)
	if err != nil || len(list) != 2 {
		t.Fatalf("tokens = %d, %v", len(list), err)
	}
	for _, tk := range list {
		if tk.Status != domain.RecordRevoked {
			t.Errorf("token %s status = %v, want revoked", tk.ID, tk.Status)
		}
	}
}

func TestAgentsListAliveAndPending(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	pending, _ := e.st.CreateAgent(ctx, domain.Agent{ID: gen.New(), OrgID: e.org.ID, Name: "pending", Harness: domain.HarnessCodex, CreatedBy: e.user.ID, Status: domain.RecordPending, CreatedAt: t0, UpdatedAt: t0})
	revoked, _ := e.st.CreateAgent(ctx, domain.Agent{ID: gen.New(), OrgID: e.org.ID, Name: "revoked", Harness: domain.HarnessCodex, CreatedBy: e.user.ID, Status: domain.RecordRevoked, CreatedAt: t0, UpdatedAt: t0})
	live, err := e.st.ListAgents(ctx, e.org.ID, false)
	if err != nil || len(live) != 2 {
		t.Errorf("live agents = %d, %v; want the active one and the pending one", len(live), err)
	}
	all, err := e.st.ListAgents(ctx, e.org.ID, true)
	if err != nil || len(all) != 3 {
		t.Errorf("all agents = %d, %v; want 3", len(all), err)
	}
	pending.Status, pending.Machine = domain.RecordActive, "box"
	updated, err := e.st.UpdateAgent(ctx, pending)
	if err != nil || updated.Status != domain.RecordActive || updated.Machine != "box" {
		t.Errorf("updated = %+v, %v", updated, err)
	}
	got, err := e.st.GetAgents(ctx, []domain.ID{revoked.ID, 42})
	if err != nil || len(got) != 1 || got[0].ID != revoked.ID {
		t.Errorf("GetAgents = %+v, %v", got, err)
	}
	users, err := e.st.GetUsers(ctx, []domain.ID{e.user.ID, 42})
	if err != nil || len(users) != 1 {
		t.Errorf("GetUsers = %+v, %v", users, err)
	}
}

func TestEventsPageAfterAndBeforeWithFilters(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	other := e.newProject(t, "other")
	subj := domain.Subject{Type: domain.SubjectCard, ID: gen.New(), Number: 1}
	var evs []domain.Event
	evs = append(evs, e.event(t, e.agent, &e.project.ID, domain.EventCardCreated, subj))
	evs = append(evs, e.event(t, e.user, &e.project.ID, domain.EventCardUpdated, subj))
	evs = append(evs, e.event(t, e.user, &other.ID, domain.EventCardCreated, subj))
	evs = append(evs, e.event(t, e.user, nil, domain.EventMembershipGranted, domain.Subject{Type: domain.SubjectMembership, ID: e.user.ID}))
	evs = append(evs, e.event(t, e.agent, &e.project.ID, domain.EventCardClaimed, subj))

	e.eventsVisible(t)
	all, remaining, err := e.st.ListEvents(ctx, e.org.ID, query.EventFilter{Limit: 2})
	if err != nil || len(all) != 2 || all[0].ID != evs[0].ID || remaining != 3 {
		t.Fatalf("first page = %d remaining %d, %v", len(all), remaining, err)
	}
	after, _, err := e.st.ListEvents(ctx, e.org.ID, query.EventFilter{After: query.EventPosition{ID: evs[2].ID}, Limit: 10})
	if err != nil || len(after) != 2 || after[0].ID != evs[3].ID {
		t.Errorf("after = %d, %v", len(after), err)
	}
	before, remaining, err := e.st.ListEvents(ctx, e.org.ID, query.EventFilter{Before: query.EventPosition{ID: evs[4].ID}, Limit: 2})
	if err != nil || len(before) != 2 || before[0].ID != evs[2].ID || before[1].ID != evs[3].ID || remaining != 2 {
		t.Errorf("before = %v remaining %d, %v; want events 2 and 3 oldest first with 2 older", len(before), remaining, err)
	}
	scoped, _, err := e.st.ListEvents(ctx, e.org.ID, query.EventFilter{ProjectIDs: []domain.ID{e.project.ID}, Limit: 10})
	if err != nil || len(scoped) != 4 {
		t.Errorf("scoped to main = %d, %v; want 3 project events plus the org-level one", len(scoped), err)
	}
	excl, _, err := e.st.ListEvents(ctx, e.org.ID, query.EventFilter{ExcludeActor: &e.agent, Limit: 10})
	if err != nil || len(excl) != 3 {
		t.Errorf("excluding agent = %d, %v; want 3", len(excl), err)
	}
	n, err := e.st.CountEventsAfter(ctx, e.org.ID, query.EventFilter{After: query.EventPosition{ID: evs[0].ID}, ExcludeActor: &e.agent, Limit: 2})
	if err != nil || n != 2 {
		t.Errorf("count capped = %d, %v; want 2", n, err)
	}
	latest, err := e.st.LatestEventPosition(ctx, e.org.ID, query.EventFilter{})
	if err != nil || latest.ID != evs[4].ID || latest.Xid == 0 {
		t.Errorf("latest = %v, %v", latest, err)
	}
	forSubject, err := e.st.ListEventsForSubject(ctx, e.org.ID, subj, 2)
	if err != nil || len(forSubject) != 2 || forSubject[0].ID != evs[4].ID {
		t.Errorf("for subject = %d, %v; want the 2 newest", len(forSubject), err)
	}
	same := domain.Subject{Type: domain.SubjectAgent, ID: subj.ID}
	if got, err := e.st.ListEventsForSubject(ctx, e.org.ID, same, 2); err != nil || len(got) != 0 {
		t.Errorf("another subject type with the same id = %d, %v; want none", len(got), err)
	}
	if _, _, err := e.st.ListEvents(ctx, e.org.ID, query.EventFilter{After: query.EventPosition{ID: gen.New()}, Limit: 10}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown cursor = %v, want ErrNotFound", err)
	}
	newest, remaining, err := e.st.ListEvents(ctx, e.org.ID, query.EventFilter{Before: query.EventPosition{ID: math.MaxInt64}, Limit: 1})
	if err != nil || len(newest) != 1 || newest[0].ID != evs[4].ID || remaining != 4 {
		t.Errorf("before the largest id = %v remaining %d, %v; want the newest event with 4 older", newest, remaining, err)
	}
}

func TestListenReceivesOrgOfAppendedEvent(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	got := make(chan domain.ID, 4)
	done := make(chan error, 1)
	go func() { done <- e.st.Listen(ctx, func(id domain.ID) { got <- id }) }()
	// Give the listener time to subscribe, then append in a committed transaction.
	time.Sleep(200 * time.Millisecond)
	subj := domain.Subject{Type: domain.SubjectCard, ID: gen.New(), Number: 1}
	err := e.st.WithTx(ctx, func(tx *Store) error {
		ev, _ := domain.NewEvent(gen.New(), e.org.ID, &e.project.ID, domain.EventCardCreated, e.agent, subj, nil)
		return tx.AppendEvent(ctx, ev)
	})
	if err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case id := <-got:
			if id == e.org.ID {
				cancel()
				if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
					t.Errorf("Listen returned %v", err)
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("no notification for the org before the deadline")
		}
	}
}

func TestCreateIdentityKeepsItsFirstLogin(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	first := t0.Add(time.Hour)
	firstPID, invitedPID := "first-"+e.user.ID.String(), "invited-"+e.user.ID.String()
	in := domain.Identity{ID: gen.New(), UserID: e.user.ID, Provider: domain.ProviderGitHub, ProviderID: firstPID, Login: "tester", LastLoginAt: &first, CreatedAt: t0}
	if _, err := e.st.CreateIdentity(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := e.st.GetIdentity(ctx, domain.ProviderGitHub, firstPID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastLoginAt == nil || !got.LastLoginAt.Equal(first) {
		t.Errorf("last_login_at = %v, want %v: a first sign-in that creates the identity is a login", got.LastLoginAt, first)
	}
	invited := domain.Identity{ID: gen.New(), UserID: e.user.ID, Provider: domain.ProviderGitHub, ProviderID: invitedPID, Login: "invitee", CreatedAt: t0}
	if _, err := e.st.CreateIdentity(ctx, invited); err != nil {
		t.Fatal(err)
	}
	if got, err := e.st.GetIdentity(ctx, domain.ProviderGitHub, invitedPID); err != nil || got.LastLoginAt != nil {
		t.Errorf("invited identity = %+v, %v; want last_login_at null", got, err)
	}
}

func TestEventExclusionNeedsActorAndSessionToMatch(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	subj := domain.Subject{Type: domain.SubjectCard, ID: gen.New(), Number: 1}
	write := func(actor domain.ActorRef, session string) domain.Event {
		t.Helper()
		ev, err := domain.NewEvent(gen.New(), e.org.ID, &e.project.ID, domain.EventCardUpdated, actor, subj, nil)
		if err != nil {
			t.Fatal(err)
		}
		ev.Session = session
		if err := e.st.AppendEvent(ctx, ev); err != nil {
			t.Fatalf("append event: %v", err)
		}
		return ev
	}
	sameSession := write(e.agent, "lead")
	otherSession := write(e.agent, "lead-c1")
	noSession := write(e.agent, "")
	otherActor := write(e.user, "lead")
	e.eventsVisible(t)

	tests := []struct {
		name    string
		session string
		want    []domain.ID
	}{
		{"caller in lead", "lead", []domain.ID{otherSession.ID, noSession.ID, otherActor.ID}},
		{"caller without a session", "", []domain.ID{sameSession.ID, otherSession.ID, otherActor.ID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := query.EventFilter{ExcludeActor: &e.agent, ExcludeSession: tt.session, Limit: 1}
			got, remaining, err := e.st.ListEvents(ctx, e.org.ID, f)
			if err != nil || len(got) != 1 || got[0].ID != tt.want[0] || remaining != 2 {
				t.Fatalf("first page = %d remaining %d, %v; want event %v with 2 remaining", len(got), remaining, err, tt.want[0])
			}
			f.Limit = 10
			page, _, err := e.st.ListEvents(ctx, e.org.ID, f)
			if err != nil || len(page) != len(tt.want) {
				t.Fatalf("all = %d, %v; want %d", len(page), err, len(tt.want))
			}
			for i, ev := range page {
				if ev.ID != tt.want[i] {
					t.Errorf("event %d = %v, want %v", i, ev.ID, tt.want[i])
				}
			}
			n, err := e.st.CountEventsAfter(ctx, e.org.ID, query.EventFilter{ExcludeActor: &e.agent, ExcludeSession: tt.session})
			if err != nil || n != len(tt.want) {
				t.Errorf("count = %d, %v; want %d", n, err, len(tt.want))
			}
		})
	}
	stored, err := e.st.ListEventsForSubject(ctx, e.org.ID, subj, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range stored {
		if ev.ID == sameSession.ID && ev.Session != "lead" {
			t.Errorf("stored session = %q, want lead", ev.Session)
		}
	}
}

func TestCardsCreatedInSessionMatchesActorSessionAndCreation(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	write := func(typ domain.EventType, actor domain.ActorRef, session string) domain.ID {
		t.Helper()
		subj := domain.Subject{Type: domain.SubjectCard, ID: gen.New(), Number: 1}
		ev, err := domain.NewEvent(gen.New(), e.org.ID, &e.project.ID, typ, actor, subj, nil)
		if err != nil {
			t.Fatal(err)
		}
		ev.Session = session
		if err := e.st.AppendEvent(ctx, ev); err != nil {
			t.Fatalf("append event: %v", err)
		}
		return subj.ID
	}
	here := write(domain.EventCardCreated, e.agent, "lead")
	there := write(domain.EventCardCreated, e.agent, "lead-c1")
	byUser := write(domain.EventCardCreated, e.user, "lead")
	updatedHere := write(domain.EventCardUpdated, e.agent, "lead")
	got, err := e.st.CardsCreatedInSession(ctx, e.org.ID, []domain.ID{here, there, byUser, updatedHere}, e.agent, "lead")
	if err != nil || len(got) != 1 || got[0] != here {
		t.Errorf("created in lead = %v, %v; want only %d", got, err, here)
	}
	if got, err := e.st.CardsCreatedInSession(ctx, e.org.ID, nil, e.agent, "lead"); err != nil || len(got) != 0 {
		t.Errorf("no ids = %v, %v; want none", got, err)
	}
}

func TestCardCreatedSessionReadsTheCreationEventOnly(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	card := domain.Subject{Type: domain.SubjectCard, ID: gen.New(), Number: 1}
	for _, w := range []struct {
		typ     domain.EventType
		session string
	}{{domain.EventCardCreated, "lead-c2"}, {domain.EventCardUpdated, "other"}} {
		ev, err := domain.NewEvent(gen.New(), e.org.ID, &e.project.ID, w.typ, e.agent, card, nil)
		if err != nil {
			t.Fatal(err)
		}
		ev.Session = w.session
		if err := e.st.AppendEvent(ctx, ev); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}
	if got, err := e.st.CardCreatedSession(ctx, e.org.ID, card.ID); err != nil || got != "lead-c2" {
		t.Errorf("created session = %q, %v; want lead-c2", got, err)
	}
	if _, err := e.st.CardCreatedSession(ctx, e.org.ID, gen.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown card = %v, want not found", err)
	}
}

// Where a card was parked from is its newest move to waiting; moves to other
// statuses in between do not count.
func TestCardParkedFromReadsTheLastMoveToWaiting(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	card := domain.Subject{Type: domain.SubjectCard, ID: gen.New(), Number: 1}
	for _, m := range [][2]domain.Status{
		{domain.StatusBacklog, domain.StatusWaiting},
		{domain.StatusWaiting, domain.StatusReady},
		{domain.StatusReady, domain.StatusWaiting},
		{domain.StatusWaiting, domain.StatusBacklog},
	} {
		ev, err := domain.NewEvent(gen.New(), e.org.ID, &e.project.ID, domain.EventCardStatusChanged, e.agent, card, map[string]any{"from": m[0].String(), "to": m[1].String()})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.st.AppendEvent(ctx, ev); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}
	if got, err := e.st.CardParkedFrom(ctx, e.org.ID, card.ID); err != nil || got != domain.StatusReady {
		t.Errorf("parked from = %v, %v; want ready, the last move to waiting", got, err)
	}
	if _, err := e.st.CardParkedFrom(ctx, e.org.ID, gen.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("card never parked = %v, want not found", err)
	}
}

// Every say over an agent joining a project is written and read back: the
// account switch, the agent's setting, the project admin's setting and a
// user's own rule for one project, which goes back to follow when deleted.
func TestAgentJoinSettingsRoundTrip(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	u, _ := e.st.GetUser(ctx, e.user.ID)
	u.AgentJoin = domain.AgentJoinAllow
	if got, err := e.st.UpdateUser(ctx, u); err != nil || got.AgentJoin != domain.AgentJoinAllow {
		t.Errorf("user agent_join = %v, %v; want allow", got.AgentJoin, err)
	}
	a, _ := e.st.GetAgent(ctx, e.agent.ID)
	a.AgentJoin = domain.AgentJoinAsk
	if got, err := e.st.UpdateAgent(ctx, a); err != nil || got.AgentJoin != domain.AgentJoinAsk {
		t.Errorf("agent agent_join = %v, %v; want ask", got.AgentJoin, err)
	}
	p := e.project
	p.AgentJoin = domain.AgentJoinAsk
	if got, err := e.st.UpdateProject(ctx, p); err != nil || got.AgentJoin != domain.AgentJoinAsk {
		t.Errorf("project agent_join = %v, %v; want ask", got.AgentJoin, err)
	}
	if _, err := e.st.GetAgentJoinRule(ctx, p.ID, e.user.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("rule before any = %v, want ErrNotFound", err)
	}
	for _, j := range []domain.AgentJoin{domain.AgentJoinAllow, domain.AgentJoinAsk} {
		if _, err := e.st.PutAgentJoinRule(ctx, domain.AgentJoinRule{ProjectID: p.ID, UserID: e.user.ID, AgentJoin: j, UpdatedAt: t0}); err != nil {
			t.Fatalf("put rule %v: %v", j, err)
		}
		if got, err := e.st.GetAgentJoinRule(ctx, p.ID, e.user.ID); err != nil || got.AgentJoin != j {
			t.Errorf("rule = %v, %v; want %v", got.AgentJoin, err, j)
		}
	}
	if err := e.st.DeleteAgentJoinRule(ctx, p.ID, e.user.ID); err != nil {
		t.Fatalf("delete rule: %v", err)
	}
	if _, err := e.st.GetAgentJoinRule(ctx, p.ID, e.user.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("rule after delete = %v, want ErrNotFound", err)
	}
}

func TestAgentRightsRoundTripOnMembershipAndProjectMember(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	want := domain.RightWork | domain.RightAnswer
	m := domain.Membership{OrgID: e.org.ID, Actor: e.agent, Role: domain.RoleViewer, Status: domain.RecordActive, Rights: want, CreatedAt: t0, UpdatedAt: t0}
	if _, err := e.st.PutMembership(ctx, m); err != nil {
		t.Fatal(err)
	}
	pm := domain.ProjectMember{ProjectID: e.project.ID, Actor: e.agent, Role: domain.RoleViewer, Status: domain.RecordActive, Rights: want, CreatedAt: t0, UpdatedAt: t0}
	if _, err := e.st.PutProjectMember(ctx, pm); err != nil {
		t.Fatal(err)
	}
	gotM, err := e.st.GetMembership(ctx, e.org.ID, e.agent)
	if err != nil || gotM.Rights != want {
		t.Errorf("membership rights = %d, %v; want %d", gotM.Rights, err, want)
	}
	gotPM, err := e.st.GetProjectMember(ctx, e.project.ID, e.agent)
	if err != nil || gotPM.Rights != want {
		t.Errorf("project member rights = %d, %v; want %d", gotPM.Rights, err, want)
	}
	pm.Rights, pm.UpdatedAt = domain.RightReady, t0.Add(time.Hour)
	if _, err := e.st.PutProjectMember(ctx, pm); err != nil {
		t.Fatal(err)
	}
	if gotPM, err = e.st.GetProjectMember(ctx, e.project.ID, e.agent); err != nil || gotPM.Rights != domain.RightReady {
		t.Errorf("project member rights after upsert = %d, %v; want %d", gotPM.Rights, err, domain.RightReady)
	}
}

func TestTouchAgentSessionWritesOncePerMinute(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	if err := e.st.TouchAgentSession(ctx, e.agent.ID, "calm-fox", t0); err != nil {
		t.Fatal(err)
	}
	if err := e.st.TouchAgentSession(ctx, e.agent.ID, "calm-fox", t0.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := e.st.ListAgentSessions(ctx, e.agent.ID, 10)
	if err != nil || len(got) != 1 || !got[0].LastSeenAt.Equal(t0) || !got[0].CreatedAt.Equal(t0) {
		t.Fatalf("sessions = %+v, %v; want one seen at %v", got, err, t0)
	}
	later := t0.Add(2 * time.Minute)
	if err := e.st.TouchAgentSession(ctx, e.agent.ID, "calm-fox", later); err != nil {
		t.Fatal(err)
	}
	if err := e.st.TouchAgentSession(ctx, e.agent.ID, "old-owl", t0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err = e.st.ListAgentSessions(ctx, e.agent.ID, 10)
	if err != nil || len(got) != 2 || got[0].Session != "calm-fox" || !got[0].LastSeenAt.Equal(later) || !got[0].CreatedAt.Equal(t0) {
		t.Fatalf("sessions = %+v, %v; want calm-fox moved to %v and listed first", got, err, later)
	}
	if got, err = e.st.ListAgentSessions(ctx, e.agent.ID, 1); err != nil || len(got) != 1 {
		t.Errorf("limited sessions = %+v, %v; want 1", got, err)
	}
}

// Trimming keeps an agent's most recently seen chats and every chat that
// holds a live grant, and leaves another agent's chats alone.
func TestTrimAgentSessionsKeepsTheNewestAndTheGranted(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	other, err := e.st.CreateAgent(ctx, domain.Agent{ID: gen.New(), OrgID: e.org.ID, Name: "other", Harness: domain.HarnessCodex, CreatedBy: e.user.ID,
		Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	for i, label := range []string{"old-owl", "calm-fox", "bold-elk", "new-yak"} {
		for _, agent := range []domain.ID{e.agent.ID, other.ID} {
			if err := e.st.TouchAgentSession(ctx, agent, label, t0.Add(time.Duration(i)*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for label, status := range map[string]domain.RecordStatus{"old-owl": domain.RecordActive, "calm-fox": domain.RecordRemoved} {
		if _, err := e.st.PutAgentSessionGrant(ctx, domain.AgentSessionGrant{ProjectID: e.project.ID, AgentID: e.agent.ID, Session: label,
			Rights: domain.RightWork, GrantedBy: e.user.ID, Status: status, CreatedAt: t0, UpdatedAt: t0}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.st.TrimAgentSessions(ctx, e.agent.ID, 2); err != nil {
		t.Fatal(err)
	}
	labels := func(agent domain.ID) []string {
		t.Helper()
		got, err := e.st.ListAgentSessions(ctx, agent, 10)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, sess := range got {
			out = append(out, sess.Session)
		}
		return out
	}
	if got, want := labels(e.agent.ID), []string{"new-yak", "bold-elk", "old-owl"}; !slices.Equal(got, want) {
		t.Errorf("chats after the trim = %v, want %v: the two newest and the one with a live grant, not calm-fox whose grant was taken back", got, want)
	}
	if got := labels(other.ID); len(got) != 4 {
		t.Errorf("the other agent's chats = %v, want all four", got)
	}
}

// A chat's name is kept apart from its touch: naming a chat never seen makes
// its row, a touch leaves the name alone, and a new name or none replaces it.
func TestSetAgentSessionNameKeepsTheLastNameSent(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	name := func() string {
		t.Helper()
		got, err := e.st.ListAgentSessionsByLabel(ctx, e.agent.ID, []string{"calm-fox"})
		if err != nil || len(got) != 1 {
			t.Fatalf("sessions = %+v, %v; want one", got, err)
		}
		return got[0].Name
	}
	if err := e.st.SetAgentSessionName(ctx, e.agent.ID, "calm-fox", "Release notes", t0); err != nil {
		t.Fatal(err)
	}
	if err := e.st.TouchAgentSession(ctx, e.agent.ID, "calm-fox", t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := name(); got != "Release notes" {
		t.Errorf("name after a touch = %q, want it kept", got)
	}
	for _, want := range []string{"Sign-in fixes", ""} {
		if err := e.st.SetAgentSessionName(ctx, e.agent.ID, "calm-fox", want, t0.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if got := name(); got != want {
			t.Errorf("name = %q, want %q", got, want)
		}
	}
	if err := e.st.TouchAgentSession(ctx, e.agent.ID, "old-owl", t0); err != nil {
		t.Fatal(err)
	}
	if got, err := e.st.ListAgentSessionsByLabel(ctx, e.agent.ID, []string{"old-owl"}); err != nil || len(got) != 1 || got[0].Name != "" {
		t.Errorf("a chat only touched = %+v, %v; want one with no name", got, err)
	}
	if err := e.st.SetAgentSessionName(ctx, e.agent.ID, "shy-elk", "", t0); err != nil {
		t.Fatal(err)
	}

	// No name is null in the table, whichever writer left it so, and never
	// an empty string: calm-fox had its name taken away, old-owl was only
	// touched, shy-elk was first seen clearing a name it never had.
	var null, empty int
	err := e.st.db.QueryRow(ctx, "SELECT count(*) FILTER (WHERE name IS NULL), count(*) FILTER (WHERE name = '') FROM agent_sessions WHERE agent_id = $1", int64(e.agent.ID)).Scan(&null, &empty)
	if err != nil || null != 3 || empty != 0 {
		t.Errorf("stored names: %d null and %d empty, %v; want 3 and 0", null, empty, err)
	}
	// A chat that has no name yet takes one: a comparison with <> would
	// never see a null as different.
	for _, session := range []string{"calm-fox", "old-owl", "shy-elk"} {
		if err := e.st.SetAgentSessionName(ctx, e.agent.ID, session, "Named later", t0.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if got, err := e.st.ListAgentSessionsByLabel(ctx, e.agent.ID, []string{session}); err != nil || len(got) != 1 || got[0].Name != "Named later" {
			t.Errorf("%s after being named = %+v, %v; want the name stored", session, got, err)
		}
	}
}

func TestListAgentSessionsByLabelReturnsOnlyTheNamedSeenOnes(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	for session, at := range map[string]time.Time{"calm-fox": t0, "old-owl": t0.Add(-time.Hour), "new-elk": t0.Add(time.Hour)} {
		if err := e.st.TouchAgentSession(ctx, e.agent.ID, session, at); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.st.ListAgentSessionsByLabel(ctx, e.agent.ID, []string{"old-owl", "calm-fox", "never-seen"})
	if err != nil || len(got) != 2 || got[0].Session != "calm-fox" || got[1].Session != "old-owl" {
		t.Errorf("sessions = %+v, %v; want calm-fox then old-owl", got, err)
	}
}

func TestAgentSessionGrantsPutGetAndListAliveOnly(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	other := e.newProject(t, "other")
	g := domain.AgentSessionGrant{
		ProjectID: e.project.ID, AgentID: e.agent.ID, Session: "calm-fox", Rights: domain.RightWork,
		GrantedBy: e.user.ID, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0,
	}
	if _, err := e.st.PutAgentSessionGrant(ctx, g); err != nil {
		t.Fatal(err)
	}
	g2 := g
	g2.ProjectID, g2.Session = other.ID, "old-owl"
	if _, err := e.st.PutAgentSessionGrant(ctx, g2); err != nil {
		t.Fatal(err)
	}
	g.Rights, g.UpdatedAt = domain.RightWork|domain.RightReady, t0.Add(time.Hour)
	if _, err := e.st.PutAgentSessionGrant(ctx, g); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := e.st.GetAgentSessionGrant(ctx, e.project.ID, e.agent.ID, "calm-fox")
	if err != nil || got.Rights != g.Rights || got.GrantedBy != e.user.ID || !got.CreatedAt.Equal(t0) || !got.UpdatedAt.Equal(g.UpdatedAt) {
		t.Errorf("grant = %+v, %v; want upserted rights with created time kept", got, err)
	}
	if _, err := e.st.GetAgentSessionGrant(ctx, e.project.ID, e.agent.ID, "nobody"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown grant err = %v, want ErrNotFound", err)
	}
	g2.Status, g2.UpdatedAt = domain.RecordRemoved, t0.Add(2*time.Hour)
	if _, err := e.st.PutAgentSessionGrant(ctx, g2); err != nil {
		t.Fatal(err)
	}
	list, err := e.st.ListAgentSessionGrants(ctx, e.agent.ID)
	if err != nil || len(list) != 1 || list[0].Session != "calm-fox" {
		t.Errorf("alive grants = %+v, %v; want calm-fox only", list, err)
	}
	if removed, err := e.st.GetAgentSessionGrant(ctx, other.ID, e.agent.ID, "old-owl"); err != nil || removed.Status != domain.RecordRemoved {
		t.Errorf("removed grant = %+v, %v; want still readable", removed, err)
	}
	g.Session = "calm-fox-c2"
	if _, err := e.st.PutAgentSessionGrant(ctx, g); err == nil {
		t.Error("a subagent label must be refused before it reaches the database")
	}
}

// A grant keeps its end and instructions, and a put replaces both. Only a
// grant still alive and past its end is ended, once, and listed as ended.
func TestAgentSessionGrantEndAndInstructions(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	end := t0.Add(8 * time.Hour)
	g := domain.AgentSessionGrant{
		ProjectID: e.project.ID, AgentID: e.agent.ID, Session: "calm-fox", Rights: domain.RightAnswer,
		GrantedBy: e.user.ID, EndsAt: &end, Instructions: "lanes server and cli", Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0,
	}
	if got, err := e.st.PutAgentSessionGrant(ctx, g); err != nil || got.EndsAt == nil || !got.EndsAt.Equal(end) || got.Instructions != g.Instructions {
		t.Fatalf("put = %+v, %v; want the end and the instructions kept", got, err)
	}
	open := g
	open.Session, open.EndsAt, open.Instructions = "old-owl", nil, ""
	if got, err := e.st.PutAgentSessionGrant(ctx, open); err != nil || got.EndsAt != nil || got.Instructions != "" {
		t.Fatalf("put without end = %+v, %v; want neither end nor instructions", got, err)
	}

	mine := func(gs []domain.AgentSessionGrant) (out []string) {
		for _, g := range gs {
			if g.AgentID == e.agent.ID {
				out = append(out, g.Session)
			}
		}
		return out
	}
	if gs, err := e.st.ListEndedAgentSessionGrants(ctx, end.Add(-time.Second), 100); err != nil || len(mine(gs)) != 0 {
		t.Errorf("ended a second before the end = %v, %v; want none", mine(gs), err)
	}
	if _, err := e.st.EndAgentSessionGrant(ctx, e.project.ID, e.agent.ID, "calm-fox", end.Add(-time.Second)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ending before the end = %v, want ErrNotFound", err)
	}
	if gs, err := e.st.ListEndedAgentSessionGrants(ctx, end, 100); err != nil || !slices.Equal(mine(gs), []string{"calm-fox"}) {
		t.Errorf("ended at the end = %v, %v; want calm-fox and never the grant without an end", mine(gs), err)
	}
	got, err := e.st.EndAgentSessionGrant(ctx, e.project.ID, e.agent.ID, "calm-fox", end)
	if err != nil || got.Status != domain.RecordRemoved || !got.UpdatedAt.Equal(end) || got.EndsAt == nil || got.Instructions != g.Instructions {
		t.Fatalf("end = %+v, %v; want removed at the end with the rest kept", got, err)
	}
	if _, err := e.st.EndAgentSessionGrant(ctx, e.project.ID, e.agent.ID, "calm-fox", end); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ending twice = %v, want ErrNotFound", err)
	}
	if _, err := e.st.EndAgentSessionGrant(ctx, e.project.ID, e.agent.ID, "old-owl", end.Add(time.Hour)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ending a grant without an end = %v, want ErrNotFound", err)
	}
	if gs, err := e.st.ListEndedAgentSessionGrants(ctx, end, 100); err != nil || len(mine(gs)) != 0 {
		t.Errorf("ended after the sweep = %v, %v; want none", mine(gs), err)
	}
}

func TestLockOrgOwnersLocksTheActiveOwnersSoASecondLeaveWaits(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	person := func(login string, role domain.Role, status domain.RecordStatus) domain.ActorRef {
		t.Helper()
		u, err := e.st.CreateUser(ctx, domain.User{ID: gen.New(), Login: login, Name: login, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.st.PutMembership(ctx, domain.Membership{OrgID: e.org.ID, Actor: u.Ref(), Role: role, Status: status, CreatedAt: t0, UpdatedAt: t0}); err != nil {
			t.Fatal(err)
		}
		return u.Ref()
	}
	ann := person("ann", domain.RoleOwner, domain.RecordActive)
	bo := person("bo", domain.RoleOwner, domain.RecordActive)
	person("cy", domain.RoleOwner, domain.RecordRemoved)
	person("dee", domain.RoleAdmin, domain.RecordActive)
	person("eve", domain.RoleOwner, domain.RecordPending)
	// A suspended account cannot act for the org, so it is no owner to leave it to.
	fay := person("fay", domain.RoleOwner, domain.RecordActive)
	u, err := e.st.GetUser(ctx, fay.ID)
	if err != nil {
		t.Fatal(err)
	}
	u.Status = domain.RecordSuspended
	if _, err := e.st.UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	// An agent row with the owner role is no person to hand an org to.
	if _, err := e.st.PutMembership(ctx, domain.Membership{OrgID: e.org.ID, Actor: e.agent, Role: domain.RoleOwner, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	actors := func(ms []domain.Membership) []domain.ActorRef {
		var out []domain.ActorRef
		for _, m := range ms {
			out = append(out, m.Actor)
		}
		return out
	}
	owners, err := e.st.LockOrgOwners(ctx, e.org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := actors(owners); !slices.Equal(got, []domain.ActorRef{ann, bo}) {
		t.Fatalf("owners = %v, want ann and bo in id order", got)
	}

	// Ann leaves while holding the lock; bo's look waits for her commit and
	// then finds bo alone, so bo cannot leave too.
	pid, bOwners := make(chan int32, 1), make(chan []domain.Membership, 1)
	bDone := make(chan error, 1)
	err = e.st.WithTx(ctx, func(tx *Store) error {
		if _, err := tx.LockOrgOwners(ctx, e.org.ID); err != nil {
			return err
		}
		go func() {
			bDone <- e.st.WithTx(ctx, func(tx *Store) error {
				var p int32
				if err := tx.db.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&p); err != nil {
					return err
				}
				pid <- p
				got, err := tx.LockOrgOwners(ctx, e.org.ID)
				bOwners <- got
				return err
			})
		}()
		var p int32
		select {
		case p = <-pid:
		case err := <-bDone:
			return err
		}
		for {
			var wait *string
			if err := e.st.db.QueryRow(ctx, "SELECT wait_event_type FROM pg_stat_activity WHERE pid = $1", p).Scan(&wait); err != nil {
				return err
			}
			if wait != nil && *wait == "Lock" {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Millisecond):
			}
		}
		m, err := tx.LockMembership(ctx, e.org.ID, ann)
		if err != nil {
			return err
		}
		m.Status = domain.RecordRemoved
		_, err = tx.PutMembership(ctx, m)
		return err
	})
	if err != nil {
		t.Fatalf("ann's leave: %v", err)
	}
	if err := <-bDone; err != nil {
		t.Fatalf("bo's look: %v", err)
	}
	if got := actors(<-bOwners); !slices.Equal(got, []domain.ActorRef{bo}) {
		t.Errorf("owners bo saw = %v, want bo alone", got)
	}
}

func TestCountOrgPeopleAndContents(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	put := func(actor domain.ActorRef, status domain.RecordStatus) {
		t.Helper()
		if _, err := e.st.PutMembership(ctx, domain.Membership{OrgID: e.org.ID, Actor: actor, Role: domain.RoleMember, Status: status, CreatedAt: t0, UpdatedAt: t0}); err != nil {
			t.Fatal(err)
		}
	}
	person := func(login string, status domain.RecordStatus) domain.ActorRef {
		t.Helper()
		u, err := e.st.CreateUser(ctx, domain.User{ID: gen.New(), Login: login, Name: login, Status: status, CreatedAt: t0, UpdatedAt: t0})
		if err != nil {
			t.Fatal(err)
		}
		return u.Ref()
	}
	put(e.user, domain.RecordActive)
	put(person("gone", domain.RecordActive), domain.RecordRemoved)
	put(person("asked", domain.RecordActive), domain.RecordPending)
	put(person("held", domain.RecordSuspended), domain.RecordActive)
	put(e.agent, domain.RecordActive)
	if n, err := e.st.CountOrgPeople(ctx, e.org.ID); err != nil || n != 1 {
		t.Errorf("people = %d, %v; want the one active person on an active account", n, err)
	}
	e.card(t, nil)
	e.card(t, nil)
	if projects, cards, err := e.st.CountOrgContents(ctx, e.org.ID); err != nil || projects != 1 || cards != 2 {
		t.Errorf("contents = %d projects, %d cards, %v; want 1 besides the global project, and 2 cards", projects, cards, err)
	}
}
