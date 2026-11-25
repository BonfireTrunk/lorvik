package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

// addFlags are the inputs shared by add and ask.
type addFlags struct {
	body     string
	pageFile string
	parent   int64
	priority string
	ready    bool
	kind     string
	choices  []string
	blocks   []int64
	assignee string
	lane     string
	// supersedes names the accepted decision a new decision replaces.
	supersedes int64
	// person asks a question only a person may answer.
	person bool
}

// createCard makes the card. queue is true for add, whose write waits in the
// outbox when the server cannot be reached; ask fails as it always did.
func (a *app) createCard(cmd *cobra.Command, title string, fl *addFlags, queue bool) error {
	if err := oneOf("kind", fl.kind, "task", "question", "story", "decision"); err != nil {
		return err
	}
	if err := oneOf("priority", fl.priority, priorities...); err != nil {
		return err
	}
	if fl.kind != "question" && (len(fl.choices) > 0 || len(fl.blocks) > 0) {
		return cmdutil.Usagef("--choice and --blocks only apply to questions; add --kind question or use `lorvik ask`")
	}
	if fl.kind == "story" && cmd.Flags().Changed("parent") {
		return cmdutil.Usagef("a story has no parent: --parent only applies to tasks, questions and decisions")
	}
	if fl.kind != "decision" && cmd.Flags().Changed("supersedes") {
		return cmdutil.Usagef("--supersedes only applies to decisions; add --kind decision or use `lorvik decide`")
	}
	if err := noStdinClash(fl.body, fl.pageFile); err != nil {
		return err
	}
	body, err := a.readBody(fl.body)
	if err != nil {
		return err
	}
	var page string
	if fl.pageFile != "" {
		if page, err = a.readPageFile(fl.pageFile); err != nil {
			return err
		}
	}
	in := cardInput{
		Kind: fl.kind, Title: title, Body: body, Page: page, Priority: fl.priority,
		Choices: fl.choices, Blocks: fl.blocks, Lane: fl.lane, Ready: fl.ready, ForPerson: fl.person,
	}
	if cmd.Flags().Changed("parent") {
		in.Parent = &fl.parent
	}
	if cmd.Flags().Changed("supersedes") {
		in.Supersedes = &fl.supersedes
	}
	if cmd.Flags().Changed("assignee") {
		in.Assignee = &fl.assignee
	}
	ctx := cmd.Context()
	if !queue {
		c, slug, err := a.api()
		if err != nil {
			return err
		}
		card, err := a.postCard(ctx, c, slug, in, "")
		if err != nil {
			return err
		}
		return a.emitCard(ctx, card)
	}
	key := newRequestKey()
	e := outboxEntry{ID: key, Command: cmdAdd, cardInput: in}
	card, queued, err := attempt(ctx, a, e, func(c *client.Client, slug string) (apitypes.Card, error) {
		return a.postCard(ctx, c, slug, in, key)
	})
	if err != nil || queued {
		return err
	}
	return a.emitCard(ctx, card)
}

// postCard is the write add makes, online and from the outbox: the assignee,
// which needs the server, is resolved here.
func (a *app) postCard(ctx context.Context, c *client.Client, slug string, in cardInput, key string) (apitypes.Card, error) {
	req := in.wire()
	if in.Assignee != nil {
		act, err := a.createAssignee(ctx, c, *in.Assignee)
		if err != nil {
			return apitypes.Card{}, err
		}
		req.Assignee = &act
	}
	return c.CreateCard(client.WithRequestKey(ctx, key), slug, req)
}

// createAssignee resolves add's --assignee: me, user:<id>, agent:<id> or a
// bare actor id. An empty value is rejected; a new card has nothing to clear.
func (a *app) createAssignee(ctx context.Context, c *client.Client, s string) (apitypes.Actor, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return apitypes.Actor{}, cmdutil.Usagef("--assignee needs a value: me, user:<id> or agent:<id>")
	}
	return a.assigneeRef(ctx, c, s)
}

func newAdd(a *app) *cobra.Command {
	var fl addFlags
	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Create a card",
		Long: `Create a card and print its line. --kind is task (the default), question,
story or decision. Tasks and stories start in backlog; pass --ready to make
them pullable by 'next'. Use '-b -' to read the body from stdin. Extra words
are joined into the title. --parent puts the card under an open story in this
project; a story has no parent. --supersedes N names the accepted decision a
new decision replaces. 'lorvik decide' is the usual way to record a decision.
--assignee takes me, an actor id, or user:<id> or agent:<id>. --lane tags the
card with a lane such as schema or web; 'next' skips a card whose lane has a
card in working. --page-file PATH attaches an HTML page (self-contained, up to
1 MiB; - reads stdin); the body may then be empty. Not with '-b -'.
When the server cannot be reached, 'add' waits in the queue, and 'lorvik sync'
or the next command sends it.

Examples:
  lorvik add "Fix login redirect" -b "Loops on expired sessions" --priority high --ready
  lorvik add "Split billing module" --parent 12
  lorvik add "Webhook delivery" --kind story -b "As an integrator, ..." --lane webhooks --ready
  echo "details..." | lorvik add "Investigate flake" -b -
  lorvik add "Review the PR" --assignee agent:106666050296328901
  lorvik add "Add a column" --lane schema --ready
  lorvik add "Design review" -b "Summary" --page-file review.html`,
		Args: usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.createCard(cmd, strings.Join(args, " "), &fl, true)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&fl.body, "body", "b", "", "Card body (- reads stdin)")
	f.StringVar(&fl.pageFile, "page-file", "", "HTML page for the card from a file (- reads stdin)")
	f.Int64Var(&fl.parent, "parent", 0, "Parent story number (not for stories)")
	f.Int64Var(&fl.supersedes, "supersedes", 0, "Accepted decision number this decision replaces (decisions only)")
	f.StringVar(&fl.priority, "priority", "", "low, normal, high or urgent")
	f.BoolVar(&fl.ready, "ready", false, "Create in ready instead of backlog")
	f.StringVar(&fl.assignee, "assignee", "", "Assignee: me, actor id, user:<id> or agent:<id>")
	f.StringVar(&fl.lane, "lane", "", "Lane tag, e.g. schema or web")
	f.StringVar(&fl.kind, "kind", "", "task (default), question, story or decision")
	f.StringArrayVar(&fl.choices, "choice", nil, "Allowed answer for a question (repeatable)")
	f.Int64SliceVar(&fl.blocks, "blocks", nil, "Card number this question blocks (repeatable)")
	return cmd
}

func newAsk(a *app) *cobra.Command {
	var fl addFlags
	cmd := &cobra.Command{
		Use:   "ask <title>",
		Short: "Ask a human a question on the board",
		Long: `Create a question card, shorthand for 'add --kind question'. Questions are
ready at once and show up in the humans' queue. Use --blocks to hold the
cards that cannot proceed until it is answered, then carry on with other work.
--person asks a person only: no agent holding the answer right answers it.
--page-file PATH attaches an HTML page (- reads stdin); not with '-b -'.

Examples:
  lorvik ask "Postgres or SQLite for the cache?" --choice postgres --choice sqlite --blocks 42
  lorvik ask "Which auth provider?" -b "GitHub only for now?"`,
		Args: usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			fl.kind = "question"
			return a.createCard(cmd, strings.Join(args, " "), &fl, false)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&fl.choices, "choice", nil, "Allowed answer (repeatable)")
	f.Int64SliceVar(&fl.blocks, "blocks", nil, "Card number this question blocks (repeatable)")
	f.StringVarP(&fl.body, "body", "b", "", "Context for the question (- reads stdin)")
	f.StringVar(&fl.pageFile, "page-file", "", "HTML page for the question from a file (- reads stdin)")
	f.Int64Var(&fl.parent, "parent", 0, "Parent story number")
	f.BoolVar(&fl.person, "person", false, "Only a person answers: no agent holding the answer right does")
	return cmd
}
