package client

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

// actorsChunk bounds how many ids go into one GET /actors request.
const actorsChunk = 100

// Health calls GET /healthz.
func (c *Client) Health(ctx context.Context) (apitypes.Health, error) {
	return call[apitypes.Health](ctx, c, http.MethodGet, "/healthz", nil)
}

// Login calls POST /login. It needs no token; the result carries the secret.
func (c *Client) Login(ctx context.Context, in apitypes.Login) (apitypes.TokenCreated, error) {
	return call[apitypes.TokenCreated](ctx, c, http.MethodPost, "/login", in)
}

// GitHubAuth calls GET /auth/github. It needs no token.
func (c *Client) GitHubAuth(ctx context.Context) (apitypes.GitHubAuth, error) {
	return call[apitypes.GitHubAuth](ctx, c, http.MethodGet, "/auth/github", nil)
}

// LoginGitHub calls POST /login/github. It needs no token; the result carries
// the secret.
func (c *Client) LoginGitHub(ctx context.Context, in apitypes.GitHubLogin) (apitypes.TokenCreated, error) {
	return call[apitypes.TokenCreated](ctx, c, http.MethodPost, "/login/github", in)
}

// SignInOrgs calls GET /login/orgs. The client must carry the sign-in token
// that Login or LoginGitHub answered.
func (c *Client) SignInOrgs(ctx context.Context) (apitypes.SignInOrgs, error) {
	return call[apitypes.SignInOrgs](ctx, c, http.MethodGet, "/login/orgs", nil)
}

// CreateOrg calls POST /login/orgs: a new org owned by the person who signed
// in. The client must carry a sign-in token.
func (c *Client) CreateOrg(ctx context.Context, in apitypes.CreateOrg) (apitypes.SignInOrg, error) {
	return call[apitypes.SignInOrg](ctx, c, http.MethodPost, "/login/orgs", in)
}

// EnterOrg calls POST /login/orgs/{id}/token and returns the ordinary token
// for that org; the sign-in token stops working. The client must carry a
// sign-in token.
func (c *Client) EnterOrg(ctx context.Context, id string, in apitypes.EnterOrg) (apitypes.TokenCreated, error) {
	return call[apitypes.TokenCreated](ctx, c, http.MethodPost, "/login/orgs/"+url.PathEscape(id)+"/token", in)
}

// Me calls GET /me.
func (c *Client) Me(ctx context.Context) (apitypes.Me, error) {
	return call[apitypes.Me](ctx, c, http.MethodGet, "/me", nil)
}

// UpdateMe calls PATCH /me.
func (c *Client) UpdateMe(ctx context.Context, in apitypes.UpdateUser) (apitypes.User, error) {
	return call[apitypes.User](ctx, c, http.MethodPatch, "/me", in)
}

// GetOrg calls GET /org.
func (c *Client) GetOrg(ctx context.Context) (apitypes.Org, error) {
	return call[apitypes.Org](ctx, c, http.MethodGet, "/org", nil)
}

// UpdateOrg calls PATCH /org. Settings replace the stored document as a
// whole, so callers send the full settings they want to keep.
func (c *Client) UpdateOrg(ctx context.Context, in apitypes.UpdateOrg) (apitypes.Org, error) {
	return call[apitypes.Org](ctx, c, http.MethodPatch, "/org", in)
}

// Actors calls GET /actors for the given ids, in chunks, and merges the
// answers. Unknown ids are simply absent from the result.
func (c *Client) Actors(ctx context.Context, ids []string) (apitypes.Actors, error) {
	out := apitypes.Actors{Users: []apitypes.User{}, Agents: []apitypes.Agent{}}
	for start := 0; start < len(ids); start += actorsChunk {
		chunk := ids[start:min(start+actorsChunk, len(ids))]
		v := url.Values{}
		v.Set("id", strings.Join(chunk, ","))
		got, err := call[apitypes.Actors](ctx, c, http.MethodGet, withQuery("/actors", v), nil)
		if err != nil {
			return apitypes.Actors{}, err
		}
		out.Users = append(out.Users, got.Users...)
		out.Agents = append(out.Agents, got.Agents...)
	}
	return out, nil
}

// ListMembers calls GET /members. With all, pending and removed members are
// included.
func (c *Client) ListMembers(ctx context.Context, all bool, p PageParams) (apitypes.MemberList, error) {
	v := url.Values{}
	setBool(v, "all", all)
	return call[apitypes.MemberList](ctx, c, http.MethodGet, pageQuery("/members", p, v), nil)
}

func memberPath(base string, a apitypes.Actor) string {
	return base + "/members/" + seg(a.Kind) + "/" + seg(a.ID)
}

// PutMember calls PUT /members/{kind}/{id}.
func (c *Client) PutMember(ctx context.Context, a apitypes.Actor, in apitypes.PutMembership) (apitypes.Membership, error) {
	return call[apitypes.Membership](ctx, c, http.MethodPut, memberPath("", a), in)
}

// RemoveMember calls DELETE /members/{kind}/{id}.
func (c *Client) RemoveMember(ctx context.Context, a apitypes.Actor) error {
	return c.do(ctx, http.MethodDelete, memberPath("", a), nil, nil)
}

// ListAgents calls GET /agents. With all, revoked agents are included;
// removed ones never are.
func (c *Client) ListAgents(ctx context.Context, all bool, p PageParams) (apitypes.AgentList, error) {
	v := url.Values{}
	setBool(v, "all", all)
	return call[apitypes.AgentList](ctx, c, http.MethodGet, pageQuery("/agents", p, v), nil)
}

// CreateAgent calls POST /agents. The result carries the secret once.
func (c *Client) CreateAgent(ctx context.Context, in apitypes.CreateAgent) (apitypes.AgentCreated, error) {
	return call[apitypes.AgentCreated](ctx, c, http.MethodPost, "/agents", in)
}

// GetAgent calls GET /agents/{id}.
func (c *Client) GetAgent(ctx context.Context, id string) (apitypes.Agent, error) {
	return call[apitypes.Agent](ctx, c, http.MethodGet, "/agents/"+seg(id), nil)
}

// ApproveAgent calls POST /agents/{id}/approve.
func (c *Client) ApproveAgent(ctx context.Context, id string) (apitypes.Agent, error) {
	return call[apitypes.Agent](ctx, c, http.MethodPost, "/agents/"+seg(id)+"/approve", nil)
}

// RevokeAgent calls DELETE /agents/{id}.
func (c *Client) RevokeAgent(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/agents/"+seg(id), nil, nil)
}

// RestoreAgent calls POST /agents/{id}/restore.
func (c *Client) RestoreAgent(ctx context.Context, id string) (apitypes.Agent, error) {
	return call[apitypes.Agent](ctx, c, http.MethodPost, "/agents/"+seg(id)+"/restore", nil)
}

// RemoveAgent calls POST /agents/{id}/remove.
func (c *Client) RemoveAgent(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/agents/"+seg(id)+"/remove", nil, nil)
}

// BindAgent calls PUT /agents/{id}/projects/{slug}.
func (c *Client) BindAgent(ctx context.Context, id, slug string, in apitypes.PutMembership) (apitypes.Agent, error) {
	return call[apitypes.Agent](ctx, c, http.MethodPut, "/agents/"+seg(id)+"/projects/"+seg(slug), in)
}

// UnbindAgent calls DELETE /agents/{id}/projects/{slug}.
func (c *Client) UnbindAgent(ctx context.Context, id, slug string) error {
	return c.do(ctx, http.MethodDelete, "/agents/"+seg(id)+"/projects/"+seg(slug), nil, nil)
}

// JoinProject calls POST /projects/{slug}/join: an agent asking to join.
func (c *Client) JoinProject(ctx context.Context, slug string) (apitypes.JoinResult, error) {
	return call[apitypes.JoinResult](ctx, c, http.MethodPost, "/projects/"+seg(slug)+"/join", nil)
}

// ListTokens calls GET /tokens.
func (c *Client) ListTokens(ctx context.Context, p PageParams) (apitypes.TokenList, error) {
	return call[apitypes.TokenList](ctx, c, http.MethodGet, pageQuery("/tokens", p, url.Values{}), nil)
}

// CreateToken calls POST /tokens. The result carries the secret once.
func (c *Client) CreateToken(ctx context.Context, in apitypes.CreateToken) (apitypes.TokenCreated, error) {
	return call[apitypes.TokenCreated](ctx, c, http.MethodPost, "/tokens", in)
}

// RevokeToken calls DELETE /tokens/{id}.
func (c *Client) RevokeToken(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/tokens/"+seg(id), nil, nil)
}

// SetSessionName calls PUT /me/session-name. An empty name clears it.
func (c *Client) SetSessionName(ctx context.Context, name string) (apitypes.AgentSession, error) {
	return call[apitypes.AgentSession](ctx, c, http.MethodPut, "/me/session-name", apitypes.PutSessionName{Name: name})
}
