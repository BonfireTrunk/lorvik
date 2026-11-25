package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

func newComment(a *app) *cobra.Command {
	var (
		msg, cursor string
		limit       int
	)
	cmd := &cobra.Command{
		Use:   "comment <number> [-m note]",
		Short: "Leave or read comments on a card",
		Long: `With -m, attach a comment for whoever picks the card up next: what is
done, what is left, gotchas. Without -m, print the comments, newest first,
and the command for older ones (-m - reads the note from stdin).
When the server cannot be reached, a comment with -m waits in the queue, and
'lorvik sync' or the next command sends it.

Examples:
  lorvik comment 42 -m "Parser done; tests for edge cases left. See internal/parse."
  lorvik comment 42`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := parseNum(args[0])
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("message") {
				return a.addComment(cmd.Context(), n, msg)
			}
			c, slug, err := a.api()
			if err != nil {
				return err
			}
			notes, next, err := historyPage(limit, cursor, func(h apitypes.Comment) string { return h.ID },
				func(hp client.HistoryParams) ([]apitypes.Comment, error) {
					return c.ListComments(cmd.Context(), slug, n, hp)
				})
			if err != nil {
				return err
			}
			a.resolve(cmd.Context(), actorsOf(notes, func(h apitypes.Comment) apitypes.Actor { return h.Actor })...)
			return a.emit(notes, func(p *cmdutil.Printer) {
				if len(notes) == 0 {
					p.Line("no comments on #%d", n)
					return
				}
				for _, h := range notes {
					p.Line("%s  %s", p.Who(h.Actor), p.Ago(h.CreatedAt))
					p.Line("%s", indent(h.Body))
				}
				p.Older(next, a.nextCommand(cmd))
			})
		},
	}
	cmd.Flags().StringVarP(&msg, "message", "m", "", "Comment to add (- reads stdin)")
	cmd.Flags().IntVarP(&limit, "limit", "L", defaultLimit, "Maximum notes to print when reading")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Read comments older than this one: the next value printed by a previous page")
	return cmd
}

// addComment reads the note, then sends it; a server that cannot be reached
// gets it later through the outbox.
func (a *app) addComment(ctx context.Context, n int64, msg string) error {
	text, err := a.readBody(msg)
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return cmdutil.Usagef("comment is empty")
	}
	key := newRequestKey()
	e := outboxEntry{ID: key, Command: cmdComment, Card: n, Text: text}
	h, queued, err := attempt(ctx, a, e, func(c *client.Client, slug string) (apitypes.Comment, error) {
		return a.postComment(ctx, c, slug, n, text, key)
	})
	if err != nil || queued {
		return err
	}
	return a.emit(h, func(p *cmdutil.Printer) { p.Line("comment added to #%d", n) })
}

// postComment is the write a comment makes, online and from the outbox.
func (a *app) postComment(ctx context.Context, c *client.Client, slug string, n int64, text, key string) (apitypes.Comment, error) {
	return c.CreateComment(client.WithRequestKey(ctx, key), slug, n, text)
}
