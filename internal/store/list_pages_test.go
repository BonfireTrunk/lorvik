//go:build integration

package store

import (
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// walkPages reads a list two rows at a time and checks that remaining counts
// what the rest of the walk returns.
func walkPages[T, C any](t *testing.T, fetch func(after *C) ([]T, int, error), key func(T) *C) []T {
	t.Helper()
	var out []T
	var after *C
	for range 100 {
		rows, remaining, err := fetch(after)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, rows...)
		if remaining == 0 {
			return out
		}
		after = key(rows[len(rows)-1])
	}
	t.Fatal("the list never ended")
	return nil
}

func TestMemberPagesFollowRoleThenTimeThenActor(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	user, agent1, agent2 := domain.ActorRef{Kind: domain.ActorUser, ID: gen.New()}, domain.ActorRef{Kind: domain.ActorAgent, ID: gen.New()}, domain.ActorRef{Kind: domain.ActorAgent, ID: gen.New()}
	gone := domain.ActorRef{Kind: domain.ActorUser, ID: gen.New()}
	rows := []struct {
		actor   domain.ActorRef
		role    domain.Role
		created time.Time
		status  domain.RecordStatus
	}{
		{agent2, domain.RoleMember, t0, domain.RecordActive},
		{agent1, domain.RoleMember, t0, domain.RecordActive},
		{user, domain.RoleMember, t0, domain.RecordActive},
		{e.agent, domain.RoleMember, t0.Add(-time.Hour), domain.RecordActive},
		{e.user, domain.RoleReviewer, t0.Add(time.Hour), domain.RecordActive},
		{gone, domain.RoleReviewer, t0, domain.RecordRemoved},
	}
	for _, r := range rows {
		if _, err := e.st.PutMembership(ctx, domain.Membership{OrgID: e.org.ID, Actor: r.actor, Role: r.role, Status: r.status, CreatedAt: r.created, UpdatedAt: r.created}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.st.PutProjectMember(ctx, domain.ProjectMember{ProjectID: e.project.ID, Actor: r.actor, Role: r.role, Status: r.status, CreatedAt: r.created, UpdatedAt: r.created}); err != nil {
			t.Fatal(err)
		}
	}
	// agent1 was generated before agent2, and users sort before agents.
	alive := []domain.ActorRef{e.user, e.agent, user, agent1, agent2}
	all := []domain.ActorRef{gone, e.user, e.agent, user, agent1, agent2}

	tests := []struct {
		name string
		all  bool
		want []domain.ActorRef
	}{{"alive", false, alive}, {"all", true, all}}
	for _, tt := range tests {
		org := walkPages(t, func(after *query.MemberCursor) ([]domain.Membership, int, error) {
			return e.st.ListMemberships(ctx, e.org.ID, query.MemberFilter{All: tt.all, Page: query.Page{Limit: 2}, Cursor: after})
		}, func(m domain.Membership) *query.MemberCursor {
			return &query.MemberCursor{Role: m.Role, CreatedAt: m.CreatedAt, Actor: m.Actor}
		})
		if len(org) != len(tt.want) {
			t.Fatalf("%s org members = %+v, want %v", tt.name, org, tt.want)
		}
		for i, m := range org {
			if m.Actor != tt.want[i] {
				t.Errorf("%s org member %d = %v, want %v", tt.name, i, m.Actor, tt.want[i])
			}
		}
		proj := walkPages(t, func(after *query.MemberCursor) ([]domain.ProjectMember, int, error) {
			return e.st.ListProjectMembers(ctx, e.project.ID, query.MemberFilter{All: tt.all, Page: query.Page{Limit: 2}, Cursor: after})
		}, func(m domain.ProjectMember) *query.MemberCursor {
			return &query.MemberCursor{Role: m.Role, CreatedAt: m.CreatedAt, Actor: m.Actor}
		})
		if len(proj) != len(tt.want) {
			t.Fatalf("%s project members = %+v, want %v", tt.name, proj, tt.want)
		}
		for i, m := range proj {
			if m.Actor != tt.want[i] {
				t.Errorf("%s project member %d = %v, want %v", tt.name, i, m.Actor, tt.want[i])
			}
		}
	}
}

func TestAgentPagesPutTheAliveFirstAndHideTheRevoked(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	mk := func(name string, status domain.RecordStatus, created time.Time) domain.Agent {
		a, err := e.st.CreateAgent(ctx, domain.Agent{ID: gen.New(), OrgID: e.org.ID, Name: name, Harness: domain.HarnessOther, CreatedBy: e.user.ID, Status: status, CreatedAt: created, UpdatedAt: created})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	b1, b2 := mk("b1", domain.RecordActive, t0.Add(time.Hour)), mk("b2", domain.RecordActive, t0.Add(time.Hour))
	pending := mk("pending", domain.RecordPending, t0)
	revoked := mk("revoked", domain.RecordRevoked, t0)
	// A removed agent is in no list, all included.
	mk("removed", domain.RecordForgotten, t0)
	boot, err := e.st.GetAgent(ctx, e.agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	walk := func(all bool) []domain.Agent {
		return walkPages(t, func(after *query.AgentCursor) ([]domain.Agent, int, error) {
			return e.st.ListAgentsPage(ctx, e.org.ID, query.AgentFilter{All: all, Page: query.Page{Limit: 2}, Cursor: after})
		}, func(a domain.Agent) *query.AgentCursor {
			return &query.AgentCursor{Status: a.Status, CreatedAt: a.CreatedAt, ID: a.ID}
		})
	}
	tests := []struct {
		name string
		all  bool
		want []domain.ID
	}{
		{"alive", false, []domain.ID{boot.ID, b1.ID, b2.ID, pending.ID}},
		{"all", true, []domain.ID{boot.ID, b1.ID, b2.ID, pending.ID, revoked.ID}},
	}
	for _, tt := range tests {
		got := walk(tt.all)
		if len(got) != len(tt.want) {
			t.Fatalf("%s = %+v, want ids %v", tt.name, got, tt.want)
		}
		for i, a := range got {
			if a.ID != tt.want[i] {
				t.Errorf("%s %d = %s (%s), want %s", tt.name, i, a.Name, a.ID, tt.want[i])
			}
		}
	}
}

func TestTokenPagesAreNewestFirstAndOneActorsOwn(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	mk := func(actor domain.ActorRef, secret string, created time.Time) domain.Token {
		tok, err := e.st.CreateToken(ctx, domain.Token{
			ID: gen.New(), OrgID: e.org.ID, Actor: actor, Name: secret, Prefix: secret, Hash: []byte(e.org.ID.String() + secret), Status: domain.RecordActive, ExpiresAt: created.Add(time.Hour), CreatedAt: created,
		})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	old := mk(e.agent, "old", t0)
	tie1, tie2 := mk(e.agent, "tie1", t0.Add(time.Hour)), mk(e.agent, "tie2", t0.Add(time.Hour))
	newest := mk(e.agent, "new", t0.Add(2*time.Hour))
	mk(e.user, "theirs", t0)

	got := walkPages(t, func(after *query.TokenCursor) ([]domain.Token, int, error) {
		return e.st.ListTokens(ctx, e.org.ID, e.agent, query.Page{Limit: 2}, after)
	}, func(tk domain.Token) *query.TokenCursor {
		return &query.TokenCursor{CreatedAt: tk.CreatedAt, ID: tk.ID}
	})
	want := []domain.ID{newest.ID, tie2.ID, tie1.ID, old.ID}
	if len(got) != len(want) {
		t.Fatalf("tokens = %+v, want ids %v", got, want)
	}
	for i, tk := range got {
		if tk.ID != want[i] {
			t.Errorf("token %d = %s (%s), want %s", i, tk.Name, tk.ID, want[i])
		}
	}
}

func TestLiveLockPagesLeaveOutTheLapsedExactlyAsDomainDoes(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	now := t0.Add(time.Minute)
	for _, l := range []struct {
		name string
		ttl  time.Duration
	}{{"d", time.Hour}, {"a", time.Hour}, {"gone", time.Second}, {"at-expiry", time.Minute}, {"c", time.Hour}, {"b", time.Hour}} {
		if err := e.st.InsertLock(ctx, e.lock(l.name, l.ttl)); err != nil {
			t.Fatal(err)
		}
	}
	got := walkPages(t, func(after *string) ([]domain.Lock, int, error) {
		name := ""
		if after != nil {
			name = *after
		}
		return e.st.ListLiveLocks(ctx, e.org.ID, now, query.Page{Limit: 2}, name)
	}, func(l domain.Lock) *string { return &l.Name })
	want := []string{"a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("live locks = %+v, want %v", got, want)
	}
	for i, l := range got {
		if l.Name != want[i] {
			t.Errorf("lock %d = %s, want %s", i, l.Name, want[i])
		}
		if l.Expired(now) {
			t.Errorf("lock %s is expired by the domain's rule but was listed", l.Name)
		}
	}
}

func TestFirstChildrenKeepTheOrderAndCountsSeeThemAll(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	story := e.card(t, func(c *domain.Card) { c.Kind = domain.KindStory; c.Status = domain.StatusBacklog })
	child := func(p domain.Priority, s domain.Status) domain.Card {
		return e.card(t, func(c *domain.Card) {
			c.ParentID, c.Priority, c.Status = &story.ID, p, s
			if s.Terminal() {
				c.ClosedAt = new(t0)
			}
		})
	}
	child(domain.PriorityLow, domain.StatusReady)
	urgent := child(domain.PriorityUrgent, domain.StatusDone)
	normal := child(domain.PriorityNormal, domain.StatusReady)
	e.card(t, nil)

	first, err := e.st.ListFirstChildren(ctx, story.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].ID != urgent.ID || first[1].ID != normal.ID {
		t.Errorf("first children = %+v, want urgent then normal", first)
	}
	counts, err := e.st.CountChildrenByStatus(ctx, story.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 2 || counts[domain.StatusReady] != 2 || counts[domain.StatusDone] != 1 {
		t.Errorf("counts = %+v, want 2 ready and 1 done", counts)
	}
}
