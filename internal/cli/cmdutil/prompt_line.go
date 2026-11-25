package cmdutil

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"syscall"

	"charm.land/lipgloss/v2"
)

// The line-mode prompts are for LORVIK_ACCESSIBLE and TERM=dumb: plain text,
// no colour, no cursor movement. Invalid input re-asks; EOF cancels.

func (p *terminalPrompter) reader() *bufio.Reader {
	if p.rd == nil {
		p.rd = bufio.NewReader(p.s.In)
	}
	return p.rd
}

// readLine returns one line without its terminator. EOF with nothing read is
// ErrCancelled; a last line without a newline still counts.
//
// A cancelled ctx returns at once. In line mode the terminal is cooked, so
// Ctrl-C arrives as SIGINT and cancels ctx while the read is blocked. The
// reading goroutine then stays blocked until stdin yields a line or closes;
// nothing reads p.rd after a cancel (the ctx check above runs first), and the
// process exits right after one, so it is not waited for.
func (p *terminalPrompter) readLine(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	rd := p.reader()
	go func() {
		line, err := rd.ReadString('\n')
		ch <- result{line, err}
	}()
	var r result
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r = <-ch:
	}
	switch {
	case r.err == nil, errors.Is(r.err, io.EOF) && r.line != "":
		return strings.TrimRight(r.line, "\r\n"), nil
	case errors.Is(r.err, io.EOF):
		return "", ErrCancelled
	default:
		return "", fmt.Errorf("read answer: %w", r.err)
	}
}

func (p *terminalPrompter) heading(q Question) {
	if q.Description != "" {
		fmt.Fprintln(p.s.ErrOut, q.Description)
	}
}

func (p *terminalPrompter) lineSelect(ctx context.Context, q Question, opts []Option, def int) (int, error) {
	if def < 0 || def >= len(opts) {
		def = 0
	}
	fmt.Fprintln(p.s.ErrOut, q.Title)
	p.heading(q)
	labelW := 0
	for _, o := range opts {
		labelW = max(labelW, lipgloss.Width(o.Label))
	}
	for i, o := range opts {
		row := fmt.Sprintf("  %d) %s", i+1, o.Label)
		if o.Detail != "" {
			row += strings.Repeat(" ", labelW-lipgloss.Width(o.Label)) + "   " + o.Detail
		}
		fmt.Fprintln(p.s.ErrOut, row)
	}
	for {
		fmt.Fprintf(p.s.ErrOut, "Choose 1-%d [%d]: ", len(opts), def+1)
		line, err := p.readLine(ctx)
		if err != nil {
			return 0, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return def, nil
		}
		n, err := strconv.Atoi(line)
		if err != nil || n < 1 || n > len(opts) {
			fmt.Fprintf(p.s.ErrOut, "Enter a number from 1 to %d.\n", len(opts))
			continue
		}
		return n - 1, nil
	}
}

func (p *terminalPrompter) lineInput(ctx context.Context, q Question, def string, validate func(string) error) (string, error) {
	p.heading(q)
	for {
		if def != "" {
			fmt.Fprintf(p.s.ErrOut, "%s [%s]: ", q.Title, def)
		} else {
			fmt.Fprintf(p.s.ErrOut, "%s: ", q.Title)
		}
		line, err := p.readLine(ctx)
		if err != nil {
			return "", err
		}
		v := strings.TrimSpace(line)
		if v == "" {
			v = def
		}
		if validate != nil {
			if err := validate(v); err != nil {
				fmt.Fprintln(p.s.ErrOut, err.Error())
				continue
			}
		}
		return v, nil
	}
}

func (p *terminalPrompter) linePassword(ctx context.Context, q Question) (string, error) {
	p.heading(q)
	fmt.Fprintf(p.s.ErrOut, "%s: ", q.Title)
	if p.s.ReadPassword == nil {
		return p.readLine(ctx)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	v, err := p.s.ReadPassword()
	fmt.Fprintln(p.s.ErrOut)
	if errors.Is(err, syscall.ENOTTY) {
		// Stdin is a pipe: there is nothing to hide, so read it as a line.
		return p.readLine(ctx)
	}
	if errors.Is(err, io.EOF) {
		return "", ErrCancelled
	}
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return v, nil
}

func (p *terminalPrompter) lineConfirm(ctx context.Context, q Question, def bool) (bool, error) {
	p.heading(q)
	for {
		fmt.Fprintf(p.s.ErrOut, "%s %s: ", q.Title, confirmHint(def))
		line, err := p.readLine(ctx)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(p.s.ErrOut, "Answer y or n.")
	}
}
