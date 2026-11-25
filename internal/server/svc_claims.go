package server

import (
	"context"
	"fmt"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

const claimList = "live-claims"

// ListLiveClaims lists the org's live claims on cards in projects the caller
// can read, or in the one project named. A lease past its end is left out
// even while the sweeper has not yet deleted its row.
func (s *Service) ListLiveClaims(ctx context.Context, projectSlug string, limit int, token string) (apitypes.LiveClaimList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.LiveClaimList{}, err
	}
	cur, err := decodeCursor(claimList, token)
	if err != nil {
		return apitypes.LiveClaimList{}, err
	}
	scope, err := s.projectScope(ctx, p, projectSlug)
	if err != nil {
		return apitypes.LiveClaimList{}, err
	}
	var after *query.LiveClaimCursor
	if cur != nil {
		after = &query.LiveClaimCursor{Actor: domain.ActorRef{Kind: domain.ActorKind(cur.ActorKind), ID: domain.ID(cur.Peer)}, Session: cur.Name, Card: domain.ID(cur.ID)}
	}
	now := s.now()
	rows, remaining, err := s.store.ListLiveClaims(ctx, p.OrgID, scope, now, pageSize(limit), after)
	if err != nil {
		return apitypes.LiveClaimList{}, fmt.Errorf("list live claims: %w", err)
	}
	lk := s.lookup(s.store, p)
	chats := make([]chatOf, len(rows))
	for i, r := range rows {
		chats[i] = chatOf{r.Actor, r.Session}
	}
	if err := lk.loadSessionNames(ctx, chats...); err != nil {
		return apitypes.LiveClaimList{}, err
	}
	items := make([]apitypes.LiveClaim, len(rows))
	for i, r := range rows {
		slug, err := lk.projectSlug(ctx, r.ProjectID)
		if err != nil {
			return apitypes.LiveClaimList{}, err
		}
		name, err := lk.sessionName(ctx, r.Actor, r.Session)
		if err != nil {
			return apitypes.LiveClaimList{}, err
		}
		held := domain.Claim{Actor: r.Actor, Session: r.Session, ExpiresAt: r.ExpiresAt}
		items[i] = apitypes.LiveClaim{
			Card:        apitypes.LiveClaimCard{Project: slug, Number: r.Number, Title: r.Title, Kind: r.Kind.String(), Status: r.Status.String()},
			Holder:      toActor(r.Actor),
			Session:     r.Session,
			SessionName: name,
			ClaimedAt:   r.ClaimedAt,
			ExpiresAt:   r.ExpiresAt,
			Mine:        held.HeldBy(p.Actor, p.Session, now),
		}
	}
	key := func(r query.LiveClaim) cursor {
		return cursor{ActorKind: int16(r.Actor.Kind), Peer: int64(r.Actor.ID), Name: r.Session, ID: int64(r.CardID)}
	}
	return apitypes.LiveClaimList{Items: items, Page: forwardPage(claimList, rows, key, remaining)}, nil
}
