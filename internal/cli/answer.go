package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

func newAnswer(a *app) *cobra.Command {
	var decide, relayed bool
	var note string
	cmd := &cobra.Command{
		Use:   "answer <number> <text>",
		Short: "Answer a question card",
		Long: `Record the answer to a question card. Humans answer from the dashboard; this
is for scripts and for humans at a terminal. With --decide the server also
records the answer as an accepted decision card, linked relates to the
question, and the new card's number is printed on stderr.

An agent with the answer right answers in its own name and must give -m: what
the answer rests on (a decision, a requirements section, a person's words on a
card; - reads stdin). The answer takes effect at once and is recorded for the
agent's owner, who keeps or changes it on the dashboard.

Any other agent answers only with --relayed, to pass on an answer the human
actually gave in chat or elsewhere, never one it chose. -m then says where and
when they gave it. The question closes with the answer; the human can change
it on the dashboard. A decision needs a human, so an agent cannot use --decide.

Examples:
  lorvik answer 17 "postgres"
  lorvik answer 17 "postgres" --decide
  lorvik answer 17 "postgres" -m "decision #7 names postgres"
  lorvik answer 17 "postgres" --relayed -m "Ana said so in chat, 14:05"`,
		Args: usageArgs(cobra.MinimumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := parseNum(args[0])
			if err != nil {
				return err
			}
			text := strings.Join(args[1:], " ")
			if relayed && decide {
				return cmdutil.Usagef("--relayed and --decide cannot be combined: a decision needs a human")
			}
			if relayed && !cmd.Flags().Changed("message") {
				return cmdutil.Usagef("--relayed needs -m: say where and when the human gave the answer")
			}
			if cmd.Flags().Changed("message") {
				if note, err = a.readBody(note); err != nil {
					return err
				}
				switch {
				case strings.TrimSpace(note) != "":
				case relayed:
					return cmdutil.Usagef("--relayed needs a non-empty -m: say where and when the human gave the answer")
				default:
					return cmdutil.Usagef("-m needs text: say what the answer rests on")
				}
			}
			return a.mutateCard(cmd.Context(), n, func(c *client.Client, slug string, cur apitypes.Card) (apitypes.Card, error) {
				res, err := c.AnswerCard(cmd.Context(), slug, n, apitypes.AnswerCard{Version: cur.Version, Answer: text, Decide: decide, Relayed: relayed, Note: note})
				if err == nil && res.DecisionLocation != "" {
					fmt.Fprintf(a.errOut(), "lorvik: decision recorded as %s\n", locationCard(res.DecisionLocation))
				}
				return res.Card, err
			})
		},
	}
	cmd.Flags().BoolVar(&decide, "decide", false, "Also record the answer as an accepted decision card")
	cmd.Flags().BoolVar(&relayed, "relayed", false, "Agents: pass on an answer the human gave elsewhere; needs -m")
	cmd.Flags().StringVarP(&note, "message", "m", "", "Agents: what the answer rests on, or with --relayed where and when the human gave it (- reads stdin)")
	return cmd
}

// locationCard reads the card number off a card Location such as
// /api/v1/projects/web/cards/8 and renders it as #8; anything else is
// returned as sent.
func locationCard(loc string) string {
	if _, last, ok := strings.CutLast(loc, "/cards/"); ok {
		if n, err := strconv.ParseInt(last, 10, 64); err == nil {
			return "#" + itoa(n)
		}
	}
	return loc
}
