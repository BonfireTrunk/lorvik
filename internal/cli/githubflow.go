package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

const (
	githubHost       = "https://github.com"
	githubScope      = "read:user user:email"
	githubDeviceGrnt = "urn:ietf:params:oauth:grant-type:device_code"
	slowDownStep     = 5 * time.Second
	maxGitHubBody    = 1 << 20
)

type deviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	githubError
}

type deviceToken struct {
	AccessToken string `json:"access_token"`
	Interval    int    `json:"interval"`
	githubError
}

// githubError is the error body GitHub sends with status 200 while a device
// flow is pending or has failed.
type githubError struct {
	Error     string `json:"error"`
	ErrorDesc string `json:"error_description"`
}

func (e githubError) describe() string {
	if e.ErrorDesc != "" {
		return e.Error + ": " + e.ErrorDesc
	}
	return e.Error
}

// githubLogin signs in through GitHub's device flow and exchanges the GitHub
// access token for a Lorvik token. The GitHub token is dropped on return.
func (a *app) githubLogin(ctx context.Context, in apitypes.Login, createOrg string) (apitypes.TokenCreated, Settings, error) {
	c, err := a.anonClient()
	if err != nil {
		return apitypes.TokenCreated{}, Settings{}, err
	}
	auth, err := c.GitHubAuth(ctx)
	if err != nil {
		return apitypes.TokenCreated{}, Settings{}, err
	}
	if !auth.Enabled || auth.ClientID == "" {
		return apitypes.TokenCreated{}, Settings{}, cmdutil.Usagef("GitHub login is not enabled on this server; sign in with `lorvik login <user>` (password) or `--token`")
	}
	ghToken, err := a.deviceFlow(ctx, auth.ClientID)
	if err != nil {
		return apitypes.TokenCreated{}, Settings{}, err
	}
	res, err := c.LoginGitHub(ctx, apitypes.GitHubLogin{AccessToken: ghToken, Org: in.Org, Choose: in.Choose, TokenName: in.TokenName})
	if err != nil {
		return apitypes.TokenCreated{}, Settings{}, err
	}
	return a.finishLogin(ctx, c, res, in.TokenName, createOrg)
}

// deviceFlow runs GitHub's device authorization grant and returns the user
// access token. The client secret is not part of this flow.
func (a *app) deviceFlow(ctx context.Context, clientID string) (string, error) {
	base := strings.TrimRight(a.f.GitHubURL, "/")
	if base == "" {
		base = githubHost
	}
	var dc deviceCode
	hc := &http.Client{Transport: client.NewTransport(), Timeout: 30 * time.Second}
	err := postGitHub(ctx, hc, base+"/login/device/code", url.Values{"client_id": {clientID}, "scope": {githubScope}}, &dc)
	if err != nil {
		return "", err
	}
	if dc.Error != "" {
		return "", fmt.Errorf("GitHub refused the sign-in: %s", dc.describe())
	}
	if dc.DeviceCode == "" || dc.UserCode == "" || dc.VerificationURI == "" || dc.ExpiresIn <= 0 {
		return "", errors.New("GitHub answered without a usable device code")
	}
	deadline := a.f.Time().Add(time.Duration(dc.ExpiresIn) * time.Second)
	fmt.Fprintf(a.errOut(), "Open %s and enter the code %s\n", dc.VerificationURI, dc.UserCode)

	interval := time.Duration(dc.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for a.f.Time().Before(deadline) {
		// A poll after expires_in would only be answered with expired_token, so
		// the last wait is cut to end at the deadline.
		if err := a.sleep(ctx, min(interval, deadline.Sub(a.f.Time()))); err != nil {
			return "", err
		}
		if !a.f.Time().Before(deadline) {
			break
		}
		var tok deviceToken
		err := postGitHub(ctx, hc, base+"/login/oauth/access_token", url.Values{
			"client_id":   {clientID},
			"device_code": {dc.DeviceCode},
			"grant_type":  {githubDeviceGrnt},
		}, &tok)
		if err != nil {
			return "", err
		}
		switch tok.Error {
		case "":
			if tok.AccessToken == "" {
				return "", errors.New("GitHub answered without an access token")
			}
			return tok.AccessToken, nil
		case "authorization_pending":
		case "slow_down":
			interval += slowDownStep
			if tok.Interval > 0 {
				interval = time.Duration(tok.Interval) * time.Second
			}
		case "expired_token":
			return "", &cmdutil.ExitError{Code: cmdutil.ExitUnauthorized, Err: errors.New("the GitHub code expired before it was entered; run lorvik login again")}
		case "access_denied":
			return "", &cmdutil.ExitError{Code: cmdutil.ExitUnauthorized, Err: errors.New("GitHub sign-in was denied")}
		default:
			return "", fmt.Errorf("GitHub sign-in failed: %s", tok.describe())
		}
	}
	return "", &cmdutil.ExitError{Code: cmdutil.ExitUnauthorized, Err: errors.New("the GitHub code expired before it was entered; run lorvik login again")}
}

func (a *app) sleep(ctx context.Context, d time.Duration) error {
	if a.f.Sleep != nil {
		a.f.Sleep(d)
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func postGitHub(ctx context.Context, hc *http.Client, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub answered %d from %s", resp.StatusCode, req.URL.Path)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxGitHubBody+1))
	if err != nil {
		return fmt.Errorf("read GitHub's answer: %w", err)
	}
	if len(body) > maxGitHubBody {
		return fmt.Errorf("GitHub's answer from %s is larger than %d bytes", req.URL.Path, maxGitHubBody)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("read GitHub's answer: %w", err)
	}
	return nil
}
