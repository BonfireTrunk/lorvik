package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	githubWebURL   = "https://github.com"
	githubAPIURL   = "https://api.github.com"
	githubScope    = "read:user user:email"
	githubMaxBytes = 1 << 20
	// githubCallTimeout bounds one call to GitHub, both of its tries together.
	githubCallTimeout = 10 * time.Second
	// githubSecondTryAfter is how long the first try of a call may go without
	// an answer before the second starts beside it (#340 A).
	githubSecondTryAfter = 5 * time.Second
	// githubBreakerFailures is how many calls in a row may end without an
	// answer before the breaker opens, and githubBreakerOpenFor how long it
	// then fails calls at once before one is let through to probe.
	githubBreakerFailures = 3
	githubBreakerOpenFor  = 15 * time.Second
)

var (
	// errGitHubRejected means GitHub refused the access token.
	errGitHubRejected = errors.New("GitHub rejected the token")
	// errGitHubNoUser means GitHub has no user of that login.
	errGitHubNoUser = errors.New("no such GitHub user")
)

// githubClient talks to GitHub's OAuth and REST endpoints. The base URLs and
// the HTTP client are injected so tests can point it at a fake. Errors it
// returns never carry the access token or the client secret.
type githubClient struct {
	clientID, clientSecret string
	webURL, apiURL         string
	http                   *http.Client
	timeout, secondAfter   time.Duration
	breaker                *githubBreaker
}

func newGitHubClient(clientID, clientSecret, webURL, apiURL string, hc *http.Client, now func() time.Time, logger *slog.Logger) *githubClient {
	return &githubClient{
		clientID: clientID, clientSecret: clientSecret, webURL: webURL, apiURL: apiURL,
		http: hc, timeout: githubCallTimeout, secondAfter: githubSecondTryAfter,
		breaker: newGitHubBreaker(now, logger),
	}
}

// githubHTTPClient opens a connection for every request and keeps none. A
// kept connection that has gone dead stalls every call sent on it, the first
// try of the next call included; with none kept, a second try is always on a
// connection of its own. The client has no timeout: twice bounds every call.
func githubHTTPClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableKeepAlives = true
	return &http.Client{Transport: t}
}

// noAnswerError is a try that got no HTTP response: the connection failed or
// stalled, or the call's time ran out.
type noAnswerError struct{ err error }

func (e noAnswerError) Error() string { return e.err.Error() }
func (e noAnswerError) Unwrap() error { return e.err }

func gotNoAnswer(err error) bool {
	_, ok := errors.AsType[noAnswerError](err)
	return ok
}

// twice makes one call to GitHub with try, unless the breaker is open, when
// it fails at once with a noAnswerError and sends nothing. See tryTwice.
func twice[T any](ctx context.Context, g *githubClient, try func(context.Context) (T, error)) (T, error) {
	ok, probe := g.breaker.allow()
	if !ok {
		var zero T
		return zero, noAnswerError{errGitHubBreakerOpen}
	}
	v, err := tryTwice(ctx, g, try)
	if ctx.Err() != nil {
		g.breaker.abandon(probe)
	} else {
		g.breaker.record(ctx, probe, gotNoAnswer(err))
	}
	return v, err
}

// tryTwice makes one call to GitHub with try. When try has not come back after
// g.secondAfter it is run once more beside the first, on a connection of its
// own: a stalled connection tends to stay stalled while a new one gets
// through. The try that succeeds first is the call's result. A try that fails
// ends the call only when no other is running, so a second try that GitHub
// refuses (a sign-in code the first one already spent) cannot beat a first
// whose answer is still on its way. When both fail, the call fails with what
// GitHub answered if either try got an answer. Both end within g.timeout.
func tryTwice[T any](ctx context.Context, g *githubClient, try func(context.Context) (T, error)) (T, error) {
	// The cancel also ends the try that lost. It is not waited for: nothing
	// reads its result, and it returns as soon as its request is cancelled.
	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	type result struct {
		v   T
		err error
	}
	done := make(chan result, 2)
	run := func() {
		go func() {
			v, err := try(ctx)
			done <- result{v, err}
		}()
	}
	run()
	second := time.NewTimer(g.secondAfter)
	defer second.Stop()
	running := 1
	var failed error
	for {
		select {
		case <-second.C:
			// The first try may have come back in the same moment; its
			// result is then taken on the next turn and no second is sent.
			if len(done) == 0 {
				run()
				running++
			}
		case r := <-done:
			if r.err == nil {
				return r.v, nil
			}
			if failed == nil || gotNoAnswer(failed) && !gotNoAnswer(r.err) {
				failed = r.err
			}
			if running--; running == 0 {
				var zero T
				return zero, failed
			}
		}
	}
}

// githubProfile is what Lorvik keeps of a GitHub user.
type githubProfile struct {
	ID        int64
	Login     string
	Name      string
	Email     string
	AvatarURL string
}

func (g *githubClient) authorizeURL(redirectURI, state string) string {
	q := url.Values{"client_id": {g.clientID}, "redirect_uri": {redirectURI}, "scope": {githubScope}, "state": {state}}
	return g.webURL + "/login/oauth/authorize?" + q.Encode()
}

// exchange trades an authorization code for a user access token. GitHub
// reports a bad code with HTTP 200 and an error field in the body.
func (g *githubClient) exchange(ctx context.Context, code, redirectURI string) (string, error) {
	form := url.Values{"client_id": {g.clientID}, "client_secret": {g.clientSecret}, "code": {code}, "redirect_uri": {redirectURI}}.Encode()
	return twice(ctx, g, func(ctx context.Context) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.webURL+"/login/oauth/access_token", strings.NewReader(form))
		if err != nil {
			return "", fmt.Errorf("build token request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		var out struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
		}
		if _, err := g.do(req, &out); err != nil {
			return "", fmt.Errorf("exchange code: %w", err)
		}
		if out.Error != "" {
			return "", fmt.Errorf("exchange code: GitHub answered %q", out.Error)
		}
		if out.AccessToken == "" {
			return "", errors.New("exchange code: no access token in the answer")
		}
		return out.AccessToken, nil
	})
}

// profile reads the token owner's profile. When the user has no public
// email it takes the primary verified address from /user/emails, or "".
func (g *githubClient) profile(ctx context.Context, accessToken string) (githubProfile, error) {
	auth := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+accessToken) }
	p, err := twice(ctx, g, func(ctx context.Context) (githubProfile, error) {
		var u struct {
			ID        int64   `json:"id"`
			Login     string  `json:"login"`
			Name      *string `json:"name"`
			Email     *string `json:"email"`
			AvatarURL string  `json:"avatar_url"`
		}
		status, err := g.get(ctx, "/user", auth, &u)
		if status == http.StatusUnauthorized {
			return githubProfile{}, errGitHubRejected
		}
		if err != nil {
			return githubProfile{}, fmt.Errorf("read GitHub profile: %w", err)
		}
		if u.ID == 0 || u.Login == "" {
			return githubProfile{}, errors.New("read GitHub profile: incomplete answer")
		}
		p := githubProfile{ID: u.ID, Login: u.Login, AvatarURL: u.AvatarURL}
		if u.Name != nil {
			p.Name = *u.Name
		}
		if u.Email != nil {
			p.Email = *u.Email
		}
		return p, nil
	})
	if err != nil || p.Email != "" {
		return p, err
	}
	p.Email, err = twice(ctx, g, func(ctx context.Context) (string, error) {
		var emails []struct {
			Email    string `json:"email"`
			Primary  bool   `json:"primary"`
			Verified bool   `json:"verified"`
		}
		if _, err := g.get(ctx, "/user/emails", auth, &emails); err != nil {
			return "", fmt.Errorf("read GitHub emails: %w", err)
		}
		for _, e := range emails {
			if e.Primary && e.Verified {
				return e.Email, nil
			}
		}
		return "", nil
	})
	if err != nil {
		return githubProfile{}, err
	}
	return p, nil
}

// issuedHere asks GitHub whether accessToken was issued to this app, with
// "Check a token" (POST /applications/{client_id}/token, the app's client id
// and secret as basic auth). GitHub answers 404 for a token that is invalid or
// belongs to another app; both come back as errGitHubRejected, so a caller
// cannot tell them apart.
func (g *githubClient) issuedHere(ctx context.Context, accessToken string) error {
	body, err := json.Marshal(map[string]string{"access_token": accessToken})
	if err != nil {
		return fmt.Errorf("build token check: %w", err)
	}
	_, err = twice(ctx, g, func(ctx context.Context) (struct{}, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.apiURL+"/applications/"+url.PathEscape(g.clientID)+"/token", bytes.NewReader(body))
		if err != nil {
			return struct{}{}, fmt.Errorf("build token check: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/vnd.github+json")
		req.SetBasicAuth(g.clientID, g.clientSecret)
		var out struct {
			App struct {
				ClientID string `json:"client_id"`
			} `json:"app"`
		}
		status, err := g.do(req, &out)
		if status == http.StatusNotFound {
			return struct{}{}, errGitHubRejected
		}
		if err != nil {
			return struct{}{}, fmt.Errorf("check GitHub token: %w", err)
		}
		if out.App.ClientID != g.clientID {
			return struct{}{}, errGitHubRejected
		}
		return struct{}{}, nil
	})
	return err
}

// lookup resolves a GitHub login to its profile with the app's own
// credentials, which raise the rate limit and need no user token.
func (g *githubClient) lookup(ctx context.Context, login string) (githubProfile, error) {
	return twice(ctx, g, func(ctx context.Context) (githubProfile, error) {
		var u struct {
			ID        int64   `json:"id"`
			Login     string  `json:"login"`
			Name      *string `json:"name"`
			AvatarURL string  `json:"avatar_url"`
		}
		status, err := g.get(ctx, "/users/"+url.PathEscape(login), func(r *http.Request) { r.SetBasicAuth(g.clientID, g.clientSecret) }, &u)
		if status == http.StatusNotFound {
			return githubProfile{}, errGitHubNoUser
		}
		if err != nil {
			return githubProfile{}, fmt.Errorf("look up GitHub user: %w", err)
		}
		if u.ID == 0 || u.Login == "" {
			return githubProfile{}, errors.New("look up GitHub user: incomplete answer")
		}
		p := githubProfile{ID: u.ID, Login: u.Login, AvatarURL: u.AvatarURL}
		if u.Name != nil {
			p.Name = *u.Name
		}
		return p, nil
	})
}

func (g *githubClient) get(ctx context.Context, path string, auth func(*http.Request), v any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.apiURL+path, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	auth(req)
	return g.do(req, v)
}

// do sends req and decodes a 2xx JSON body into v. It returns the status so
// callers can tell a 401 or 404 from a failure.
func (g *githubClient) do(req *http.Request, v any) (int, error) {
	resp, err := g.http.Do(req)
	if err != nil {
		return 0, noAnswerError{fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, githubMaxBytes))
		return resp.StatusCode, fmt.Errorf("%s %s: status %d", req.Method, req.URL.Path, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, githubMaxBytes)).Decode(v); err != nil {
		return resp.StatusCode, fmt.Errorf("%s %s: decode answer: %w", req.Method, req.URL.Path, err)
	}
	return resp.StatusCode, nil
}

func githubID(id int64) string { return strconv.FormatInt(id, 10) }
