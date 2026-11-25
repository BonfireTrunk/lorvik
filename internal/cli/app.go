package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

const defaultLimit = 10

// app holds the state shared by every command: the factory plus the values
// of the global flags. It is created per NewRoot call, so there is no
// package-level state.
type app struct {
	f           *cmdutil.Factory
	apiURL      string
	project     string
	asJSON      bool
	quiet       bool
	sessionFlag string
	cl          *client.Client
	sessInfo    sessionInfo
	sessDone    bool
	nameInfo    chatName
	nameDone    bool
	names       map[string]string
	// agent is the harness's own agent this chat acts as, once client() ran.
	agent *agentIdentity
	// noHolding is set by a command whose output must not carry the list of
	// held cards: it prints its claims itself, its stdout is a payload, or it
	// waited so long that the list it has is old.
	noHolding bool
	// ctx is the running command's context, for work that starts before a
	// command's own request, such as sending queued entries.
	ctx context.Context
	// queued is set when the command stored its write in the outbox instead
	// of sending it: there was no response to report the unread count from.
	queued bool
	// flushed is set once queued entries were sent this process; noFlush
	// keeps the sync command from sending them twice.
	flushed, noFlush bool
	// outboxDown is why the queued entries could not be sent, when the server
	// was unreachable.
	outboxDown error
}

func (a *app) getenv(k string) string {
	if a.f.Env != nil {
		return a.f.Env(k)
	}
	return os.Getenv(k)
}

func (a *app) out() io.Writer    { return a.f.IOStreams.Out }
func (a *app) errOut() io.Writer { return a.f.IOStreams.ErrOut }

// interactive reports whether a command may prompt: stdin, stdout and stderr
// are all terminals, --json is off, CI is empty and no agent harness variable
// is set. The harness check matters because an agent can run commands inside
// a PTY, where all three streams look like terminals; a prompt there would
// block the agent forever, so a harness always gets the flags-only path.
func (a *app) interactive() bool {
	s := a.f.IOStreams
	if !s.IsStdinTTY() || !s.IsStdoutTTY() || !s.IsStderrTTY() || a.asJSON || a.getenv("CI") != "" {
		return false
	}
	for _, h := range sessionHarnesses {
		if a.getenv(h.env) != "" {
			return false
		}
	}
	return true
}

// prompter returns the injected Prompter, else the terminal one.
func (a *app) prompter() cmdutil.Prompter {
	if a.f.Prompter != nil {
		return a.f.Prompter
	}
	return cmdutil.NewTerminalPrompter(a.f.IOStreams, a.getenv)
}

func (a *app) printer() *cmdutil.Printer {
	return cmdutil.NewPrinter(a.f.IOStreams, a.f.Now, a.nameOf)
}

func (a *app) settings() (Settings, error) {
	getwd := os.Getwd
	if a.f.Getwd != nil {
		getwd = a.f.Getwd
	}
	cwd, err := getwd()
	if err != nil {
		return Settings{}, fmt.Errorf("working directory: %w", err)
	}
	s, err := Resolve(Flags{APIURL: a.apiURL, Project: a.project}, a.getenv, cwd)
	if err != nil {
		return Settings{}, err
	}
	s.ConfigPath = userConfigPath(a.getenv)
	if id, ok := a.harnessIdentity(); ok {
		return s.asAgent(id)
	}
	return s, nil
}

// client returns the API client, building it once. It carries the stored
// inbox cursor and the token rotation hook.
func (a *app) client() (*client.Client, error) {
	if a.cl != nil {
		return a.cl, nil
	}
	var c *client.Client
	var err error
	if a.f.Client != nil {
		c, err = a.f.Client()
	} else {
		var s Settings
		var tok string
		if s, err = a.settings(); err == nil && s.Agent != nil {
			s, err = a.agentToken(s, "")
		}
		if err == nil {
			a.agent = s.Agent
			a.warnBorrowedConfig()
			if tok, err = s.credential(); err == nil {
				c, err = client.New(s.APIURL, tok)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	c.Session = a.session()
	c.Holding = true
	// The name goes only where the token goes: a .lorvik.toml in a cloned
	// repo may point at a server you never signed in to.
	if c.Token() != "" {
		c.SessionName = a.chatName().Name
	}
	c.Cursor = a.storedCursor(c.Token(), c.Session)
	c.OnToken = a.tokenRotated(c.Token())
	if a.agent != nil {
		c.Renew = a.renewRefused
	}
	a.cl = c
	return c, nil
}

// anonClient returns a client without a token, for login. A factory client is
// reused as is.
func (a *app) anonClient() (*client.Client, error) {
	if a.f.Client != nil {
		return a.client()
	}
	s, err := a.settings()
	if err != nil {
		return nil, err
	}
	c, err := client.New(s.APIURL, "")
	if err != nil {
		return nil, err
	}
	a.cl = c
	return c, nil
}

// slug returns the project, from --project, LORVIK_PROJECT or .lorvik.toml.
func (a *app) slug() (string, error) {
	s, err := a.settings()
	if err != nil {
		return "", err
	}
	if s.Project == "" {
		return "", cmdutil.Usagef("no project: pass --project <slug>, set LORVIK_PROJECT, or run `lorvik init`")
	}
	return s.Project, nil
}

// optSlug is slug without the error: empty when no project is bound.
func (a *app) optSlug() string {
	s, err := a.settings()
	if err != nil {
		return ""
	}
	return s.Project
}

// api resolves the client and project together, the start of most commands.
func (a *app) api() (*client.Client, string, error) {
	slug, err := a.slug()
	if err != nil {
		return nil, "", err
	}
	c, err := a.client()
	if err != nil {
		return nil, "", err
	}
	err = a.joinProject(c, slug)
	if err == nil {
		a.flushOutbox(c, slug)
	}
	return c, slug, err
}

// emit prints v as JSON under --json, otherwise runs the human renderer. A
// JSON object gains the holding field: what the chat holds, when the server
// said.
func (a *app) emit(v any, human func(p *cmdutil.Printer)) error {
	if a.asJSON {
		if h := a.held(); h != nil {
			return cmdutil.WriteJSONField(a.out(), v, "holding", h)
		}
		return cmdutil.WriteJSON(a.out(), v)
	}
	human(a.printer())
	return nil
}

// emitCard prints a mutated card in its compact one-line form.
func (a *app) emitCard(ctx context.Context, c apitypes.Card, suffix ...string) error {
	a.resolve(ctx, briefActors(cmdutil.BriefOf(c))...)
	return a.emit(c, func(p *cmdutil.Printer) {
		p.Line("%s%s", cmdutil.CardLine(cmdutil.BriefOf(c), p.Color, p.Name), strings.Join(suffix, ""))
	})
}

// mutateCard refetches the card for its current version, then applies fn.
// An optional suffix is appended to the human card line.
func (a *app) mutateCard(ctx context.Context, n int64, fn func(c *client.Client, slug string, cur apitypes.Card) (apitypes.Card, error), suffix ...string) error {
	c, slug, err := a.api()
	if err != nil {
		return err
	}
	cur, err := c.GetCard(ctx, slug, n)
	if err != nil {
		return err
	}
	out, err := fn(c, slug, cur)
	if err != nil {
		return err
	}
	return a.emitCard(ctx, out, suffix...)
}

// parseNum accepts 42, #42 or D42 style references.
func parseNum(s string) (int64, error) {
	t := strings.TrimLeft(strings.TrimSpace(s), "#dD")
	n, err := strconv.ParseInt(t, 10, 64)
	if err != nil || n < 0 {
		return 0, cmdutil.Usagef("%q is not a number; use the card number, e.g. 42", s)
	}
	return n, nil
}

// usageArgs turns cobra argument-count errors into usage errors (exit 2).
func usageArgs(v cobra.PositionalArgs) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error {
		if err := v(c, args); err != nil {
			return cmdutil.Usagef("%v (see `%s --help`)", err, c.CommandPath())
		}
		return nil
	}
}

// readBody returns s, or all of stdin when s is "-".
func (a *app) readBody(s string) (string, error) {
	if s != "-" {
		return s, nil
	}
	b, err := io.ReadAll(a.f.IOStreams.In)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return strings.TrimRight(string(b), "\n"), nil
}

// isNotFound reports a 404 from the server.
func isNotFound(err error) bool {
	ae, ok := errors.AsType[*client.APIError](err)
	return ok && ae.Status == 404
}

// isFieldError reports a 422 from the server on the named field.
func isFieldError(err error, field string) bool {
	ae, ok := errors.AsType[*client.APIError](err)
	return ok && ae.Status == 422 && slices.ContainsFunc(ae.Errors, func(f apitypes.ProblemField) bool { return f.Field == field })
}

// isUnauthorized reports a 401 from the server: no token, or one that expired
// or was revoked.
func isUnauthorized(err error) bool {
	ae, ok := errors.AsType[*client.APIError](err)
	return ok && ae.Status == 401
}

// oneOf validates a flag value against its allowed set; empty is allowed.
func oneOf(flag, v string, allowed ...string) error {
	if v == "" {
		return nil
	}
	return requiredOneOf(flag, v, allowed...)
}

func requiredOneOf(flag, v string, allowed ...string) error {
	if slices.Contains(allowed, v) {
		return nil
	}
	return cmdutil.Usagef("--%s %q: want one of %s", flag, v, strings.Join(allowed, ", "))
}

var priorities = []string{"low", "normal", "high", "urgent"}

// group makes a parent command that only routes to subcommands. A bare call
// prints help; an unknown subcommand is a usage error.
func group(use, short, long string, subs ...*cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use: use, Short: short, Long: long,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cmdutil.Usagef("unknown command %q for %q (see `%s --help`)", args[0], cmd.CommandPath(), cmd.CommandPath())
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(subs...)
	return cmd
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func joinArgs(args []string) string { return strings.Join(args, " ") }

func (a *app) emitCardList(cmd *cobra.Command, list apitypes.CardList, empty string, line func(*cmdutil.Printer, apitypes.CardBrief)) error {
	a.resolve(cmd.Context(), briefsActors(list.Items)...)
	return a.emit(list, func(p *cmdutil.Printer) {
		if len(list.Items) == 0 {
			p.Line("%s", empty)
			return
		}
		for _, it := range list.Items {
			if line == nil {
				p.Card(it)
			} else {
				line(p, it)
			}
		}
		p.Paging(list.Page, a.nextCommand(cmd))
	})
}

func orElse(s, placeholder string) string {
	if s == "" {
		return placeholder
	}
	return s
}

// pageFlags adds the page flags of a forward-only list, which prints the server's
// default page unless told otherwise.
func pageFlags(cmd *cobra.Command, pg *client.PageParams, limitUsage string) {
	cmd.Flags().IntVarP(&pg.Limit, "limit", "L", apitypes.DefaultPageSize, limitUsage)
	cmd.Flags().StringVar(&pg.Cursor, "cursor", "", "Continue from the next value printed by a previous page")
}

// allPages follows a list to its end, for a command whose answer is wrong with
// only part of it.
func allPages[T any](fetch func(client.PageParams) ([]T, apitypes.Page, error)) ([]T, error) {
	var out []T
	pg := client.PageParams{Limit: apitypes.MaxPageSize}
	for {
		items, page, err := fetch(pg)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if page.Next == "" || page.Remaining <= 0 {
			return out, nil
		}
		pg.Cursor = page.Next
	}
}

// historyPage reads one page of a newest-first comment or revision list, which
// has no cursor or count. It asks for one row more than limit, so a full page
// knows whether older rows exist; next is the before value that reads them,
// empty at the end. At the server's cap a full page cannot tell, so it offers
// a next page that may come back empty.
func historyPage[T any](limit int, before string, key func(T) string, fetch func(client.HistoryParams) ([]T, error)) (rows []T, next string, err error) {
	if limit < 1 {
		return nil, "", cmdutil.Usagef("--limit %d: want 1 or more", limit)
	}
	rows, err = fetch(client.HistoryParams{Before: before, Limit: min(limit+1, apitypes.MaxPageSize)})
	if err != nil {
		return nil, "", err
	}
	if len(rows) > limit || len(rows) == apitypes.MaxPageSize {
		rows = rows[:min(limit, len(rows))]
		next = key(rows[len(rows)-1])
	}
	return rows, next, nil
}

// The results of commands whose API call returns no body, shaped like
// releasedView: what was acted on, then what happened to it.
type (
	revokedView struct {
		ID      string `json:"id"`
		Revoked bool   `json:"revoked"`
	}
	removedView struct {
		Kind    string `json:"kind"`
		ID      string `json:"id"`
		Project string `json:"project,omitempty"`
		Removed bool   `json:"removed"`
	}
	unlinkedView struct {
		Number   int64  `json:"number"`
		Kind     string `json:"kind"`
		To       int64  `json:"to"`
		Unlinked bool   `json:"unlinked"`
	}
)

func revokeCmd(a *app, kind, short, long string, revoke func(*client.Client, context.Context, string) error) *cobra.Command {
	return &cobra.Command{
		Use: "revoke <id>", Short: short, Long: long,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			if err := revoke(c, cmd.Context(), args[0]); err != nil {
				return err
			}
			return a.emit(revokedView{ID: args[0], Revoked: true}, func(p *cmdutil.Printer) { p.Line("revoked %s %s", kind, args[0]) })
		},
	}
}
