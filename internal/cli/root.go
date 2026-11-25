// Package cli defines the lorvik command tree used by agents and humans.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

const (
	groupFind  = "find"
	groupMove  = "move"
	groupWrite = "write"
	groupKnow  = "knowledge"
	groupSetup = "setup"
	groupOrg   = "org"
	groupOther = "other"

	// annotationNoNotice marks commands that already report updates, so the
	// "N unread" line would be noise.
	annotationNoNotice = "lorvik/no-notice"
)

// Main is the entry point called by cmd/lorvik. It returns the exit code.
func Main(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	streams := cmdutil.System()
	f := &cmdutil.Factory{IOStreams: streams}
	root := NewRoot(f)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err != nil && !errors.Is(err, cmdutil.ErrSilent) {
		fmt.Fprintln(streams.ErrOut, cmdutil.FormatError(err, os.Getenv))
	}
	return cmdutil.ExitCode(err)
}

// NewRoot builds the command tree against the given factory. Tests pass a
// factory with in-memory streams and a client pointed at httptest.
func NewRoot(f *cmdutil.Factory) *cobra.Command {
	a := &app{f: f}
	bold := headingStyle(f.IOStreams.ColorEnabled())
	root := &cobra.Command{
		Use:   "lorvik",
		Short: "Track work shared between humans and AI agents",
		Long:  rootLong(bold),
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cmdutil.Usagef("unknown command %q (see `lorvik --help`)", args[0])
			}
			return cmd.Help()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			a.ctx = cmd.Context()
		},
		PersistentPostRun: func(cmd *cobra.Command, _ []string) {
			a.notice(cmd)
			a.remind(cmd)
		},
	}
	root.SetOut(f.IOStreams.Out)
	root.SetErr(f.IOStreams.ErrOut)
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return cmdutil.Usagef("%v (see `%s --help`)", err, c.CommandPath())
	})
	pf := root.PersistentFlags()
	pf.StringVarP(&a.project, "project", "p", "", "Project slug (default: LORVIK_PROJECT or .lorvik.toml)")
	pf.StringVar(&a.apiURL, "api-url", "", "Server URL (default: LORVIK_API_URL)")
	pf.BoolVar(&a.asJSON, "json", false, "Output the raw API JSON")
	pf.StringVar(&a.sessionFlag, "session", "", "Session label for this chat (default: LORVIK_SESSION or derived; see lorvik session)")
	pf.BoolVarP(&a.quiet, "quiet", "Q", false, "Suppress the unread notice on stderr and an empty inbox")

	root.AddGroup(
		&cobra.Group{ID: groupFind, Title: bold("Find work")},
		&cobra.Group{ID: groupMove, Title: bold("Move cards")},
		&cobra.Group{ID: groupWrite, Title: bold("Write to the board")},
		&cobra.Group{ID: groupKnow, Title: bold("Decisions and notes")},
		&cobra.Group{ID: groupSetup, Title: bold("Setup")},
		&cobra.Group{ID: groupOrg, Title: bold("Org and access")},
		&cobra.Group{ID: groupOther, Title: bold("Other")},
	)
	root.SetCompletionCommandGroupID(groupSetup)
	root.SetHelpCommandGroupID(groupOther)
	root.SetUsageTemplate(usageTemplate(bold))
	add := func(id string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = id
			root.AddCommand(c)
		}
	}
	add(groupFind, newInbox(a), newLs(a), newNext(a), newSearch(a), newShow(a), newStatus(a), newWatch(a), newWho(a))
	add(groupMove,
		newClaim(a), newDone(a), newDrop(a), newFold(a), newHeartbeat(a), newLock(a), newLocks(a), newReady(a), newRelease(a),
		newReopen(a), newStart(a), newUnlock(a), newWait(a),
	)
	add(groupWrite, newAdd(a), newAnswer(a), newAsk(a), newComment(a), newDecide(a), newHistory(a), newEdit(a), newEscalate(a), newSubscribe(a), newLink(a), newSync(a), newUnsubscribe(a), newUnlink(a))
	add(groupKnow, newDecisions(a), newNotes(a))
	add(groupSetup, newInit(a), newLogin(a), newMe(a), newSession(a))
	add(groupOrg, newAgent(a), newMember(a), newOrg(a), newProject(a), newToken(a))
	add(groupOther, newActors(a), newPing(a), newVersion(a))
	root.AddCommand(topics(bold)...)
	return root
}

// notice prints the one-line unread hint after a successful command.
func (a *app) notice(cmd *cobra.Command) {
	if a.quiet || a.queued || a.cl == nil || cmd.Annotations[annotationNoNotice] != "" {
		return
	}
	if msg := unreadNotice(a.cl.LastUpdates().Unread); msg != "" {
		fmt.Fprintln(a.errOut(), msg)
	}
}

// headingStyle returns the function that styles help headings: bold on a
// colour terminal, the identity otherwise. It is per root, not a cobra
// template func, because those live in a package-global map.
func headingStyle(color bool) func(string) string {
	if !color {
		return func(s string) string { return s }
	}
	return func(s string) string { return "\x1b[1m" + s + "\x1b[0m" }
}

func rootLong(bold func(string) string) string {
	return `Lorvik is a task board shared by humans and AI agents: cards, decisions,
notes, and an inbox of what everyone else did. This is its command line.

` + bold("Get started") + `
  lorvik login              sign in to your server
  lorvik init               bind this repository to a project
  lorvik status             the board at a glance

` + bold("The loop") + `
  lorvik next               show the best ready card
  lorvik start 42           claim it and mark it working
  lorvik done 42 -m "..."   finish it with a summary`
}

// usageTemplate is cobra's default usage template with plain headings, the
// root's own usage line and help topics, and the footer wording. It is
// inherited by every subcommand. The ANSI codes are spliced in here, once,
// so colour needs no template function.
func usageTemplate(bold func(string) string) string {
	return bold("Usage") + `{{if .Runnable}}
  {{if .HasParent}}{{.UseLine}}{{else}}{{.CommandPath}} <command> [flags]{{end}}{{end}}{{if .HasAvailableSubCommands}}{{if .HasParent}}
  {{.CommandPath}} [command]{{end}}{{end}}{{if gt (len .Aliases) 0}}

` + bold("Aliases") + `
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

` + bold("Examples") + `
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

` + bold("Commands") + `{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

` + bold("More commands") + `{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasHelpSubCommands}}

` + bold("Help topics") + `{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

` + bold("Flags") + `
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

` + bold("Global flags") + `
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

{{if .HasParent}}Run '{{.CommandPath}} <command> --help' for more information about a command.{{else}}Run '{{.CommandPath}} <command> --help' for one command, '{{.CommandPath}} help config' for setup.{{end}}{{end}}
`
}
