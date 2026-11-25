package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

func newHistory(a *app) *cobra.Command {
	var (
		version int64
		page    bool
		limit   int
		cursor  string
	)
	cmd := &cobra.Command{
		Use:   "history <number> [--version v]",
		Short: "List the past versions of a card's title and body",
		Long: `List a card's earlier title and body texts, newest first, as: vN  @editor  time
title. Every edit of the title or body keeps the text it replaced. Bodies are
not listed; with --version N print that past version: a header line, the title
and the body, plus a 'Page:' line when that version had an HTML page. --page
with --version N prints that version's page HTML (exit 3 when none). A full
page ends with the command for older versions.

Examples:
  lorvik history 42
  lorvik history 42 --version 2
  lorvik history 42 --version 2 --page > old.html`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := parseNum(args[0])
			if err != nil {
				return err
			}
			if page && !cmd.Flags().Changed("version") {
				return cmdutil.Usagef("--page needs --version")
			}
			c, slug, err := a.api()
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("version") {
				rev, err := c.GetCardRevision(cmd.Context(), slug, n, version)
				if err != nil {
					return err
				}
				if page {
					return a.showRevisionPage(fmt.Sprintf("card #%d version %d", n, version), rev, rev.Page)
				}
				a.resolve(cmd.Context(), rev.UpdatedBy)
				return a.emit(rev, func(p *cmdutil.Printer) {
					p.Line("#%d  v%d  %s  %s", n, rev.Version, p.Who(rev.UpdatedBy), p.Ago(rev.CreatedAt))
					if rev.Page != "" {
						revisionPageLine(p, rev.Page, fmt.Sprintf("lorvik history %d --version %d --page", n, rev.Version))
					}
					p.Line("%s", rev.Title)
					if rev.Body != "" {
						p.Line("")
						p.Line("%s", strings.TrimRight(rev.Body, "\n"))
					}
				})
			}
			revs, next, err := historyPage(limit, cursor, func(r apitypes.CardRevision) string { return itoa(r.Version) },
				func(hp client.HistoryParams) ([]apitypes.CardRevision, error) {
					return c.ListCardRevisions(cmd.Context(), slug, n, hp)
				})
			if err != nil {
				return err
			}
			a.resolve(cmd.Context(), actorsOf(revs, func(r apitypes.CardRevision) apitypes.Actor { return r.UpdatedBy })...)
			return a.emit(revs, func(p *cmdutil.Printer) {
				if len(revs) == 0 {
					p.Line("no history")
				}
				for _, r := range revs {
					p.Line("v%d  %s  %s  %s", r.Version, p.Who(r.UpdatedBy), p.Ago(r.CreatedAt), r.Title)
				}
				p.Older(next, a.nextCommand(cmd))
			})
		},
	}
	cmd.Flags().Int64Var(&version, "version", 0, "Show this past version")
	cmd.Flags().BoolVar(&page, "page", false, "With --version, print that version's HTML page source instead")
	cmd.Flags().IntVarP(&limit, "limit", "L", defaultLimit, "Maximum versions to list")
	cmd.Flags().StringVar(&cursor, "cursor", "", "List versions older than this one: the next value printed by a previous page")
	return cmd
}
