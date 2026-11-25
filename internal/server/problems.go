package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// problemError is an RFC 9457 error body that huma can return: it carries
// the status, the hint and the current version on top of the standard fields.
type problemError struct {
	apitypes.Problem
	// retryAfter, when set, is sent as the Retry-After header.
	retryAfter time.Duration
}

// Error implements error.
func (p *problemError) Error() string {
	if p.Detail != "" {
		return p.Title + ": " + p.Detail
	}
	return p.Title
}

// GetHeaders implements huma.HeadersError.
func (p *problemError) GetHeaders() http.Header {
	if p.retryAfter <= 0 {
		return nil
	}
	return http.Header{"Retry-After": {strconv.Itoa(retrySeconds(p.retryAfter))}}
}

// GetStatus implements huma.StatusError.
func (p *problemError) GetStatus() int { return p.Status }

// ContentType implements huma.ContentTypeFilter so the body is served as
// application/problem+json.
func (p *problemError) ContentType(ct string) string {
	if ct == "application/json" {
		return "application/problem+json"
	}
	return ct
}

// annotatedError wraps an error with advice for the caller and, for version
// conflicts, the version the caller should retry with.
type annotatedError struct {
	err     error
	hint    string
	version int64
}

// Error implements error.
func (a *annotatedError) Error() string { return a.err.Error() }

// Unwrap exposes the wrapped error to errors.Is and errors.As.
func (a *annotatedError) Unwrap() error { return a.err }

func withHint(err error, hint string) error {
	return &annotatedError{err: err, hint: hint}
}

func versionConflict(current int64, msg string) error {
	return &annotatedError{
		err:     fmt.Errorf("%w: %s", domain.ErrVersionConflict, msg),
		hint:    fmt.Sprintf("re-read the item and retry with version %d", current),
		version: current,
	}
}

// newProblem builds a problem with a standard title for status.
func newProblem(status int, detail, hint string) *problemError {
	return &problemError{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
		Hint:   hint}
}

// toProblem maps any error returned by the service to a problem. Unknown
// errors become a 500 without leaking internals; the caller logs them.
func toProblem(err error) *problemError {
	if pe, ok := errors.AsType[*problemError](err); ok {
		return pe
	}
	hint, version := "", int64(0)
	if ann, ok := errors.AsType[*annotatedError](err); ok {
		hint, version = ann.hint, ann.version
	}
	if ves := collectValidation(err); len(ves) > 0 {
		p := newProblem(http.StatusUnprocessableEntity, "the request has invalid fields", hint)
		for _, ve := range ves {
			p.Errors = append(p.Errors, apitypes.ProblemField{Field: ve.Field, Message: ve.Msg})
		}
		return p
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return newProblem(http.StatusNotFound, err.Error(), orDefault(hint, "check the number or slug; `lorvik ls` shows what exists"))
	case errors.Is(err, domain.ErrVersionConflict):
		p := newProblem(http.StatusConflict, err.Error(), orDefault(hint, "re-read the item and retry with its current version"))
		p.CurrentVersion = version
		return p
	case errors.Is(err, domain.ErrClaimedByOther):
		return newProblem(http.StatusConflict, err.Error(), orDefault(hint, "pick another card with `lorvik next`, or wait for the lease to expire"))
	case errors.Is(err, domain.ErrLockHeld):
		return newProblem(http.StatusConflict, err.Error(), orDefault(hint, "wait, or pick other work"))
	case errors.Is(err, domain.ErrInvalidTransition):
		return newProblem(http.StatusConflict, err.Error(), orDefault(hint, "check the card status with `lorvik show <n>`"))
	case errors.Is(err, domain.ErrNotClaimant):
		return newProblem(http.StatusForbidden, err.Error(), orDefault(hint, "claim the card first with `lorvik claim <n>`"))
	case errors.Is(err, domain.ErrUnauthorized):
		return newProblem(http.StatusUnauthorized, err.Error(), orDefault(hint, "set LORVIK_TOKEN"))
	case errors.Is(err, domain.ErrForbidden):
		return newProblem(http.StatusForbidden, err.Error(), hint)
	case errors.Is(err, errGlobalArchive):
		return newProblem(http.StatusConflict, err.Error(), orDefault(hint, "the global project cannot be archived"))
	case errors.Is(err, errAgentRevoked):
		return newProblem(http.StatusConflict, err.Error(), hint)
	case errors.Is(err, errExists):
		return newProblem(http.StatusConflict, err.Error(), orDefault(hint, "choose a different name or slug"))
	}
	return newProblem(http.StatusInternalServerError, "internal error", "retry later; if it persists report the request id")
}

// statusClientClosed is the nginx status for a request its client abandoned.
const statusClientClosed = 499

// clientGone reports whether err comes from the request's own context being
// canceled, which means the client went away. A deadline is not that.
func clientGone(ctx context.Context, err error) bool {
	return err != nil && errors.Is(err, context.Canceled) && ctx.Err() != nil
}

// abandoned is the problem recorded for a request its client gave up on.
func abandoned() *problemError {
	return newProblem(statusClientClosed, "the client closed the request", "")
}

// isInternal reports whether the problem is an unexpected failure to log.
func isInternal(p *problemError) bool { return p.Status >= http.StatusInternalServerError }

func orDefault(s, def string) string {
	if s != "" {
		return s
	}
	return def
}

// collectValidation gathers every ValidationError in err, descending into
// errors.Join trees.
func collectValidation(err error) []*domain.ValidationError {
	var out []*domain.ValidationError
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		if multi, ok := e.(interface{ Unwrap() []error }); ok {
			for _, c := range multi.Unwrap() {
				walk(c)
			}
			return
		}
		if ve, ok := errors.AsType[*domain.ValidationError](e); ok {
			out = append(out, ve)
		}
	}
	walk(err)
	return out
}

// errGlobalArchive refuses archiving the org's global project.
var errGlobalArchive = errors.New("the global project cannot be archived")

// errExists marks a uniqueness conflict detected by the service.
var errExists = errors.New("already exists")

// errAgentRevoked refuses new tokens or a new registration for an agent its
// owner revoked.
var errAgentRevoked = errors.New("agent revoked")

func invalidField(field, msg string) error {
	return &domain.ValidationError{Field: field, Msg: msg}
}
