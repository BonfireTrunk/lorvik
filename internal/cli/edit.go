package cli

import (
	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

func newEdit(a *app) *cobra.Command {
	var (
		title, body, priority, assignee, branch, lane, pageFile string
		parent                                                  int64
	)
	cmd := &cobra.Command{
		Use:   "edit <number>",
		Short: "Change a card's title, body, page, priority, parent or assignee",
		Long: `Update only the fields you pass. The current version is fetched first and
sent with the change; on a conflict the server's hint is printed and the exit
code is 4. --parent 0 removes the parent. --assignee takes an actor id, or
user:<id> or agent:<id>; "" clears it. --branch and --lane set the git branch and
the lane (a tag such as schema or web: next skips a card whose lane has a
card in working); "" clears them. --page-file PATH sets the HTML page (- reads
stdin; not together with '-b -'); --page-file "" removes it.

Examples:
  lorvik edit 42 --priority urgent
  lorvik edit 42 -b "New description" --parent 0
  lorvik edit 42 --assignee agent:106666050296328877
  lorvik edit 42 --branch card-42-lanes --lane schema
  lorvik edit 42 --page-file review.html
  lorvik edit 42 --page-file ""`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := parseNum(args[0])
			if err != nil {
				return err
			}
			fl := cmd.Flags()
			in := apitypes.UpdateCard{}
			if err := noStdinClash(body, pageFile); err != nil {
				return err
			}
			if fl.Changed("title") {
				in.Title = &title
			}
			if fl.Changed("body") {
				b, err := a.readBody(body)
				if err != nil {
					return err
				}
				in.Body = &b
			}
			if fl.Changed("page-file") {
				page := ""
				if pageFile != "" {
					if page, err = a.readPageFile(pageFile); err != nil {
						return err
					}
				}
				in.Page = &page
			}
			if fl.Changed("priority") {
				if err := oneOf("priority", priority, priorities...); err != nil {
					return err
				}
				in.Priority = &priority
			}
			if fl.Changed("parent") {
				in.Parent = &parent
			}
			if fl.Changed("branch") {
				in.Branch = &branch
			}
			if fl.Changed("lane") {
				in.Lane = &lane
			}
			if in.Title == nil && in.Body == nil && in.Priority == nil && in.Parent == nil && in.Branch == nil && in.Lane == nil && in.Page == nil && !fl.Changed("assignee") {
				return cmdutil.Usagef("nothing to change: pass --title, --body, --priority, --parent, --assignee, --branch, --lane or --page-file")
			}
			return a.mutateCard(cmd.Context(), n, func(c *client.Client, slug string, cur apitypes.Card) (apitypes.Card, error) {
				if fl.Changed("assignee") {
					act, err := a.assigneeRef(cmd.Context(), c, assignee)
					if err != nil {
						return apitypes.Card{}, err
					}
					in.Assignee = &act
				}
				in.Version = cur.Version
				return c.UpdateCard(cmd.Context(), slug, n, in)
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&title, "title", "", "New title")
	f.StringVarP(&body, "body", "b", "", "New body (- reads stdin)")
	f.StringVar(&pageFile, "page-file", "", "HTML page from a file (- reads stdin); empty removes it")
	f.StringVar(&priority, "priority", "", "low, normal, high or urgent")
	f.Int64Var(&parent, "parent", 0, "Parent card number; 0 removes the parent")
	f.StringVar(&assignee, "assignee", "", "Assignee: actor id, user:<id> or agent:<id>; empty clears")
	f.StringVar(&branch, "branch", "", "Git branch the work is on; empty clears")
	f.StringVar(&lane, "lane", "", "Lane tag; empty clears")
	return cmd
}
