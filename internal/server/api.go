package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/build"
)

// out wraps a response body for huma.
type out[T any] struct {
	Body T
}

// noContent is the output of operations that answer 204.
type noContent struct{}

// routes holds what the registered handlers share.
type routes struct {
	api huma.API
	svc *Service
	d   Deps
}

// newAPI builds the versioned API: a huma API on a sub-mux behind the bearer
// token middleware. Health, the OpenAPI document, the docs page and login are
// public. svc is nil without a store; the middleware then answers 503.
func newAPI(d Deps, svc *Service) http.Handler {
	mux := http.NewServeMux()
	api := humago.NewWithPrefix(mux, apiPrefix, apiConfig())
	r := &routes{api: api, svc: svc, d: d}
	r.registerHealth()
	r.registerProjects()
	r.registerCards()
	r.registerNotes()
	r.registerFeed()
	r.registerLocks()
	r.registerActors()
	newGitHubWeb(svc, d.Config, d.Logger).registerLink(mux)
	h := newAuthenticator(svc, d.Logger).middleware(mux)
	if svc == nil {
		return h
	}
	return svc.limits.guard(h)
}

func apiConfig() huma.Config {
	cfg := huma.DefaultConfig("Lorvik API", build.Current().Version)
	cfg.CreateHooks = nil // no $schema fields or Link headers: the wire shape is apitypes
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearer": {Type: "http", Scheme: "bearer", Description: "Lorvik token (lvk_...)"},
	}
	cfg.Security = []map[string][]string{{"bearer": {}}}
	return cfg
}

// operation builds a huma operation with the conventions shared by all routes.
func operation(id, method, path, tag, summary string, status int) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{tag}, Summary: summary, DefaultStatus: status}
}

// carryingPage raises the request body limit of an operation whose body can
// hold a page, which JSON escaping can make larger than the page itself.
func carryingPage(op huma.Operation) huma.Operation {
	op.MaxBodyBytes = pageRequestBytes
	return op
}

// fail maps a service error to a problem and logs unexpected failures.
func (r *routes) fail(ctx context.Context, err error) error {
	if clientGone(ctx, err) {
		r.d.Logger.DebugContext(ctx, "request abandoned by the client", "request_id", RequestIDFrom(ctx))
		return abandoned()
	}
	p := toProblem(err)
	if isInternal(p) {
		r.d.Logger.ErrorContext(ctx, "request failed", "error", err, "request_id", RequestIDFrom(ctx))
	}
	return p
}

// writeProblem answers with err, a problem from fail, outside a huma
// handler; huma writes a handler's returned error the same way.
func (r *routes) writeProblem(hctx huma.Context, err error) {
	p := toProblem(err)
	for k, v := range p.GetHeaders() {
		hctx.SetHeader(k, v[0])
	}
	hctx.SetHeader("Content-Type", p.ContentType("application/json"))
	hctx.SetStatus(p.Status)
	if merr := r.api.Marshal(hctx.BodyWriter(), "application/json", p); merr != nil {
		r.d.Logger.ErrorContext(hctx.Context(), "write problem", "error", merr)
	}
}

// handle adapts a service call returning a body into a huma handler.
func handle[I, O any](r *routes, fn func(ctx context.Context, in *I) (O, error)) func(context.Context, *I) (*out[O], error) {
	return func(ctx context.Context, in *I) (*out[O], error) {
		v, err := fn(ctx, in)
		if err != nil {
			return nil, r.fail(ctx, err)
		}
		return &out[O]{Body: v}, nil
	}
}

// handleEmpty adapts a service call with no body into a huma handler.
func handleEmpty[I any](r *routes, fn func(ctx context.Context, in *I) error) func(context.Context, *I) (*noContent, error) {
	return func(ctx context.Context, in *I) (*noContent, error) {
		if err := fn(ctx, in); err != nil {
			return nil, r.fail(ctx, err)
		}
		return &noContent{}, nil
	}
}

type healthOut struct {
	Status int `json:"-"`
	Body   apitypes.Health
}

func (r *routes) registerHealth() {
	op := operation("healthz", http.MethodGet, "/healthz", "meta", "Liveness and database health", http.StatusOK)
	op.Security = []map[string][]string{}
	huma.Register(r.api, op, func(ctx context.Context, _ *struct{}) (*healthOut, error) {
		h := apitypes.Health{Status: "ok", Version: build.Current().Version, Database: "unconfigured"}
		res := &healthOut{Status: http.StatusOK}
		if r.d.DBPing != nil {
			h.Database = "ok"
			if err := r.d.DBPing(ctx); err != nil {
				h.Database, h.Status = "unavailable", "degraded"
				res.Status = http.StatusServiceUnavailable
			}
		}
		res.Body = h
		return res, nil
	})
}
