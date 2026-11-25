// Package store implements, on Postgres with pgx and sqlc, the persistence interfaces that internal/server declares.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/BonfireTrunk/lorvik/internal/store/sqlcgen"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store is the Postgres implementation of the server's store interfaces. The root Store owns a
// connection pool; the Store handed to a WithTx callback shares one
// transaction and does not own the pool.
type Store struct {
	pool *pgxpool.Pool
	db   sqlcgen.DBTX
	q    *sqlcgen.Queries
	// inTx is true for the Store handed to a WithTx callback.
	inTx bool
}

// Open connects to Postgres and verifies the connection.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnLifetimeJitter = 5 * time.Minute
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = time.Minute
	// Instants are stored as timestamptz and come back as time.Time values.
	// pgx builds those with time.Unix, which attaches the process's local
	// zone; scanning in UTC keeps every time the server renders in UTC
	// regardless of where lorvikd runs. Clients convert for display.
	cfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		conn.TypeMap().RegisterType(&pgtype.Type{
			Name: "timestamptz", OID: pgtype.TimestamptzOID,
			Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
		})
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Store{pool: pool, db: pool, q: sqlcgen.New(pool)}, nil
}

// Close releases the pool. It is a no-op on a transaction-scoped Store.
func (s *Store) Close() {
	if !s.inTx {
		s.pool.Close()
	}
}

// Ping checks connectivity.
func (s *Store) Ping(ctx context.Context) error {
	if _, err := s.db.Exec(ctx, "SELECT 1"); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	return nil
}

// WithTx runs fn in a transaction, committing when fn returns nil and rolling
// back otherwise. Called on a transaction-scoped Store it reuses that
// transaction.
func (s *Store) WithTx(ctx context.Context, fn func(tx *Store) error) error {
	if s.inTx {
		return fn(s)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		// Rollback after a successful commit is a no-op; after a failure or
		// panic it releases the connection even if ctx is already cancelled.
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()
	inner := &Store{pool: s.pool, db: tx, q: s.q.WithTx(tx), inTx: true}
	if err := fn(inner); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// WithSnapshot runs fn in one read-only REPEATABLE READ transaction, so
// every statement in it sees the same snapshot, xmin included. Readers that
// pair a page with a cursor taken in a second statement use it so that no
// event can become visible between the two.
func (s *Store) WithSnapshot(ctx context.Context, fn func(tx *Store) error) error {
	if s.inTx {
		return fn(s)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(&Store{pool: s.pool, db: tx, q: s.q.WithTx(tx), inTx: true}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("end snapshot: %w", err)
	}
	return nil
}

// MigrationInfo describes one embedded migration and whether it is applied.
type MigrationInfo struct {
	Version   int64
	Name      string
	Applied   bool
	AppliedAt time.Time
}

// provider builds a goose provider over a stdlib view of the pool. The
// returned cleanup closes the provider and its sql.DB wrapper, not the pool.
func (s *Store) provider() (*goose.Provider, func(), error) {
	if s.inTx {
		return nil, nil, errors.New("migrations cannot run inside a transaction")
	}
	fsys, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, nil, fmt.Errorf("open embedded migrations: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, nil, fmt.Errorf("create migration locker: %w", err)
	}
	// The stdlib adapter is used by goose only; everything else uses pgx.
	db := stdlib.OpenDBFromPool(s.pool)
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("create migration provider: %w", err)
	}
	return p, func() { _ = p.Close() }, nil
}

// Migrate applies every pending embedded migration.
func (s *Store) Migrate(ctx context.Context) error {
	p, cleanup, err := s.provider()
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// MigrationStatus lists the embedded migrations with their applied state.
func (s *Store) MigrationStatus(ctx context.Context) ([]MigrationInfo, error) {
	p, cleanup, err := s.provider()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	statuses, err := p.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("read migration status: %w", err)
	}
	out := make([]MigrationInfo, 0, len(statuses))
	for _, st := range statuses {
		out = append(out, MigrationInfo{
			Version:   st.Source.Version,
			Name:      st.Source.Path,
			Applied:   st.State == goose.StateApplied,
			AppliedAt: st.AppliedAt,
		})
	}
	return out, nil
}
