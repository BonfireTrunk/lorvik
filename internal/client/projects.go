package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

// ListProjects calls GET /projects.
func (c *Client) ListProjects(ctx context.Context, p PageParams) (apitypes.ProjectList, error) {
	return call[apitypes.ProjectList](ctx, c, http.MethodGet, pageQuery("/projects", p, url.Values{}), nil)
}

// CreateProject calls POST /projects.
func (c *Client) CreateProject(ctx context.Context, in apitypes.CreateProject) (apitypes.Project, error) {
	return call[apitypes.Project](ctx, c, http.MethodPost, "/projects", in)
}

// GetProject calls GET /projects/{slug}; the result includes counts.
func (c *Client) GetProject(ctx context.Context, slug string) (apitypes.Project, error) {
	return call[apitypes.Project](ctx, c, http.MethodGet, "/projects/"+seg(slug), nil)
}

// UpdateProject calls PATCH /projects/{slug}.
func (c *Client) UpdateProject(ctx context.Context, slug string, in apitypes.UpdateProject) (apitypes.Project, error) {
	return call[apitypes.Project](ctx, c, http.MethodPatch, "/projects/"+seg(slug), in)
}

// ListProjectMembers calls GET /projects/{slug}/members.
func (c *Client) ListProjectMembers(ctx context.Context, slug string, p PageParams) (apitypes.ProjectMemberList, error) {
	return call[apitypes.ProjectMemberList](ctx, c, http.MethodGet, pageQuery("/projects/"+seg(slug)+"/members", p, url.Values{}), nil)
}

// PutProjectMember calls PUT /projects/{slug}/members/{kind}/{id}.
func (c *Client) PutProjectMember(ctx context.Context, slug string, a apitypes.Actor, in apitypes.PutMembership) (apitypes.ProjectMember, error) {
	return call[apitypes.ProjectMember](ctx, c, http.MethodPut, memberPath("/projects/"+seg(slug), a), in)
}

// RemoveProjectMember calls DELETE /projects/{slug}/members/{kind}/{id}.
func (c *Client) RemoveProjectMember(ctx context.Context, slug string, a apitypes.Actor) error {
	return c.do(ctx, http.MethodDelete, memberPath("/projects/"+seg(slug), a), nil, nil)
}
