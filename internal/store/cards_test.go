//go:build integration

package store

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

func TestCreateCardAllocatesNumbersPerProject(t *testing.T) {
	e := newEnv(t)
	other := e.newProject(t, "other")
	a := e.card(t, nil)
	b := e.card(t, nil)
	c := e.card(t, func(c *domain.Card) { c.ProjectID = other.ID })
	if a.Number != 1 || b.Number != 2 || c.Number != 1 || a.Version != 1 {
		t.Errorf("numbers = %d %d %d (version %d), want 1 2 1 (1)", a.Number, b.Number, c.Number, a.Version)
	}
	got, err := e.st.GetCardByNumber(t.Context(), other.ID, 1)
	if err != nil || got.ID != c.ID {
		t.Errorf("GetCardByNumber(other, 1) = %v, %v; want card c", got.ID, err)
	}
	if _, err := e.st.GetCardByNumber(t.Context(), other.ID, 2); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing number err = %v, want ErrNotFound", err)
	}
}

func TestUpdateCardIsVersioned(t *testing.T) {
	e := newEnv(t)
	c := e.card(t, nil)
	c.Title = "renamed"
	c.Assignee = &e.user
	updated, err := e.st.UpdateCard(t.Context(), c, 1)
	if err != nil || updated.Version != 2 || updated.Title != "renamed" || updated.Assignee == nil || *updated.Assignee != e.user {
		t.Fatalf("update = %+v, %v", updated, err)
	}
	if _, err := e.st.UpdateCard(t.Context(), c, 1); !errors.Is(err, domain.ErrVersionConflict) {
		t.Errorf("stale update err = %v, want ErrVersionConflict", err)
	}
	q := e.card(t, func(c *domain.Card) {
		c.Kind = domain.KindQuestion
		c.Question = &domain.Question{Choices: []string{"a", "b"}}
	})
	now := t0.Add(time.Hour)
	if _, err := q.Answer("a", e.user, false, nil, now); err != nil {
		t.Fatal(err)
	}
	answered, err := e.st.UpdateCard(t.Context(), q, 1)
	if err != nil || answered.Question == nil || answered.Question.Answer != "a" || answered.Question.AnsweredBy == nil || answered.Status != domain.StatusDone || answered.ClosedAt == nil {
		t.Errorf("answered = %+v, %v", answered, err)
	}
}

// An agent's answer is stored with whom it was given for, and the filter
// lists it for that person only until they keep it.
func TestAnAnswerForAPersonIsStoredAndListedUntilKept(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	question := func(c *domain.Card) { c.Kind = domain.KindQuestion }
	now := t0.Add(time.Hour)
	q := e.card(t, question)
	if _, err := q.Answer("a", e.agent, false, &domain.Behalf{For: e.user.ID, By: e.agent.ID, Reason: "decision #7", Session: "chat-a"}, now); err != nil {
		t.Fatal(err)
	}
	stored, err := e.st.UpdateCard(ctx, q, 1)
	if b := stored.Behalf; err != nil || b == nil || b.For != e.user.ID || b.By != e.agent.ID || b.Reason != "decision #7" || b.Session != "chat-a" || b.KeptAt != nil {
		t.Fatalf("stored behalf = %+v, %v; want it as given, not kept", stored.Behalf, err)
	}
	plain := e.card(t, question)
	if _, err := plain.Answer("a", e.user, false, nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.UpdateCard(ctx, plain, 1); err != nil {
		t.Fatal(err)
	}
	waiting := func(who domain.ID) []int64 {
		t.Helper()
		got, _, err := e.st.ListCards(ctx, e.project.ID, query.CardFilter{AwaitsKeepBy: &who, Page: query.Page{Limit: 10}})
		if err != nil {
			t.Fatal(err)
		}
		return numbers(got)
	}
	if got := waiting(e.user.ID); !equalInts(got, []int64{stored.Number}) {
		t.Errorf("waiting for the person = %v, want [%d]", got, stored.Number)
	}
	if got := waiting(e.agent.ID); len(got) != 0 {
		t.Errorf("waiting for another id = %v, want none", got)
	}
	if err := stored.Keep(e.user, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	kept, err := e.st.UpdateCard(ctx, stored, stored.Version)
	if err != nil || kept.Behalf == nil || kept.Behalf.KeptAt == nil || !kept.Behalf.KeptAt.Equal(now.Add(time.Minute)) || kept.Behalf.Reason != "decision #7" {
		t.Fatalf("kept behalf = %+v, %v; want kept at the given time", kept.Behalf, err)
	}
	if got := waiting(e.user.ID); len(got) != 0 {
		t.Errorf("waiting after keep = %v, want none", got)
	}
	if _, err := kept.Answer("b", e.user, false, nil, now.Add(2*time.Minute)); err == nil {
		t.Fatal("a kept answer was answered again")
	}
	q2 := e.card(t, question)
	if _, err := q2.Answer("a", e.agent, false, &domain.Behalf{For: e.user.ID, Reason: "r"}, now); err != nil {
		t.Fatal(err)
	}
	s2, err := e.st.UpdateCard(ctx, q2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Answer("b", e.user, false, nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	changed, err := e.st.UpdateCard(ctx, s2, s2.Version)
	if err != nil || changed.Behalf != nil {
		t.Errorf("changed behalf = %+v, %v; want cleared", changed.Behalf, err)
	}
}

func TestAQuestionKeepsItsForAPersonMark(t *testing.T) {
	e := newEnv(t)
	q := e.card(t, func(c *domain.Card) {
		c.Kind = domain.KindQuestion
		c.Question = &domain.Question{ForPerson: true}
	})
	got, err := e.st.GetCardByNumber(t.Context(), e.project.ID, q.Number)
	if err != nil || !got.ForPerson() {
		t.Fatalf("stored question for a person = %v, %v; want true", got.ForPerson(), err)
	}
	if err := got.ClearForPerson(e.user, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	cleared, err := e.st.UpdateCard(t.Context(), got, got.Version)
	if err != nil || cleared.ForPerson() {
		t.Errorf("cleared question for a person = %v, %v; want false", cleared.ForPerson(), err)
	}
	if plain := e.card(t, func(c *domain.Card) { c.Kind = domain.KindQuestion }); plain.ForPerson() {
		t.Error("a plain question reads as for a person")
	}
}

func TestListCardsPagesInBoardOrderBothWays(t *testing.T) {
	e := newEnv(t)
	e.card(t, func(c *domain.Card) { c.Priority = domain.PriorityLow })    // #1
	e.card(t, func(c *domain.Card) { c.Priority = domain.PriorityUrgent }) // #2
	e.card(t, nil)                                                         // #3
	e.card(t, func(c *domain.Card) { c.Status = domain.StatusBacklog })    // #4
	e.card(t, func(c *domain.Card) { c.Priority = domain.PriorityUrgent }) // #5
	ctx := t.Context()
	// Board order: status asc, priority desc, number: backlog #4, then ready #2 #5 #3 #1.
	p1, remaining, err := e.st.ListCards(ctx, e.project.ID, query.CardFilter{Page: query.Page{Limit: 2}})
	if err != nil || !equalInts(numbers(p1), []int64{4, 2}) || remaining != 3 {
		t.Fatalf("page 1 = %v remaining %d, %v; want [4 2] 3", numbers(p1), remaining, err)
	}
	cur := &query.CardCursor{Status: p1[1].Status, Priority: p1[1].Priority, Number: p1[1].Number}
	p2, remaining, err := e.st.ListCards(ctx, e.project.ID, query.CardFilter{Page: query.Page{Limit: 2}, Cursor: cur})
	if err != nil || !equalInts(numbers(p2), []int64{5, 3}) || remaining != 1 {
		t.Fatalf("page 2 = %v remaining %d, %v; want [5 3] 1", numbers(p2), remaining, err)
	}
	cur = &query.CardCursor{Status: p2[0].Status, Priority: p2[0].Priority, Number: p2[0].Number}
	back, remaining, err := e.st.ListCards(ctx, e.project.ID, query.CardFilter{Page: query.Page{Limit: 5, Backward: true}, Cursor: cur})
	if err != nil || !equalInts(numbers(back), []int64{4, 2}) || remaining != 0 {
		t.Fatalf("backward from #5 = %v remaining %d, %v; want [4 2] 0 in forward order", numbers(back), remaining, err)
	}
	open, _, err := e.st.ListCards(ctx, e.project.ID, query.CardFilter{Statuses: []domain.Status{domain.StatusReady}, Page: query.Page{Limit: 10}})
	if err != nil || len(open) != 4 {
		t.Errorf("ready only = %d, %v; want 4", len(open), err)
	}
}

func TestListCardsFiltersByActorAndQuery(t *testing.T) {
	e := newEnv(t)
	mine := e.card(t, func(c *domain.Card) { c.Title = "Rotate the signing keys" })
	assigned := e.card(t, func(c *domain.Card) { c.Title = "Write docs"; c.Assignee = &e.user })
	e.card(t, func(c *domain.Card) { c.Title = "Unrelated" })
	e.claim(t, mine, e.user, t0.Add(time.Hour))
	ctx := t.Context()
	got, _, err := e.st.ListCards(ctx, e.project.ID, query.CardFilter{Actor: &e.user, Page: query.Page{Limit: 10}})
	if err != nil || !equalInts(numbers(got), []int64{mine.Number, assigned.Number}) {
		t.Errorf("actor filter = %v, %v; want claimed and assigned", numbers(got), err)
	}
	hits, remaining, err := e.st.ListCards(ctx, e.project.ID, query.CardFilter{Query: "signing key", Page: query.Page{Limit: 10}})
	if err != nil || len(hits) != 1 || hits[0].ID != mine.ID || remaining != 0 {
		t.Errorf("query = %v remaining %d, %v; want the keys card", numbers(hits), remaining, err)
	}
}

func TestNextReadySkipsClaimedBlockedDroppedParentAndOtherAssignee(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	if _, err := e.st.NextReady(ctx, e.project.ID, e.agent, time.Now()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("empty project err = %v, want ErrNotFound", err)
	}
	urgentClaimed := e.card(t, func(c *domain.Card) { c.Priority = domain.PriorityUrgent })
	e.claim(t, urgentClaimed, e.agent, t0.Add(time.Hour))
	highBlocked := e.card(t, func(c *domain.Card) { c.Priority = domain.PriorityHigh })
	blocker := e.card(t, func(c *domain.Card) { c.Kind = domain.KindQuestion; c.Question = &domain.Question{} })
	if err := e.st.AddCardLink(ctx, domain.CardLink{FromCardID: blocker.ID, ToCardID: highBlocked.ID, Kind: domain.LinkBlocks, CreatedBy: e.agent, CreatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	dropped := e.card(t, func(c *domain.Card) { c.Status = domain.StatusDropped; c.DropReason = domain.DropObsolete })
	e.card(t, func(c *domain.Card) { c.Priority = domain.PriorityHigh; c.ParentID = &dropped.ID })
	someoneElse := e.agent
	someoneElse.ID++
	e.card(t, func(c *domain.Card) { c.Priority = domain.PriorityHigh; c.Assignee = &someoneElse })
	want := e.card(t, func(c *domain.Card) { c.Assignee = &e.agent })
	e.card(t, nil)
	got, err := e.st.NextReady(ctx, e.project.ID, e.agent, time.Now())
	if err != nil || got.ID != want.ID {
		t.Errorf("next = #%d, %v; want #%d", got.Number, err, want.Number)
	}
	blockers, err := e.st.ListBlockers(ctx, highBlocked.ID)
	if err != nil || len(blockers) != 1 || blockers[0].ID != blocker.ID {
		t.Errorf("blockers = %v, %v; want the question", numbers(blockers), err)
	}
}

func TestListChildrenReturnsDirectChildrenOnly(t *testing.T) {
	e := newEnv(t)
	root := e.card(t, nil)
	mid := e.card(t, func(c *domain.Card) { c.ParentID = &root.ID })
	e.card(t, func(c *domain.Card) { c.ParentID = &mid.ID })
	children, err := e.st.ListChildren(t.Context(), root.ID)
	if err != nil || len(children) != 1 || children[0].ID != mid.ID {
		t.Errorf("children of root = %v, %v", numbers(children), err)
	}
}

func TestClaimsRenewPastHalfLeaseAndExpire(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	fresh := e.card(t, nil)
	stale := e.card(t, nil)
	theirs := e.card(t, nil)
	e.claim(t, fresh, e.agent, t0.Add(25*time.Minute)) // 25 of 30 minutes left: untouched
	e.claim(t, stale, e.agent, t0.Add(10*time.Minute)) // under half: renewed
	e.claim(t, theirs, e.user, t0.Add(10*time.Minute))
	renewed, err := e.st.RenewClaims(ctx, e.org.ID, e.agent, "", t0)
	if err != nil || len(renewed) != 1 || renewed[0].CardID != stale.ID || !renewed[0].ExpiresAt.Equal(t0.Add(30*time.Minute)) {
		t.Fatalf("renewed = %+v, %v; want only the stale claim at now+30m", renewed, err)
	}
	mine, err := e.st.ListClaimsByActor(ctx, e.org.ID, e.agent)
	if err != nil || len(mine) != 2 {
		t.Errorf("claims by agent = %d, %v; want 2", len(mine), err)
	}
	sweep, err := e.st.ListExpiredClaims(ctx, t0.Add(20*time.Minute), maxPageSize)
	if err != nil {
		t.Fatal(err)
	}
	var expired []domain.Claim
	for _, cl := range sweep {
		if cl.OrgID == e.org.ID {
			expired = append(expired, cl)
		}
	}
	if len(expired) != 1 || expired[0].CardID != theirs.ID {
		t.Fatalf("expired in the org = %+v; want the user's claim only", expired)
	}
	if cl, err := e.st.LockExpiredClaim(ctx, theirs.ID, t0.Add(20*time.Minute)); err != nil || cl.Actor != e.user {
		t.Errorf("lock expired claim = %+v, %v; want the user's claim", cl, err)
	}
	if _, err := e.st.LockExpiredClaim(ctx, stale.ID, t0.Add(20*time.Minute)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("lock renewed claim err = %v, want ErrNotFound: its lease has not passed", err)
	}
	// PutClaim replaces an existing row for the card.
	e.claim(t, fresh, e.user, t0.Add(time.Hour))
	cl, err := e.st.GetClaim(ctx, fresh.ID)
	if err != nil || cl.Actor != e.user {
		t.Errorf("replaced claim = %+v, %v; want the user", cl, err)
	}
	if err := e.st.DeleteClaim(ctx, fresh.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.st.DeleteClaim(ctx, fresh.ID); err != nil {
		t.Errorf("deleting a missing claim = %v, want nil", err)
	}
}

func TestCardLinksAreIdempotentAndRemovable(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	a := e.card(t, nil)
	b := e.card(t, nil)
	l := domain.CardLink{FromCardID: a.ID, ToCardID: b.ID, Kind: domain.LinkRelates, CreatedBy: e.agent, CreatedAt: t0}
	if err := e.st.AddCardLink(ctx, l); err != nil {
		t.Fatal(err)
	}
	if err := e.st.AddCardLink(ctx, l); err != nil {
		t.Errorf("duplicate link = %v, want nil", err)
	}
	links, err := e.st.ListCardLinks(ctx, b.ID)
	if err != nil || len(links) != 1 || links[0].FromCardID != a.ID {
		t.Errorf("links of b = %+v, %v", links, err)
	}
	if err := e.st.RemoveCardLink(ctx, a.ID, b.ID, domain.LinkRelates); err != nil {
		t.Fatal(err)
	}
	if err := e.st.RemoveCardLink(ctx, a.ID, b.ID, domain.LinkRelates); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("removing a missing link = %v, want ErrNotFound", err)
	}
}

func TestCommentsListNewestFirst(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	c := e.card(t, nil)
	if _, err := e.st.LatestComment(ctx, c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("no comment err = %v", err)
	}
	for _, body := range []string{"first", "second"} {
		if err := e.st.AddComment(ctx, domain.Comment{ID: gen.New(), CardID: c.ID, Actor: e.agent, Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := e.st.LatestComment(ctx, c.ID)
	if err != nil || latest.Body != "second" {
		t.Errorf("latest = %+v, %v", latest, err)
	}
	all, err := e.st.ListComments(ctx, c.ID, query.HistoryPage{})
	if err != nil || len(all) != 2 || all[0].Body != "second" {
		t.Fatalf("list = %+v, %v", all, err)
	}
	newest, err := e.st.ListComments(ctx, c.ID, query.HistoryPage{Limit: 1})
	if err != nil || len(newest) != 1 || newest[0].ID != all[0].ID {
		t.Errorf("limit 1 = %+v, %v; want the newest", newest, err)
	}
	older, err := e.st.ListComments(ctx, c.ID, query.HistoryPage{Before: int64(all[0].ID)})
	if err != nil || len(older) != 1 || older[0].ID != all[1].ID {
		t.Errorf("before the newest = %+v, %v; want the first comment", older, err)
	}
}

func TestCountCardsByStatusAndOpenQuestions(t *testing.T) {
	e := newEnv(t)
	e.card(t, nil)
	e.card(t, func(c *domain.Card) { c.Status = domain.StatusWaiting })
	e.card(t, func(c *domain.Card) { c.Kind = domain.KindQuestion; c.Question = &domain.Question{} })
	e.card(t, func(c *domain.Card) {
		c.Kind = domain.KindQuestion
		c.Question = &domain.Question{}
		c.Status = domain.StatusDone
	})
	counts, err := e.st.CountCards(t.Context(), e.project.ID)
	if err != nil || counts.ByStatus[domain.StatusReady] != 2 || counts.ByStatus[domain.StatusWaiting] != 1 || counts.ByStatus[domain.StatusDone] != 1 || counts.OpenQuestions != 1 {
		t.Errorf("counts = %+v, %v", counts, err)
	}
}

func TestRelayedQuestionsAreListedButNotCountedAsOpen(t *testing.T) {
	e := newEnv(t)
	question := func(answeredBy *domain.ActorRef, relayed bool) domain.Card {
		return e.card(t, func(c *domain.Card) {
			c.Kind = domain.KindQuestion
			c.Question = &domain.Question{}
			if answeredBy != nil {
				c.Status = domain.StatusDone
				c.Question.Answer = "a"
				c.Question.AnsweredBy = answeredBy
				c.Question.AnsweredAt = &t0
				c.Question.Relayed = relayed
			}
		})
	}
	open := question(nil, false)
	byUser := question(&e.user, false)
	byEntitledAgent := question(&e.agent, false)
	relayed := question(&e.agent, true)
	e.card(t, func(c *domain.Card) { c.Status = domain.StatusDone })

	got, _, err := e.st.ListCards(t.Context(), e.project.ID, query.CardFilter{Relayed: true, Sort: query.SortNumber, Page: query.Page{Limit: 10}})
	if err != nil || len(got) != 1 || got[0].Number != relayed.Number {
		t.Errorf("relayed list = %v, %v; want only #%d (not #%d, #%d or #%d)", numbers(got), err, relayed.Number, open.Number, byUser.Number, byEntitledAgent.Number)
	}
	counts, err := e.st.CountCards(t.Context(), e.project.ID)
	if err != nil || counts.OpenQuestions != 1 {
		t.Errorf("open questions = %d, %v; want only the open one, a relayed answer does not wait for a human", counts.OpenQuestions, err)
	}
	if n, err := e.st.CountOpenQuestionsBy(t.Context(), e.project.ID, e.agent); err != nil || n != 1 {
		t.Errorf("agent's own count = %d, %v; want 1: only the open question, a relayed one is closed for its asker", n, err)
	}
}

func TestAStatedLeaseSurvivesRoundTripAndSetsTheRenewal(t *testing.T) {
	e := newEnv(t)
	c := e.card(t, nil)
	cl := domain.Claim{CardID: c.ID, OrgID: e.org.ID, ProjectID: c.ProjectID, Actor: e.agent, Session: "brisk-otter", ExpiresAt: t0.Add(2 * time.Hour), CreatedAt: t0, Lease: 2 * time.Hour}
	if err := e.st.PutClaim(t.Context(), cl); err != nil {
		t.Fatalf("put claim: %v", err)
	}
	if got, err := e.st.GetClaim(t.Context(), c.ID); err != nil || got.Lease != 2*time.Hour {
		t.Fatalf("GetClaim = %+v, %v; want the stated two hours", got, err)
	}
	// Inside the first half of the stated lease nothing is renewed, though
	// the project's far shorter lease would be past its half.
	if renewed, err := e.st.RenewClaims(t.Context(), e.org.ID, e.agent, "brisk-otter", t0.Add(50*time.Minute)); err != nil || len(renewed) != 0 {
		t.Errorf("renewed at 50m = %+v, %v; want none", renewed, err)
	}
	at := t0.Add(70 * time.Minute)
	renewed, err := e.st.RenewClaims(t.Context(), e.org.ID, e.agent, "brisk-otter", at)
	if err != nil || len(renewed) != 1 || !renewed[0].ExpiresAt.Equal(at.Add(2*time.Hour)) || renewed[0].Lease != 2*time.Hour {
		t.Errorf("renewed at 70m = %+v, %v; want one claim ending two hours later", renewed, err)
	}
	cl.Lease, cl.ExpiresAt = 0, t0.Add(time.Hour)
	if err := e.st.PutClaim(t.Context(), cl); err != nil {
		t.Fatalf("put claim: %v", err)
	}
	if got, err := e.st.GetClaim(t.Context(), c.ID); err != nil || got.Lease != 0 {
		t.Errorf("GetClaim after a claim that states nothing = %+v, %v; want no stated lease", got, err)
	}
}

// One last call per expiry: a claim is listed once its call is due, marking
// it takes it off the list, and a renewal lists it again for the new expiry.
func TestAnEndingClaimIsListedOncePerExpiry(t *testing.T) {
	e := newEnv(t)
	long, short := e.card(t, nil), e.card(t, nil)
	claims := []domain.Claim{
		{CardID: long.ID, OrgID: e.org.ID, ProjectID: long.ProjectID, Actor: e.agent, Session: "brisk-otter", ExpiresAt: t0.Add(30 * time.Minute), CreatedAt: t0},
		// A stated four minutes: the call is due a quarter of that before the end.
		{CardID: short.ID, OrgID: e.org.ID, ProjectID: short.ProjectID, Actor: e.agent, Session: "calm-heron", ExpiresAt: t0.Add(34 * time.Minute), CreatedAt: t0.Add(30 * time.Minute), Lease: 4 * time.Minute},
	}
	for _, cl := range claims {
		if err := e.st.PutClaim(t.Context(), cl); err != nil {
			t.Fatalf("put claim: %v", err)
		}
	}
	ending := func(at time.Duration) []domain.ID {
		t.Helper()
		got, err := e.st.ListEndingClaims(t.Context(), t0.Add(at), maxPageSize)
		if err != nil {
			t.Fatalf("list ending claims: %v", err)
		}
		// The sweeper's list covers every org, and tests share the database.
		var out []domain.ID
		for _, cl := range got {
			if cl.OrgID == e.org.ID {
				out = append(out, cl.CardID)
			}
		}
		return out
	}
	if got := ending(24 * time.Minute); len(got) != 0 {
		t.Errorf("ending at 24m = %v, want none: the call is due five minutes before the end", got)
	}
	if got := ending(25 * time.Minute); !slices.Equal(got, []domain.ID{long.ID}) {
		t.Fatalf("ending at 25m = %v, want the long claim", got)
	}
	if ok, err := e.st.MarkClaimEnding(t.Context(), long.ID, claims[0].ExpiresAt); err != nil || !ok {
		t.Fatalf("mark = %v, %v; want marked", ok, err)
	}
	if ok, err := e.st.MarkClaimEnding(t.Context(), long.ID, claims[0].ExpiresAt); err != nil || ok {
		t.Errorf("second mark = %v, %v; want nothing to mark", ok, err)
	}
	if got := ending(26 * time.Minute); len(got) != 0 {
		t.Errorf("ending after the mark = %v, want none", got)
	}
	// A lapsed claim is the sweeper's to settle, not to announce.
	if got := ending(30 * time.Minute); len(got) != 0 {
		t.Errorf("ending at the expiry = %v, want none", got)
	}

	renewed, err := e.st.RenewClaims(t.Context(), e.org.ID, e.agent, "brisk-otter", t0.Add(26*time.Minute))
	if err != nil || len(renewed) != 1 {
		t.Fatalf("renewed = %+v, %v; want the long claim", renewed, err)
	}
	if ok, err := e.st.MarkClaimEnding(t.Context(), long.ID, claims[0].ExpiresAt); err != nil || ok {
		t.Errorf("mark for the expiry before the renewal = %v, %v; want nothing to mark", ok, err)
	}
	if got := ending(51 * time.Minute); !slices.Equal(got, []domain.ID{long.ID}) {
		t.Errorf("ending at 51m = %v, want the renewed claim again", got)
	}

	if got := ending(32*time.Minute + 59*time.Second); len(got) != 0 {
		t.Errorf("ending at 32m59s = %v, want none: a four-minute lease calls one minute ahead", got)
	}
	if got := ending(33 * time.Minute); !slices.Equal(got, []domain.ID{short.ID}) {
		t.Errorf("ending at 33m = %v, want the short claim", got)
	}
}

func TestClaimSessionSurvivesRoundTripAndRenewal(t *testing.T) {
	e := newEnv(t)
	c := e.card(t, nil)
	cl := domain.Claim{CardID: c.ID, OrgID: e.org.ID, ProjectID: c.ProjectID, Actor: e.agent, Session: "brisk-otter", ExpiresAt: t0.Add(time.Minute), CreatedAt: t0}
	if err := e.st.PutClaim(t.Context(), cl); err != nil {
		t.Fatalf("put claim: %v", err)
	}
	got, err := e.st.GetClaim(t.Context(), c.ID)
	if err != nil || got.Session != "brisk-otter" {
		t.Fatalf("GetClaim = %+v, %v; want session brisk-otter", got, err)
	}
	renewed, err := e.st.RenewClaims(t.Context(), e.org.ID, e.agent, "brisk-otter", t0)
	if err != nil {
		t.Fatalf("renew claims: %v", err)
	}
	if len(renewed) != 1 || renewed[0].Session != "brisk-otter" {
		t.Errorf("renewed = %+v, want the one claim with its session kept", renewed)
	}
	plain := e.card(t, nil)
	e.claim(t, plain, e.agent, t0.Add(time.Hour))
	if got, err := e.st.GetClaim(t.Context(), plain.ID); err != nil || got.Session != "" {
		t.Errorf("claim without session = %+v, %v; want empty", got, err)
	}
}

func TestRenewClaimsScopesAgentsToTheirSessionAndHumansToNone(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	// Far from t0 so the org-wide expired-claim queries of other tests on the
	// shared database never see these rows.
	now := t0.Add(48 * time.Hour)
	put := func(actor domain.ActorRef, session string) domain.Card {
		t.Helper()
		c := e.card(t, nil)
		cl := domain.Claim{CardID: c.ID, OrgID: e.org.ID, ProjectID: c.ProjectID, Actor: actor, Session: session, ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now}
		if err := e.st.PutClaim(ctx, cl); err != nil {
			t.Fatalf("put claim: %v", err)
		}
		return c
	}
	mine := put(e.agent, "brisk-otter")
	put(e.agent, "quiet-heron")
	put(e.agent, "")
	renewed, err := e.st.RenewClaims(ctx, e.org.ID, e.agent, "brisk-otter", now)
	if err != nil || len(renewed) != 1 || renewed[0].CardID != mine.ID {
		t.Errorf("agent renewal = %+v, %v; want only the brisk-otter claim", renewed, err)
	}
	human := put(e.user, "laptop")
	renewed, err = e.st.RenewClaims(ctx, e.org.ID, e.user, "", now)
	if err != nil || len(renewed) != 1 || renewed[0].CardID != human.ID {
		t.Errorf("human renewal = %+v, %v; want the laptop claim renewed from no session", renewed, err)
	}
}

func TestBranchAndLaneRoundTripAndLaneGatesNextReady(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	working := e.card(t, func(c *domain.Card) { c.Lane = "schema"; c.Branch = "card-1-schema" })
	held := e.card(t, func(c *domain.Card) { c.Lane = "schema"; c.Priority = domain.PriorityHigh })
	free := e.card(t, func(c *domain.Card) { c.Lane = "web" })
	if got, err := e.st.GetCard(ctx, working.ID); err != nil || got.Lane != "schema" || got.Branch != "card-1-schema" {
		t.Fatalf("round trip = %+v, %v; want lane and branch back", got, err)
	}
	working.Status = domain.StatusWorking
	working.UpdatedAt = t0
	if _, err := e.st.UpdateCard(ctx, working, working.Version); err != nil {
		t.Fatal(err)
	}
	got, err := e.st.NextReady(ctx, e.project.ID, e.agent, time.Now())
	if err != nil || got.ID != free.ID {
		t.Fatalf("next = #%d, %v; want the web card, not #%d behind the busy schema lane", got.Number, err, held.Number)
	}
	e.claim(t, free, e.agent, t0.Add(time.Hour))
	if _, err := e.st.NextReady(ctx, e.project.ID, e.agent, time.Now()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("next with only a busy lane left = %v, want ErrNotFound", err)
	}
	waiting, err := e.st.CountOnBusyLanes(ctx, e.project.ID, e.agent, time.Now())
	if err != nil || len(waiting) != 1 || waiting[0].Lane != "schema" || waiting[0].Cards != 1 {
		t.Errorf("busy lanes = %+v, %v; want one card on schema", waiting, err)
	}
	working.Version++
	working.Lane, working.Branch = "", ""
	if got, err := e.st.UpdateCard(ctx, working, working.Version); err != nil || got.Lane != "" || got.Branch != "" {
		t.Errorf("clearing = %+v, %v; want empty lane and branch", got, err)
	}
}

func TestProjectConcurrencyIsStoredAndUpdated(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	p := e.newProject(t, "policy")
	if p.Concurrency != domain.ConcurrencyParallel {
		t.Fatalf("created concurrency = %v, want parallel", p.Concurrency)
	}
	p.Concurrency = domain.ConcurrencySerial
	if _, err := e.st.UpdateProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := e.st.GetProject(ctx, p.ID)
	if err != nil || got.Concurrency != domain.ConcurrencySerial {
		t.Errorf("after update = %v, %v; want serial", got.Concurrency, err)
	}
}

func TestSnapshotCardRevisionKeepsPreviousTextOnce(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	c := e.card(t, func(c *domain.Card) { c.Title, c.Body = "old title", "old body" })
	rev := gen.New()
	if err := e.st.SnapshotCardRevision(ctx, c.ID, 1, rev, e.user, t0, false); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	// A second writer that read the same version loses with a version
	// conflict, not a unique-index error, so the API answers 409.
	if err := e.st.SnapshotCardRevision(ctx, c.ID, 1, gen.New(), e.user, t0, false); !errors.Is(err, domain.ErrVersionConflict) {
		t.Errorf("racing snapshot err = %v, want ErrVersionConflict", err)
	}
	c.Title, c.Body = "new title", "new body"
	if _, err := e.st.UpdateCard(ctx, c, 1); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := e.st.SnapshotCardRevision(ctx, c.ID, 1, gen.New(), e.user, t0, false); !errors.Is(err, domain.ErrVersionConflict) {
		t.Errorf("stale snapshot err = %v, want ErrVersionConflict", err)
	}
	revs, err := e.st.ListCardRevisions(ctx, c.ID, query.HistoryPage{})
	if err != nil || len(revs) != 1 || revs[0].ID != rev || revs[0].Version != 1 || revs[0].Title != "old title" ||
		revs[0].Body != "" || revs[0].UpdatedBy != e.user || !revs[0].CreatedAt.Equal(t0) {
		t.Errorf("revisions = %+v, %v; want version 1 with its title and no body", revs, err)
	}
	if none, err := e.st.ListCardRevisions(ctx, c.ID, query.HistoryPage{Before: 1}); err != nil || len(none) != 0 {
		t.Errorf("before version 1 = %+v, %v; want none", none, err)
	}
	r, err := e.st.GetCardRevision(ctx, c.ID, 1)
	if err != nil || r.Body != "old body" {
		t.Errorf("revision 1 = %+v, %v", r, err)
	}
	if _, err := e.st.GetCardRevision(ctx, c.ID, 2); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing revision err = %v", err)
	}
}

func TestListRepliesReportsNewestUnseenCommentByOthers(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	c := e.card(t, nil)
	other := e.card(t, nil)
	mark := func(card domain.Card, seen domain.ID) {
		t.Helper()
		r := domain.CardRead{CardID: card.ID, ProjectID: card.ProjectID, Actor: e.user, SeenID: seen}
		if err := e.st.MarkCardSeen(ctx, r); err != nil {
			t.Fatalf("mark seen: %v", err)
		}
	}
	say := func(card domain.Card, by domain.ActorRef, body string) domain.Comment {
		t.Helper()
		h := domain.Comment{ID: gen.New(), CardID: card.ID, Actor: by, Body: body}
		if err := e.st.AddComment(ctx, h); err != nil {
			t.Fatalf("add comment: %v", err)
		}
		return h
	}

	say(c, e.agent, "before the mark")
	first := gen.New()
	mark(c, first)
	say(other, e.agent, "on a card the reader never opened")
	say(c, e.agent, "one")
	say(c, e.user, "the reader's own words")
	newest := say(c, e.agent, "two")

	got, _, err := e.st.ListReplies(ctx, e.user, e.project.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Comment.ID != newest.ID || got[0].Comment.Body != "two" || got[0].Comment.Actor != e.agent || got[0].Unseen != 2 {
		t.Fatalf("replies = %+v, want one entry: newest agent comment %d, unseen 2", got, newest.ID)
	}

	mark(c, first-1)
	if got, _, err = e.st.ListReplies(ctx, e.user, e.project.ID, 0, 10); err != nil || len(got) != 1 || got[0].Unseen != 2 {
		t.Errorf("after an earlier mark replies = %+v, %v; want GREATEST to keep the later seen_id (unseen 2)", got, err)
	}

	mark(c, gen.New())
	if got, _, err = e.st.ListReplies(ctx, e.user, e.project.ID, 0, 10); err != nil || len(got) != 0 {
		t.Errorf("after a later mark replies = %+v, %v; want none", got, err)
	}
	if got, _, err = e.st.ListReplies(ctx, e.agent, e.project.ID, 0, 10); err != nil || len(got) != 0 {
		t.Errorf("agent replies = %+v, %v; want none, it never marked a card", got, err)
	}
}

func TestListRepliesPagesBeforeAndCountsWhatRemains(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	var newest []domain.ID
	for range 3 {
		c := e.card(t, nil)
		if err := e.st.MarkCardSeen(ctx, domain.CardRead{CardID: c.ID, ProjectID: c.ProjectID, Actor: e.user, SeenID: gen.New()}); err != nil {
			t.Fatal(err)
		}
		h := domain.Comment{ID: gen.New(), CardID: c.ID, Actor: e.agent, Body: "re"}
		if err := e.st.AddComment(ctx, h); err != nil {
			t.Fatal(err)
		}
		newest = append([]domain.ID{h.ID}, newest...)
	}
	first, remaining, err := e.st.ListReplies(ctx, e.user, e.project.ID, 0, 2)
	if err != nil || len(first) != 2 || remaining != 1 || first[0].Comment.ID != newest[0] || first[1].Comment.ID != newest[1] {
		t.Fatalf("first page = %+v, remaining %d, %v; want the two newest, 1 left", first, remaining, err)
	}
	rest, remaining, err := e.st.ListReplies(ctx, e.user, e.project.ID, first[1].Comment.ID, 2)
	if err != nil || len(rest) != 1 || remaining != 0 || rest[0].Comment.ID != newest[2] {
		t.Errorf("second page = %+v, remaining %d, %v; want the oldest, none left", rest, remaining, err)
	}
}

func TestHistoryListWithoutALimitReadsOnlyTheDefaultPage(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	c := e.card(t, nil)
	for range defaultPageSize + 1 {
		if err := e.st.AddComment(ctx, domain.Comment{ID: gen.New(), CardID: c.ID, Actor: e.agent, Body: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.st.ListComments(ctx, c.ID, query.HistoryPage{})
	if err != nil || len(got) != defaultPageSize {
		t.Errorf("no limit = %d rows, %v; want the default page of %d", len(got), err, defaultPageSize)
	}
}

func TestCountOpenQuestionsByCountsOnlyTheActorsOpenQuestions(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	question := func(by domain.ActorRef, status domain.Status) {
		e.card(t, func(c *domain.Card) { c.Kind = domain.KindQuestion; c.CreatedBy = by; c.Status = status })
	}
	question(e.agent, domain.StatusReady)
	question(e.agent, domain.StatusWaiting)
	question(e.agent, domain.StatusDone)
	question(e.user, domain.StatusReady)
	e.card(t, func(c *domain.Card) { c.CreatedBy = e.agent })
	if n, err := e.st.CountOpenQuestionsBy(ctx, e.project.ID, e.agent); err != nil || n != 2 {
		t.Errorf("agent's open questions = %d, %v; want 2", n, err)
	}
}
