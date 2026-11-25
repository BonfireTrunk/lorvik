package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

// statusView is the --json shape of `status`: the project plus my cards.
// Mine holds the cards whose claim is the caller's own and those assigned to
// me but not claimed; MineOtherSessions holds those whose claim is not: for
// an agent another chat's, for a human never their own.
type statusView struct {
	Project           apitypes.Project        `json:"project"`
	Session           string                  `json:"session"`
	SessionSource     string                  `json:"session_source"`
	Mine              []apitypes.CardBrief    `json:"mine"`
	MineOtherSessions []apitypes.CardBrief    `json:"mine_other_sessions"`
	Sessions          []sessionHolding        `json:"sessions"`
	Locks             []apitypes.Lock         `json:"locks"`
	Subscribed        []apitypes.Subscription `json:"subscribed"`
}

// sessionHolding is one session label and how many working cards it holds.
type sessionHolding struct {
	Label string `json:"label"`
	Cards int    `json:"cards"`
}

// allWorkingCards reads every page, since a session's holding count is wrong if
// any of its cards are left out.
func allWorkingCards(ctx context.Context, c *client.Client, slug string) ([]apitypes.CardBrief, error) {
	return allPages(func(pg client.PageParams) ([]apitypes.CardBrief, apitypes.Page, error) {
		list, err := c.ListCards(ctx, slug, apitypes.ListCardsParams{Status: []string{"working"}, Limit: pg.Limit, Cursor: pg.Cursor})
		return list.Items, list.Page, err
	})
}

// holdingSessions counts working cards per claim session, most cards first,
// ties by label. Claims without a session are not counted. Never returns nil.
func holdingSessions(cards []apitypes.CardBrief) []sessionHolding {
	n := map[string]int{}
	for _, c := range cards {
		if c.Claim != nil && c.Claim.Session != "" {
			n[c.Claim.Session]++
		}
	}
	out := make([]sessionHolding, 0, len(n))
	for l, c := range n {
		out = append(out, sessionHolding{Label: l, Cards: c})
	}
	slices.SortFunc(out, func(x, y sessionHolding) int {
		return cmp.Or(cmp.Compare(y.Cards, x.Cards), cmp.Compare(x.Label, y.Label))
	})
	return out
}

// splitBySession separates cards whose claim the caller does not own from the
// rest. The server decides ownership (Claim.Owned), so a human's claim counts
// in every session and an agent's only in the session that took it. Never
// returns nil slices, so --json prints [] rather than null.
func splitBySession(cards []apitypes.CardBrief) (this, other []apitypes.CardBrief) {
	this, other = []apitypes.CardBrief{}, []apitypes.CardBrief{}
	for _, c := range cards {
		if c.Claim != nil && !c.Claim.Owned {
			other = append(other, c)
		} else {
			this = append(this, c)
		}
	}
	return this, other
}

func newStatus(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Summarise the project: counts, open questions, my cards",
		Long: `Print the project name, this session's label and where it came
from, card counts per status, the number of open
questions and the open cards I hold or am assigned, split into this
session's and other sessions', then every session holding a working card
with its count, then the cards this chat is subscribed to (lorvik subscribe), then the
live locks of the org. Read-only: it does not touch
your inbox cursor. A person's own claims count as this session's wherever
they were taken; an agent's only in the chat that took them. The line of a
claimed card ends with the time left on its lease.

Example:
  lorvik status`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, slug, err := a.api()
			if err != nil {
				return err
			}
			proj, err := c.GetProject(cmd.Context(), slug)
			if err != nil {
				return err
			}
			mine, err := c.ListCards(cmd.Context(), slug, apitypes.ListCardsParams{Mine: true, Open: true, Limit: defaultLimit})
			if err != nil {
				return err
			}
			working, err := allWorkingCards(cmd.Context(), c, slug)
			if err != nil {
				return err
			}
			locks, err := listLocks(cmd.Context(), c)
			if err != nil {
				return err
			}
			subscribed, err := listSubscribed(cmd.Context(), c)
			if err != nil {
				return err
			}
			a.resolve(cmd.Context(), append(briefsActors(mine.Items), lockActors(locks)...)...)
			si := a.sessionInfo()
			sess := si.Label
			this, other := splitBySession(mine.Items)
			v := statusView{Project: proj, Session: sess, SessionSource: si.Source, Mine: this, MineOtherSessions: other, Sessions: holdingSessions(working), Locks: locks, Subscribed: subscribed}
			return a.emit(v, func(p *cmdutil.Printer) {
				p.Line("%s  %s", proj.Slug, proj.Name)
				if sess == "" {
					p.Line("session none (%s is not a valid label)", si.Source)
				} else {
					p.Line("session %s (%s)", sess, si.Source)
				}
				p.Line("%s", projectMeta(proj))
				if g := proj.ChatGrant; g != nil {
					for _, l := range chatGrantLines(p, *g) {
						p.Line("%s", l)
					}
				}
				if n := proj.Counts; n != nil {
					p.Line("backlog %d  ready %d  working %d  waiting %d  done %d  dropped %d",
						n.Backlog, n.Ready, n.Working, n.Waiting, n.Done, n.Dropped)
					p.Line("open questions: %d", n.OpenQuestions)
				}
				if len(this) == 0 {
					p.Line("my cards (this session): none")
				} else {
					p.Line("my cards (this session):")
					for _, it := range this {
						p.Line("  %s", claimLine(p, it))
					}
				}
				if len(other) > 0 {
					p.Line("my cards (other sessions):")
					for _, it := range other {
						p.Line("  %s", claimLine(p, it))
					}
				}
				if mine.Remaining > 0 {
					p.Line("  more: lorvik ls --mine --open")
				}
				if len(v.Sessions) == 0 {
					p.Line("sessions holding cards: none")
				} else {
					parts := make([]string, len(v.Sessions))
					for i, h := range v.Sessions {
						parts[i] = fmt.Sprintf("%s (%d)", h.Label, h.Cards)
					}
					p.Line("sessions holding cards: %s", strings.Join(parts, ", "))
				}
				printSubscribed(p, subscribed)
				printLocks(p, locks)
			})
		},
	}
}

// listSubscribed reads the cards this chat is subscribed to. A caller that
// cannot subscribe to any, a person or a request without a chat label, gets none: the server
// refuses it with 422. So does a server that predates subscriptions (404).
func listSubscribed(ctx context.Context, c *client.Client) ([]apitypes.Subscription, error) {
	list, err := c.ListSubscriptions(ctx)
	if ae, ok := errors.AsType[*client.APIError](err); ok && (ae.Status == http.StatusUnprocessableEntity || ae.Status == http.StatusNotFound) {
		return []apitypes.Subscription{}, nil
	}
	if err != nil {
		return nil, err
	}
	if list.Items == nil {
		return []apitypes.Subscription{}, nil
	}
	return list.Items, nil
}

// printSubscribed lists the cards this chat is subscribed to, when it has any.
func printSubscribed(p *cmdutil.Printer, subscribed []apitypes.Subscription) {
	if len(subscribed) == 0 {
		return
	}
	p.Line("subscribed (this chat):")
	for _, w := range subscribed {
		p.Line("  #%d  %s  %s  since %s", w.Number, w.Project, w.Title, p.Ago(w.SubscribedAt))
	}
}

// chatGrantLines say what the owner gave this chat in the project: the
// rights, when they end, and the owner's instructions under them.
func chatGrantLines(p *cmdutil.Printer, g apitypes.SessionGrant) []string {
	line := "this chat was given " + strings.Join(g.Rights, ", ")
	if g.EndsAt != nil {
		line += ", ending " + cmdutil.RelTime(p.Now(), *g.EndsAt)
	}
	if g.Instructions == "" {
		return []string{line}
	}
	return []string{line, "instructions:", indent(g.Instructions)}
}
