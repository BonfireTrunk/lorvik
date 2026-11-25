package cli

import (
	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

var linkKinds = []string{"blocks", "relates", "duplicates", "applies"}

func linkArgs(args []string) (n int64, kind string, m int64, err error) {
	if n, err = parseNum(args[0]); err != nil {
		return 0, "", 0, err
	}
	if m, err = parseNum(args[2]); err != nil {
		return 0, "", 0, err
	}
	if err = oneOf("kind", args[1], linkKinds...); err != nil {
		return 0, "", 0, cmdutil.Usagef("link kind %q: want blocks, relates, duplicates or applies", args[1])
	}
	return n, args[1], m, nil
}

func newLink(a *app) *cobra.Command {
	var toProject string
	cmd := &cobra.Command{
		Use:   "link <number> blocks|relates|duplicates|applies <other>",
		Short: "Link two cards",
		Long: `Add a link from card N to card M. "N blocks M" means M cannot proceed until
N is done. "duplicates" points a dropped duplicate at its survivor. "applies"
runs from a decision to a card it governs ('lorvik decisions link' is the
shorter way). --to-project names M's project when it is not this one; the
server allows that only to or from the global project.

Examples:
  lorvik link 42 blocks 43
  lorvik link 44 relates 42
  lorvik -p global link 7 applies 42 --to-project web`,
		Args: usageArgs(cobra.ExactArgs(3)),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, kind, m, err := linkArgs(args)
			if err != nil {
				return err
			}
			return a.linkRun(cmd, n, kind, m, toProject, true)
		},
	}
	cmd.Flags().StringVar(&toProject, "to-project", "", "Project of the other card, when it is not this one")
	return cmd
}

func newUnlink(a *app) *cobra.Command {
	var toProject string
	cmd := &cobra.Command{
		Use:   "unlink <number> blocks|relates|duplicates|applies <other>",
		Short: "Remove a link between two cards",
		Long: `Remove the link of the given kind from card N to card M. --to-project names
M's project when it is not this one.

Example:
  lorvik unlink 42 blocks 43`,
		Args: usageArgs(cobra.ExactArgs(3)),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, kind, m, err := linkArgs(args)
			if err != nil {
				return err
			}
			return a.linkRun(cmd, n, kind, m, toProject, false)
		},
	}
	cmd.Flags().StringVar(&toProject, "to-project", "", "Project of the other card, when it is not this one")
	return cmd
}

func (a *app) linkRun(cmd *cobra.Command, n int64, kind string, m int64, toProject string, link bool) error {
	c, slug, err := a.api()
	if err != nil {
		return err
	}
	if !link {
		if err := c.UnlinkCards(cmd.Context(), slug, n, kind, m, toProject); err != nil {
			return err
		}
		return a.emit(unlinkedView{Number: n, Kind: kind, To: m, Unlinked: true}, func(p *cmdutil.Printer) { p.Line("unlinked #%d %s #%d", n, kind, m) })
	}
	card, err := c.LinkCards(cmd.Context(), slug, n, apitypes.LinkCards{Kind: kind, To: m, Project: toProject})
	if err != nil {
		return err
	}
	return a.emit(card, func(p *cmdutil.Printer) { p.Line("#%d %s #%d", n, kind, m) })
}
