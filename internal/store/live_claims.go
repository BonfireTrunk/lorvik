package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

type liveClaimRow struct {
	CardID    int64     `db:"card_id"`
	ProjectID int64     `db:"project_id"`
	Number    int64     `db:"number"`
	Title     string    `db:"title"`
	Kind      int16     `db:"kind"`
	Status    int16     `db:"status"`
	ActorKind int16     `db:"actor_kind"`
	ActorID   int64     `db:"actor_id"`
	Session   string    `db:"session"`
	ClaimedAt time.Time `db:"claimed_at"`
	ExpiresAt time.Time `db:"expires_at"`
}

const (
	liveClaimCols = "cl.card_id, cl.project_id, c.number, c.title, c.kind, c.status, cl.actor_kind, cl.actor_id, cl.session, " +
		"cl.created_at AS claimed_at, cl.expires_at"
	liveClaimFrom = "claims cl JOIN cards c ON c.id = cl.card_id"
)

var liveClaimKeyset = keyset[liveClaimRow]{
	order: "cl.actor_kind, cl.actor_id, cl.session, cl.card_id",
	after: "(cl.actor_kind, cl.actor_id, cl.session, cl.card_id) > (@c_kind, @c_actor, @c_session, @c_card)",
	key: func(r liveClaimRow) pgx.NamedArgs {
		return pgx.NamedArgs{"c_kind": r.ActorKind, "c_actor": r.ActorID, "c_session": r.Session, "c_card": r.CardID}
	},
}

// ListLiveClaims returns one page of the org's claims whose lease runs past
// now, with the card each holds, limited to projects when that is not nil.
func (s *Store) ListLiveClaims(ctx context.Context, orgID domain.ID, projects []domain.ID, now time.Time, limit int, after *query.LiveClaimCursor) ([]query.LiveClaim, int, error) {
	const op = "list live claims"
	q := newFilter()
	q.add("cl.org_id = @org_id AND cl.expires_at > @now", "org_id", int64(orgID), "now", now)
	if projects != nil {
		q.add("cl.project_id = ANY(@projects::bigint[])", "projects", ids(projects))
	}
	var cursor pgx.NamedArgs
	if after != nil {
		cursor = pgx.NamedArgs{"c_kind": int16(after.Actor.Kind), "c_actor": int64(after.Actor.ID), "c_session": after.Session, "c_card": int64(after.Card)}
	}
	rows, remaining, err := listKeyset(ctx, s.db, q, liveClaimCols, liveClaimFrom, liveClaimKeyset, query.Page{Limit: limit}, cursor)
	if err != nil {
		return nil, 0, wrap(op, err)
	}
	return mapRows(rows, func(r liveClaimRow) query.LiveClaim {
		return query.LiveClaim{
			CardID: domain.ID(r.CardID), ProjectID: domain.ID(r.ProjectID), Number: r.Number, Title: r.Title,
			Kind: domain.CardKind(r.Kind), Status: domain.Status(r.Status), Actor: actor(r.ActorKind, r.ActorID),
			Session: r.Session, ClaimedAt: r.ClaimedAt, ExpiresAt: r.ExpiresAt,
		}
	}), remaining, nil
}
