package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func newSearch(a *app) *cobra.Command {
	var (
		limit  int
		all    bool
		cursor string
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search cards, decisions and notes",
		Long: `Full-text search across everything the token can see. Each hit is
"type ref  status  title  (project)" with the matching excerpt indented underneath,
where ref is #42 for a card and the slug for a note. Questions, stories and
decisions have a "? ", "S " or "D " marker before the title. Status is the card
status (notes have none); a decision is in force when it is done. Searches the
current project; --project names another one you can read (global included),
and --all searches every project you can read. With no project bound, every
project is searched. Prefer this to 'show' when you only need to know whether
something exists.

Examples:
  lorvik search "session expiry"
  lorvik search pgx -L 5 --project global
  lorvik search pgx --all`,
		Args: usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			project := ""
			if !all {
				project = a.optSlug()
			}
			res, err := c.Search(cmd.Context(), strings.Join(args, " "), project, cursor, limit)
			if err != nil {
				return err
			}
			return a.emit(res, func(p *cmdutil.Printer) {
				if len(res.Items) == 0 {
					p.Line("no matches")
					return
				}
				for _, h := range res.Items {
					p.Line("%s", hitLine(h))
					if h.Snippet != "" {
						p.Line("  %s", strings.Join(strings.Fields(h.Snippet), " "))
					}
				}
				p.Paging(res.Page, a.nextCommand(cmd))
			})
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "L", defaultLimit, "Maximum hits to print")
	cmd.Flags().BoolVar(&all, "all", false, "Every project you can read, not just the current one")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Continue from the next value printed by a previous page")
	return cmd
}

// hitLine renders `card #42  ready  Title  (project)`; notes have no status
// column.
func hitLine(h apitypes.SearchHit) string {
	ref := h.Slug
	if h.Type == "card" {
		ref = fmt.Sprintf("#%d", h.Number)
	}
	scope := "org"
	if h.Project != nil {
		scope = *h.Project
	}
	title := cmdutil.KindMarker(h.Kind) + h.Title
	if h.Status == "" {
		return fmt.Sprintf("%s %s  %s  (%s)", h.Type, ref, title, scope)
	}
	return fmt.Sprintf("%s %s  %s  %s  (%s)", h.Type, ref, h.Status, title, scope)
}
