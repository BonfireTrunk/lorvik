package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

// exitInterrupted is the conventional status of a process stopped by SIGINT.
const exitInterrupted = 130

var errWatchDone = errors.New("watch: event seen")

func newWatch(a *app) *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "watch <number> [--timeout 30m]",
		Short: "Block until the first event on a card, then exit (only when idle)",
		Long: `Block on the server's event stream until something happens on card N (an
answer, a status change, a comment, a claim), print that event as one
sentence and exit 0. It exits after the first event on the card; run it again
to wait for the next. Events already in the log are not replayed: only events
after the command starts count.

This is the one sanctioned way to block. Agents must not poll with repeated
'show' or 'inbox' calls. Use watch only when you hold nothing else to do, for
example after asking a question that blocks your only card.

Exit codes: 0 event seen, 1 timeout ("lorvik: timed out waiting on #N") or a
stream error, 130 interrupted. With --json the event is printed as JSON.

Examples:
  lorvik watch 42
  lorvik watch 42 --timeout 10m`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The wait can last many minutes and the stream says nothing
			// of what the chat holds, so the list would be an old one.
			a.noHolding = true
			n, err := parseNum(args[0])
			if err != nil {
				return err
			}
			if timeout <= 0 {
				return cmdutil.Usagef("--timeout must be positive, e.g. 30m")
			}
			c, slug, err := a.api()
			if err != nil {
				return err
			}
			parent := cmd.Context()
			ctx, cancel := context.WithTimeout(parent, timeout)
			defer cancel()
			var seen *apitypes.Event
			after := ""
			if a.agent != nil {
				// A harness agent's stream ends with its token, so it may
				// reconnect, and needs a position to resume from.
				if after, err = c.EventHead(ctx); err != nil {
					return err
				}
				if after == "0" {
					after = ""
				}
			}
			for {
				err = c.StreamEvents(ctx, slug, after, func(e apitypes.Event) error {
					after = cmp.Or(e.Position, after)
					if e.Subject.Type == "card" && e.Subject.Number == n {
						seen = &e
						return errWatchDone
					}
					return nil
				})
				if err != nil || seen != nil || ctx.Err() != nil {
					break
				}
				// Reopen only a stream that ended with the agent's token.
				old := c.Token()
				if err = a.renewAgent(c); err != nil || c.Token() == old {
					break
				}
			}
			switch {
			case seen != nil:
				return a.emitEvent(ctx, *seen)
			case parent.Err() != nil:
				return &cmdutil.ExitError{Code: exitInterrupted, Err: cmdutil.ErrSilent}
			case ctx.Err() != nil:
				return fmt.Errorf("timed out waiting on #%d", n)
			case err != nil && !errors.Is(err, errWatchDone):
				return err
			}
			return fmt.Errorf("event stream closed while waiting on #%d", n)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "give up after this long (exit 1)")
	return cmd
}

func (a *app) emitEvent(ctx context.Context, e apitypes.Event) error {
	a.resolve(ctx, eventsActors([]apitypes.Event{e})...)
	return a.emit(e, func(p *cmdutil.Printer) { p.Line("%s", eventSentence(e, p.Name)) })
}
