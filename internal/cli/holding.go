package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

// held is what the server last said this chat holds: nil when it said
// nothing, or when the command's output has no place for the list.
func (a *app) held() *apitypes.Holding {
	if a.cl == nil || a.noHolding {
		return nil
	}
	return a.cl.LastUpdates().Holding
}

// payload writes output that is one value a script captures or redirects to
// a file, such as a page's source or a secret shown once: nothing follows it.
func (a *app) payload(s string) error {
	a.noHolding = true
	_, err := io.WriteString(a.out(), s)
	return err
}

// remind lists the cards the chat holds after a successful command's own
// output, on stdout: a chat that works several cards forgets one, and a
// forgotten card stays claimed for as long as the chat makes requests. Under
// --json the list is the holding field emit adds instead. A command marked
// annotationNoNotice reports for itself (inbox prints its claims) or prints
// a payload (session, lock run) and is left alone, as is one that printed
// through payload.
func (a *app) remind(cmd *cobra.Command) {
	if a.asJSON || cmd.Annotations[annotationNoNotice] != "" {
		return
	}
	if h := a.held(); h != nil {
		printHolding(a.printer(), *h)
	}
}

// printHolding prints one line per held card, each complete on its own and
// starting with the same word: a listener's harness hands every line to the
// chat as a separate piece of text, and a script can drop them all with one
// filter. A chat that holds nothing gets no line.
func printHolding(p *cmdutil.Printer, h apitypes.Holding) {
	for _, c := range h.Cards {
		p.Line("holding  #%d  %s  %s  %s", c.Number, c.Project, c.Title, leaseLeft(p, c.ExpiresAt))
	}
	if h.More > 0 {
		p.Line("holding  %d more", h.More)
	}
}

// printHeld ends a listener's batch of wake events with what the chat holds:
// the lines of printHolding, or under --json one line {"holding": ...} after
// the events. Nothing is printed for a chat that holds nothing.
func (a *app) printHeld(h apitypes.Holding) error {
	if len(h.Cards)+h.More == 0 {
		return nil
	}
	if !a.asJSON {
		printHolding(a.printer(), h)
		return nil
	}
	enc := json.NewEncoder(a.out())
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]apitypes.Holding{"holding": h}); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}

// claimLine is a card line with the time left on its claim. A lapsed claim
// stays on its card until the sweeper settles it.
func claimLine(p *cmdutil.Printer, c apitypes.CardBrief) string {
	line := cmdutil.CardLine(c, p.Color, p.Name)
	switch {
	case c.Claim == nil:
	case c.Claim.ExpiresAt.After(p.Now()):
		line += "  " + leaseLeft(p, c.Claim.ExpiresAt)
	default:
		line += "  lease ran out"
	}
	return line
}
