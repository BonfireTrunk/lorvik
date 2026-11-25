package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

func newEscalate(a *app) *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "escalate <number> -m <why>",
		Short: "Hand a question to a person",
		Long: `Mark an open question as one only a person answers. From then on no agent
answers it (relaying what a person said stays allowed) and it leads that
person's list. For the chat that asked it, and for an agent holding the answer
right that will not answer it. -m says why a person must (- reads stdin) and
is posted as a comment. Only a person takes the mark off, on the dashboard.

Example:
  lorvik escalate 17 -m "about spending, and no decision covers it"`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := parseNum(args[0])
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("message") {
				return cmdutil.Usagef("escalate needs -m: say why a person must answer this")
			}
			if note, err = a.readBody(note); err != nil {
				return err
			}
			if strings.TrimSpace(note) == "" {
				return cmdutil.Usagef("escalate needs a non-empty -m: say why a person must answer this")
			}
			return a.mutateCard(cmd.Context(), n, func(c *client.Client, slug string, cur apitypes.Card) (apitypes.Card, error) {
				return c.MarkForPerson(cmd.Context(), slug, n, apitypes.MarkForPerson{Version: cur.Version, Note: note})
			})
		},
	}
	cmd.Flags().StringVarP(&note, "message", "m", "", "Why a person must answer (- reads stdin)")
	return cmd
}
