package cmdutil

import tea "charm.land/bubbletea/v2"

// confirmModel asks a yes or no question; Enter gives the default.
type confirmModel struct {
	promptBase
	def    bool
	answer bool
}

func newConfirmModel(q Question, def bool, th promptTheme) *confirmModel {
	return &confirmModel{q: q, th: th, def: def}
}

// Init asks the terminal for its background colour.
func (m *confirmModel) Init() tea.Cmd { return tea.RequestBackgroundColor }

// Update applies one key press, paste or terminal reply.
func (m *confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.background(msg)
	k, ok := msg.(tea.KeyPressMsg)
	if !ok || m.done {
		return m, nil
	}
	if isCancelKey(k) {
		return m, m.cancel()
	}
	switch k.Keystroke() {
	case "enter":
		m.answer = m.def
		return m, m.finish()
	case "y", "Y", "shift+y":
		m.answer = true
		return m, m.finish()
	case "n", "N", "shift+n":
		m.answer = false
		return m, m.finish()
	}
	return m, nil
}

// View draws the prompt, or the collapsed answer once it is done.
func (m *confirmModel) View() tea.View {
	if m.cancelled {
		return tea.NewView("")
	}
	if m.done {
		answer := "No"
		if m.answer {
			answer = "Yes"
		}
		return m.view([]string{m.collapsed(answer), ""})
	}
	return m.view(m.header(m.th.faint(confirmHint(m.def))))
}

func confirmHint(def bool) string {
	if def {
		return "(Y/n)"
	}
	return "(y/N)"
}
