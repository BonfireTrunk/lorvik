package cmdutil

import (
	"context"
	"errors"
)

// ErrCancelled is returned by a Prompter when the person presses Esc or
// Ctrl-C. Commands report it as "Cancelled. Nothing was changed."
var ErrCancelled = errors.New("cancelled")

// Question is the title of a prompt and an optional one-line description
// shown dimmed under it.
type Question struct {
	Title       string
	Description string
}

// Option is one choice in a Select. Detail is shown dimmed after the label.
type Option struct {
	Label  string
	Detail string
}

// Prompter asks a person at a terminal. Commands call it only on the
// interactive path, never for agents, pipes or --json, so every prompt has a
// flag that answers it instead.
type Prompter interface {
	// Select returns the index of the chosen option; def is preselected.
	Select(ctx context.Context, q Question, opts []Option, def int) (int, error)
	// Input returns a line of text, prefilled with def and editable.
	// validate, when not nil, keeps the prompt open with its message shown
	// until it returns nil.
	Input(ctx context.Context, q Question, def string, validate func(string) error) (string, error)
	// Password reads a secret without echoing it.
	Password(ctx context.Context, q Question) (string, error)
	// Confirm returns the answer; def is what Enter gives.
	Confirm(ctx context.Context, q Question, def bool) (bool, error)
}
