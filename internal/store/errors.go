package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// Postgres SQLSTATE codes the store translates into domain errors.
const (
	pgUniqueViolation = "23505"
	pgFKViolation     = "23503"
)

// wrap adds op as context and translates database errors into domain errors:
// no rows and foreign key violations become ErrNotFound, unique violations
// become a ValidationError. Anything else is wrapped unchanged. ErrNotFound
// carries no op: it reaches clients as the problem detail, and the service
// names the missing thing ("card #42: not found") when that helps.
func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case pgUniqueViolation:
			return fmt.Errorf("%s: %w", op, &domain.ValidationError{Field: pgErr.ConstraintName, Msg: "already exists"})
		case pgFKViolation:
			return fmt.Errorf("referenced row: %w", domain.ErrNotFound)
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

// isNoRows reports whether err is pgx.ErrNoRows.
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// conflictOrNotFound explains why a versioned update touched no row: the row
// exists with another version (ErrVersionConflict) or does not exist.
func conflictOrNotFound(ctx context.Context, op string, exists func(context.Context) (bool, error)) error {
	ok, err := exists(ctx)
	if err != nil {
		return fmt.Errorf("%s: check existence: %w", op, err)
	}
	if ok {
		return fmt.Errorf("%s: %w", op, domain.ErrVersionConflict)
	}
	return domain.ErrNotFound
}
