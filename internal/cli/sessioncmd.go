package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

// sessionView is the --json shape of `session`.
type sessionView struct {
	Session       string `json:"session"`
	SessionSource string `json:"session_source"`
	Generated     bool   `json:"generated"`
	// SessionName is the name the CLI sends for this chat, with
	// SessionNameSource where it found it: LORVIK_SESSION_NAME, a harness,
	// "off" when the user config stops names, or empty when there is none.
	SessionName       string `json:"session_name"`
	SessionNameSource string `json:"session_name_source"`
}

// sessionNameView is the --json shape of `session name`.
type sessionNameView struct {
	Session     string `json:"session"`
	SessionName string `json:"session_name"`
}

func newSession(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Print this chat's session label and whether to pass --session",
		Long: `Print the session label this chat uses and where it came from. For a
chat lorvik can identify (a harness variable, a chat process or a terminal
tab) the label is derived and there is nothing to pass. When it can only
fall back to the repo, every chat in the repo would share one label, so this
prints a fresh random label instead; pass it as --session <label> on every
lorvik call in this chat. Nothing is written to disk and no server is
contacted.

The second line is the name people call this chat, which the board shows
before the label. lorvik sends it with every command when LORVIK_SESSION_NAME
is set or the harness keeps a chat name it can read (Claude Code), so a
rename there reaches the board by itself. For any other chat, see
` + "`lorvik session name`" + `. Everyone who can see the chat's cards or
locks sees the name. To send no names at all, put session_names = "off" in
your lorvik config file.

Example:
  lorvik session`,
		Args:        usageArgs(cobra.NoArgs),
		Annotations: map[string]string{annotationNoNotice: "1"},
		RunE: func(_ *cobra.Command, _ []string) error {
			si := a.sessionInfo()
			v := sessionView{Session: si.Label, SessionSource: si.Source}
			if si.Source == sourceRepo {
				label, err := a.generateSession()
				if err != nil {
					return err
				}
				v = sessionView{Session: label, SessionSource: "generated", Generated: true}
			}
			cn := a.chatName()
			v.SessionName, v.SessionNameSource = cn.Name, cn.Source
			if cn.Off {
				v.SessionNameSource = sessionNamesOff
			}
			return a.emit(v, func(p *cmdutil.Printer) {
				switch {
				case v.Session == "":
					p.Line("session none (%s is not a valid label)", v.SessionSource)
				case v.Generated:
					p.Line("session %s (generated)", v.Session)
					p.Line("pass --session %s on every lorvik call in this chat", v.Session)
				case v.SessionSource == "--session":
					p.Line("session %s (--session)", v.Session)
					p.Line("keep passing --session %s on every lorvik call in this chat", v.Session)
				default:
					p.Line("session %s (%s)", v.Session, v.SessionSource)
					p.Line("nothing to pass: this label is derived for this chat")
				}
				switch {
				case cn.Off:
					p.Line("name not sent: %s is %q in %s", configSessionNames, sessionNamesOff, userConfigPath(a.getenv))
				case cn.Name != "":
					p.Line("name %s (%s): the board follows it, rename the chat there", cn.Name, cn.Source)
				default:
					p.Line("name none found: when your human names this chat, run lorvik session name \"<that name>\"")
				}
			})
		},
	}
	cmd.AddCommand(newSessionName(a))
	return cmd
}

func newSessionName(a *app) *cobra.Command {
	var clearName bool
	cmd := &cobra.Command{
		Use:   `name "<text>"`,
		Short: "Name this chat on the board when its harness has no chat name lorvik can read",
		Long: `Store the name people call this chat, which the board shows before its
label. Use the name your human gave the chat, never one you made up, and run
it again when they rename the chat. --clear removes the name.

A chat whose name lorvik already reads (LORVIK_SESSION_NAME, or the chat's
name in Claude Code) is refused with exit 4: change the name there and the
board follows on the next lorvik command.

Examples:
  lorvik session name "billing migration"
  lorvik session name --clear`,
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = cleanSentName(args[0])
			}
			switch {
			case clearName && len(args) == 1:
				return cmdutil.Usagef("give a name or --clear, not both")
			case !clearName && name == "":
				return cmdutil.Usagef("give the name your human calls this chat, or --clear to remove it")
			}
			cn := a.chatName()
			switch {
			case cn.Name != "":
				return &cmdutil.ExitError{Code: cmdutil.ExitConflict, Err: fmt.Errorf(
					"this chat's name comes from %s (%q): change it there and the board follows on the next lorvik command", cn.Source, cn.Name)}
			case cn.Off && !clearName:
				return &cmdutil.ExitError{Code: cmdutil.ExitConflict, Err: fmt.Errorf(
					"chat names are off: %s is %q in %s", configSessionNames, sessionNamesOff, userConfigPath(a.getenv))}
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			s, err := c.SetSessionName(cmd.Context(), name)
			if err != nil {
				return err
			}
			v := sessionNameView{Session: s.Label, SessionName: s.Name}
			return a.emit(v, func(p *cmdutil.Printer) {
				if v.SessionName == "" {
					p.Line("session %s has no name now", v.Session)
					return
				}
				p.Line("session %s is named %s", v.Session, v.SessionName)
			})
		},
	}
	cmd.Flags().BoolVar(&clearName, "clear", false, "remove this chat's name")
	return cmd
}
