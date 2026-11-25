package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

const requirementsDir = "docs/requirements"

// foldBrief is what `fold N` prints for a story: everything the person who
// updates the requirements documents needs, composed from ordinary reads.
type foldBrief struct {
	Story     apitypes.Card  `json:"story"`
	Children  []foldChild    `json:"children"`
	Decisions []foldDecision `json:"decisions"`
	Lanes     []string       `json:"lanes"`
	Documents []foldDocument `json:"documents"`
}

// foldChild is a child card with how it was finished: Summary is the note
// given to `done -m`, Done the latest comment that starts with "Done:".
type foldChild struct {
	Card    apitypes.CardBrief `json:"card"`
	Summary string             `json:"summary,omitempty"`
	Done    string             `json:"done,omitempty"`
}

// foldDecision is a decision of the story, with its body on one line.
type foldDecision struct {
	Card apitypes.CardBrief `json:"card"`
	Text string             `json:"text,omitempty"`
}

// foldDocument is a requirements document the story's lanes point at.
type foldDocument struct {
	Path    string `json:"path"`
	Missing bool   `json:"missing"`
}

func newFold(a *app) *cobra.Command {
	var (
		into       string
		noContract bool
		limit      int
		cursor     string
	)
	cmd := &cobra.Command{
		Use:   "fold [number]",
		Short: "List unfolded work, print a story's fold brief, or record a fold",
		Long: `Closed work is folded into the requirements documents (docs/requirements).

  lorvik fold             done cards with no parent and no fold mark, newest
                          closed first, each ending (closed <ago>)
  lorvik fold N           the brief for story N: its children with their done
                          summaries and Done: comments, its decisions, and the
                          documents its lanes suggest. Read-only; the CLI
                          composes it from card reads
  lorvik fold N --into P  record that standalone done card N went into document
                          P, or --no-contract-change when nothing durable
                          changed

A story is folded by closing it: 'lorvik done N --into P'. A card with a
parent folds with its story. Folding is refused for a card that is not done or
is already folded. A suggested document is marked missing when it is not found
under the repository root; create it from docs/requirements/README.md.

Examples:
  lorvik fold
  lorvik fold 20
  lorvik fold 31 --no-contract-change`,
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			fold, err := foldArg(into, noContract)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				if fold != nil {
					return cmdutil.Usagef("--into and --no-contract-change need a card number: lorvik fold <number> --into <path>")
				}
				return a.listUnfolded(cmd, limit, cursor)
			}
			n, err := parseNum(args[0])
			if err != nil {
				return err
			}
			if fold == nil {
				return a.foldBrief(cmd, n)
			}
			return a.mutateCard(cmd.Context(), n, func(c *client.Client, slug string, cur apitypes.Card) (apitypes.Card, error) {
				return c.FoldCard(cmd.Context(), slug, n, apitypes.FoldCard{Version: cur.Version, Fold: *fold})
			}, "  folded")
		},
	}
	f := cmd.Flags()
	f.StringVar(&into, "into", "", "Requirements document the card went into")
	f.BoolVar(&noContract, "no-contract-change", false, "Record the card as folded with nothing durable to write")
	f.IntVarP(&limit, "limit", "L", defaultLimit, "Maximum cards to list")
	f.StringVar(&cursor, "cursor", "", "Continue from the next value printed by a previous page")
	return cmd
}

func (a *app) listUnfolded(cmd *cobra.Command, limit int, cursor string) error {
	c, slug, err := a.api()
	if err != nil {
		return err
	}
	list, err := c.ListUnfolded(cmd.Context(), slug, limit, cursor)
	if err != nil {
		return err
	}
	return a.emitCardList(cmd, list, "nothing to fold", func(p *cmdutil.Printer, it apitypes.CardBrief) {
		// Older servers send no closed_at; their done cards are not edited,
		// so the last update is when they closed.
		closed := it.UpdatedAt
		if it.ClosedAt != nil {
			closed = *it.ClosedAt
		}
		p.Line("%s  (closed %s)", cmdutil.CardLine(it, p.Color, p.Name), p.Ago(closed))
	})
}

func (a *app) foldBrief(cmd *cobra.Command, n int64) error {
	ctx := cmd.Context()
	c, slug, err := a.api()
	if err != nil {
		return err
	}
	story, err := c.GetCard(ctx, slug, n)
	if err != nil {
		return err
	}
	if story.Kind != "story" {
		return cmdutil.Usagef("#%d is a %s, which has no brief; record its fold with `lorvik fold %d --into <path>` or --no-contract-change", n, story.Kind, n)
	}
	story, err = withAllChildren(ctx, c, slug, story)
	if err != nil {
		return err
	}
	b := foldBrief{Story: story}
	lanes := newLaneSet(story.Lane)
	for _, ch := range story.Children {
		lanes.add(ch.Lane)
		if ch.Kind == "decision" {
			continue
		}
		fc, err := a.foldChild(ctx, c, slug, ch)
		if err != nil {
			return err
		}
		b.Children = append(b.Children, fc)
	}
	b.Decisions, err = a.foldDecisions(ctx, c, slug, story)
	if err != nil {
		return err
	}
	b.Lanes = lanes.list
	root := a.repoRoot()
	for _, lane := range b.Lanes {
		if filepath.Base(lane) != lane {
			continue
		}
		path := requirementsDir + "/" + lane + ".md"
		_, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		b.Documents = append(b.Documents, foldDocument{Path: path, Missing: statErr != nil})
	}
	var who []apitypes.Actor
	who = append(who, cardActors(story)...)
	a.resolve(ctx, who...)
	return a.emit(b, func(p *cmdutil.Printer) { renderFoldBrief(p, b) })
}

// foldChild reads what the child's finisher left: the closing note the card
// carries (the event list is only the newest few, so it cannot hold it) and
// the newest comment that starts with "Done:".
func (a *app) foldChild(ctx context.Context, c *client.Client, slug string, ch apitypes.CardBrief) (foldChild, error) {
	full, err := c.GetCard(ctx, slug, ch.Number)
	if err != nil {
		return foldChild{}, err
	}
	out := foldChild{Card: ch}
	if full.ClosingNote != "" {
		out.Summary = oneSpaced(full.ClosingNote)
	}
	for before := ""; ; {
		comments, next, err := historyPage(apitypes.MaxPageSize, before, func(cm apitypes.Comment) string { return cm.ID },
			func(p client.HistoryParams) ([]apitypes.Comment, error) {
				return c.ListComments(ctx, slug, ch.Number, p)
			})
		if err != nil {
			return foldChild{}, err
		}
		for _, cm := range comments {
			if strings.HasPrefix(strings.TrimSpace(cm.Body), "Done:") {
				out.Done = oneSpaced(cm.Body)
				return out, nil
			}
		}
		if next == "" {
			return out, nil
		}
		before = next
	}
}

// foldDecisions gathers the story's decision children and the decisions
// applied to it, which may live in another project (the global one).
func (a *app) foldDecisions(ctx context.Context, c *client.Client, slug string, story apitypes.Card) ([]foldDecision, error) {
	type ref struct {
		project string
		number  int64
	}
	var refs []ref
	for _, ch := range story.Children {
		if ch.Kind == "decision" {
			refs = append(refs, ref{slug, ch.Number})
		}
	}
	for _, l := range story.Links {
		if isAppliedIn(l) {
			proj := l.Project
			if proj == "" {
				proj = slug
			}
			refs = append(refs, ref{proj, l.Number})
		}
	}
	var out []foldDecision
	for _, r := range refs {
		d, err := c.GetCard(ctx, r.project, r.number)
		if err != nil {
			return nil, err
		}
		brief := cmdutil.BriefOf(d)
		if r.project != slug {
			brief.Project = r.project
		}
		out = append(out, foldDecision{Card: brief, Text: oneLine(d.Body)})
	}
	return out, nil
}

// laneSet is an ordered set of non-empty lanes.
type laneSet struct{ list []string }

func newLaneSet(first string) *laneSet {
	s := &laneSet{}
	s.add(first)
	return s
}

func (s *laneSet) add(lane string) {
	if lane == "" {
		return
	}
	if slices.Contains(s.list, lane) {
		return
	}
	s.list = append(s.list, lane)
}

func oneSpaced(s string) string { return strings.Join(strings.Fields(s), " ") }

func renderFoldBrief(p *cmdutil.Printer, b foldBrief) {
	story := cmdutil.BriefOf(b.Story)
	line := cmdutil.CardLine(story, p.Color, p.Name)
	if pr := b.Story.Progress; pr != nil {
		line += fmt.Sprintf("  progress %d/%d", pr.Done, pr.Total)
	}
	p.Line("%s", line)
	if b.Story.Lane != "" {
		p.Line("lane %s", b.Story.Lane)
	}
	const detail = "       "
	section(p, "Children", len(b.Children), func() {
		for _, ch := range b.Children {
			p.Line("  %s", cmdutil.CardLine(ch.Card, p.Color, p.Name))
			if ch.Summary != "" {
				p.Line("%s%s", detail, ch.Summary)
			}
			if ch.Done != "" {
				p.Line("%s%s", detail, ch.Done)
			}
		}
	})
	section(p, "Decisions", len(b.Decisions), func() {
		for _, d := range b.Decisions {
			p.Line("  %s", cmdutil.CardLine(d.Card, p.Color, p.Name))
			if d.Text != "" {
				p.Line("%s%s", detail, d.Text)
			}
		}
	})
	section(p, "Suggested documents", len(b.Documents), func() {
		for _, d := range b.Documents {
			if d.Missing {
				p.Line("  %s  (missing, create from %s/README.md)", d.Path, requirementsDir)
				continue
			}
			p.Line("  %s", d.Path)
		}
	})
}
