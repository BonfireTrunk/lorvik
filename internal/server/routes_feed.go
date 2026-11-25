package server

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

type eventsIn struct {
	Project string `query:"project"`
	After   string `query:"after" doc:"events after this position in feed order, oldest first"`
	Before  string `query:"before" doc:"the newest events before this position, oldest first; cannot be combined with after"`
	Limit   int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}

type liveClaimsIn struct {
	ListPage
	Project string `query:"project" doc:"limit the list to one project"`
}

type streamIn struct {
	Project string `query:"project"`
	After   string `query:"after" doc:"resume after this event position; omit to start at the current head"`
}

type inboxIn struct {
	Project     string `query:"project"`
	ChatProject string `query:"chat_project" doc:"the project the calling chat works in: a card assigned to the caller wakes the chat only there; omit for any project"`
	Cursor      string `query:"cursor" doc:"the cursor returned by the previous call; omit on first use"`
	Limit       int    `query:"limit" default:"20" minimum:"1" maximum:"200"`
}

// IDPath is a snowflake id path parameter.
type IDPath struct {
	ID string `path:"id"`
}

// SessionGrantPath addresses the grant of one chat of an agent in a project.
// Exported so huma reads its embedded path structs.
type SessionGrantPath struct {
	IDPath
	SlugPath
	Label string `path:"label"`
}

func (p IDPath) id() (domain.ID, error) {
	id, err := domain.ParseID(p.ID)
	if err != nil {
		return 0, invalidField("id", "must be a numeric id")
	}
	return id, nil
}

// streamStartKey carries a stream's start from the check that runs before
// the 200 to the handler that sends the events.
type streamStartKey struct{}

// registerFeed mounts events, the SSE stream, the inbox and the live claims.
func (r *routes) registerFeed() {
	huma.Register(r.api, operation("list-events", http.MethodGet, "/events", "events", "Events after a position (or before one), oldest first", http.StatusOK),
		handle(r, func(ctx context.Context, in *eventsIn) (apitypes.EventList, error) {
			return r.svc.ListEvents(ctx, in.Project, in.After, in.Before, in.Limit)
		}))
	huma.Register(r.api, operation("get-inbox", http.MethodGet, "/inbox", "events", "What others did since your cursor, your claims and open questions", http.StatusOK),
		handle(r, func(ctx context.Context, in *inboxIn) (apitypes.Inbox, error) {
			return r.svc.Inbox(ctx, in.Project, in.ChatProject, in.Cursor, in.Limit)
		}))
	huma.Register(r.api, operation("list-live-claims", http.MethodGet, "/claims", "events", "Live claims of the org on cards the caller can see, by holder", http.StatusOK),
		handle(r, func(ctx context.Context, in *liveClaimsIn) (apitypes.LiveClaimList, error) {
			return r.svc.ListLiveClaims(ctx, in.Project, in.Limit, in.Cursor)
		}))

	op := operation("stream-events", http.MethodGet, "/events/stream", "events", "Server-sent events", http.StatusOK)
	op.Middlewares = huma.Middlewares{func(hctx huma.Context, next func(huma.Context)) {
		start, err := r.svc.StreamStart(hctx.Context(), hctx.Query("project"), hctx.Query("after"))
		if err != nil {
			r.writeProblem(hctx, r.fail(hctx.Context(), err))
			return
		}
		next(huma.WithValue(hctx, streamStartKey{}, start))
	}}
	sse.Register(r.api, op, map[string]any{"message": apitypes.Event{}},
		func(ctx context.Context, in *streamIn, send sse.Sender) {
			start, _ := ctx.Value(streamStartKey{}).(query.EventPosition)
			err := r.svc.StreamEvents(ctx, in.Project, start,
				func(e apitypes.Event) error {
					id, _ := domain.ParseID(e.ID)
					return send(sse.Message{ID: int(id), Data: e})
				},
				func() error {
					r.svc.KeepChatSeen(ctx)
					return send.Comment("keepalive " + time.Now().UTC().Format(time.RFC3339))
				})
			if err != nil {
				r.d.Logger.WarnContext(ctx, "event stream ended", "error", err)
			}
		})
}

// registerActors mounts me, the org, members, agents, tokens and login.
func (r *routes) registerActors() {
	huma.Register(r.api, operation("get-me", http.MethodGet, "/me", "actors", "Who am I", http.StatusOK),
		handle(r, func(ctx context.Context, _ *struct{}) (apitypes.Me, error) { return r.svc.Me(ctx) }))
	huma.Register(r.api, operation("put-session-name", http.MethodPut, "/me/session-name", "actors",
		"Name the calling agent's chat, or clear its name; a name its client sends later replaces it", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct{ Body apitypes.PutSessionName }) (apitypes.AgentSession, error) {
			return r.svc.SetSessionName(ctx, in.Body)
		}))
	huma.Register(r.api, operation("list-subscriptions", http.MethodGet, "/me/subscriptions", "actors", "The cards the calling agent chat is subscribed to, not done or dropped", http.StatusOK),
		handle(r, func(ctx context.Context, _ *struct{}) (apitypes.SubscriptionList, error) {
			return r.svc.ListSubscriptions(ctx)
		}))
	huma.Register(r.api, operation("update-me", http.MethodPatch, "/me", "actors", "Update your profile (users only)", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct{ Body apitypes.UpdateUser }) (apitypes.User, error) {
			return r.svc.UpdateMe(ctx, in.Body)
		}))

	huma.Register(r.api, operation("get-deletion", http.MethodGet, "/me/deletion", "actors",
		"What deleting your account would do now: the orgs it closes and the ones that refuse it (users only)", http.StatusOK),
		handle(r, func(ctx context.Context, _ *struct{}) (apitypes.DeletionPreview, error) {
			return r.svc.DeletionPreview(ctx)
		}))
	huma.Register(r.api, operation("delete-me", http.MethodDelete, "/me", "actors",
		"Delete your account for good (users only; also takes a sign-in token)", http.StatusNoContent),
		handleEmpty(r, func(ctx context.Context, in *struct{ Body apitypes.DeleteMe }) error {
			return r.svc.DeleteMe(ctx, in.Body)
		}))
	huma.Register(r.api, operation("unlink-github", http.MethodDelete, "/me/github-link", "actors", "Remove your GitHub sign-in (users only)", http.StatusNoContent),
		handleEmpty(r, func(ctx context.Context, _ *struct{}) error { return r.svc.UnlinkGitHub(ctx) }))

	huma.Register(r.api, operation("get-org", http.MethodGet, "/org", "org", "The org and, for admins, its settings", http.StatusOK),
		handle(r, func(ctx context.Context, _ *struct{}) (apitypes.Org, error) { return r.svc.GetOrg(ctx) }))
	huma.Register(r.api, operation("update-org", http.MethodPatch, "/org", "org", "Update the org name or settings (admins)", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct{ Body apitypes.UpdateOrg }) (apitypes.Org, error) {
			return r.svc.UpdateOrg(ctx, in.Body)
		}))

	type actorsIn struct {
		ID []string `query:"id" required:"true" maxItems:"100" doc:"comma-separated user or agent ids, at most 100; unknown ids are omitted"`
	}
	huma.Register(r.api, operation("get-actors", http.MethodGet, "/actors", "actors", "Resolve actor ids to users and agents", http.StatusOK),
		handle(r, func(ctx context.Context, in *actorsIn) (apitypes.Actors, error) {
			ids := make([]domain.ID, 0, len(in.ID))
			for _, s := range in.ID {
				id, err := domain.ParseID(s)
				if err != nil {
					return apitypes.Actors{}, invalidField("id", "must be numeric ids")
				}
				ids = append(ids, id)
			}
			return r.svc.Actors(ctx, ids)
		}))

	huma.Register(r.api, operation("list-members", http.MethodGet, "/members", "org", "Org members and their roles", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct {
			All bool `query:"all" doc:"include pending and removed members"`
			ListPage
		}) (apitypes.MemberList, error) {
			return r.svc.ListMembers(ctx, in.All, in.Cursor, in.Limit)
		}))
	huma.Register(r.api, operation("put-member", http.MethodPut, "/members/{kind}/{id}", "org", "Add a member or change their org role", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct {
			ActorPath
			Body apitypes.PutMembership
		}) (apitypes.Membership, error) {
			ref, err := in.ref()
			if err != nil {
				return apitypes.Membership{}, err
			}
			return r.svc.PutMember(ctx, ref, in.Body)
		}))
	huma.Register(r.api, operation("invite-member", http.MethodPost, "/members/invite", "org", "Invite a GitHub user to the org with a role (admins)", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct{ Body apitypes.Invite }) (apitypes.Membership, error) {
			return r.svc.Invite(ctx, in.Body)
		}))
	huma.Register(r.api, operation("remove-member", http.MethodDelete, "/members/{kind}/{id}", "org", "Remove a member and revoke their tokens", http.StatusNoContent),
		handleEmpty(r, func(ctx context.Context, in *ActorPath) error {
			ref, err := in.ref()
			if err != nil {
				return err
			}
			return r.svc.RemoveMember(ctx, ref)
		}))

	r.registerAgents()
	r.registerTokens()
}

func (r *routes) registerAgents() {
	one := "/agents/{id}"
	huma.Register(r.api, operation("list-agents", http.MethodGet, "/agents", "agents", "List agents", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct {
			All bool `query:"all" doc:"include revoked agents; removed ones are never listed"`
			ListPage
		}) (apitypes.AgentList, error) {
			return r.svc.ListAgents(ctx, in.All, in.Cursor, in.Limit)
		}))
	huma.Register(r.api, operation("create-agent", http.MethodPost, "/agents", "agents", "Register an agent and mint its token (users only)", http.StatusCreated),
		handle(r, func(ctx context.Context, in *struct{ Body apitypes.CreateAgent }) (apitypes.AgentCreated, error) {
			return r.svc.CreateAgent(ctx, in.Body)
		}))
	huma.Register(r.api, operation("get-agent", http.MethodGet, one, "agents", "Get an agent with its roles", http.StatusOK),
		handle(r, func(ctx context.Context, in *IDPath) (apitypes.Agent, error) {
			id, err := in.id()
			if err != nil {
				return apitypes.Agent{}, err
			}
			return r.svc.GetAgent(ctx, id)
		}))
	huma.Register(r.api, operation("update-agent", http.MethodPatch, one, "agents", "Change an agent's say over joining projects (its owner or an agent manager)", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct {
			IDPath
			Body apitypes.UpdateAgent
		}) (apitypes.Agent, error) {
			id, err := in.id()
			if err != nil {
				return apitypes.Agent{}, err
			}
			return r.svc.UpdateAgent(ctx, id, in.Body)
		}))
	huma.Register(r.api, operation("approve-agent", http.MethodPost, one+"/approve", "agents", "Approve a pending agent (admins)", http.StatusOK),
		handle(r, func(ctx context.Context, in *IDPath) (apitypes.Agent, error) {
			id, err := in.id()
			if err != nil {
				return apitypes.Agent{}, err
			}
			return r.svc.ApproveAgent(ctx, id)
		}))
	huma.Register(r.api, operation("revoke-agent", http.MethodDelete, one, "agents", "Revoke an agent and its tokens; it can be restored", http.StatusNoContent),
		handleEmpty(r, func(ctx context.Context, in *IDPath) error {
			id, err := in.id()
			if err != nil {
				return err
			}
			return r.svc.RevokeAgent(ctx, id)
		}))
	huma.Register(r.api, operation("restore-agent", http.MethodPost, one+"/restore", "agents", "Bring a revoked agent back with the project access it had", http.StatusOK),
		handle(r, func(ctx context.Context, in *IDPath) (apitypes.Agent, error) {
			id, err := in.id()
			if err != nil {
				return apitypes.Agent{}, err
			}
			return r.svc.RestoreAgent(ctx, id)
		}))
	huma.Register(r.api, operation("remove-agent", http.MethodPost, one+"/remove", "agents", "Remove an agent for good; the next chat of its harness registers a new one", http.StatusNoContent),
		handleEmpty(r, func(ctx context.Context, in *IDPath) error {
			id, err := in.id()
			if err != nil {
				return err
			}
			return r.svc.RemoveAgent(ctx, id)
		}))
	type bindIn struct {
		IDPath
		SlugPath
		Body apitypes.PutMembership
	}
	huma.Register(r.api, operation("bind-agent", http.MethodPut, one+"/projects/{slug}", "agents", "Give an agent a role in a project", http.StatusOK),
		handle(r, func(ctx context.Context, in *bindIn) (apitypes.Agent, error) {
			id, err := in.id()
			if err != nil {
				return apitypes.Agent{}, err
			}
			return r.svc.BindAgent(ctx, id, in.Slug, in.Body)
		}))
	type unbindIn struct {
		IDPath
		SlugPath
	}
	huma.Register(r.api, operation("unbind-agent", http.MethodDelete, one+"/projects/{slug}", "agents", "Remove an agent from a project", http.StatusNoContent),
		handleEmpty(r, func(ctx context.Context, in *unbindIn) error {
			id, err := in.id()
			if err != nil {
				return err
			}
			return r.svc.UnbindAgent(ctx, id, in.Slug)
		}))
	sessionGrant := one + "/sessions/{label}/projects/{slug}"
	huma.Register(r.api, operation("put-agent-session-grant", http.MethodPut, sessionGrant, "agents",
		"Give one chat of an agent rights in a project, on top of the agent's own (its owner or an agent manager)", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct {
			SessionGrantPath
			Body apitypes.AgentSessionGrantBody
		}) (apitypes.Agent, error) {
			id, err := in.id()
			if err != nil {
				return apitypes.Agent{}, err
			}
			return r.svc.PutAgentSessionGrant(ctx, id, in.Label, in.Slug, in.Body)
		}))
	huma.Register(r.api, operation("delete-agent-session-grant", http.MethodDelete, sessionGrant, "agents",
		"Take back the rights given to one chat of an agent in a project", http.StatusNoContent),
		handleEmpty(r, func(ctx context.Context, in *SessionGrantPath) error {
			id, err := in.id()
			if err != nil {
				return err
			}
			return r.svc.DeleteAgentSessionGrant(ctx, id, in.Label, in.Slug)
		}))
	huma.Register(r.api, operation("join-project", http.MethodPost, "/projects/{slug}/join", "agents",
		"Ask to join a project (agents): join now if the owner's settings allow, else wait for the owner", http.StatusOK),
		handle(r, func(ctx context.Context, in *SlugPath) (apitypes.JoinResult, error) {
			return r.svc.JoinProject(ctx, in.Slug)
		}))
	huma.Register(r.api, operation("list-agent-requests", http.MethodGet, "/agent-requests", "agents", "Your agents' pending requests to join projects", http.StatusOK),
		handle(r, func(ctx context.Context, _ *struct{}) (apitypes.AgentRequestList, error) {
			return r.svc.ListAgentRequests(ctx)
		}))
	huma.Register(r.api, operation("allow-agent-request", http.MethodPut, "/agent-requests/{id}/{slug}", "agents", "Let your agent into the project it asked to join", http.StatusOK),
		handle(r, func(ctx context.Context, in *unbindIn) (apitypes.Agent, error) {
			id, err := in.id()
			if err != nil {
				return apitypes.Agent{}, err
			}
			return r.svc.AllowAgentRequest(ctx, id, in.Slug)
		}))
	huma.Register(r.api, operation("deny-agent-request", http.MethodDelete, "/agent-requests/{id}/{slug}", "agents", "Refuse your agent the project it asked to join", http.StatusNoContent),
		handleEmpty(r, func(ctx context.Context, in *unbindIn) error {
			id, err := in.id()
			if err != nil {
				return err
			}
			return r.svc.DenyAgentRequest(ctx, id, in.Slug)
		}))
}

func (r *routes) registerTokens() {
	huma.Register(r.api, operation("list-tokens", http.MethodGet, "/tokens", "tokens", "Your tokens", http.StatusOK),
		handle(r, func(ctx context.Context, in *ListPage) (apitypes.TokenList, error) {
			return r.svc.ListTokens(ctx, in.Cursor, in.Limit)
		}))
	huma.Register(r.api, operation("create-token", http.MethodPost, "/tokens", "tokens", "Mint a token for yourself or an agent you manage", http.StatusCreated),
		handle(r, func(ctx context.Context, in *struct{ Body apitypes.CreateToken }) (apitypes.TokenCreated, error) {
			return r.svc.CreateToken(ctx, in.Body)
		}))
	huma.Register(r.api, operation("revoke-token", http.MethodDelete, "/tokens/{id}", "tokens", "Revoke a token", http.StatusNoContent),
		handleEmpty(r, func(ctx context.Context, in *IDPath) error {
			id, err := in.id()
			if err != nil {
				return err
			}
			return r.svc.RevokeToken(ctx, id)
		}))

	login := operation("login", http.MethodPost, "/login", "tokens", "Exchange a login and password for a token (when password login is enabled)", http.StatusCreated)
	login.Security = []map[string][]string{}
	huma.Register(r.api, login,
		handle(r, func(ctx context.Context, in *struct{ Body apitypes.Login }) (apitypes.TokenCreated, error) {
			return r.svc.Login(ctx, in.Body)
		}))

	ghAuth := operation("github-auth", http.MethodGet, "/auth/github", "tokens", "Whether GitHub sign-in is on, and the OAuth app's client id", http.StatusOK)
	ghAuth.Security = []map[string][]string{}
	huma.Register(r.api, ghAuth,
		handle(r, func(ctx context.Context, _ *struct{}) (apitypes.GitHubAuth, error) {
			if !r.d.Config.GitHubEnabled() {
				return apitypes.GitHubAuth{}, nil
			}
			return apitypes.GitHubAuth{Enabled: true, ClientID: r.d.Config.GitHubClientID}, nil
		}))
	ghLogin := operation("login-github", http.MethodPost, "/login/github", "tokens", "Exchange a GitHub access token from the device flow for a token", http.StatusCreated)
	ghLogin.Security = []map[string][]string{}
	huma.Register(r.api, ghLogin,
		handle(r, func(ctx context.Context, in *struct{ Body apitypes.GitHubLogin }) (apitypes.TokenCreated, error) {
			if r.svc == nil {
				return apitypes.TokenCreated{}, newProblem(http.StatusServiceUnavailable, "database not configured", "set LORVIK_DATABASE_URL and restart lorvikd")
			}
			return r.svc.LoginGitHub(ctx, in.Body)
		}))

	huma.Register(r.api, operation("list-sign-in-orgs", http.MethodGet, "/login/orgs", "tokens", "The orgs open to a sign-in that has not chosen one (sign-in token)", http.StatusOK),
		handle(r, func(ctx context.Context, _ *struct{}) (apitypes.SignInOrgs, error) {
			return r.svc.SignInOrgs(ctx)
		}))
	huma.Register(r.api, operation("create-org", http.MethodPost, "/login/orgs", "tokens", "Create an org owned by the person who signed in (sign-in token)", http.StatusCreated),
		handle(r, func(ctx context.Context, in *struct{ Body apitypes.CreateOrg }) (apitypes.SignInOrg, error) {
			return r.svc.CreateOrg(ctx, in.Body)
		}))
	huma.Register(r.api, operation("enter-org", http.MethodPost, "/login/orgs/{id}/token", "tokens", "End the sign-in with a token for one org (sign-in token)", http.StatusCreated),
		handle(r, func(ctx context.Context, in *struct {
			IDPath
			Body apitypes.EnterOrg
		}) (apitypes.TokenCreated, error) {
			id, err := in.id()
			if err != nil {
				return apitypes.TokenCreated{}, err
			}
			return r.svc.EnterOrg(ctx, id, in.Body)
		}))
}
