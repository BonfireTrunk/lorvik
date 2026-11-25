package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// plain strips colour and the trailing newline that keeps the shell prompt off
// the collapsed line.
func plain(v tea.View) string {
	return strings.TrimSuffix(ansiRE.ReplaceAllString(v.Content, ""), "\n")
}

// press builds a key press from a keystroke name or a printable string.
func press(s string) tea.Msg {
	special := map[string]tea.KeyPressMsg{
		"enter":     {Code: tea.KeyEnter},
		"esc":       {Code: tea.KeyEscape},
		"up":        {Code: tea.KeyUp},
		"down":      {Code: tea.KeyDown},
		"left":      {Code: tea.KeyLeft},
		"right":     {Code: tea.KeyRight},
		"home":      {Code: tea.KeyHome},
		"end":       {Code: tea.KeyEnd},
		"pgup":      {Code: tea.KeyPgUp},
		"pgdown":    {Code: tea.KeyPgDown},
		"backspace": {Code: tea.KeyBackspace},
		"delete":    {Code: tea.KeyDelete},
		"ctrl+a":    {Code: 'a', Mod: tea.ModCtrl},
		"ctrl+c":    {Code: 'c', Mod: tea.ModCtrl},
		"ctrl+e":    {Code: 'e', Mod: tea.ModCtrl},
		"ctrl+u":    {Code: 'u', Mod: tea.ModCtrl},
		"ctrl+w":    {Code: 'w', Mod: tea.ModCtrl},
	}
	if k, ok := special[s]; ok {
		return k
	}
	r := []rune(s)
	return tea.KeyPressMsg{Code: r[0], Text: s}
}

func drive(m tea.Model, keys ...string) {
	for _, k := range keys {
		m.Update(press(k))
	}
}

func typeText(m tea.Model, s string) {
	for _, r := range s {
		m.Update(press(string(r)))
	}
}

func numbered(n int) []Option {
	opts := make([]Option, n)
	for i := range opts {
		opts[i] = Option{Label: fmt.Sprintf("option%02d", i+1), Detail: fmt.Sprintf("detail%02d", i+1)}
	}
	return opts
}

func TestSelectMovesAndClamps(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		keys []string
		want int
	}{
		{name: "down twice", keys: []string{"down", "down"}, want: 2},
		{name: "j and k move when not filtering", keys: []string{"j", "j", "k"}, want: 1},
		{name: "clamps at the top", keys: []string{"up", "up"}, want: 0},
		{name: "clamps at the bottom", keys: []string{"down", "down", "down", "down", "down"}, want: 3},
		{name: "end then home", keys: []string{"end", "home", "down"}, want: 1},
		{name: "pgdn jumps to the last", keys: []string{"pgdown"}, want: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newSelectModel(Question{Title: "Pick"}, numbered(4), 0, promptTheme{})
			drive(m, tt.keys...)
			drive(m, "enter")
			if !m.done || m.result() != tt.want {
				t.Errorf("after %v: done=%v result=%d, want result %d", tt.keys, m.done, m.result(), tt.want)
			}
		})
	}
}

func TestSelectDefaultIsPreselected(t *testing.T) {
	t.Parallel()
	m := newSelectModel(Question{Title: "Pick"}, numbered(4), 2, promptTheme{})
	drive(m, "enter")
	if m.result() != 2 {
		t.Errorf("result = %d, want 2", m.result())
	}
}

func TestSelectFilteringReturnsOriginalIndex(t *testing.T) {
	t.Parallel()
	m := newSelectModel(Question{Title: "Pick"}, numbered(12), 0, promptTheme{})
	// j would be movement without filtering; here every key is filter text.
	typeText(m, "Option1")
	if len(m.visible) != 3 {
		t.Fatalf("visible = %v, want option10 to option12", m.visible)
	}
	drive(m, "down", "enter")
	if m.result() != 10 {
		t.Errorf("result = %d, want original index 10 (option11)", m.result())
	}
}

func TestSelectFilterMatchesDetailAndBackspaceWidens(t *testing.T) {
	t.Parallel()
	m := newSelectModel(Question{Title: "Pick"}, numbered(12), 0, promptTheme{})
	typeText(m, "DETAIL05")
	if len(m.visible) != 1 || m.visible[0] != 4 {
		t.Fatalf("visible = %v, want [4]", m.visible)
	}
	drive(m, "backspace")
	if len(m.visible) != 9 { // "detail0" matches 01 to 09
		t.Errorf("after one backspace visible = %d, want 9", len(m.visible))
	}
}

func TestSelectNoMatchesBlocksEnter(t *testing.T) {
	t.Parallel()
	m := newSelectModel(Question{Title: "Pick"}, numbered(12), 0, promptTheme{})
	typeText(m, "zzz")
	drive(m, "enter")
	if m.done {
		t.Error("enter with no matches finished the prompt")
	}
	if v := plain(m.View()); !strings.Contains(v, "no matches") {
		t.Errorf("view lacks 'no matches':\n%s", v)
	}
}

func TestSelectWindowScrollsToCursor(t *testing.T) {
	t.Parallel()
	m := newSelectModel(Question{Title: "Pick"}, numbered(30), 0, promptTheme{})
	v := plain(m.View())
	if rows := strings.Count(v, "detail"); rows != selectMaxRows {
		t.Errorf("visible rows = %d, want %d", rows, selectMaxRows)
	}
	drive(m, "end")
	v = plain(m.View())
	if !strings.Contains(v, "› option30") || strings.Contains(v, "option01 ") {
		t.Errorf("window did not follow the cursor to the end:\n%s", v)
	}
}

func TestSelectViewShape(t *testing.T) {
	t.Parallel()
	small := newSelectModel(Question{Title: "Pick", Description: "one of these"}, []Option{{Label: "a", Detail: "first"}, {Label: "longer", Detail: "second"}}, 0, promptTheme{})
	v := plain(small.View())
	for _, want := range []string{"? Pick", "one of these", "  › a        first", "    longer   second", "↑/↓ move · enter select · esc cancel"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "type to filter") {
		t.Error("small list advertises filtering")
	}
	big := newSelectModel(Question{Title: "Pick"}, numbered(8), 0, promptTheme{})
	if v := plain(big.View()); !strings.Contains(v, "· type to filter") {
		t.Errorf("big list lacks the filter hint:\n%s", v)
	}
	drive(small, "down", "enter")
	if v := plain(small.View()); v != "? Pick longer" {
		t.Errorf("collapsed view = %q, want %q", v, "? Pick longer")
	}
}

func TestEscAndCtrlCCancelEveryPrompt(t *testing.T) {
	t.Parallel()
	models := map[string]func() promptModel{
		"select":   func() promptModel { return newSelectModel(Question{Title: "q"}, numbered(2), 0, promptTheme{}) },
		"input":    func() promptModel { return newInputModel(Question{Title: "q"}, "", nil, false, promptTheme{}) },
		"password": func() promptModel { return newInputModel(Question{Title: "q"}, "", nil, true, promptTheme{}) },
		"confirm":  func() promptModel { return newConfirmModel(Question{Title: "q"}, false, promptTheme{}) },
	}
	for name, mk := range models {
		for _, key := range []string{"esc", "ctrl+c"} {
			t.Run(name+"/"+key, func(t *testing.T) {
				t.Parallel()
				m := mk()
				_, cmd := m.Update(press(key))
				if !m.wasCancelled() || cmd == nil {
					t.Errorf("%s did not cancel (cancelled=%v, quit cmd=%v)", key, m.wasCancelled(), cmd != nil)
				}
			})
		}
	}
}

func TestInputEditingKeys(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		def  string
		keys []string
		want string
	}{
		{name: "prefilled with cursor at end", def: "abc", keys: []string{"d"}, want: "abcd"},
		{name: "insert at cursor", def: "ac", keys: []string{"left", "b"}, want: "abc"},
		{name: "backspace", def: "abc", keys: []string{"backspace"}, want: "ab"},
		{name: "backspace at start is a no-op", def: "abc", keys: []string{"home", "backspace"}, want: "abc"},
		{name: "delete removes under the cursor", def: "abc", keys: []string{"home", "delete"}, want: "bc"},
		{name: "left and right", def: "abc", keys: []string{"left", "left", "right", "X"}, want: "abXc"},
		{name: "ctrl-a is home", def: "bc", keys: []string{"ctrl+a", "a"}, want: "abc"},
		{name: "ctrl-e is end", def: "ab", keys: []string{"home", "ctrl+e", "c"}, want: "abc"},
		{name: "end", def: "ab", keys: []string{"home", "end", "c"}, want: "abc"},
		{name: "ctrl-u deletes to start", def: "hello world", keys: []string{"left", "left", "left", "left", "left", "ctrl+u"}, want: "world"},
		{name: "ctrl-w deletes a word back", def: "one two", keys: []string{"ctrl+w"}, want: "one "},
		{name: "ctrl-w skips trailing spaces first", def: "one two  ", keys: []string{"ctrl+w"}, want: "one "},
		{name: "unicode is edited by rune", def: "héé", keys: []string{"backspace"}, want: "hé"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newInputModel(Question{Title: "q"}, tt.def, nil, false, promptTheme{})
			drive(m, tt.keys...)
			if got := string(m.runes); got != tt.want {
				t.Errorf("after %v on %q: text = %q, want %q", tt.keys, tt.def, got, tt.want)
			}
		})
	}
}

func TestInputPasteStripsNewlines(t *testing.T) {
	t.Parallel()
	m := newInputModel(Question{Title: "q"}, "ab", nil, false, promptTheme{})
	drive(m, "left")
	m.Update(tea.PasteMsg{Content: "x\r\ny\nz"})
	if got := string(m.runes); got != "axyzb" {
		t.Errorf("text after paste = %q, want %q", got, "axyzb")
	}
	drive(m, "enter")
	if m.value != "axyzb" {
		t.Errorf("value = %q, want axyzb", m.value)
	}
}

func TestInputEnterTrimsAndValidateKeepsPromptOpen(t *testing.T) {
	t.Parallel()
	validate := func(s string) error {
		if s == "" {
			return errors.New("required")
		}
		return nil
	}
	m := newInputModel(Question{Title: "Name"}, "  ", validate, false, promptTheme{})
	drive(m, "enter")
	if m.done {
		t.Fatal("prompt finished despite a validation error")
	}
	if v := plain(m.View()); !strings.Contains(v, "✗ required") {
		t.Errorf("view lacks the validation error:\n%s", v)
	}
	typeText(m, "ok ")
	if v := plain(m.View()); strings.Contains(v, "✗") {
		t.Errorf("error line stayed after editing:\n%s", v)
	}
	drive(m, "enter")
	if !m.done || m.value != "ok" {
		t.Errorf("done=%v value=%q, want done with %q", m.done, m.value, "ok")
	}
	if v := plain(m.View()); v != "? Name ok" {
		t.Errorf("collapsed view = %q, want %q", v, "? Name ok")
	}
}

func TestPasswordNeverShowsTheText(t *testing.T) {
	t.Parallel()
	m := newInputModel(Question{Title: "Token"}, "", nil, true, promptTheme{})
	typeText(m, "hunter2")
	if v := plain(m.View()); strings.Contains(v, "hunter") || strings.Count(v, "•") != 7 {
		t.Errorf("editing view = %q, want seven bullets and no text", v)
	}
	drive(m, "backspace")
	if v := plain(m.View()); strings.Count(v, "•") != 6 {
		t.Errorf("after backspace view = %q, want six bullets", v)
	}
	drive(m, "enter")
	if m.value != "hunter" {
		t.Errorf("value = %q, want hunter", m.value)
	}
	if v := plain(m.View()); v != "? Token ••••••••" {
		t.Errorf("collapsed view = %q, want fixed eight bullets", v)
	}
}

func TestPasswordIsNotTrimmed(t *testing.T) {
	t.Parallel()
	m := newInputModel(Question{Title: "Token"}, "", nil, true, promptTheme{})
	typeText(m, " a ")
	drive(m, "enter")
	if m.value != " a " {
		t.Errorf("value = %q, want spaces kept", m.value)
	}
}

func TestConfirm(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		def      bool
		key      string
		want     bool
		wantView string
	}{
		{name: "enter gives default no", def: false, key: "enter", want: false, wantView: "? Bind it? No"},
		{name: "enter gives default yes", def: true, key: "enter", want: true, wantView: "? Bind it? Yes"},
		{name: "y overrides default no", def: false, key: "y", want: true, wantView: "? Bind it? Yes"},
		{name: "N overrides default yes", def: true, key: "N", want: false, wantView: "? Bind it? No"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newConfirmModel(Question{Title: "Bind it?"}, tt.def, promptTheme{})
			drive(m, tt.key)
			if !m.done || m.answer != tt.want {
				t.Errorf("done=%v answer=%v, want %v", m.done, m.answer, tt.want)
			}
			if v := plain(m.View()); v != tt.wantView {
				t.Errorf("collapsed view = %q, want %q", v, tt.wantView)
			}
		})
	}
}

func TestConfirmHintShowsDefault(t *testing.T) {
	t.Parallel()
	for def, want := range map[bool]string{false: "? Bind it? (y/N)", true: "? Bind it? (Y/n)"} {
		m := newConfirmModel(Question{Title: "Bind it?"}, def, promptTheme{})
		if v := plain(m.View()); v != want {
			t.Errorf("view with def=%v = %q, want %q", def, v, want)
		}
	}
}

func TestBackgroundReplySwitchesTheme(t *testing.T) {
	t.Parallel()
	m := newConfirmModel(Question{Title: "q"}, false, promptTheme{colored: true, dark: true})
	m.Update(tea.BackgroundColorMsg{Color: lightColor{}})
	if m.th.dark {
		t.Error("a light background left the dark theme on")
	}
}

type lightColor struct{}

func (lightColor) RGBA() (r, g, b, a uint32) { return 0xffff, 0xffff, 0xffff, 0xffff }

func TestNoColorKeepsEmphasisButNoHue(t *testing.T) {
	t.Parallel()
	off := promptTheme{}
	if got := off.accent("x"); got != "x" {
		t.Errorf("uncoloured accent = %q, want plain text", got)
	}
	if got := off.bold("x"); !strings.Contains(got, "\x1b[1m") {
		t.Errorf("uncoloured bold = %q, want bold kept", got)
	}
	if got := off.faint("x"); !strings.Contains(got, "\x1b[2m") {
		t.Errorf("uncoloured faint = %q, want faint kept", got)
	}
	if got := (promptTheme{colored: true, dark: true}).accent("x"); !strings.Contains(got, "\x1b[") {
		t.Errorf("coloured accent = %q, want an escape", got)
	}
}

func lineStreams(in string) (*terminalPrompter, *Buffer) {
	s, _, errOut := Test(strings.NewReader(in))
	return NewTerminalPrompter(s, func(k string) string {
		if k == "LORVIK_ACCESSIBLE" {
			return "1"
		}
		return ""
	}).(*terminalPrompter), errOut
}

func TestLineSelect(t *testing.T) {
	t.Parallel()
	opts := []Option{{Label: "alpha", Detail: "first"}, {Label: "be", Detail: "second"}}
	tests := []struct {
		name    string
		in      string
		def     int
		want    int
		wantErr error
		wantOut []string
	}{
		{name: "empty line takes the default", in: "\n", def: 1, want: 1, wantOut: []string{"  1) alpha   first", "  2) be      second", "Choose 1-2 [2]: "}},
		{name: "number picks", in: "1\n", def: 1, want: 0},
		{name: "invalid then valid re-asks with the reason", in: "9\nx\n2\n", want: 1, wantOut: []string{"Enter a number from 1 to 2."}},
		{name: "EOF cancels", in: "", wantErr: ErrCancelled},
		{name: "last line without newline counts", in: "2", want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, out := lineStreams(tt.in)
			got, err := p.Select(context.Background(), Question{Title: "Pick"}, opts, tt.def)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Fatalf("Select = %d, %v; want %d, %v", got, err, tt.want, tt.wantErr)
			}
			for _, w := range tt.wantOut {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output lacks %q:\n%s", w, out.String())
				}
			}
			if strings.Contains(out.String(), "\x1b") {
				t.Errorf("line mode wrote an escape sequence: %q", out.String())
			}
		})
	}
}

func TestLineInput(t *testing.T) {
	t.Parallel()
	nonEmpty := func(s string) error {
		if s == "" {
			return errors.New("required")
		}
		return nil
	}
	tests := []struct {
		name     string
		in       string
		def      string
		validate func(string) error
		want     string
		wantErr  error
		wantOut  string
	}{
		{name: "empty line takes the default", in: "\n", def: "lorvik", want: "lorvik", wantOut: "Name [lorvik]: "},
		{name: "no default shows no brackets", in: "x\n", want: "x", wantOut: "Name: "},
		{name: "answer is trimmed", in: "  hi  \n", want: "hi"},
		{name: "invalid then valid", in: "\n  \nok\n", validate: nonEmpty, want: "ok", wantOut: "required"},
		{name: "EOF cancels", in: "", wantErr: ErrCancelled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, out := lineStreams(tt.in)
			got, err := p.Input(context.Background(), Question{Title: "Name"}, tt.def, tt.validate)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Fatalf("Input = %q, %v; want %q, %v", got, err, tt.want, tt.wantErr)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("output lacks %q:\n%s", tt.wantOut, out.String())
			}
		})
	}
}

func TestLineModeKeepsReadAheadBetweenPrompts(t *testing.T) {
	t.Parallel()
	p, _ := lineStreams("first\nsecond\ny\n")
	ctx := context.Background()
	a, _ := p.Input(ctx, Question{Title: "A"}, "", nil)
	b, _ := p.Input(ctx, Question{Title: "B"}, "", nil)
	c, err := p.Confirm(ctx, Question{Title: "C"}, false)
	if a != "first" || b != "second" || !c || err != nil {
		t.Errorf("answers = %q, %q, %v, %v; want first, second, true", a, b, c, err)
	}
}

func TestLinePassword(t *testing.T) {
	t.Parallel()
	t.Run("uses ReadPassword when set", func(t *testing.T) {
		t.Parallel()
		p, out := lineStreams("not-this\n")
		p.s.ReadPassword = func() (string, error) { return "s3cret", nil }
		got, err := p.Password(context.Background(), Question{Title: "Password"})
		if got != "s3cret" || err != nil {
			t.Errorf("Password = %q, %v; want s3cret", got, err)
		}
		if !strings.HasPrefix(out.String(), "Password: ") {
			t.Errorf("output = %q, want the Password: prompt", out.String())
		}
	})
	t.Run("falls back to a line", func(t *testing.T) {
		t.Parallel()
		p, _ := lineStreams("piped\n")
		got, err := p.Password(context.Background(), Question{Title: "Password"})
		if got != "piped" || err != nil {
			t.Errorf("Password = %q, %v; want piped", got, err)
		}
	})
	t.Run("a pipe makes ReadPassword fail with ENOTTY and the line is read instead", func(t *testing.T) {
		t.Parallel()
		p, _ := lineStreams("from-pipe\n")
		p.s.ReadPassword = func() (string, error) { return "", fmt.Errorf("read: %w", syscall.ENOTTY) }
		got, err := p.Password(context.Background(), Question{Title: "Password"})
		if got != "from-pipe" || err != nil {
			t.Errorf("Password = %q, %v; want from-pipe", got, err)
		}
	})
	t.Run("EOF from ReadPassword cancels", func(t *testing.T) {
		t.Parallel()
		p, _ := lineStreams("")
		p.s.ReadPassword = func() (string, error) { return "", io.EOF }
		if _, err := p.Password(context.Background(), Question{Title: "Password"}); !errors.Is(err, ErrCancelled) {
			t.Errorf("Password on EOF = %v, want ErrCancelled", err)
		}
	})
}

func TestLineConfirm(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		def     bool
		want    bool
		wantErr error
		wantOut string
	}{
		{name: "empty takes default no", in: "\n", want: false, wantOut: "Bind it? (y/N): "},
		{name: "empty takes default yes", in: "\n", def: true, want: true, wantOut: "Bind it? (Y/n): "},
		{name: "yes", in: "YES\n", want: true},
		{name: "invalid then valid", in: "maybe\nn\n", def: true, want: false, wantOut: "Answer y or n."},
		{name: "EOF cancels", in: "", wantErr: ErrCancelled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, out := lineStreams(tt.in)
			got, err := p.Confirm(context.Background(), Question{Title: "Bind it?"}, tt.def)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Fatalf("Confirm = %v, %v; want %v, %v", got, err, tt.want, tt.wantErr)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("output lacks %q:\n%s", tt.wantOut, out.String())
			}
		})
	}
}

func TestCancelledContextReturnsItsError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p, _ := lineStreams("1\n")
	if _, err := p.Select(ctx, Question{Title: "Pick"}, numbered(2), 0); !errors.Is(err, context.Canceled) {
		t.Errorf("line Select with a cancelled ctx = %v, want context.Canceled", err)
	}
	s, _, _ := Test(nil)
	tp := NewTerminalPrompter(s, nil)
	if _, err := tp.Confirm(ctx, Question{Title: "q"}, false); !errors.Is(err, context.Canceled) {
		t.Errorf("tea Confirm with a cancelled ctx = %v, want context.Canceled", err)
	}
}

func TestLineReadReturnsWhenTheContextIsCancelled(t *testing.T) {
	t.Parallel()
	// A pipe that never yields a line stands in for a cooked terminal where
	// Ctrl-C cancels the context while the read is blocked.
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	s, _, _ := Test(pr)
	p := NewTerminalPrompter(s, func(k string) string {
		if k == "LORVIK_ACCESSIBLE" {
			return "1"
		}
		return ""
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := p.Input(ctx, Question{Title: "Name"}, "", nil)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Input after cancel = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Input still blocked 5s after the context was cancelled")
	}
}

func TestDumbTerminalUsesLineMode(t *testing.T) {
	t.Parallel()
	s, _, errOut := Test(strings.NewReader("y\n"))
	p := NewTerminalPrompter(s, func(k string) string {
		if k == "TERM" {
			return "dumb"
		}
		return ""
	})
	got, err := p.Confirm(context.Background(), Question{Title: "Go"}, false)
	if !got || err != nil || !strings.Contains(errOut.String(), "Go (y/N): ") {
		t.Errorf("Confirm = %v, %v, output %q; want true via the line prompt", got, err, errOut.String())
	}
}
