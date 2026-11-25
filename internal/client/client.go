// Package client is the Go HTTP client for the Lorvik API, used by the CLI.
// It knows nothing about the server's internals; it speaks apitypes only.
package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/build"
)

const (
	apiPrefix     = "/api/v1"
	maxErrorBytes = 8192
)

// LastUpdates is what the server said in the most recent response.
type LastUpdates struct {
	// Unread counts events by others after the Cursor the client sent. It is
	// zero when no cursor was sent. Values above apitypes.UnreadCap mean
	// "more than the cap".
	Unread int
	// Holding is what the chat holds, as of the last response that said: nil
	// when none did (the server was not asked, or is older than the header).
	// The event stream never says, so its responses leave this alone.
	Holding *apitypes.Holding
}

// Client talks to one Lorvik server with one token. Set Cursor and OnToken
// before the first call; they are read on every request.
type Client struct {
	// Cursor is the caller's inbox cursor (an event position). When non-empty it is
	// sent as X-Lorvik-Cursor on every request and the server answers with
	// X-Lorvik-Unread.
	Cursor string
	// Session, when non-empty, is sent as X-Lorvik-Session on every request
	// so claims record which chat or process made them.
	Session string
	// SessionName, when non-empty, is what people call this chat. It is sent
	// percent-encoded as X-Lorvik-Session-Name on every request.
	SessionName string
	// Listener marks every request as a background listener's, which the
	// server does not count as the chat working: no claim is renewed.
	Listener bool
	// Holding asks the server to say, on every response, what the chat holds.
	Holding bool
	// OnToken, when set, is called with the replacement secret whenever a
	// response carries X-Lorvik-Token. The client has already switched to the
	// new secret by then. It runs on the goroutine that made the call.
	OnToken func(secret string)
	// Renew, when set, is called once when the server refuses the token
	// (401) with the secret that failed; a different secret it returns
	// replaces the token and the request is sent again, once.
	Renew func(ctx context.Context, refused string) (string, error)

	base *url.URL
	http *http.Client

	mu    sync.Mutex
	token string
	last  LastUpdates
}

// New returns a client for baseURL. The token may be empty for unauthenticated
// routes such as healthz.
func New(baseURL, token string) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("api url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("api url %q: need scheme and host", baseURL)
	}
	return &Client{base: u, token: token, http: &http.Client{Transport: NewTransport(), Timeout: 30 * time.Second, CheckRedirect: stayOnOrigin}}, nil
}

// NewTransport returns a transport of its own with Go's default settings.
// http.DefaultTransport is shared by the whole process: anything that closes
// its idle connections, as httptest.Server.Close does, breaks a request of
// someone else that had just picked one up.
func NewTransport() http.RoundTripper {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	return t.Clone()
}

// stayOnOrigin follows a redirect only within the origin the request started
// on. Go's own rule keeps the Authorization header across a change of port or
// scheme and to any subdomain, which would hand the token to another server.
func stayOnOrigin(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return redirectError("stopped after 10 redirects")
	}
	if !SameOrigin(req.URL, via[0].URL) {
		return redirectError(fmt.Sprintf("refused a redirect to %s://%s: not the server the request was for", req.URL.Scheme, req.URL.Host))
	}
	return nil
}

// redirectError is a redirect the client did not follow. Something
// answered, so it is not a ConnError: a write is not queued for a server
// that is up behind a portal or a proxy.
type redirectError string

func (e redirectError) Error() string { return string(e) }

// SameOrigin reports whether a and b have the same scheme, host and port, with
// the scheme's default port filled in.
func SameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

func port(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}

// BaseURL returns the server URL this client talks to.
func (c *Client) BaseURL() string { return c.base.String() }

// Token returns the secret the next request will use. It changes when the
// server rotates the token.
func (c *Client) Token() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

// SetToken switches later requests to secret, for a caller that renewed the
// token itself.
func (c *Client) SetToken(secret string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = secret
}

// LastUpdates returns the unread count from the latest response. It is the
// zero value until a call has completed.
func (c *Client) LastUpdates() LastUpdates {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// APIError is a non-2xx response, parsed from a problem+json body when the
// server sent one.
type APIError struct {
	Status         int
	Title          string
	Detail         string
	Hint           string
	CurrentVersion int64
	// WaitingCards and BusyLanes describe ready cards held back by busy lanes
	// on a 404 from next.
	WaitingCards int
	BusyLanes    []string
	Errors       []apitypes.ProblemField
	// Body is the raw response text, kept when it was not a problem document.
	Body string
}

func (e *APIError) Error() string {
	var b strings.Builder
	b.WriteString("api " + strconv.Itoa(e.Status))
	switch {
	case e.Title != "":
		b.WriteString(": " + e.Title)
		if e.Detail != "" {
			b.WriteString(": " + e.Detail)
		}
	case e.Body != "":
		b.WriteString(": " + strings.TrimSpace(e.Body))
	}
	return b.String()
}

// ConnError means the server could not be reached at all.
type ConnError struct {
	URL string
	Err error
}

func (e *ConnError) Error() string { return fmt.Sprintf("cannot reach %s: %v", e.URL, e.Err) }
func (e *ConnError) Unwrap() error { return e.Err }

func parseAPIError(status int, raw []byte) *APIError {
	e := &APIError{Status: status}
	var p apitypes.Problem
	if err := json.Unmarshal(raw, &p); err == nil && p.Title != "" {
		e.Title, e.Detail, e.Hint = p.Title, p.Detail, p.Hint
		e.CurrentVersion, e.Errors = p.CurrentVersion, p.Errors
		e.WaitingCards, e.BusyLanes = p.WaitingCards, p.BusyLanes
		return e
	}
	e.Body = string(raw)
	return e
}

type requestKeyCtx struct{}

// WithRequestKey returns a context whose requests carry key as the
// Idempotency-Key header: a write sent again with the same key happens once.
func WithRequestKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, requestKeyCtx{}, key)
}

// newRequest builds an authenticated request with the cursor header.
func (c *Client) newRequest(ctx context.Context, method, path string, in any) (*http.Request, error) {
	var body io.Reader
	if in != nil {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(in); err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+apiPrefix+path, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "lorvik/"+build.Current().Version)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok := c.Token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if c.Cursor != "" {
		req.Header.Set(apitypes.HeaderCursor, c.Cursor)
	}
	if c.Session != "" {
		req.Header.Set(apitypes.HeaderSession, c.Session)
	}
	if c.SessionName != "" {
		req.Header.Set(apitypes.HeaderSessionName, url.PathEscape(c.SessionName))
	}
	if key, ok := ctx.Value(requestKeyCtx{}).(string); ok && key != "" {
		req.Header.Set(apitypes.HeaderIdempotencyKey, key)
	}
	if c.Listener {
		req.Header.Set(apitypes.HeaderListener, "1")
	}
	if c.Holding {
		req.Header.Set(apitypes.HeaderHolding, "1")
	}
	return req, nil
}

// connErr names the server in a transport failure. A refused redirect is an
// answer, not a failure to reach the server, and stays a plain error.
func (c *Client) connErr(err error) error {
	if rr, ok := errors.AsType[redirectError](err); ok {
		return fmt.Errorf("%s: %w", c.base.String(), rr)
	}
	if ue, ok := errors.AsType[*url.Error](err); ok {
		err = ue.Err
	}
	return &ConnError{URL: c.base.String(), Err: err}
}

// do performs a request and decodes a JSON response into out (when non-nil).
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	_, err := c.doHeader(ctx, method, path, in, out)
	return err
}

func (c *Client) doHeader(ctx context.Context, method, path string, in, out any) (http.Header, error) {
	h, err := c.doOnce(ctx, method, path, in, out)
	if ae, ok := errors.AsType[*APIError](err); ok && ae.Status == http.StatusUnauthorized && c.Renew != nil {
		refused := c.Token()
		secret, rerr := c.Renew(ctx, refused)
		if rerr != nil {
			return h, rerr
		}
		if secret != "" && secret != refused {
			c.SetToken(secret)
			return c.doOnce(ctx, method, path, in, out)
		}
	}
	return h, err
}

func (c *Client) doOnce(ctx context.Context, method, path string, in, out any) (http.Header, error) {
	req, err := c.newRequest(ctx, method, path, in)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.connErr(err)
	}
	defer resp.Body.Close()
	c.capture(resp.Header)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		return resp.Header, parseAPIError(resp.StatusCode, raw)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return resp.Header, nil
	}
	// A reply cut off on the way is a transport failure like a request that
	// never arrived: a keyed write is then sent again under its key and gets
	// what the first one made.
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.Header, c.connErr(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return resp.Header, fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	return resp.Header, nil
}

// capture records the unread count and switches to a rotated token. It runs
// before the status check so error responses are honoured too.
func (c *Client) capture(h http.Header) {
	unread, _ := strconv.Atoi(h.Get(apitypes.HeaderUnread))
	next := h.Get(apitypes.HeaderToken)
	c.mu.Lock()
	c.last.Unread = max(unread, 0)
	if held := holdingOf(h); held != nil {
		c.last.Holding = held
	}
	if next != "" {
		c.token = next
	}
	c.mu.Unlock()
	if next != "" && c.OnToken != nil {
		c.OnToken(next)
	}
}

// holdingOf decodes X-Lorvik-Holding. A value that does not decode is no
// answer: the list is a reminder and never worth failing a command for.
func holdingOf(h http.Header) *apitypes.Holding {
	raw, err := base64.StdEncoding.DecodeString(h.Get(apitypes.HeaderHolding))
	if err != nil || len(raw) == 0 {
		return nil
	}
	var held apitypes.Holding
	if json.Unmarshal(raw, &held) != nil {
		return nil
	}
	return &held
}

// call is the generic form of do for a typed result.
func call[T any](ctx context.Context, c *Client, method, path string, in any) (T, error) {
	var out T
	err := c.do(ctx, method, path, in, &out)
	return out, err
}
