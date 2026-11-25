package cmdutil

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/BonfireTrunk/lorvik/internal/client"
)

// Exit codes. They are part of the CLI contract.
const (
	ExitOK           = 0
	ExitGeneric      = 1
	ExitUsage        = 2
	ExitNotFound     = 3
	ExitConflict     = 4
	ExitUnauthorized = 5
)

// ErrSilent means the command already printed what the user needs. Main
// exits without printing anything more.
var ErrSilent = errors.New("silent")

// UsageError is a mistake in how the command was invoked.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Usagef builds a UsageError.
func Usagef(format string, args ...any) error {
	return &UsageError{Msg: fmt.Sprintf(format, args...)}
}

// ExitError forces a specific exit code. Err is what Main prints; wrap
// ErrSilent to print nothing.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode maps an error to a process exit code. Stable codes let agents and
// scripts branch on outcomes without parsing text.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	if ee, ok := errors.AsType[*ExitError](err); ok {
		return ee.Code
	}
	if _, ok := errors.AsType[*UsageError](err); ok {
		return ExitUsage
	}
	if ae, ok := errors.AsType[*client.APIError](err); ok {
		switch ae.Status {
		case http.StatusNotFound:
			return ExitNotFound
		case http.StatusConflict:
			return ExitConflict
		case http.StatusUnauthorized, http.StatusForbidden:
			return ExitUnauthorized
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			return ExitUsage
		}
	}
	return ExitGeneric
}

// sandboxHints maps a variable a harness sets inside a sandbox that blocks
// the network to the line that says how to get it back. Rows are verified
// against a real chat, never guessed.
var sandboxHints = []struct{ env, value, hint string }{
	{"CODEX_SANDBOX_NETWORK_DISABLED", "1", "the Codex sandbox blocks network access: approve it when asked, or set network_access = true under [sandbox_workspace_write] in ~/.codex/config.toml"},
}

// FormatError renders err for stderr: the message plus a line naming the fix.
// getenv (nil allowed) lets a connection error name the sandbox that caused it.
func FormatError(err error, getenv func(string) string) string {
	var b strings.Builder
	b.WriteString("lorvik: " + err.Error())
	if ae, ok := errors.AsType[*client.APIError](err); ok {
		b.Reset()
		b.WriteString("lorvik: " + apiMessage(ae))
		for _, f := range ae.Errors {
			fmt.Fprintf(&b, "\n  %s: %s", f.Field, f.Message)
		}
		if ae.Status == http.StatusConflict && ae.CurrentVersion > 0 {
			fmt.Fprintf(&b, "\ncurrent_version: %d", ae.CurrentVersion)
		}
		if h := hintFor(ae); h != "" {
			b.WriteString("\nhint: " + h)
		}
	}
	if ce, ok := errors.AsType[*client.ConnError](err); ok {
		fmt.Fprintf(&b, "\nhint: is the server running at %s? check LORVIK_API_URL or run `lorvik ping`", ce.URL)
		if getenv != nil {
			for _, sh := range sandboxHints {
				if getenv(sh.env) == sh.value {
					b.WriteString("\nhint: " + sh.hint)
				}
			}
		}
	}
	return b.String()
}

func apiMessage(ae *client.APIError) string {
	switch {
	case ae.Title != "" && ae.Detail != "":
		return ae.Title + ": " + ae.Detail
	case ae.Title != "":
		return ae.Title
	case strings.TrimSpace(ae.Body) != "":
		return fmt.Sprintf("server returned %d: %s", ae.Status, strings.TrimSpace(ae.Body))
	}
	return fmt.Sprintf("server returned %d", ae.Status)
}

func hintFor(ae *client.APIError) string {
	switch {
	case ae.Hint != "":
		return ae.Hint
	case ae.Status == http.StatusUnauthorized:
		return "sign in with `lorvik login`; agents and CI set LORVIK_TOKEN or LORVIK_CONFIG"
	case ae.Status == http.StatusForbidden:
		return "this token may not do that; use a user token or ask an owner"
	}
	return ""
}
