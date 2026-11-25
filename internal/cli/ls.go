package cli

import (
	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func newLs(a *app) *cobra.Command {
	var (
		p      apitypes.ListCardsParams
		parent int64
		story  int64
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List cards, one line each",
		Long: `List cards in the project in board order, capped at --limit (default 10).
Each line is: #number  status  priority  title  @claimant. Question cards have
a "? " title prefix, stories "S " and decisions "D ". --story N lists a story's
children, the same as --parent N. When more match, the last line says how many and gives
the command for the next page:

  remaining 37, next: lorvik ls --cursor <token>

Times are YYYY-MM-DD (start of that day, machine time zone), a span back from
now such as 7d, 24h or 30m, or an RFC 3339 time. "after" includes the instant,
"before" excludes it, so --created-before 2026-10-20 means before the start of
that day. A relative span is fixed to an absolute time in the paging line.
--closed-* match done and dropped cards only.

--assignee takes an actor id, me or none; it matches cards assigned to or
claimed by that actor. Without --sort the order is the board: status, then
priority, then number. --sort created|updated|priority|number is ascending;
add --desc to reverse it (--sort priority --desc puts urgent first). A cursor
only continues the sort it came from.

Examples:
  lorvik ls --open --mine
  lorvik ls --status ready,waiting -L 25
  lorvik ls -q "login" --kind question
  lorvik ls --story 20
  lorvik ls --kind decision --status ready
  lorvik ls --status backlog --created-after 7d
  lorvik ls --status ready --assignee none --priority high,urgent
  lorvik ls --updated-after 24h --sort updated --desc
  lorvik ls --closed-after 2026-10-20 --closed-before 2026-10-21
  lorvik ls --cursor <token>`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := oneOf("kind", p.Kind, "task", "question", "story", "decision"); err != nil {
				return err
			}
			if err := oneOf("sort", p.Sort, "created", "updated", "priority", "number"); err != nil {
				return err
			}
			if p.Desc && p.Sort == "" {
				return cmdutil.Usagef("--desc needs --sort created, updated, priority or number")
			}
			if p.Sort != "" && p.Query != "" {
				return cmdutil.Usagef("--sort cannot be combined with -q: matches are ranked by relevance")
			}
			for _, pr := range p.Priority {
				if err := oneOf("priority", pr, priorities...); err != nil {
					return err
				}
			}
			if err := a.resolveTimes(cmd, "created-after", "created-before", "updated-after", "updated-before",
				"closed-after", "closed-before"); err != nil {
				return err
			}
			fl := cmd.Flags()
			if fl.Changed("parent") && fl.Changed("story") && parent != story {
				return cmdutil.Usagef("--parent and --story name the same thing; pass one")
			}
			switch {
			case fl.Changed("story"):
				p.Parent = &story
			case fl.Changed("parent"):
				p.Parent = &parent
			}
			c, slug, err := a.api()
			if err != nil {
				return err
			}
			list, err := c.ListCards(cmd.Context(), slug, p)
			if err != nil {
				return err
			}
			return a.emitCardList(cmd, list, "no cards", nil)
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&p.Status, "status", nil, "Only these statuses, comma separated (backlog,ready,working,waiting,done,dropped)")
	f.StringVar(&p.Kind, "kind", "", "Only cards of this kind: task, question, story or decision")
	f.Int64Var(&parent, "parent", 0, "Only children of this card number")
	f.Int64Var(&story, "story", 0, "Only children of this story (same as --parent)")
	f.BoolVar(&p.Mine, "mine", false, "Only cards claimed by or assigned to me")
	f.BoolVar(&p.Open, "open", false, "Exclude done and dropped cards")
	f.BoolVar(&p.Relayed, "relayed", false, "Only questions whose answer an agent relayed")
	f.StringVar(&p.AnsweredFor, "answered-for", "", "Only cards an agent settled for this user id or me (an answer or a verdict), not kept yet")
	f.StringVarP(&p.Query, "query", "q", "", "Full-text match on title and body")
	f.IntVarP(&p.Limit, "limit", "L", defaultLimit, "Maximum cards to print")
	timeFlags(f, "created", &p.CreatedAfter, &p.CreatedBefore)
	timeFlags(f, "updated", &p.UpdatedAfter, &p.UpdatedBefore)
	timeFlags(f, "closed", &p.ClosedAfter, &p.ClosedBefore)
	f.StringVar(&p.Assignee, "assignee", "", "Only cards assigned to or claimed by this actor id, me, or none")
	f.StringSliceVar(&p.Priority, "priority", nil, "Only these priorities, comma separated (low,normal,high,urgent)")
	f.StringVar(&p.Sort, "sort", "", "Order by created, updated, priority or number instead of board order")
	f.BoolVar(&p.Desc, "desc", false, "Reverse the --sort order")
	f.StringVar(&p.Cursor, "cursor", "", "Continue from the next value printed by a previous page")
	return cmd
}
