package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

// LockPath addresses a lock by name. It is exported because huma reads path
// values only into exported fields, and an embedded unexported type is one.
type LockPath struct {
	Name string `path:"name" doc:"1 to 40 characters of a-z, 0-9, hyphen and slash; escape a slash as %2F"`
}

// registerLocks mounts the named locks.
func (r *routes) registerLocks() {
	one := "/locks/{name}"
	huma.Register(r.api, operation("list-locks", http.MethodGet, "/locks", "locks", "Live locks of the org, by name", http.StatusOK),
		handle(r, func(ctx context.Context, in *ListPage) (apitypes.LockList, error) {
			return r.svc.ListLocks(ctx, in.Cursor, in.Limit)
		}))
	huma.Register(r.api, operation("acquire-lock", http.MethodPut, one, "locks", "Take a named lock, or renew your own with a new purpose", http.StatusCreated),
		handle(r, func(ctx context.Context, in *struct {
			LockPath
			Body apitypes.AcquireLock
		}) (apitypes.Lock, error) {
			return r.svc.AcquireLock(ctx, in.Name, in.Body)
		}))
	huma.Register(r.api, operation("renew-lock", http.MethodPost, one+"/renew", "locks", "Extend a lock you hold (same session)", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct {
			LockPath
			Body apitypes.RenewLock
		}) (apitypes.Lock, error) {
			return r.svc.RenewLock(ctx, in.Name, in.Body)
		}))
	type releaseIn struct {
		LockPath
		Force bool `query:"force" doc:"release another actor's lock (reviewers and above, users only)"`
	}
	huma.Register(r.api, operation("release-lock", http.MethodDelete, one, "locks", "Release a lock", http.StatusNoContent),
		handleEmpty(r, func(ctx context.Context, in *releaseIn) error {
			return r.svc.ReleaseLock(ctx, in.Name, in.Force)
		}))
}
