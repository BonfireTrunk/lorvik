// Package cmdutil holds the shared plumbing for lorvik CLI commands: the
// dependency Factory, IO streams, and the JSON exporter.
package cmdutil

import (
	"io"
	"os"

	"golang.org/x/term"
)

// IOStreams bundles the three standard streams with TTY detection, so
// commands can decide between human and machine output without touching os.
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	// ReadPassword reads a secret from the terminal without echoing it. Nil
	// means no terminal is available. Tests set it.
	ReadPassword func() (string, error)

	stdoutIsTTY bool
	stdinIsTTY  bool
	stderrIsTTY bool
	colorOff    bool
}

// System returns streams bound to the process's stdin, stdout and stderr.
func System() *IOStreams {
	return &IOStreams{
		In:          os.Stdin,
		Out:         os.Stdout,
		ErrOut:      os.Stderr,
		stdoutIsTTY: term.IsTerminal(int(os.Stdout.Fd())),
		stdinIsTTY:  term.IsTerminal(int(os.Stdin.Fd())),
		stderrIsTTY: term.IsTerminal(int(os.Stderr.Fd())),
		colorOff:    os.Getenv("NO_COLOR") != "",
		ReadPassword: func() (string, error) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			return string(b), err
		},
	}
}

// Test returns in-memory streams for tests. Pass nil for an empty stdin.
func Test(in io.Reader) (*IOStreams, *Buffer, *Buffer) {
	if in == nil {
		in = emptyReader{}
	}
	out, errOut := &Buffer{}, &Buffer{}
	return &IOStreams{In: in, Out: out, ErrOut: errOut}, out, errOut
}

// SetTTY marks stdin and stdout as terminals or not. Tests use it to cover
// the interactive paths; System detects the real thing.
func (s *IOStreams) SetTTY(stdin, stdout bool) { s.stdinIsTTY, s.stdoutIsTTY = stdin, stdout }

// SetStderrTTY marks stderr as a terminal or not, for tests of prompts,
// which draw on stderr.
func (s *IOStreams) SetStderrTTY(v bool) { s.stderrIsTTY = v }

// IsStdoutTTY reports whether stdout is an interactive terminal. Agents and
// pipes get false, which turns off color, truncation and prompts.
func (s *IOStreams) IsStdoutTTY() bool { return s.stdoutIsTTY }

// IsStdinTTY reports whether stdin is interactive. False means never prompt.
func (s *IOStreams) IsStdinTTY() bool { return s.stdinIsTTY }

// IsStderrTTY reports whether stderr is a terminal, where prompts draw.
func (s *IOStreams) IsStderrTTY() bool { return s.stderrIsTTY }

// ColorEnabled is true only for a TTY without NO_COLOR set.
func (s *IOStreams) ColorEnabled() bool { return s.stdoutIsTTY && !s.colorOff }

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }
