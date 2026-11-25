package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// subscriptionLostAfter is how long a chat may stay unseen before the sweeper
// drops its subscriptions. An open event stream keeps its chat seen
// (KeepChatSeen), so a chat waiting on a listener is never lost.
const subscriptionLostAfter = 24 * time.Hour

// closedSubscriptionKept is how long a subscription outlives the closing of its
// card, so a listener that is reconnecting still reads the closing event: the one
// a waiting chat most wants, such as a person's answer.
const closedSubscriptionKept = time.Hour

// chatSeen records that a chat of an agent is in use right now. The
// authenticator implements it, keeping its once-a-minute write limit.
type chatSeen interface {
	keepSeen(ctx context.Context, agent domain.ID, label string, now time.Time)
}

// KeepChatSeen records that the caller's chat is in use, for a request that
// stays open without the auth middleware seeing more of it, such as an event
// stream. It does nothing for a person or an agent request without a chat.
func (s *Service) KeepChatSeen(ctx context.Context) {
	p, ok := PrincipalFrom(ctx)
	if !ok || s.chats == nil || !p.IsAgent() || p.Session == "" {
		return
	}
	s.chats.keepSeen(ctx, p.Actor.ID, domain.BaseSession(p.Session), s.now())
}

// subscriber is the agent and base session label a subscription belongs to.
// Only an agent's chat subscribes: a person sees replies on the dashboard.
func subscriber(p Principal) (domain.ID, string, error) {
	switch {
	case !p.IsAgent():
		return 0, "", newProblem(http.StatusUnprocessableEntity, "subscribing is for an agent's chat; people see replies on the dashboard", "")
	case p.Session == "":
		return 0, "", newProblem(http.StatusUnprocessableEntity, "send the chat's session label",
			"the lorvik CLI sends it; other clients set the "+apitypes.HeaderSession+" header")
	}
	return p.Actor.ID, domain.BaseSession(p.Session), nil
}

// SubscribeCard subscribes the calling chat to the card, so an event on it wakes
// the chat. It is a no-op when the chat is already subscribed.
func (s *Service) SubscribeCard(ctx context.Context, slug string, number int64) error {
	p, err := s.principal(ctx)
	if err != nil {
		return err
	}
	agent, session, err := subscriber(p)
	if err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx Store) error {
		_, _, c, err := s.loadCard(ctx, tx, p, slug, number)
		if err != nil {
			return err
		}
		if c.Status.Terminal() {
			return conflict(fmt.Sprintf("card #%d is %s", c.Number, c.Status), "a closed card gets no more events; reopen it first if you wait on it")
		}
		have, err := tx.ListCardSubscriptions(ctx, agent, session)
		if err != nil {
			return fmt.Errorf("list subscriptions: %w", err)
		}
		if slices.ContainsFunc(have, func(w query.SubscribedCard) bool { return w.CardID == c.ID }) {
			return nil
		}
		if len(have) >= domain.MaxChatSubscriptions {
			return conflict(fmt.Sprintf("this chat is subscribed to %d cards; unsubscribe from one first", domain.MaxChatSubscriptions), "see them with `lorvik status`, drop one with `lorvik unsubscribe <n>`")
		}
		return tx.SubscribeCard(ctx, domain.CardSubscription{OrgID: p.OrgID, CardID: c.ID, AgentID: agent, Session: session, CreatedAt: s.now()})
	})
}

// UnsubscribeCard ends the calling chat's subscription to the card, also when there was
// none.
func (s *Service) UnsubscribeCard(ctx context.Context, slug string, number int64) error {
	p, err := s.principal(ctx)
	if err != nil {
		return err
	}
	agent, session, err := subscriber(p)
	if err != nil {
		return err
	}
	_, _, c, err := s.loadCard(ctx, s.store, p, slug, number)
	if err != nil {
		return err
	}
	return s.store.UnsubscribeCard(ctx, c.ID, agent, session)
}

// ListSubscriptions returns the calling chat's subscriptions to cards that are not done
// or dropped, leaving out the cards of projects the caller can no longer
// read.
func (s *Service) ListSubscriptions(ctx context.Context) (apitypes.SubscriptionList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.SubscriptionList{}, err
	}
	agent, session, err := subscriber(p)
	if err != nil {
		return apitypes.SubscriptionList{}, err
	}
	rows, err := s.store.ListCardSubscriptions(ctx, agent, session)
	if err != nil {
		return apitypes.SubscriptionList{}, fmt.Errorf("list subscriptions: %w", err)
	}
	readable := map[domain.ID]bool{}
	items := make([]apitypes.Subscription, 0, len(rows))
	for _, r := range rows {
		ok, known := readable[r.ProjectID]
		if !known {
			_, aerr := s.accessByID(ctx, s.store, p, r.ProjectID)
			if aerr != nil && !errors.Is(aerr, domain.ErrNotFound) && !errors.Is(aerr, domain.ErrForbidden) {
				return apitypes.SubscriptionList{}, aerr
			}
			ok = aerr == nil
			readable[r.ProjectID] = ok
		}
		if ok {
			items = append(items, apitypes.Subscription{Project: r.ProjectSlug, Number: r.Number, Title: r.Title, Status: r.Status.String(), SubscribedAt: r.SubscribedAt})
		}
	}
	return apitypes.SubscriptionList{Items: items}, nil
}

// sweepSubscriptions deletes the subscriptions of chats unseen for subscriptionLostAfter and the
// subscriptions on cards done or dropped more than closedSubscriptionKept ago. It reports how
// many of each.
func (s *Service) sweepSubscriptions(ctx context.Context, now time.Time) (lost, closed int, err error) {
	if lost, err = s.store.DeleteCardSubscriptionsOfLostChats(ctx, now.Add(-subscriptionLostAfter)); err != nil {
		return 0, 0, fmt.Errorf("delete subscriptions of lost chats: %w", err)
	}
	if closed, err = s.store.DeleteCardSubscriptionsOfClosedCards(ctx, now.Add(-closedSubscriptionKept)); err != nil {
		return lost, 0, fmt.Errorf("delete subscriptions of closed cards: %w", err)
	}
	return lost, closed, nil
}
