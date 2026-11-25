package cmdutil

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	selectFilterAbove = 7  // filtering turns on above this many options
	selectMaxRows     = 10 // rows visible at once
)

type selectModel struct {
	promptBase
	opts      []Option
	filtering bool
	filter    []rune
	visible   []int // indices into opts that match the filter
	cursor    int   // position in visible
	offset    int   // first visible row shown
	labelW    int
}

func newSelectModel(q Question, opts []Option, def int, th promptTheme) *selectModel {
	m := &selectModel{
		q: q, th: th,
		opts:      opts,
		filtering: len(opts) > selectFilterAbove,
	}
	for _, o := range opts {
		m.labelW = max(m.labelW, lipgloss.Width(o.Label))
	}
	m.refilter()
	if def > 0 && def < len(opts) {
		m.cursor = def
		m.scroll()
	}
	return m
}

func (m *selectModel) refilter() {
	f := strings.ToLower(string(m.filter))
	m.visible = m.visible[:0]
	for i, o := range m.opts {
		if f == "" || strings.Contains(strings.ToLower(o.Label), f) || strings.Contains(strings.ToLower(o.Detail), f) {
			m.visible = append(m.visible, i)
		}
	}
	m.cursor, m.offset = 0, 0
}

// result is the index into the original options of the chosen one.
func (m *selectModel) result() int { return m.visible[m.cursor] }

func (m *selectModel) move(to int) {
	m.cursor = max(0, min(to, len(m.visible)-1))
	m.scroll()
}

func (m *selectModel) scroll() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+selectMaxRows {
		m.offset = m.cursor - selectMaxRows + 1
	}
}

// Init asks the terminal for its background colour.
func (m *selectModel) Init() tea.Cmd { return tea.RequestBackgroundColor }

// Update applies one key press, paste or terminal reply.
func (m *selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.background(msg)
	k, ok := msg.(tea.KeyPressMsg)
	if !ok || m.done {
		return m, nil
	}
	if isCancelKey(k) {
		return m, m.cancel()
	}
	switch k.Keystroke() {
	case "up":
		m.move(m.cursor - 1)
	case "down":
		m.move(m.cursor + 1)
	case "home":
		m.move(0)
	case "end":
		m.move(len(m.visible) - 1)
	case "pgup":
		m.move(m.cursor - selectMaxRows)
	case "pgdown":
		m.move(m.cursor + selectMaxRows)
	case "enter":
		if len(m.visible) > 0 {
			return m, m.finish()
		}
	case "backspace":
		if m.filtering && len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
			m.refilter()
		}
	default:
		text := typed(k)
		switch {
		case m.filtering && text != "":
			m.filter = append(m.filter, []rune(text)...)
			m.refilter()
		case !m.filtering && text == "k":
			m.move(m.cursor - 1)
		case !m.filtering && text == "j":
			m.move(m.cursor + 1)
		}
	}
	return m, nil
}

// View draws the prompt, or the collapsed answer once it is done.
func (m *selectModel) View() tea.View {
	if m.cancelled {
		return tea.NewView("")
	}
	if m.done {
		return m.view([]string{m.collapsed(m.opts[m.result()].Label), ""})
	}
	lines := m.header("")
	if len(m.filter) > 0 {
		lines = append(lines, m.th.faint("  filter: "+string(m.filter)))
	}
	if len(m.visible) == 0 {
		lines = append(lines, m.th.faint("  no matches"))
	}
	end := min(m.offset+selectMaxRows, len(m.visible))
	for row := m.offset; row < end; row++ {
		o := m.opts[m.visible[row]]
		label := o.Label + strings.Repeat(" ", m.labelW-lipgloss.Width(o.Label))
		detail := ""
		if o.Detail != "" {
			detail = "   " + m.th.faint(o.Detail)
		}
		if row == m.cursor {
			lines = append(lines, "  "+m.th.accent("› "+label)+detail)
		} else {
			lines = append(lines, "    "+label+detail)
		}
	}
	hint := "↑/↓ move · enter select · esc cancel"
	if m.filtering {
		hint += " · type to filter"
	}
	lines = append(lines, m.th.faint(hint))
	return m.view(lines)
}
