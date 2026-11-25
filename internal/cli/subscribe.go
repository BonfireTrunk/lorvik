package cli

import (
	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

// subscribeResult is the --json shape of subscribe and unsubscribe. Title is
// set by subscribe only.
type subscribeResult struct {
	Project    string `json:"project"`
	Number     int64  `json:"number"`
	Title      string `json:"title,omitempty"`
	Subscribed bool   `json:"subscribed"`
}

func newSubscribe(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "subscribe <number>",
		Short: "Wake this chat when something happens on a card",
		Long: `Subscribe this chat to card N: an event on it, such as a person's comment or
answer, wakes the chat's listener (lorvik inbox --follow) even though the chat
does not hold the card. Use it when you leave a card waiting on a person.
Run 'lorvik unsubscribe N' when you no longer wait.

Only an agent's chat can subscribe, to at most 50 open cards at once; a
person sees replies on the dashboard. A subscription is dropped once this
chat has been gone for a day, and when the card is done or dropped. 'lorvik
status' lists the cards this chat is subscribed to. (Not 'lorvik watch', which
blocks until the next event on a card.)

Example:
  lorvik subscribe 42`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := parseNum(args[0])
			if err != nil {
				return err
			}
			c, slug, err := a.api()
			if err != nil {
				return err
			}
			card, err := c.GetCard(cmd.Context(), slug, n)
			if err != nil {
				return err
			}
			if err := c.SubscribeCard(cmd.Context(), slug, n); err != nil {
				return err
			}
			return a.emit(subscribeResult{Project: slug, Number: n, Title: card.Title, Subscribed: true}, func(p *cmdutil.Printer) {
				p.Line("subscribed to #%d %s", n, card.Title)
			})
		},
	}
}

func newUnsubscribe(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "unsubscribe <number>",
		Short: "Stop waking this chat for a card",
		Long: `Unsubscribe this chat from card N, which 'lorvik subscribe N' started. Safe
to run when the chat was not subscribed.

Example:
  lorvik unsubscribe 42`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := parseNum(args[0])
			if err != nil {
				return err
			}
			c, slug, err := a.api()
			if err != nil {
				return err
			}
			if err := c.UnsubscribeCard(cmd.Context(), slug, n); err != nil {
				return err
			}
			return a.emit(subscribeResult{Project: slug, Number: n}, func(p *cmdutil.Printer) {
				p.Line("unsubscribed from #%d", n)
			})
		},
	}
}
