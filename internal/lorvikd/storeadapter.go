package lorvikd

import (
	"context"

	"github.com/BonfireTrunk/lorvik/internal/server"
	"github.com/BonfireTrunk/lorvik/internal/store"
)

// serverStore adapts the Postgres store to server.Store. The store cannot
// name server.Store, so its WithTx hands callbacks a *store.Store; this
// wrapper re-wraps that so the callback sees a server.Store.
type serverStore struct{ *store.Store }

var (
	_ server.Store    = serverStore{}
	_ server.Notifier = serverStore{}
)

// WithSnapshot runs fn in one read-only snapshot, handing it a server.Store.
func (s serverStore) WithSnapshot(ctx context.Context, fn func(tx server.Store) error) error {
	return s.Store.WithSnapshot(ctx, func(tx *store.Store) error { return fn(serverStore{tx}) })
}

// WithTx runs fn in one transaction, handing it a server.Store.
func (s serverStore) WithTx(ctx context.Context, fn func(tx server.Store) error) error {
	return s.Store.WithTx(ctx, func(tx *store.Store) error { return fn(serverStore{tx}) })
}
