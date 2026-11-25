package cmdutil

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// collapsedSecret is what a password leaves behind, whatever its length.
const collapsedSecret = "••••••••"

// inputModel edits one line of text. With secret set it shows one bullet per
// rune and never the text.
type inputModel struct {
	promptBase
	runes    []rune
	pos      int
	secret   bool
	validate func(string) error
	errMsg   string
	value    string
}

func newInputModel(q Question, def string, validate func(string) error, secret bool, th promptTheme) *inputModel {
	r := []rune(def)
	return &inputModel{
		q: q, th: th,
		runes:    r,
		pos:      len(r),
		secret:   secret,
		validate: validate,
	}
}

// Init asks the terminal for its background colour.
func (m *inputModel) Init() tea.Cmd { return tea.RequestBackgroundColor }

func (m *inputModel) insert(s string) {
	s = strings.NewReplacer("\r", "", "\n", "").Replace(s)
	if s == "" {
		return
	}
	add := []rune(s)
	out := make([]rune, 0, len(m.runes)+len(add))
	out = append(out, m.runes[:m.pos]...)
	out = append(out, add...)
	out = append(out, m.runes[m.pos:]...)
	m.runes = out
	m.pos += len(add)
	m.errMsg = ""
}

func (m *inputModel) deleteRange(from, to int) {
	m.runes = append(m.runes[:from:from], m.runes[to:]...)
	m.pos = from
	m.errMsg = ""
}

func (m *inputModel) wordStart() int {
	i := m.pos
	for i > 0 && unicode.IsSpace(m.runes[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(m.runes[i-1]) {
		i--
	}
	return i
}

func (m *inputModel) submit() tea.Cmd {
	v := string(m.runes)
	if !m.secret {
		v = strings.TrimSpace(v)
		if m.validate != nil {
			if err := m.validate(v); err != nil {
				m.errMsg = err.Error()
				return nil
			}
		}
	}
	m.value = v
	return m.finish()
}

// Update applies one key press, paste or terminal reply.
func (m *inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.background(msg)
	if m.done {
		return m, nil
	}
	if p, ok := msg.(tea.PasteMsg); ok {
		m.insert(p.Content)
		return m, nil
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if isCancelKey(k) {
		return m, m.cancel()
	}
	switch k.Keystroke() {
	case "enter":
		return m, m.submit()
	case "backspace":
		if m.pos > 0 {
			m.deleteRange(m.pos-1, m.pos)
		}
	case "delete":
		if m.pos < len(m.runes) {
			m.deleteRange(m.pos, m.pos+1)
		}
	case "left":
		m.pos = max(0, m.pos-1)
	case "right":
		m.pos = min(len(m.runes), m.pos+1)
	case "home", "ctrl+a":
		m.pos = 0
	case "end", "ctrl+e":
		m.pos = len(m.runes)
	case "ctrl+u":
		m.deleteRange(0, m.pos)
	case "ctrl+w":
		m.deleteRange(m.wordStart(), m.pos)
	default:
		m.insert(typed(k))
	}
	return m, nil
}

// text is what the field shows: the runes, or one bullet per rune.
func (m *inputModel) text() []rune {
	if !m.secret {
		return m.runes
	}
	return []rune(strings.Repeat("•", len(m.runes)))
}

// View draws the prompt, or the collapsed answer once it is done.
func (m *inputModel) View() tea.View {
	if m.cancelled {
		return tea.NewView("")
	}
	if m.done {
		answer := m.value
		if m.secret {
			answer = collapsedSecret
		}
		return m.view([]string{m.collapsed(answer), ""})
	}
	t := m.text()
	cursor := " "
	after := ""
	if m.pos < len(t) {
		cursor = string(t[m.pos])
		after = string(t[m.pos+1:])
	}
	field := string(t[:m.pos]) + lipgloss.NewStyle().Reverse(true).Render(cursor) + after
	lines := m.header(field)
	if m.errMsg != "" {
		lines = append(lines, m.th.red("✗ "+m.errMsg))
	}
	return m.view(lines)
}
