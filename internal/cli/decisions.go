package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

// decisionBody is the text of a decision card, kept as three markdown
// sections so a decision reads the same wherever it was written.
type decisionBody struct {
	Context, Decision, Consequences string
}

var decisionHeadings = []string{"Context", "Decision", "Consequences"}

func (d decisionBody) parts() [3]string { return [3]string{d.Context, d.Decision, d.Consequences} }

// render writes the non-empty sections as `## Heading`, a blank line, the
// text, in the fixed order Context, Decision, Consequences.
func (d decisionBody) render() string {
	var out []string
	for i, text := range d.parts() {
		if text = strings.TrimSpace(text); text != "" {
			out = append(out, "## "+decisionHeadings[i]+"\n\n"+text)
		}
	}
	return strings.Join(out, "\n\n")
}

// parseDecisionBody splits a body into its sections. ok is false when the
// body holds anything else (text before the first heading, another heading),
// so an edit never silently drops text it cannot place.
func parseDecisionBody(body string) (d decisionBody, ok bool) {
	var cur *string
	var buf []string
	flush := func() {
		if cur != nil {
			*cur = strings.TrimSpace(strings.Join(buf, "\n"))
		}
		buf = nil
	}
	for line := range strings.SplitSeq(body, "\n") {
		if h, found := strings.CutPrefix(strings.TrimRight(line, " \t\r"), "## "); found {
			flush()
			switch h {
			case "Context":
				cur = &d.Context
			case "Decision":
				cur = &d.Decision
			case "Consequences":
				cur = &d.Consequences
			default:
				return decisionBody{}, false
			}
			continue
		}
		if cur == nil && strings.TrimSpace(line) != "" {
			return decisionBody{}, false
		}
		buf = append(buf, line)
	}
	flush()
	return d, true
}

// oneLine renders the sections of a decision body as `Context: ... Decision:
// ... Consequences: ...` on one line. A body that is not in sections is
// returned with its whitespace collapsed.
func oneLine(body string) string {
	d, ok := parseDecisionBody(body)
	if !ok {
		return strings.Join(strings.Fields(body), " ")
	}
	var out []string
	for i, text := range d.parts() {
		if text = strings.Join(strings.Fields(text), " "); text != "" {
			out = append(out, decisionHeadings[i]+": "+text)
		}
	}
	return strings.Join(out, " ")
}

func newDecide(a *app) *cobra.Command {
	var (
		decision, ctxText, consequences string
		parent, supersedes              int64
		proposed                        bool
	)
	cmd := &cobra.Command{
		Use:   "decide <title> -m <decision>",
		Short: "Record a decision",
		Long: `Create a decision card and print its line. The body is the sections
## Context, ## Decision and ## Consequences; -m is the decision text and is
required, --context and --consequences are optional and left out when empty.

An agent's decision is proposed (ready) and a human reviewer accepts it with
'lorvik decisions accept'. A human caller records it as accepted (done) at
once, which needs the reviewer role (exit 5 otherwise); pass --proposed to
leave it for review instead. --parent puts it under an open story.
--supersedes N names the accepted decision it replaces; N is dropped as
superseded when the new one is accepted. -p global records an org-wide one.

Examples:
  lorvik decide "Use a pgx pool" -m "pgxpool, no database/sql." --context "Queue needs SKIP LOCKED." --parent 2
  lorvik decide "Use pgx v6" -m "Move to v6." --supersedes 7 --proposed`,
		Args: usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(decision) == "" {
				return cmdutil.Usagef("-m is required: the decision itself, in a sentence or two")
			}
			text, err := a.readBody(decision)
			if err != nil {
				return err
			}
			in := apitypes.CreateCard{
				Kind:  "decision",
				Title: joinArgs(args),
				Body:  decisionBody{Context: ctxText, Decision: text, Consequences: consequences}.render(),
			}
			if cmd.Flags().Changed("parent") {
				in.Parent = &parent
			}
			if cmd.Flags().Changed("supersedes") {
				in.Supersedes = &supersedes
			}
			c, slug, err := a.api()
			if err != nil {
				return err
			}
			// Only a human records a decision as accepted; an agent proposes.
			if !proposed {
				me, err := c.Me(cmd.Context())
				if err != nil {
					return err
				}
				if me.Actor.Kind == "user" {
					in.Status = "done"
				}
			}
			card, err := c.CreateCard(cmd.Context(), slug, in)
			if err != nil {
				return err
			}
			return a.emitCard(cmd.Context(), card)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&decision, "message", "m", "", "The decision (required; - reads stdin)")
	f.StringVar(&ctxText, "context", "", "What is being decided and what constrains it")
	f.StringVar(&consequences, "consequences", "", "What gets easier, harder or given up")
	f.Int64Var(&parent, "parent", 0, "Parent story number")
	f.Int64Var(&supersedes, "supersedes", 0, "Accepted decision number this one replaces")
	f.BoolVar(&proposed, "proposed", false, "Leave it proposed for a reviewer even when a human records it")
	return cmd
}

func newDecisions(a *app) *cobra.Command {
	return group("decisions", "List, read, accept and edit decision cards",
		`Decisions are cards of kind decision, shown with a "D " marker. Proposed is
ready, accepted is done (in force), rejected and revoked are dropped with that
reason, superseded is dropped when a newer decision replaces it. Accepted text
is frozen: record a new decision with 'decide --supersedes N'.

Examples:
  lorvik decisions ls --status ready
  lorvik decisions search "queue backend"
  lorvik decisions show 7
  lorvik decisions accept 8
  lorvik decisions link 7 42`,
		newDecisionsLs(a), newDecisionsShow(a), newDecisionsSearch(a),
		newDecisionMove(a, "accept", "Accept a proposed decision", "Mark a proposed decision accepted (done). Needs the reviewer role. Accepting a\ndecision that supersedes another drops the old one.\n\nAn agent needs the decide right and -m with what the verdict rests on. It\naccepts for its owner, who looks at it afterwards, and never its own chat's\nproposal, one that replaces an accepted decision, or one in the global project\n(exit 5).\n\nExamples:\n  lorvik decisions accept 8\n  lorvik decisions accept 8 -m \"docs/requirements/board.md 2.3 already says so\"", "done", ""),
		newDecisionMove(a, "reject", "Reject a proposed decision", "Drop a proposed decision with the reason rejected. A rejected decision can be proposed again with 'lorvik reopen'.\nAn agent rejects under the rules it accepts under: 'lorvik decisions accept --help'.\n\nExample:\n  lorvik decisions reject 8 -m \"not now\"", "dropped", "rejected"),
		newDecisionMove(a, "revoke", "Revoke an accepted decision", "Drop an accepted decision with the reason revoked. To replace it instead, record a new decision with 'decide --supersedes N'.\n\nExample:\n  lorvik decisions revoke 8 -m \"no longer true\"", "dropped", "revoked"),
		newDecisionsEdit(a), newDecisionsLink(a, true), newDecisionsLink(a, false))
}

// decisionScope is the project value for GET /cards: every project with
// --all or when no project is bound, else the current one.
func (a *app) decisionScope(all bool) string {
	if all {
		return "all"
	}
	if s := a.optSlug(); s != "" {
		return s
	}
	return "all"
}

func (a *app) listDecisions(cmd *cobra.Command, p apitypes.ListAllCardsParams) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	list, err := c.ListAllCards(cmd.Context(), p)
	if err != nil {
		return err
	}
	return a.emitCardList(cmd, list, "no decisions", nil)
}

func newDecisionsLs(a *app) *cobra.Command {
	var (
		p       apitypes.ListAllCardsParams
		all     bool
		inForce bool
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List decisions, newest first",
		Long: `List decision cards of the current project, newest first, as card lines with
the "D " marker. --all lists every project you can read, each line ending
with its project in parentheses; -p global lists the global project. The
status shows whether a decision is in force: done. --in-force is
--status done.

Examples:
  lorvik decisions ls --status ready
  lorvik decisions ls --in-force --all
  lorvik -p global decisions ls`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if inForce {
				if cmd.Flags().Changed("status") {
					return cmdutil.Usagef("--in-force is --status done; pass one")
				}
				p.Status = []string{"done"}
			}
			if err := a.resolveTimes(cmd, "created-after", "created-before", "updated-after", "updated-before"); err != nil {
				return err
			}
			p.Kind = "decision"
			p.Project = a.decisionScope(all)
			return a.listDecisions(cmd, p)
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&p.Status, "status", nil, "Only these statuses, comma separated (ready is proposed, done is accepted, dropped)")
	f.BoolVar(&inForce, "in-force", false, "Only accepted decisions (--status done)")
	f.BoolVar(&all, "all", false, "Every project you can read, not just the current one")
	f.StringVarP(&p.Query, "query", "q", "", "Full-text match on title and body")
	f.IntVarP(&p.Limit, "limit", "L", defaultLimit, "Maximum decisions to print")
	f.StringVar(&p.Cursor, "cursor", "", "Continue from the next value printed by a previous page")
	timeFlags(f, "created", &p.CreatedAfter, &p.CreatedBefore)
	timeFlags(f, "updated", &p.UpdatedAfter, &p.UpdatedBefore)
	return cmd
}

func newDecisionsSearch(a *app) *cobra.Command {
	var (
		p   apitypes.ListAllCardsParams
		all bool
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search decisions",
		Long: `Full-text search over decision cards only, in the same lines as 'decisions ls'.
The status shows whether a decision is still in force. Searches the current
project; --all searches every project you can read.

Examples:
  lorvik decisions search "queue backend"
  lorvik decisions search pgx --all -L 5`,
		Args: usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			p.Kind = "decision"
			p.Query = joinArgs(args)
			p.Project = a.decisionScope(all)
			return a.listDecisions(cmd, p)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&all, "all", false, "Every project you can read, not just the current one")
	f.IntVarP(&p.Limit, "limit", "L", defaultLimit, "Maximum decisions to print")
	f.StringVar(&p.Cursor, "cursor", "", "Continue from the next value printed by a previous page")
	return cmd
}

func newDecisionsShow(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show <number>",
		Short: "Show one decision in full",
		Long: `Print a decision card as 'show' does; the meta line adds who accepted it and
when, and the decisions it supersedes or that supersede it.

Example:
  lorvik decisions show 7`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.showCard(cmd, args[0], "decision", false)
		},
	}
}

// newDecisionMove builds accept, reject and revoke: a transition of a
// decision card, refused client-side for any other kind.
func newDecisionMove(a *app, verb, short, long, status, reason string) *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use: verb + " <number>", Short: short, Long: long,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := parseNum(args[0])
			if err != nil {
				return err
			}
			return a.mutateCard(cmd.Context(), n, func(c *client.Client, slug string, cur apitypes.Card) (apitypes.Card, error) {
				if err := wantKind(cur, "decision"); err != nil {
					return apitypes.Card{}, err
				}
				return c.TransitionCard(cmd.Context(), slug, n, apitypes.TransitionCard{Version: cur.Version, Status: status, DropReason: reason, Note: note})
			})
		},
	}
	cmd.Flags().StringVarP(&note, "message", "m", "", "Short note recorded on the event; from an agent, what its verdict rests on")
	return cmd
}

func newDecisionsEdit(a *app) *cobra.Command {
	var title, ctxText, decision, consequences string
	cmd := &cobra.Command{
		Use:   "edit <number>",
		Short: "Edit a proposed decision's title or sections",
		Long: `Change the title or the Context, Decision and Consequences sections of a
decision; sections you do not pass are kept. Only the proposer or a reviewer
may edit, and only while the decision is proposed: accepted or dropped text is
frozen (exit 4; record a new decision with 'decide --supersedes N'). The old
text stays in 'lorvik history N'. A body that is not in the three sections is
refused; replace it with 'lorvik edit N -b'.

Example:
  lorvik decisions edit 8 --consequences "No driver portability."`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := parseNum(args[0])
			if err != nil {
				return err
			}
			fl := cmd.Flags()
			sections := fl.Changed("context") || fl.Changed("decision") || fl.Changed("consequences")
			if !sections && !fl.Changed("title") {
				return cmdutil.Usagef("nothing to change: pass --title, --context, --decision or --consequences")
			}
			return a.mutateCard(cmd.Context(), n, func(c *client.Client, slug string, cur apitypes.Card) (apitypes.Card, error) {
				if err := wantKind(cur, "decision"); err != nil {
					return apitypes.Card{}, err
				}
				in := apitypes.UpdateCard{Version: cur.Version}
				if fl.Changed("title") {
					in.Title = &title
				}
				if sections {
					d, ok := parseDecisionBody(cur.Body)
					if !ok {
						return apitypes.Card{}, cmdutil.Usagef("decision #%d is not in Context, Decision and Consequences sections; replace it with `lorvik edit %d -b`", n, n)
					}
					if fl.Changed("context") {
						d.Context = ctxText
					}
					if fl.Changed("decision") {
						d.Decision = decision
					}
					if fl.Changed("consequences") {
						d.Consequences = consequences
					}
					body := d.render()
					in.Body = &body
				}
				return c.UpdateCard(cmd.Context(), slug, n, in)
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&title, "title", "", "New title")
	f.StringVar(&ctxText, "context", "", "New Context section; empty removes it")
	f.StringVar(&decision, "decision", "", "New Decision section; empty removes it")
	f.StringVar(&consequences, "consequences", "", "New Consequences section; empty removes it")
	return cmd
}

func newDecisionsLink(a *app, link bool) *cobra.Command {
	var toProject string
	use, short, long := "link <number> <card>", "Apply a decision to a card", `Link decision N to card M with kind applies, so M shows the decision it
follows. A decision in the global project may apply to a card in any project:
pass the card's project with --to-project.

Examples:
  lorvik decisions link 7 42
  lorvik -p global decisions link 3 42 --to-project web`
	if !link {
		use, short, long = "unlink <number> <card>", "Remove a decision from a card", `Remove the applies link from decision N to card M. --to-project names M's
project when it is not this one.

Example:
  lorvik decisions unlink 7 42`
	}
	cmd := &cobra.Command{
		Use: use, Short: short, Long: long,
		Args: usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := parseNum(args[0])
			if err != nil {
				return err
			}
			m, err := parseNum(args[1])
			if err != nil {
				return err
			}
			return a.linkRun(cmd, n, "applies", m, toProject, link)
		},
	}
	cmd.Flags().StringVar(&toProject, "to-project", "", "Project of the card, when it is not this one")
	return cmd
}
