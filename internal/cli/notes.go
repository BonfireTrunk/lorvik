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

func newNotes(a *app) *cobra.Command {
	cmd := group("note", "List, read, write, archive and search notes",
		`Notes are markdown pages that outlive any card: architecture, runbooks,
conventions. Notes belong to the current project; -p global addresses the
org-wide ones. A note is addressed by its slug, or by its id. Link with [[card:42]]
or [[note:slug]] in the body (decisions are cards, so [[card:7]] covers them). Every edit keeps the old text: see 'note history'.

Examples:
  lorvik note ls
  lorvik note show architecture
  lorvik note new runbook --title "Runbook" -b "How to deploy"
  lorvik note history runbook
  lorvik note show runbook --version 2`,
		newNotesLs(a), newNotesShow(a), newNotesNew(a), newNotesEdit(a), newNotesHistory(a), newNotesArchive(a), newNotesSearch(a))
	cmd.Aliases = []string{"notes"}
	return cmd
}

// noteProject is the project value for note routes: "" for an id, which needs
// no project.
func (a *app) noteProject(key string) (string, error) {
	if isNoteID(key) {
		return "", nil
	}
	return a.slug()
}

// isNoteID reports whether key is a snowflake id rather than a slug. Ids are
// 15 or more digits; a slug that long and all digits is not realistic.
func isNoteID(key string) bool { return len(key) >= 15 && isID(key) }

func (a *app) listNotes(cmd *cobra.Command, p apitypes.ListNotesParams) error {
	c, err := a.client()
	if err != nil {
		return err
	}
	list, err := c.ListNotes(cmd.Context(), p)
	if err != nil {
		return err
	}
	who := actorsOf(list.Items, func(n apitypes.NoteBrief) apitypes.Actor { return n.UpdatedBy })
	a.resolve(cmd.Context(), who...)
	return a.emit(list, func(pr *cmdutil.Printer) {
		if len(list.Items) == 0 {
			pr.Line("no notes")
			return
		}
		for _, n := range list.Items {
			pr.Note(n)
		}
		pr.Paging(list.Page, a.nextCommand(cmd))
	})
}

func newNotesLs(a *app) *cobra.Command {
	var (
		archived bool
		query    string
		limit    int
		cursor   string
		p        apitypes.ListNotesParams
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List notes",
		Long: `List notes as: slug  title  @editor  time  (scope). Defaults to the current
project's notes (or everything if no project is bound); -p global lists the
org-wide ones. Archived notes are hidden unless --archived.

Times are YYYY-MM-DD (start of that day, machine time zone), a span back from
now such as 7d, 24h or 30m, or an RFC 3339 time; "after" includes the instant
and "before" excludes it. --author takes an actor id or me and matches the
creator.

Examples:
  lorvik note ls
  lorvik note ls -p global -q onboarding
  lorvik note ls --archived
  lorvik note ls --updated-after 7d --author me`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.resolveTimes(cmd, "created-after", "created-before", "updated-after", "updated-before"); err != nil {
				return err
			}
			p.Project, p.Archived, p.Query, p.Limit, p.Cursor = a.optSlug(), archived, query, limit, cursor
			return a.listNotes(cmd, p)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&archived, "archived", false, "Include archived notes")
	f.StringVarP(&query, "query", "q", "", "Full-text match")
	timeFlags(f, "created", &p.CreatedAfter, &p.CreatedBefore)
	timeFlags(f, "updated", &p.UpdatedAfter, &p.UpdatedBefore)
	f.StringVar(&p.Author, "author", "", "Only notes created by this actor id, or me")
	f.IntVarP(&limit, "limit", "L", defaultLimit, "Maximum notes to print")
	f.StringVar(&cursor, "cursor", "", "Continue from the next value printed by a previous page")
	return cmd
}

func newNotesSearch(a *app) *cobra.Command {
	var (
		limit  int
		cursor string
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search notes",
		Long: `Full-text search over notes only; same line format as 'note ls'.

Example:
  lorvik note search "deploy"`,
		Args: usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.listNotes(cmd, apitypes.ListNotesParams{Project: a.optSlug(), Query: joinArgs(args), Limit: limit, Cursor: cursor})
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "L", defaultLimit, "Maximum notes to print")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Continue from the next value printed by a previous page")
	return cmd
}

func newNotesShow(a *app) *cobra.Command {
	var (
		version int64
		page    bool
	)
	cmd := &cobra.Command{
		Use:   "show <slug|id>",
		Short: "Show a note",
		Long: `Print a note: header line, body, and what links back to it. With --version N
print that past version of the note instead (see 'note history'). A note with
an HTML page adds a line 'Page: size, open <dashboard link>' after the version
line (a past version says how to print its page). --page prints the page's
HTML source instead (exit 3 when there is none), of the current note or, with
--version N, of that version; with --json it prints the HTMLPage, or the
revision with --version.

Examples:
  lorvik note show architecture
  lorvik note show conventions -p global
  lorvik note show architecture --version 2
  lorvik note show architecture --page > architecture.html
  lorvik note show architecture --page --version 2`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			project, err := a.noteProject(key)
			if err != nil {
				return err
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			if page && !cmd.Flags().Changed("version") {
				return a.showPage("note "+key,
					func() (apitypes.HTMLPage, error) { return c.GetNotePage(cmd.Context(), key, project) },
					func() error { _, err := c.GetNote(cmd.Context(), key, project); return err })
			}
			if cmd.Flags().Changed("version") {
				rev, err := c.GetNoteRevision(cmd.Context(), key, project, version)
				if err != nil {
					return err
				}
				if page {
					return a.showRevisionPage(fmt.Sprintf("note %s version %d", key, version), rev, rev.Page)
				}
				a.resolve(cmd.Context(), rev.UpdatedBy)
				return a.emit(rev, func(p *cmdutil.Printer) {
					p.Line("%s  v%d  %s  %s  %s", key, rev.Version, rev.Title, p.Who(rev.UpdatedBy), p.Ago(rev.CreatedAt))
					if rev.Page != "" {
						revisionPageLine(p, rev.Page, fmt.Sprintf("lorvik note show %s --page --version %d", key, rev.Version))
					}
					if rev.Body != "" {
						p.Line("")
						p.Line("%s", strings.TrimRight(rev.Body, "\n"))
					}
				})
			}
			n, err := c.GetNote(cmd.Context(), key, project)
			if err != nil {
				return err
			}
			a.resolve(cmd.Context(), n.CreatedBy, n.UpdatedBy)
			return a.emit(n, func(p *cmdutil.Printer) { renderNote(p, n, dashboardOrigin(c)) })
		},
	}
	cmd.Flags().Int64Var(&version, "version", 0, "Show this past version")
	cmd.Flags().BoolVar(&page, "page", false, "Print the note's HTML page source instead")
	return cmd
}

func renderNote(p *cmdutil.Printer, n apitypes.Note, origin string) {
	p.Note(apitypes.NoteBrief{ID: n.ID, Slug: n.Slug, Title: n.Title, Project: n.Project, Status: n.Status, UpdatedBy: n.UpdatedBy, UpdatedAt: n.UpdatedAt})
	p.Line("version %d  created by %s %s", n.Version, p.Author(n.CreatedBy), p.Ago(n.CreatedAt))
	if n.PageBytes > 0 {
		pageLine(p, n.PageBytes, noteLink(origin, n.Project, n.Slug))
	}
	if n.Body != "" {
		p.Line("")
		p.Line("%s", strings.TrimRight(n.Body, "\n"))
	}
	section(p, "Backlinks", len(n.Backlinks), func() {
		for _, b := range n.Backlinks {
			p.Line("  %s %s  %s", b.Type, backlinkRef(b), b.Title)
		}
	})
}

func backlinkRef(b apitypes.Backlink) string {
	if b.Type == "card" {
		return fmt.Sprintf("#%d", b.Number)
	}
	return b.Slug
}

func newNotesHistory(a *app) *cobra.Command {
	var (
		limit  int
		cursor string
	)
	cmd := &cobra.Command{
		Use:   "history <slug|id>",
		Short: "List the past versions of a note",
		Long: `List a note's versions, newest first, as: vN  title  @editor  time. Bodies are
not printed; read one with 'note show <key> --version N'. A full page ends with
the command for older versions.

Example:
  lorvik note history runbook`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := a.noteProject(args[0])
			if err != nil {
				return err
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			revs, next, err := historyPage(limit, cursor, func(r apitypes.NoteRevision) string { return itoa(r.Version) },
				func(hp client.HistoryParams) ([]apitypes.NoteRevision, error) {
					return c.ListNoteRevisions(cmd.Context(), args[0], project, hp)
				})
			if err != nil {
				return err
			}
			a.resolve(cmd.Context(), actorsOf(revs, func(r apitypes.NoteRevision) apitypes.Actor { return r.UpdatedBy })...)
			return a.emit(revs, func(p *cmdutil.Printer) {
				if len(revs) == 0 {
					p.Line("no earlier versions")
				}
				for _, r := range revs {
					p.Line("v%d  %s  %s  %s", r.Version, r.Title, p.Who(r.UpdatedBy), p.Ago(r.CreatedAt))
				}
				p.Older(next, a.nextCommand(cmd))
			})
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "L", defaultLimit, "Maximum versions to list")
	cmd.Flags().StringVar(&cursor, "cursor", "", "List versions older than this one: the next value printed by a previous page")
	return cmd
}

func newNotesNew(a *app) *cobra.Command {
	var (
		title, body, pageFile string
	)
	cmd := &cobra.Command{
		Use:   "new <slug> --title <t> -b <body>",
		Short: "Create a note",
		Long: `Create a markdown note under a slug (lowercase, dashes). -b - reads the body
from stdin. --page-file PATH attaches an HTML page (self-contained, up to 1 MiB;
- reads stdin, but not together with '-b -'); a note needs a body or a page.

Examples:
  lorvik note new runbook --title "Deploy runbook" -b "1. Tag. 2. Release."
  lorvik note new design --title "Design" --page-file design.html
  lorvik note new onboarding -p global --title "Onboarding" -b - < onboarding.md`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := a.noteProject(args[0])
			if err != nil {
				return err
			}
			if err := noStdinClash(body, pageFile); err != nil {
				return err
			}
			text, err := a.readBody(body)
			if err != nil {
				return err
			}
			var page string
			if pageFile != "" {
				if page, err = a.readPageFile(pageFile); err != nil {
					return err
				}
			}
			if strings.TrimSpace(text) == "" && page == "" {
				return cmdutil.Usagef("-b <body> or --page-file is required")
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			n, err := c.CreateNote(cmd.Context(), apitypes.CreateNote{Project: project, Slug: args[0], Title: title, Body: text, Page: page})
			if err != nil {
				return err
			}
			return a.emit(n, func(p *cmdutil.Printer) { p.Line("created note %s", n.Slug) })
		},
	}
	f := cmd.Flags()
	f.StringVar(&title, "title", "", "Note title")
	f.StringVarP(&body, "body", "b", "", "Markdown body (- reads stdin)")
	f.StringVar(&pageFile, "page-file", "", "HTML page from a file (- reads stdin)")
	return cmd
}

// updateNote fetches the note for its version, applies the change and prints
// the one-line result.
func (a *app) updateNote(ctx context.Context, key string, build func(cur apitypes.Note) (apitypes.UpdateNote, error), done string) error {
	project, err := a.noteProject(key)
	if err != nil {
		return err
	}
	c, err := a.client()
	if err != nil {
		return err
	}
	cur, err := c.GetNote(ctx, key, project)
	if err != nil {
		return err
	}
	in, err := build(cur)
	if err != nil {
		return err
	}
	n, err := c.UpdateNote(ctx, key, project, in)
	if err != nil {
		return err
	}
	return a.emit(n, func(p *cmdutil.Printer) { p.Line("%s note %s", done, n.Slug) })
}

func newNotesEdit(a *app) *cobra.Command {
	var (
		title, body, pageFile string
	)
	cmd := &cobra.Command{
		Use:   "edit <slug|id> [--title t] [-b body] [--page-file path]",
		Short: "Change a note's title, body or page",
		Long: `Replace a note's title, body and/or HTML page. The current version is fetched
and sent first; a conflict exits 4 with the server's hint. The old text stays
in 'note history'. --page-file PATH sets the page (- reads stdin, but not
together with '-b -'); --page-file "" removes it.

Examples:
  lorvik note edit runbook -b "Updated steps"
  lorvik note edit design --page-file design.html
  lorvik note edit design --page-file ""`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			fl := cmd.Flags()
			if !fl.Changed("title") && !fl.Changed("body") && !fl.Changed("page-file") {
				return cmdutil.Usagef("nothing to change: pass --title, -b or --page-file")
			}
			if err := noStdinClash(body, pageFile); err != nil {
				return err
			}
			return a.updateNote(cmd.Context(), args[0], func(cur apitypes.Note) (apitypes.UpdateNote, error) {
				in := apitypes.UpdateNote{Version: cur.Version}
				if fl.Changed("title") {
					in.Title = &title
				}
				if fl.Changed("body") {
					b, err := a.readBody(body)
					if err != nil {
						return in, err
					}
					in.Body = &b
				}
				if fl.Changed("page-file") {
					page := ""
					if pageFile != "" {
						var err error
						if page, err = a.readPageFile(pageFile); err != nil {
							return in, err
						}
					}
					in.Page = &page
				}
				return in, nil
			}, "updated")
		},
	}
	f := cmd.Flags()
	f.StringVar(&title, "title", "", "New title")
	f.StringVarP(&body, "body", "b", "", "New body (- reads stdin)")
	f.StringVar(&pageFile, "page-file", "", "HTML page from a file (- reads stdin); empty removes it")
	return cmd
}

func newNotesArchive(a *app) *cobra.Command {
	var (
		undo bool
	)
	cmd := &cobra.Command{
		Use:   "archive <slug|id>",
		Short: "Archive a note (or restore it with --undo)",
		Long: `Archive a note so it drops out of 'note ls' and search. Nothing is deleted:
'note ls --archived' still lists it and --undo restores it.

Example:
  lorvik note archive old-runbook`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			status, done := "archived", "archived"
			if undo {
				status, done = "active", "restored"
			}
			return a.updateNote(cmd.Context(), args[0], func(cur apitypes.Note) (apitypes.UpdateNote, error) {
				return apitypes.UpdateNote{Version: cur.Version, Status: &status}, nil
			}, done)
		},
	}
	cmd.Flags().BoolVar(&undo, "undo", false, "Restore an archived note")
	return cmd
}
