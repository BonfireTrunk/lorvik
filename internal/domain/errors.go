package domain

import (
	"errors"
	"fmt"
)

// Sentinel errors. Transport layers map these to status codes; the CLI maps
// them to exit codes and advice. Wrap them with context, never return bare.
var (
	ErrNotFound          = errors.New("not found")
	ErrVersionConflict   = errors.New("version conflict")
	ErrClaimedByOther    = errors.New("claimed by another actor")
	ErrNotClaimant       = errors.New("caller does not hold the claim")
	ErrLockHeld          = errors.New("lock held")
	ErrInvalidTransition = errors.New("invalid status transition")
	ErrUnauthorized      = errors.New("unauthorized")
	ErrForbidden         = errors.New("forbidden")
)

// ValidationError reports one or more field problems. Callers aggregate with
// errors.Join when several fields fail.
type ValidationError struct {
	Field string
	Msg   string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Msg
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Msg)
}

func invalid(field, msg string) error { return &ValidationError{Field: field, Msg: msg} }
