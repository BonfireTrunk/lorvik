package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

func newWho(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "who",
		Short: "List who is working on what, across the org",
		Long: `List every live claim of the org on cards you can see, grouped by holder and
session: a header line per holder and session (name, agent or person,
session), then one line per card with its number, project, title, time since
it was claimed and the lease left. A summary line gives how many chats
(sessions), agents, people and projects. Org-wide: the global -p does not
narrow it.

Example:
  lorvik who`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			claims, err := listLiveClaims(cmd.Context(), c)
			if err != nil {
				return err
			}
			a.resolve(cmd.Context(), actorsOf(claims, func(l apitypes.LiveClaim) apitypes.Actor { return l.Holder })...)
			return a.emit(apitypes.LiveClaimList{Items: claims}, func(p *cmdutil.Printer) { printWho(p, claims) })
		},
	}
}

// listLiveClaims reads every live claim, for a count that must not miss one.
func listLiveClaims(ctx context.Context, c *client.Client) ([]apitypes.LiveClaim, error) {
	claims, err := allPages(func(pg client.PageParams) ([]apitypes.LiveClaim, apitypes.Page, error) {
		list, err := c.ListLiveClaims(ctx, pg)
		return list.Items, list.Page, err
	})
	if claims == nil && err == nil {
		claims = []apitypes.LiveClaim{}
	}
	return claims, err
}

// whoGroup is the run of claims one holder has in one session. The server
// lists claims by holder, session, card, so a group is contiguous.
type whoGroup struct {
	holder      apitypes.Actor
	session     string
	sessionName string
	claims      []apitypes.LiveClaim
}

func groupClaims(claims []apitypes.LiveClaim) []whoGroup {
	var groups []whoGroup
	for _, l := range claims {
		if n := len(groups); n > 0 && groups[n-1].holder == l.Holder && groups[n-1].session == l.Session {
			groups[n-1].claims = append(groups[n-1].claims, l)
			continue
		}
		groups = append(groups, whoGroup{holder: l.Holder, session: l.Session, sessionName: l.SessionName, claims: []apitypes.LiveClaim{l}})
	}
	return groups
}

func printWho(p *cmdutil.Printer, claims []apitypes.LiveClaim) {
	if len(claims) == 0 {
		p.Line("nobody is working on anything")
		return
	}
	groups := groupClaims(claims)
	for _, g := range groups {
		kind := "person"
		if g.holder.Kind == "agent" {
			kind = "agent"
		}
		head := fmt.Sprintf("%s  %s", p.Named(g.holder), kind)
		if g.session != "" {
			head += "  session " + cmdutil.ChatLabel(g.session, g.sessionName)
		}
		p.Line("%s", head)
		for _, l := range g.claims {
			p.Line("  #%d  %s  %s  claimed %s  %s", l.Card.Number, l.Card.Project, l.Card.Title, p.Ago(l.ClaimedAt), leaseLeft(p, l.ExpiresAt))
		}
	}
	p.Line("%s", whoSummary(claims))
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// whoSummary is the dashboard's headline: chats (a holder's sessions), then
// the agents and people holding them, then the projects the cards are in.
func whoSummary(claims []apitypes.LiveClaim) string {
	holders := map[apitypes.Actor]bool{}
	projects := map[string]bool{}
	for _, l := range claims {
		holders[l.Holder] = true
		projects[l.Card.Project] = true
	}
	agents := 0
	for h := range holders {
		if h.Kind == "agent" {
			agents++
		}
	}
	var who []string
	if agents > 0 {
		who = append(who, plural(agents, "agent"))
	}
	if people := len(holders) - agents; people > 0 {
		if people == 1 {
			who = append(who, "1 person")
		} else {
			who = append(who, fmt.Sprintf("%d people", people))
		}
	}
	return fmt.Sprintf("%s of %s working in %s", plural(len(groupClaims(claims)), "chat"), strings.Join(who, " and "), plural(len(projects), "project"))
}
