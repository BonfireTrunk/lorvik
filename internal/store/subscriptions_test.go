//go:build integration

package store

import (
	"slices"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
)

func (e *env) subscribe(t *testing.T, c domain.Card, session string, at time.Time) {
	t.Helper()
	w := domain.CardSubscription{OrgID: e.org.ID, CardID: c.ID, AgentID: e.agent.ID, Session: session, CreatedAt: at}
	if err := e.st.SubscribeCard(t.Context(), w); err != nil {
		t.Fatalf("subscribe card: %v", err)
	}
}

func (e *env) seen(t *testing.T, session string, at time.Time) {
	t.Helper()
	if err := e.st.TouchAgentSession(t.Context(), e.agent.ID, session, at); err != nil {
		t.Fatalf("touch session: %v", err)
	}
}

func (e *env) subscribed(t *testing.T, session string) []domain.ID {
	t.Helper()
	rows, err := e.st.ListCardSubscriptions(t.Context(), e.agent.ID, session)
	if err != nil {
		t.Fatalf("list card subscriptions: %v", err)
	}
	out := make([]domain.ID, len(rows))
	for i, r := range rows {
		out[i] = r.CardID
	}
	return out
}

func TestSubscribeCardIsIdempotentAndUnsubscribeDeletesEveryRowOfTheChat(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	c := e.card(t, func(c *domain.Card) { c.Title = "waiting on the owner" })
	e.seen(t, "lead", t0)
	e.seen(t, "other", t0)
	e.subscribe(t, c, "lead", t0)
	e.subscribe(t, c, "lead", t0.Add(time.Hour))
	e.subscribe(t, c, "other", t0)

	rows, err := e.st.ListCardSubscriptions(ctx, e.agent.ID, "lead")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("subscriptions after subscribing twice = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.CardID != c.ID || r.ProjectSlug != e.project.Slug || r.Number != c.Number || r.Title != "waiting on the owner" ||
		r.Status != domain.StatusReady || !r.SubscribedAt.Equal(t0) {
		t.Errorf("subscription = %+v, want the card's project, number, title and status, started at the first call", r)
	}

	for range 2 {
		if err := e.st.UnsubscribeCard(ctx, c.ID, e.agent.ID, "lead"); err != nil {
			t.Fatalf("unsubscribe: %v", err)
		}
	}
	if got := e.subscribed(t, "lead"); len(got) != 0 {
		t.Errorf("subscriptions after unsubscribing = %v, want none", got)
	}
	if got := e.subscribed(t, "other"); !slices.Equal(got, []domain.ID{c.ID}) {
		t.Errorf("another chat's subscriptions = %v, want its own kept", got)
	}
}

func TestListCardSubscriptionsLeavesOutClosedCardsAndOrdersOldestFirst(t *testing.T) {
	e := newEnv(t)
	first := e.card(t, nil)
	second := e.card(t, nil)
	done := e.card(t, func(c *domain.Card) { c.Status = domain.StatusDone })
	dropped := e.card(t, func(c *domain.Card) { c.Status = domain.StatusDropped; c.DropReason = domain.DropObsolete })
	e.seen(t, "order", t0)
	e.subscribe(t, second, "order", t0.Add(time.Minute))
	e.subscribe(t, first, "order", t0)
	e.subscribe(t, done, "order", t0)
	e.subscribe(t, dropped, "order", t0)
	if got, want := e.subscribed(t, "order"), []domain.ID{first.ID, second.ID}; !slices.Equal(got, want) {
		t.Errorf("subscriptions = %v, want the open cards oldest first %v", got, want)
	}
}

func TestSubscribedCardsAmongReturnsOnlyTheChatsOwnSubscriptions(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	mine := e.card(t, nil)
	others := e.card(t, nil)
	unsubscribed := e.card(t, nil)
	for _, s := range []string{"lead", "other"} {
		e.seen(t, s, t0)
	}
	e.subscribe(t, mine, "lead", t0)
	e.subscribe(t, mine, "lead", t0)
	e.subscribe(t, others, "other", t0)
	got, err := e.st.SubscribedCardsAmong(ctx, e.org.ID, []domain.ID{mine.ID, others.ID, unsubscribed.ID}, e.agent.ID, "lead")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []domain.ID{mine.ID}) {
		t.Errorf("subscribed = %v, want only the card this chat subscribed to, once", got)
	}
}
func TestSweepDeletesSubscriptionsOfLostChatsAndClosedCardsAndKeepsLiveOnes(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	open := e.card(t, nil)
	closed := e.card(t, func(c *domain.Card) { c.Status = domain.StatusDone })
	justClosed := e.card(t, func(c *domain.Card) {
		closedAt := t0.Add(2 * time.Hour)
		c.Status, c.ClosedAt = domain.StatusDone, &closedAt
	})
	e.seen(t, "stale", t0.Add(-30*24*time.Hour))
	e.seen(t, "live", t0)
	e.subscribe(t, open, "stale", t0)
	e.subscribe(t, open, "norow", t0)
	e.subscribe(t, open, "live", t0)
	e.subscribe(t, closed, "live", t0)
	e.subscribe(t, justClosed, "live", t0)

	if _, err := e.st.DeleteCardSubscriptionsOfLostChats(ctx, t0.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.DeleteCardSubscriptionsOfClosedCards(ctx, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for session, want := range map[string][]domain.ID{"stale": nil, "norow": nil, "live": {open.ID, justClosed.ID}} {
		rows := e.subscriptionRows(t, session)
		if !slices.Equal(rows, want) {
			t.Errorf("chat %q keeps subscriptions to %v, want %v", session, rows, want)
		}
	}
}

// subscriptionRows reads the cards the chat still has a subscription row on, the closed
// ones included, which ListCardSubscriptions would hide.
func (e *env) subscriptionRows(t *testing.T, session string) []domain.ID {
	t.Helper()
	rows, err := e.st.db.Query(t.Context(), "SELECT card_id FROM card_subscriptions WHERE org_id = $1 AND agent_id = $2 AND session = $3 ORDER BY card_id",
		int64(e.org.ID), int64(e.agent.ID), session)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []domain.ID
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, domain.ID(id))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
