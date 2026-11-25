//go:build integration

package store

import (
	"slices"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

func (e *env) claimFor(t *testing.T, c domain.Card, actor domain.ActorRef, session string, created, expires time.Time) {
	t.Helper()
	cl := domain.Claim{CardID: c.ID, OrgID: e.org.ID, ProjectID: c.ProjectID, Actor: actor, Session: session, ExpiresAt: expires, CreatedAt: created}
	if err := e.st.PutClaim(t.Context(), cl); err != nil {
		t.Fatalf("put claim: %v", err)
	}
}

func TestListLiveClaimsOrdersByHolderSessionCardAndPagesEveryRowOnce(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	now := t0.Add(time.Hour)
	live := now.Add(10 * time.Minute)
	a1 := e.card(t, func(c *domain.Card) { c.Title = "a1" })
	a2 := e.card(t, func(c *domain.Card) { c.Title = "a2" })
	b1 := e.card(t, func(c *domain.Card) { c.Title = "b1" })
	u1 := e.card(t, func(c *domain.Card) { c.Title = "u1" })
	e.claimFor(t, a2, e.agent, "alpha", t0, live)
	e.claimFor(t, a1, e.agent, "alpha", t0, live)
	e.claimFor(t, b1, e.agent, "beta", t0.Add(time.Minute), live)
	e.claimFor(t, u1, e.user, "", t0, live)

	want := []domain.ID{u1.ID, a1.ID, a2.ID, b1.ID}
	var got []query.LiveClaim
	var after *query.LiveClaimCursor
	for range 10 {
		page, remaining, err := e.st.ListLiveClaims(ctx, e.org.ID, nil, now, 3, after)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 && (len(page) != 3 || remaining != 1) {
			t.Fatalf("page 1 = %d remaining %d, want 3 and 1", len(page), remaining)
		}
		got = append(got, page...)
		if remaining == 0 {
			break
		}
		last := page[len(page)-1]
		after = &query.LiveClaimCursor{Actor: last.Actor, Session: last.Session, Card: last.CardID}
	}
	var ids []domain.ID
	for _, c := range got {
		ids = append(ids, c.CardID)
	}
	if !slices.Equal(ids, want) {
		t.Fatalf("claim order = %v, want %v: user first, then the agent by session and card", ids, want)
	}
	last := got[3]
	if last.Title != "b1" || last.Kind != domain.KindTask || last.Status != domain.StatusReady || last.Actor != e.agent ||
		last.Session != "beta" || !last.ClaimedAt.Equal(t0.Add(time.Minute)) || !last.ExpiresAt.Equal(live) {
		t.Errorf("last row = %+v, want b1's card fields, holder, session and lease", last)
	}
}

func TestListLiveClaimsLeavesOutExpiredLeasesAndOtherOrgsAndFiltersProjects(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	otherOrg, err := e.st.CreateOrg(ctx, domain.Org{ID: gen.New(), Name: "Other", Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	now := t0.Add(time.Hour)
	web := e.newProject(t, "web")
	inMain := e.card(t, nil)
	inWeb := e.card(t, func(c *domain.Card) { c.ProjectID = web.ID })
	expired := e.card(t, nil)
	boundary := e.card(t, nil)
	foreign := e.card(t, nil)
	e.claimFor(t, inMain, e.agent, "s", t0, now.Add(time.Minute))
	e.claimFor(t, inWeb, e.agent, "s", t0, now.Add(time.Minute))
	e.claimFor(t, expired, e.agent, "s", t0, now.Add(-time.Minute))
	e.claimFor(t, boundary, e.agent, "s", t0, now)
	if err := e.st.PutClaim(ctx, domain.Claim{CardID: foreign.ID, OrgID: otherOrg.ID, ProjectID: foreign.ProjectID, Actor: e.agent, Session: "s", ExpiresAt: now.Add(time.Minute), CreatedAt: t0}); err != nil {
		t.Fatalf("put claim: %v", err)
	}

	idsOf := func(projects []domain.ID) []domain.ID {
		t.Helper()
		rows, remaining, err := e.st.ListLiveClaims(ctx, e.org.ID, projects, now, 50, nil)
		if err != nil || remaining != 0 {
			t.Fatalf("list = %v, remaining %d", err, remaining)
		}
		var out []domain.ID
		for _, r := range rows {
			out = append(out, r.CardID)
		}
		slices.Sort(out)
		return out
	}
	both := []domain.ID{inMain.ID, inWeb.ID}
	slices.Sort(both)
	if got := idsOf(nil); !slices.Equal(got, both) {
		t.Errorf("org-wide = %v, want %v: not the expired or boundary lease, not another org's", got, both)
	}
	if got := idsOf([]domain.ID{web.ID}); !slices.Equal(got, []domain.ID{inWeb.ID}) {
		t.Errorf("web only = %v, want %v", got, inWeb.ID)
	}
	if got := idsOf([]domain.ID{}); len(got) != 0 {
		t.Errorf("no visible project = %v, want none", got)
	}
}
