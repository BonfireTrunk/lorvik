package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

const recentEvents = 5

func newShow(a *app) *cobra.Command {
	var page bool
	cmd := &cobra.Command{
		Use:   "show <number>",
		Short: "Show one card in full",
		Long: `Print a card: header line, a meta line, the body, then the sections that
are not empty: Links, Children, Blocked by, Decisions, Comment (latest) and
Recent events (5). Showing a card you hold renews your lease. A card with an
HTML page adds a line 'Page: size, open <dashboard link>' after the meta line.
--page prints the page's HTML source instead (exit 3 when there is none); with
--json it prints the HTMLPage.

On a story the meta line adds progress (children done or dropped over all
children) and, once closed, where it was folded. Decisions lists the story's
decision cards and the decisions applied to it. On a decision the meta line
adds who accepted it and when, and the decisions it supersedes or that
supersede it.

Examples:
  lorvik show 42
  lorvik show 42 --json
  lorvik show 42 --page > card-42.html`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.showCard(cmd, args[0], "", page)
		},
	}
	cmd.Flags().BoolVar(&page, "page", false, "Print the card's HTML page source instead")
	return cmd
}

// showCard prints one card in full. A non-empty kind makes any other kind a
// usage error, which is how `decisions show` stays a view over decisions.
func (a *app) showCard(cmd *cobra.Command, ref, kind string, page bool) error {
	n, err := parseNum(ref)
	if err != nil {
		return err
	}
	c, slug, err := a.api()
	if err != nil {
		return err
	}
	if page {
		return a.showPage(fmt.Sprintf("card #%d", n),
			func() (apitypes.HTMLPage, error) { return c.GetCardPage(cmd.Context(), slug, n) },
			func() error { _, err := c.GetCard(cmd.Context(), slug, n); return err })
	}
	card, err := c.GetCard(cmd.Context(), slug, n)
	if err != nil {
		return err
	}
	if err := wantKind(card, kind); err != nil {
		return err
	}
	card, err = withAllChildren(cmd.Context(), c, slug, card)
	if err != nil {
		return err
	}
	a.resolve(cmd.Context(), cardActors(card)...)
	return a.emit(card, func(p *cmdutil.Printer) { renderCard(p, card, dashboardOrigin(c)) })
}

// withAllChildren completes a card whose response held only its first children.
// The child list is read from the top rather than appended, since it runs in
// board order and the embedded ones do not.
func withAllChildren(ctx context.Context, c *client.Client, slug string, card apitypes.Card) (apitypes.Card, error) {
	if card.ChildrenRemaining == 0 {
		return card, nil
	}
	children, err := allPages(func(pg client.PageParams) ([]apitypes.CardBrief, apitypes.Page, error) {
		list, err := c.ListCards(ctx, slug, apitypes.ListCardsParams{Parent: &card.Number, Limit: pg.Limit, Cursor: pg.Cursor})
		return list.Items, list.Page, err
	})
	if err != nil {
		return card, err
	}
	card.Children, card.ChildrenRemaining = children, 0
	return card, nil
}

// renderCard prints the compact full form shared by show and next. origin is
// where the dashboard is served, for the link to a page.
func renderCard(p *cmdutil.Printer, c apitypes.Card, origin string) {
	p.Card(cmdutil.BriefOf(c))
	meta := []string{"created " + p.Ago(c.CreatedAt) + " by " + p.Author(c.CreatedBy), "updated " + p.Ago(c.UpdatedAt), "version " + itoa(c.Version)}
	if c.Parent != nil {
		meta = append([]string{"parent #" + itoa(*c.Parent)}, meta...)
	}
	if c.Claim != nil {
		lease := "lease " + p.Ago(c.Claim.ExpiresAt)
		if c.Claim.Lease != "" {
			lease += " of " + c.Claim.Lease
		}
		meta = append(meta, lease)
	}
	if c.Branch != "" {
		meta = append(meta, "branch "+c.Branch)
	}
	if c.Lane != "" {
		meta = append(meta, "lane "+c.Lane)
	}
	if c.DropReason != "" {
		meta = append(meta, "dropped: "+c.DropReason)
	}
	if c.ForPerson {
		meta = append(meta, "for a person")
	}
	meta = append(meta, decisionMeta(p, c)...)
	if c.Progress != nil {
		meta = append(meta, fmt.Sprintf("progress %d/%d", c.Progress.Done, c.Progress.Total))
	}
	if f := c.Fold; f != nil {
		meta = append(meta, foldMeta(*f))
	}
	p.Line("%s", strings.Join(meta, "  "))
	if q := c.Question; q != nil {
		if len(q.Choices) > 0 {
			p.Line("choices: %s", strings.Join(q.Choices, " | "))
		}
		if q.Answer != "" {
			by := ""
			switch {
			case c.Relayed && q.AnsweredBy != nil:
				by = " (relayed by " + p.Who(*q.AnsweredBy) + ")"
			case c.Relayed:
				by = " (relayed)"
			case q.AnsweredBy != nil:
				by = " (" + p.Who(*q.AnsweredBy) + ")"
			}
			p.Line("answer: %s%s", q.Answer, by)
		}
	}
	if b := c.Behalf; b != nil {
		state := "not looked at yet"
		if b.KeptAt != nil {
			state = "kept " + p.Ago(*b.KeptAt)
		}
		p.Line("for %s, %s: %s", p.Who(b.For), state, b.Reason)
	}
	if c.PageBytes > 0 {
		pageLine(p, c.PageBytes, cardLink(origin, c.Project, c.Number))
	}
	if c.Body != "" {
		p.Line("")
		p.Line("%s", strings.TrimRight(c.Body, "\n"))
	}
	decisions := storyDecisions(c)
	links := c.Links
	if c.Kind == "story" {
		// Applied decisions are listed under Decisions; repeating them here is noise.
		links = slices.DeleteFunc(slices.Clone(links), isAppliedIn)
	}
	section(p, "Links", len(links), func() {
		for _, l := range links {
			state := l.Status
			if l.Project != "" {
				state += ", " + l.Project
			}
			p.Line("  %s #%d  %s  (%s)", linkLabel(l), l.Number, l.Title, state)
		}
	})
	children := c.Children
	if c.Kind == "story" {
		children = slices.DeleteFunc(slices.Clone(children), func(b apitypes.CardBrief) bool { return b.Kind == "decision" })
	}
	section(p, "Children", len(children), func() {
		for _, ch := range children {
			p.Line("  %s", cmdutil.CardLine(ch, p.Color, p.Name))
		}
	})
	section(p, "Decisions", len(decisions), func() {
		for _, d := range decisions {
			p.Line("  %s", d)
		}
	})
	section(p, "Blocked by", len(c.BlockedBy), func() {
		for _, b := range c.BlockedBy {
			p.Line("  %s", cmdutil.CardLine(b, p.Color, p.Name))
		}
	})
	if h := c.Comment; h != nil {
		p.Line("")
		p.Line("Comment (latest): %s %s", p.Who(h.Actor), p.Ago(h.CreatedAt))
		p.Line("%s", indent(h.Body))
	}
	n := min(len(c.Events), recentEvents)
	section(p, "Recent events", n, func() {
		for _, e := range c.Events[:n] {
			p.Line("  %s", eventLine(p, e))
		}
	})
}

// wantKind is nil when kind is empty or the card is of that kind.
func wantKind(c apitypes.Card, kind string) error {
	if kind == "" || c.Kind == kind {
		return nil
	}
	return cmdutil.Usagef("card #%d is a %s, not a %s; use `lorvik show %d`", c.Number, c.Kind, kind, c.Number)
}

// decisionMeta is the meta-line facts of a decision card: acceptance and
// supersession.
func decisionMeta(p *cmdutil.Printer, c apitypes.Card) []string {
	d := c.Decision
	if d == nil {
		return nil
	}
	var out []string
	if d.AcceptedBy != nil {
		s := "accepted by " + p.Author(*d.AcceptedBy)
		if d.AcceptedAt != nil {
			s += " " + p.Ago(*d.AcceptedAt)
		}
		out = append(out, s)
	}
	if d.Supersedes != nil {
		out = append(out, "supersedes #"+itoa(*d.Supersedes))
	}
	if d.SupersededBy != nil {
		out = append(out, "superseded by #"+itoa(*d.SupersededBy))
	}
	return out
}

func foldMeta(f apitypes.CardFold) string {
	if f.Into == "" {
		return "folded, no contract change"
	}
	return "folded into " + f.Into
}

func isAppliedIn(l apitypes.CardLink) bool { return l.Kind == "applies" && l.Direction == "in" }

// storyDecisions renders the decisions of a story: its decision children and
// the decisions applied to it, each as `#N  status  D title`, with the
// project in parentheses for one in another project.
func storyDecisions(c apitypes.Card) []string {
	if c.Kind != "story" {
		return nil
	}
	var out []string
	for _, ch := range c.Children {
		if ch.Kind == "decision" {
			out = append(out, fmt.Sprintf("#%d  %s  D %s", ch.Number, ch.Status, ch.Title))
		}
	}
	for _, l := range c.Links {
		if !isAppliedIn(l) {
			continue
		}
		s := fmt.Sprintf("#%d  %s  D %s", l.Number, l.Status, l.Title)
		if l.Project != "" {
			s += "  (" + l.Project + ")"
		}
		out = append(out, s)
	}
	return out
}

func section(p *cmdutil.Printer, title string, n int, body func()) {
	if n == 0 {
		return
	}
	p.Line("")
	p.Line("%s:", title)
	body()
}

func linkLabel(l apitypes.CardLink) string {
	if l.Direction != "in" {
		return l.Kind
	}
	switch l.Kind {
	case "blocks":
		return "blocked by"
	case "duplicates":
		return "duplicated by"
	case "supersedes":
		return "superseded by"
	case "applies":
		return "applied by"
	}
	return l.Kind
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}
