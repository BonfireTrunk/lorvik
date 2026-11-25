package cmdutil

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// NewTerminalPrompter returns the Prompter that draws on s.ErrOut and reads
// keys from s.In. env looks up environment variables.
//
// It draws inline with bubbletea. When LORVIK_ACCESSIBLE is set or TERM is
// dumb it falls back to plain line prompts that never move the cursor.
func NewTerminalPrompter(s *IOStreams, env func(string) string) Prompter {
	if env == nil {
		env = func(string) string { return "" }
	}
	return &terminalPrompter{s: s, env: env}
}

type terminalPrompter struct {
	s   *IOStreams
	env func(string) string
	// rd buffers s.In in line mode. It lives as long as the prompter so a
	// second prompt does not lose what the first one read ahead.
	rd *bufio.Reader
}

func (p *terminalPrompter) lineMode() bool {
	return p.env("LORVIK_ACCESSIBLE") != "" || p.env("TERM") == "dumb"
}

func (p *terminalPrompter) theme() promptTheme {
	off := p.s.colorOff || p.env("NO_COLOR") != "" || p.env("TERM") == "dumb"
	return promptTheme{colored: !off, dark: true}
}

// Select shows a list and returns the index of the chosen option.
func (p *terminalPrompter) Select(ctx context.Context, q Question, opts []Option, def int) (int, error) {
	if len(opts) == 0 {
		return 0, errors.New("select: no options")
	}
	if p.lineMode() {
		return p.lineSelect(ctx, q, opts, def)
	}
	m := newSelectModel(q, opts, def, p.theme())
	if err := p.run(ctx, m); err != nil {
		return 0, err
	}
	return m.result(), nil
}

// Input reads an editable line of text.
func (p *terminalPrompter) Input(ctx context.Context, q Question, def string, validate func(string) error) (string, error) {
	if p.lineMode() {
		return p.lineInput(ctx, q, def, validate)
	}
	m := newInputModel(q, def, validate, false, p.theme())
	if err := p.run(ctx, m); err != nil {
		return "", err
	}
	return m.value, nil
}

// Password reads a secret, shown as bullets.
func (p *terminalPrompter) Password(ctx context.Context, q Question) (string, error) {
	if p.lineMode() {
		return p.linePassword(ctx, q)
	}
	m := newInputModel(q, "", nil, true, p.theme())
	if err := p.run(ctx, m); err != nil {
		return "", err
	}
	return m.value, nil
}

// Confirm asks a yes or no question.
func (p *terminalPrompter) Confirm(ctx context.Context, q Question, def bool) (bool, error) {
	if p.lineMode() {
		return p.lineConfirm(ctx, q, def)
	}
	m := newConfirmModel(q, def, p.theme())
	if err := p.run(ctx, m); err != nil {
		return false, err
	}
	return m.answer, nil
}

// promptModel is a bubbletea model that records whether the person gave up.
type promptModel interface {
	tea.Model
	wasCancelled() bool
}

// run drives m inline until it finishes. A cancelled ctx returns ctx.Err();
// Esc and Ctrl-C return ErrCancelled.
func (p *terminalPrompter) run(ctx context.Context, m promptModel) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	prog := tea.NewProgram(m,
		tea.WithInput(p.s.In),
		tea.WithOutput(p.s.ErrOut),
		tea.WithContext(ctx),
	)
	_, err := prog.Run()
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if errors.Is(err, tea.ErrInterrupted) {
		return ErrCancelled
	}
	if err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	if m.wasCancelled() {
		return ErrCancelled
	}
	return nil
}

// promptTheme holds the colour decisions for one prompt. dark is assumed
// until the terminal reports its background.
type promptTheme struct {
	colored bool
	dark    bool
}

func (t promptTheme) pick(dark, light string) lipgloss.Style {
	if !t.colored {
		return lipgloss.NewStyle()
	}
	if t.dark {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(dark))
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(light))
}

func (t promptTheme) accent(s string) string { return t.pick("#5FD7FF", "#0066CC").Render(s) }
func (t promptTheme) red(s string) string    { return t.pick("#FF5F5F", "#C41E3A").Render(s) }
func (t promptTheme) bold(s string) string   { return lipgloss.NewStyle().Bold(true).Render(s) }
func (t promptTheme) faint(s string) string  { return lipgloss.NewStyle().Faint(true).Render(s) }

// promptBase is the state every prompt model shares.
type promptBase struct {
	q         Question
	th        promptTheme
	done      bool
	cancelled bool
}

func (b *promptBase) wasCancelled() bool { return b.cancelled }

func (b *promptBase) cancel() tea.Cmd {
	b.cancelled = true
	return tea.Quit
}

func (b *promptBase) finish() tea.Cmd {
	b.done = true
	return tea.Quit
}

// background records the terminal's reply to RequestBackgroundColor.
func (b *promptBase) background(msg tea.Msg) {
	if bg, ok := msg.(tea.BackgroundColorMsg); ok {
		b.th.dark = bg.IsDark()
	}
}

// header is the question line plus the dimmed description, if any.
func (b *promptBase) header(suffix string) []string {
	line := b.th.accent("?") + " " + b.th.bold(b.q.Title)
	if suffix != "" {
		line += " " + suffix
	}
	lines := []string{line}
	if b.q.Description != "" {
		lines = append(lines, b.th.faint(b.q.Description))
	}
	return lines
}

// collapsed is the one line left behind after an answer.
func (b *promptBase) collapsed(answer string) string {
	line := b.th.accent("?") + " " + b.th.bold(b.q.Title)
	if answer != "" {
		line += " " + b.th.accent(answer)
	}
	return line
}

func (b *promptBase) view(lines []string) tea.View {
	return tea.NewView(strings.Join(lines, "\n"))
}

// isCancelKey reports whether msg is Esc or Ctrl-C.
func isCancelKey(k tea.KeyPressMsg) bool {
	s := k.Keystroke()
	return s == "esc" || s == "ctrl+c"
}

// typed returns the printable text of a key press, or "" for special keys and
// for combinations that hold ctrl or alt.
func typed(k tea.KeyPressMsg) string {
	key := k.Key()
	if key.Mod.Contains(tea.ModCtrl) || key.Mod.Contains(tea.ModAlt) {
		return ""
	}
	return key.Text
}
