package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// SlugPath is the {slug} project path parameter.
type SlugPath struct {
	Slug string `path:"slug"`
}

// CardPath addresses a card by project slug and number.
type CardPath struct {
	Slug   string `path:"slug"`
	Number int64  `path:"number" minimum:"1"`
}

// RevisionPage pages a revision list, newest first. Exported because huma does
// not read the query tags of an unexported embedded struct.
type RevisionPage struct {
	Before int64 `query:"before" minimum:"0" doc:"only versions older than this one"`
	Limit  int   `query:"limit" minimum:"1" maximum:"200" default:"50" doc:"at most this many"`
}

// ListPage pages a list that only goes forward. Exported for the same reason.
type ListPage struct {
	Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50" doc:"page size"`
	Cursor string `query:"cursor" doc:"the next value of the previous page"`
}

func (r RevisionPage) page() query.HistoryPage {
	return query.HistoryPage{Before: r.Before, Limit: r.Limit}
}

// ActorPath addresses a user or agent by kind and id.
type ActorPath struct {
	Kind string `path:"kind" enum:"user,agent"`
	ID   string `path:"id"`
}

func (a ActorPath) ref() (domain.ActorRef, error) { return parseActorPath(a.Kind, a.ID) }

// ProjectMemberPath addresses one member of a project.
type ProjectMemberPath struct {
	SlugPath
	ActorPath
}

func (r *routes) registerProjects() {
	huma.Register(r.api, operation("list-projects", http.MethodGet, "/projects", "projects", "List the projects you can see", http.StatusOK),
		handle(r, func(ctx context.Context, in *ListPage) (apitypes.ProjectList, error) {
			return r.svc.ListProjects(ctx, in.Cursor, in.Limit)
		}))
	huma.Register(r.api, operation("create-project", http.MethodPost, "/projects", "projects", "Create a project", http.StatusCreated),
		handle(r, func(ctx context.Context, in *struct{ Body apitypes.CreateProject }) (apitypes.Project, error) {
			return r.svc.CreateProject(ctx, in.Body)
		}))
	huma.Register(r.api, operation("get-project", http.MethodGet, "/projects/{slug}", "projects", "Get a project with card counts", http.StatusOK),
		handle(r, func(ctx context.Context, in *SlugPath) (apitypes.Project, error) {
			return r.svc.GetProject(ctx, in.Slug)
		}))
	huma.Register(r.api, operation("update-project", http.MethodPatch, "/projects/{slug}", "projects", "Update a project", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct {
			SlugPath
			Body apitypes.UpdateProject
		}) (apitypes.Project, error) {
			return r.svc.UpdateProject(ctx, in.Slug, in.Body)
		}))
	huma.Register(r.api, operation("put-my-agent-join", http.MethodPut, "/projects/{slug}/agent-join", "projects",
		"Set your own rule for your agents joining this project (follow, allow or ask)", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct {
			SlugPath
			Body apitypes.PutAgentJoin
		}) (apitypes.Project, error) {
			return r.svc.PutMyAgentJoin(ctx, in.Slug, in.Body)
		}))

	members := "/projects/{slug}/members"
	huma.Register(r.api, operation("list-project-members", http.MethodGet, members, "projects", "Project members and their roles", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct {
			SlugPath
			ListPage
		}) (apitypes.ProjectMemberList, error) {
			return r.svc.ListProjectMembers(ctx, in.Slug, in.Cursor, in.Limit)
		}))
	huma.Register(r.api, operation("put-project-member", http.MethodPut, members+"/{kind}/{id}", "projects", "Add a member or change their project role", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct {
			ProjectMemberPath
			Body apitypes.PutMembership
		}) (apitypes.ProjectMember, error) {
			ref, err := in.ref()
			if err != nil {
				return apitypes.ProjectMember{}, err
			}
			return r.svc.PutProjectMember(ctx, in.Slug, ref, in.Body)
		}))
	huma.Register(r.api, operation("remove-project-member", http.MethodDelete, members+"/{kind}/{id}", "projects", "Remove a project member", http.StatusNoContent),
		handleEmpty(r, func(ctx context.Context, in *ProjectMemberPath) error {
			ref, err := in.ref()
			if err != nil {
				return err
			}
			return r.svc.RemoveProjectMember(ctx, in.Slug, ref)
		}))

	type searchIn struct {
		Q       string `query:"q" required:"true" minLength:"1"`
		Project string `query:"project"`
		Cursor  string `query:"cursor"`
		Limit   int    `query:"limit" default:"10" minimum:"1" maximum:"50"`
	}
	huma.Register(r.api, operation("search", http.MethodGet, "/search", "search", "Search cards and notes", http.StatusOK),
		handle(r, func(ctx context.Context, in *searchIn) (apitypes.SearchResult, error) {
			return r.svc.Search(ctx, in.Q, in.Project, in.Cursor, in.Limit)
		}))
}
