package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func lockPath(name, rest string) string { return "/locks/" + seg(name) + rest }

// AcquireLock calls PUT /locks/{name}. A 409 APIError names the holder; the
// same actor and session acquiring again renews the lock.
func (c *Client) AcquireLock(ctx context.Context, name string, in apitypes.AcquireLock) (apitypes.Lock, error) {
	return call[apitypes.Lock](ctx, c, http.MethodPut, lockPath(name, ""), in)
}

// RenewLock calls POST /locks/{name}/renew.
func (c *Client) RenewLock(ctx context.Context, name string, in apitypes.RenewLock) (apitypes.Lock, error) {
	return call[apitypes.Lock](ctx, c, http.MethodPost, lockPath(name, "/renew"), in)
}

// ReleaseLock calls DELETE /locks/{name}. Force releases another actor's lock
// and needs the reviewer role.
func (c *Client) ReleaseLock(ctx context.Context, name string, force bool) error {
	v := url.Values{}
	setBool(v, "force", force)
	return c.do(ctx, http.MethodDelete, withQuery(lockPath(name, ""), v), nil, nil)
}

// ListLiveClaims calls GET /claims: the live claims of the org, on the cards
// the caller can see, by holder.
func (c *Client) ListLiveClaims(ctx context.Context, p PageParams) (apitypes.LiveClaimList, error) {
	return call[apitypes.LiveClaimList](ctx, c, http.MethodGet, pageQuery("/claims", p, url.Values{}), nil)
}

// ListLocks calls GET /locks: the org's live locks by name.
func (c *Client) ListLocks(ctx context.Context, p PageParams) (apitypes.LockList, error) {
	return call[apitypes.LockList](ctx, c, http.MethodGet, pageQuery("/locks", p, url.Values{}), nil)
}
