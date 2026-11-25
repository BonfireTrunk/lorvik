package store

import (
	"context"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
	"github.com/BonfireTrunk/lorvik/internal/store/sqlcgen"
)

// SubscribeCard subscribes the chat to the card, unless it already is.
func (s *Store) SubscribeCard(ctx context.Context, w domain.CardSubscription) error {
	return wrap("subscribe card", s.q.InsertCardSubscription(ctx, sqlcgen.InsertCardSubscriptionParams{
		OrgID: int64(w.OrgID), CardID: int64(w.CardID), AgentID: int64(w.AgentID), Session: w.Session, CreatedAt: w.CreatedAt,
	}))
}

// UnsubscribeCard removes every subscription of the chat to the card; no error when
// there is none.
func (s *Store) UnsubscribeCard(ctx context.Context, cardID, agentID domain.ID, session string) error {
	return wrap("unsubscribe card", s.q.DeleteCardSubscription(ctx, sqlcgen.DeleteCardSubscriptionParams{
		CardID: int64(cardID), AgentID: int64(agentID), Session: session,
	}))
}

// ListCardSubscriptions returns the chat's subscriptions to cards that are not done or
// dropped, oldest first, one per card.
func (s *Store) ListCardSubscriptions(ctx context.Context, agentID domain.ID, session string) ([]query.SubscribedCard, error) {
	rows, err := s.q.ListCardSubscriptions(ctx, sqlcgen.ListCardSubscriptionsParams{AgentID: int64(agentID), Session: session})
	if err != nil {
		return nil, wrap("list card subscriptions", err)
	}
	return mapRows(rows, func(r sqlcgen.ListCardSubscriptionsRow) query.SubscribedCard {
		return query.SubscribedCard{
			CardID: domain.ID(r.CardID), ProjectID: domain.ID(r.ProjectID), ProjectSlug: r.ProjectSlug, Number: r.Number,
			Title: r.Title, Status: domain.Status(r.Status), SubscribedAt: r.SubscribedAt,
		}
	}), nil
}

// SubscribedCardsAmong returns the cards among cardIDs that the chat is
// subscribed to.
func (s *Store) SubscribedCardsAmong(ctx context.Context, orgID domain.ID, cardIDs []domain.ID, agentID domain.ID, session string) ([]domain.ID, error) {
	if len(cardIDs) == 0 {
		return nil, nil
	}
	rows, err := s.q.SubscribedCardsAmong(ctx, sqlcgen.SubscribedCardsAmongParams{
		OrgID: int64(orgID), AgentID: int64(agentID), Session: session, CardIds: ids(cardIDs),
	})
	if err != nil {
		return nil, wrap("subscribed cards among", err)
	}
	return mapRows(rows, func(id int64) domain.ID { return domain.ID(id) }), nil
}

// DeleteCardSubscriptionsOfLostChats deletes the subscriptions of chats not seen since
// seenSince, or with no chat row, and returns how many.
func (s *Store) DeleteCardSubscriptionsOfLostChats(ctx context.Context, seenSince time.Time) (int, error) {
	n, err := s.q.DeleteCardSubscriptionsOfLostChats(ctx, seenSince)
	return int(n), wrap("delete subscriptions of lost chats", err)
}

// DeleteCardSubscriptionsOfClosedCards deletes the subscriptions on cards done or
// dropped before closedBefore (by closed_at, else their last update), or gone, and
// returns how many.
func (s *Store) DeleteCardSubscriptionsOfClosedCards(ctx context.Context, closedBefore time.Time) (int, error) {
	n, err := s.q.DeleteCardSubscriptionsOfClosedCards(ctx, closedBefore)
	return int(n), wrap("delete subscriptions of closed cards", err)
}
