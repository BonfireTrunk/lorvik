package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

const (
	sweepBatch = 100
	// eventRetentionWarn is the per-org event count at which the sweeper
	// starts warning; retention or partitioning is the next step from there.
	eventRetentionWarn = 1_000_000
	retentionCheck     = time.Hour
)

// RunSweeper releases expired claims, writes the last call for those about
// to expire, removes expired locks, takes back chat grants past their end and
// drops subscriptions of lost chats and closed cards every interval, and checks
// event volume once an hour, until ctx ends.
func (s *Service) RunSweeper(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	nextRetention := time.Time{}
	for {
		n, err := s.sweepOnce(ctx, s.now())
		switch {
		case err != nil && ctx.Err() == nil:
			s.logger.ErrorContext(ctx, "sweep expired claims", "error", err)
		case n > 0:
			s.logger.InfoContext(ctx, "expired claims released", "count", n)
		}
		ne, eerr := s.announceEnding(ctx, s.now())
		switch {
		case eerr != nil && ctx.Err() == nil:
			s.logger.ErrorContext(ctx, "announce ending claims", "error", eerr)
		case ne > 0:
			s.logger.InfoContext(ctx, "ending claims announced", "count", ne)
		}
		nl, lerr := s.sweepLocks(ctx, s.now())
		switch {
		case lerr != nil && ctx.Err() == nil:
			s.logger.ErrorContext(ctx, "sweep expired locks", "error", lerr)
		case nl > 0:
			s.logger.InfoContext(ctx, "expired locks removed", "count", nl)
		}
		ng, gerr := s.endGrants(ctx, s.now())
		switch {
		case gerr != nil && ctx.Err() == nil:
			s.logger.ErrorContext(ctx, "end chat grants", "error", gerr)
		case ng > 0:
			s.logger.InfoContext(ctx, "chat grants ended", "count", ng)
		}
		nwl, nwc, werr := s.sweepSubscriptions(ctx, s.now())
		switch {
		case werr != nil && ctx.Err() == nil:
			s.logger.ErrorContext(ctx, "sweep card subscriptions", "error", werr)
		case nwl+nwc > 0:
			s.logger.InfoContext(ctx, "card subscriptions removed", "lost_chats", nwl, "closed_cards", nwc)
		}
		if now := s.now(); !now.Before(nextRetention) {
			nextRetention = now.Add(retentionCheck)
			if err := s.checkRetention(ctx); err != nil && ctx.Err() == nil {
				s.logger.ErrorContext(ctx, "check event retention", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// sweepOnce settles every lapsed claim it can see and reports how many it
// settled. Each claim is settled in a transaction of its own: deleted, its
// working card returned to ready and the event written, all or none. A claim
// renewed, released or retaken meanwhile is skipped, and one whose card
// changed meanwhile is left for the next sweep.
func (s *Service) sweepOnce(ctx context.Context, now time.Time) (int, error) {
	claims, err := s.store.ListExpiredClaims(ctx, now, sweepBatch)
	if err != nil {
		return 0, fmt.Errorf("list expired claims: %w", err)
	}
	released := 0
	for _, cl := range claims {
		settled, err := s.expireClaim(ctx, cl.CardID, now)
		switch {
		case errors.Is(err, domain.ErrVersionConflict):
			s.logger.DebugContext(ctx, "card changed during sweep", "card", cl.CardID)
		case err != nil:
			return released, err
		case settled:
			released++
		}
	}
	return released, nil
}

// expireClaim settles the card's claim if its lease has still passed once
// the claim row is locked. It reports false when there was nothing to settle,
// or when the claim's card is gone, in which case the claim is only deleted.
func (s *Service) expireClaim(ctx context.Context, cardID domain.ID, now time.Time) (bool, error) {
	settled := false
	err := s.store.WithTx(ctx, func(tx Store) error {
		cl, err := tx.LockExpiredClaim(ctx, cardID, now)
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock claim: %w", err)
		}
		if err := tx.DeleteClaim(ctx, cardID); err != nil {
			return fmt.Errorf("delete claim: %w", err)
		}
		c, err := tx.GetCard(ctx, cardID)
		if errors.Is(err, domain.ErrNotFound) {
			s.logger.DebugContext(ctx, "claimed card gone during sweep", "card", cardID)
			return nil
		}
		if err != nil {
			return err
		}
		if c.Released(now) {
			if c, err = tx.UpdateCard(ctx, c, c.Version); err != nil {
				return fmt.Errorf("return #%d to ready: %w", c.Number, err)
			}
		}
		settled = true
		// The former claimant is the event actor so their inbox shows it, and
		// it is nobody's own doing: the session of the claim goes in the
		// payload, which is how the inbox finds the chat that lost it.
		pl := payload(c.Title, "status", c.Status.String(), "expired_at", cl.ExpiresAt)
		if cl.Session != "" {
			pl["session"] = cl.Session
		}
		return s.emitSession(ctx, tx, cl.Actor, domain.UnpromptedSession, c.OrgID, &c.ProjectID, domain.EventCardClaimExpired, cardSubject(c), pl)
	})
	return settled, err
}

// announceEnding writes the last call, card.claim_ending, for every live
// claim that is due one (Claim.LastCallAt) and reports how many it wrote. A
// claim gets one call per expiry: a renewal moves the expiry and so arms the
// next. Each is written in a transaction of its own, and a claim renewed,
// released or retaken since it was listed is skipped. The holder is the
// event's actor so the call reaches their inbox, and it is written in
// UnpromptedSession so it is never taken for the holder's own doing.
func (s *Service) announceEnding(ctx context.Context, now time.Time) (int, error) {
	claims, err := s.store.ListEndingClaims(ctx, now, sweepBatch)
	if err != nil {
		return 0, fmt.Errorf("list ending claims: %w", err)
	}
	announced := 0
	for _, cl := range claims {
		err := s.store.WithTx(ctx, func(tx Store) error {
			marked, err := tx.MarkClaimEnding(ctx, cl.CardID, cl.ExpiresAt)
			if err != nil || !marked {
				return err
			}
			c, err := tx.GetCard(ctx, cl.CardID)
			if errors.Is(err, domain.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			announced++
			pl := payload(c.Title, "expires_at", cl.ExpiresAt)
			if cl.Session != "" {
				pl["session"] = cl.Session
			}
			return s.emitSession(ctx, tx, cl.Actor, domain.UnpromptedSession, c.OrgID, &c.ProjectID, domain.EventCardClaimEnding, cardSubject(c), pl)
		})
		if err != nil {
			return announced, err
		}
	}
	return announced, nil
}

// endGrants takes back every chat grant past its end and reports how many.
// The grant gave nothing from its end on (AgentSessionGrant.Live); this
// settles the row and writes the one event that says so. Each is ended in a
// transaction of its own, and one extended or taken back since it was listed
// is skipped. The agent is the event's actor, in UnpromptedSession, so the
// event reaches the chat that held the grant and is not taken for its doing.
func (s *Service) endGrants(ctx context.Context, now time.Time) (int, error) {
	grants, err := s.store.ListEndedAgentSessionGrants(ctx, now, sweepBatch)
	if err != nil {
		return 0, fmt.Errorf("list ended chat grants: %w", err)
	}
	ended := 0
	for _, g := range grants {
		err := s.store.WithTx(ctx, func(tx Store) error {
			g, err := tx.EndAgentSessionGrant(ctx, g.ProjectID, g.AgentID, g.Session, now)
			if errors.Is(err, domain.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			agent, err := tx.GetAgent(ctx, g.AgentID)
			if err != nil {
				return fmt.Errorf("load agent of chat %s: %w", g.Session, err)
			}
			proj, err := tx.GetProject(ctx, g.ProjectID)
			if err != nil {
				return fmt.Errorf("load project of chat %s: %w", g.Session, err)
			}
			ended++
			return s.emitSession(ctx, tx, agent.Ref(), domain.UnpromptedSession, proj.OrgID, &proj.ID, domain.EventAgentSessionRights, agentSubject(agent.ID),
				payload(agent.Name, "project", proj.Slug, "session", g.Session, "rights", []string{}, "ended_at", *g.EndsAt))
		})
		if err != nil {
			return ended, err
		}
	}
	return ended, nil
}

// checkRetention logs a warning for every org whose event log has grown past
// eventRetentionWarn rows.
func (s *Service) checkRetention(ctx context.Context) error {
	orgs, err := s.store.ListOrgs(ctx)
	if err != nil {
		return fmt.Errorf("list orgs: %w", err)
	}
	for _, o := range orgs {
		n, err := s.store.CountEventsAfter(ctx, o.ID, query.EventFilter{Limit: eventRetentionWarn, Limited: query.LimitedReach{All: true}})
		if err != nil {
			return fmt.Errorf("count events of org %s: %w", o.ID, err)
		}
		if n >= eventRetentionWarn {
			s.logger.WarnContext(ctx, "event log past retention marker; plan retention or partitioning", "org", o.ID, "events", n)
		}
	}
	return nil
}
