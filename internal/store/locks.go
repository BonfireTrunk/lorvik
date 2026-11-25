package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
	"github.com/BonfireTrunk/lorvik/internal/store/sqlcgen"
)

// GetLock returns the org's lock of that name, expired or not, or
// ErrNotFound. Inside a transaction the row stays locked until it ends.
func (s *Store) GetLock(ctx context.Context, orgID domain.ID, name string) (domain.Lock, error) {
	row, err := s.q.GetLock(ctx, sqlcgen.GetLockParams{OrgID: int64(orgID), Name: name})
	if err != nil {
		return domain.Lock{}, wrap("get lock", err)
	}
	return lockFromRow(row), nil
}

// InsertLock adds a lock row and returns ErrLockHeld when the org already has
// a row of that name.
func (s *Store) InsertLock(ctx context.Context, l domain.Lock) error {
	n, err := s.q.InsertLock(ctx, sqlcgen.InsertLockParams{
		ID: int64(l.ID), OrgID: int64(l.OrgID), HolderKind: int16(l.Holder.Kind), HolderID: int64(l.Holder.ID),
		CardID: idPtr(l.CardID), AcquiredAt: l.AcquiredAt, ExpiresAt: l.ExpiresAt,
		Name: l.Name, Session: l.Session, Purpose: l.Purpose,
	})
	if err != nil {
		return wrap("insert lock", err)
	}
	if n == 0 {
		return domain.ErrLockHeld
	}
	return nil
}

// UpdateLockHolder rewrites holder, session, purpose, card and both times of
// the row with l.ID, or returns ErrNotFound.
func (s *Store) UpdateLockHolder(ctx context.Context, l domain.Lock) error {
	n, err := s.q.UpdateLockHolder(ctx, sqlcgen.UpdateLockHolderParams{
		ID: int64(l.ID), HolderKind: int16(l.Holder.Kind), HolderID: int64(l.Holder.ID), Session: l.Session,
		Purpose: l.Purpose, CardID: idPtr(l.CardID), AcquiredAt: l.AcquiredAt, ExpiresAt: l.ExpiresAt,
	})
	if err != nil {
		return wrap("update lock holder", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// RenewLock sets the expiry of the row, or returns ErrNotFound.
func (s *Store) RenewLock(ctx context.Context, id domain.ID, expiresAt time.Time) error {
	n, err := s.q.RenewLock(ctx, sqlcgen.RenewLockParams{ID: int64(id), ExpiresAt: expiresAt})
	if err != nil {
		return wrap("renew lock", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DeleteLock removes the row, or returns ErrNotFound.
func (s *Store) DeleteLock(ctx context.Context, id domain.ID) error {
	n, err := s.q.DeleteLock(ctx, int64(id))
	if err != nil {
		return wrap("delete lock", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListLocks returns every row of the org by name, expired ones included.
func (s *Store) ListLocks(ctx context.Context, orgID domain.ID) ([]domain.Lock, error) {
	rows, err := s.q.ListLocks(ctx, int64(orgID))
	if err != nil {
		return nil, wrap("list locks", err)
	}
	return mapRows(rows, lockFromRow), nil
}

// ListLiveLocks returns one page of the org's unexpired locks by name, after
// the named lock when after is set; remaining counts the live locks beyond it,
// capped.
func (s *Store) ListLiveLocks(ctx context.Context, orgID domain.ID, now time.Time, pg query.Page, after string) ([]domain.Lock, int, error) {
	const op = "list live locks"
	q := newFilter()
	q.add("org_id = @org_id AND expires_at > @now", "org_id", int64(orgID), "now", now)
	var cursor pgx.NamedArgs
	if after != "" {
		cursor = pgx.NamedArgs{"c_name": after}
	}
	rows, remaining, err := listKeyset(ctx, s.db, q, "*", "locks", lockKeyset, pg, cursor)
	if err != nil {
		return nil, 0, wrap(op, err)
	}
	return mapRows(rows, lockFromRow), remaining, nil
}

// ListExpiredLocks returns up to limit rows whose lease passed, oldest first.
func (s *Store) ListExpiredLocks(ctx context.Context, before time.Time, limit int) ([]domain.Lock, error) {
	rows, err := s.q.ListExpiredLocks(ctx, sqlcgen.ListExpiredLocksParams{Before: before, MaxRows: int32(clampLimit(limit))})
	if err != nil {
		return nil, wrap("list expired locks", err)
	}
	return mapRows(rows, lockFromRow), nil
}
