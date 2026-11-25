package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

func newNext(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "next",
		Short: "Show the best ready card nobody has claimed",
		Long: `Print the one ready, unclaimed, unblocked card with the best priority, in the
same compact form as 'show'. It does not claim the card; run 'lorvik claim N'
or 'lorvik start N' next. Cards assigned to someone else are skipped, and so are
cards whose lane has a card in working; when those are the only ones left,
stderr says how many wait on which lanes. Exits 3 with "no ready cards" when
nothing is available.

Example:
  lorvik next && lorvik start 42`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, slug, err := a.api()
			if err != nil {
				return err
			}
			card, err := c.NextCard(cmd.Context(), slug)
			if isNotFound(err) {
				if ae, ok := errors.AsType[*client.APIError](err); ok && ae.WaitingCards > 0 {
					fmt.Fprintf(a.errOut(), "lorvik: %d %s waiting on busy lanes: %s\n", ae.WaitingCards,
						pluralCards(ae.WaitingCards), strings.Join(ae.BusyLanes, ", "))
				}
				if !a.asJSON {
					fmt.Fprintln(a.out(), "no ready cards")
					return &cmdutil.ExitError{Code: cmdutil.ExitNotFound, Err: cmdutil.ErrSilent}
				}
				return &cmdutil.ExitError{Code: cmdutil.ExitNotFound, Err: fmt.Errorf("no ready cards")}
			}
			if err != nil {
				return err
			}
			a.resolve(cmd.Context(), cardActors(card)...)
			return a.emit(card, func(p *cmdutil.Printer) { renderCard(p, card, dashboardOrigin(c)) })
		},
	}
}

func pluralCards(n int) string {
	if n == 1 {
		return "card"
	}
	return "cards"
}
