package cli

import (
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

type syncItem struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Card    int64  `json:"card,omitempty"`
	Title   string `json:"title,omitempty"`
	// Number is the card a sent add made.
	Number  int64  `json:"number,omitempty"`
	Status  int    `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
}

type syncView struct {
	Sent []syncItem `json:"sent"`
	// Refused are the entries the server refused in this run.
	Refused []syncItem `json:"refused"`
	// Left counts this caller's entries still queued that this run neither
	// sent nor refused: refusals from earlier runs, and entries not reached
	// after a stop.
	Left int `json:"left"`
	// Others counts entries of other owners, servers or projects.
	Others int `json:"others"`
}

func itemOf(e outboxEntry) syncItem {
	return syncItem{ID: e.ID, Command: e.Command, Card: e.Card, Title: e.Title}
}

func newSync(a *app) *cobra.Command {
	var (
		list bool
		drop string
	)
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Send the writes queued while the server was unreachable",
		Long: `When the server cannot be reached, 'comment -m', 'add' and 'done' are stored
on this machine instead of failing. 'sync' sends them, in order, as whoever
queued them. A chat's own entries are also sent by its next command that talks
to the server; 'sync' is for the rest, and for an entry nobody's next command
will send.

An entry the server refuses (the card is closed, it is held by someone else,
a right is missing) stays in the queue marked refused, and 'sync' goes on with
the next one. A refused entry is never sent again: read it with --list and
remove it with --drop. When the server cannot be reached again, 'sync' stops
and exits 1, leaving that entry and the rest queued.

Entries of other agents or people, and of other servers, are left alone and
counted.

Examples:
  lorvik sync
  lorvik sync --list
  lorvik sync --drop 3J7K2QX4NVD5B6WYT0HMP9R8LC`,
		Annotations: map[string]string{annotationNoNotice: "1"},
		Args:        usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			a.noFlush = true
			if list && drop != "" {
				return cmdutil.Usagef("--list and --drop are exclusive: pass one")
			}
			switch {
			case list:
				return a.syncList()
			case drop != "":
				return a.syncDrop(drop)
			}
			return a.syncSend(cmd)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&list, "list", false, "Print the queued entries without sending")
	f.StringVar(&drop, "drop", "", "Remove the queued entry with this id, whoever queued it")
	return cmd
}

// syncOutbox is the queue of the bound project on the resolved server.
func (a *app) syncOutbox() (o outbox, server, slug string, err error) {
	if slug, err = a.slug(); err != nil {
		return outbox{}, "", "", err
	}
	if server, err = a.serverURL(); err != nil {
		return outbox{}, "", "", err
	}
	o, err = a.outboxAt(server, slug)
	return o, server, slug, err
}

func (a *app) syncList() error {
	o, server, slug, err := a.syncOutbox()
	if err != nil {
		return err
	}
	all, err := o.read()
	if err != nil {
		return err
	}
	o.warnBad(a.errOut(), all)
	var es []outboxEntry
	for _, e := range all {
		if e.raw == "" {
			es = append(es, e)
		}
	}
	owner, _ := a.outboxOwner()
	if a.asJSON {
		if es == nil {
			es = []outboxEntry{}
		}
		return cmdutil.WriteJSON(a.out(), es)
	}
	p := a.printer()
	if len(es) == 0 {
		p.Line("nothing queued")
		return nil
	}
	for _, e := range es {
		line := fmt.Sprintf("%s  %s  %s  %s", e.ID, p.Ago(e.At), e.Session, e.what())
		if e.Owner != owner || !sameServer(e.Server, server) || e.Project != slug {
			line += "  (other)"
		}
		if e.Refused != nil {
			line += fmt.Sprintf("  refused: %s", refusalText(*e.Refused))
		}
		p.Line("%s", line)
	}
	return nil
}

// refusalText is the status and the server's message; a refusal that came
// from the entry's own inputs has no status.
func refusalText(r outboxRefusal) string {
	if r.Status == 0 {
		return r.Message
	}
	return fmt.Sprintf("%d %s", r.Status, r.Message)
}

func (a *app) syncDrop(id string) error {
	o, _, _, err := a.syncOutbox()
	if err != nil {
		return err
	}
	notFound := &cmdutil.ExitError{Code: cmdutil.ExitNotFound, Err: fmt.Errorf("no queued entry %s; lorvik sync --list shows them", id)}
	if !o.exists() {
		return notFound
	}
	var dropped outboxEntry
	err = o.edit(func(es []outboxEntry) ([]outboxEntry, bool, error) {
		for i, e := range es {
			if e.raw == "" && e.ID == id {
				dropped = e
				return slices.Delete(es, i, i+1), true, nil
			}
		}
		return es, false, notFound
	})
	if err != nil {
		return err
	}
	if a.asJSON {
		return cmdutil.WriteJSON(a.out(), struct {
			Dropped bool `json:"dropped"`
			syncItem
		}{true, itemOf(dropped)})
	}
	a.printer().Line("dropped  %s", dropped.what())
	return nil
}

func (a *app) syncSend(cmd *cobra.Command) error {
	o, server, slug, err := a.syncOutbox()
	if err != nil {
		return err
	}
	view := syncView{Sent: []syncItem{}, Refused: []syncItem{}}
	p := a.printer()
	var stop error
	earlier := 0
	if o.exists() {
		owner, ok := a.outboxOwner()
		if !ok {
			return errors.New("cannot tell whose entries are yours: the agent has no id yet")
		}
		start, err := o.read()
		if err != nil {
			return err
		}
		o.warnBad(a.errOut(), start)
		c, _, err := a.api()
		if err != nil {
			return err
		}
		mine := func(e outboxEntry) bool {
			return e.raw == "" && e.Owner == owner && sameServer(e.Server, server) && e.Project == slug
		}
		refusedNow := map[string]bool{}
		stop = a.drain(cmd.Context(), c, o, slug, func(e outboxEntry) bool { return mine(e) && e.Refused == nil }, func(out sendOutcome) {
			it := itemOf(out.entry)
			if out.refusal != nil {
				refusedNow[out.entry.ID] = true
				it.Status, it.Message = out.refusal.Status, out.refusal.Message
				view.Refused = append(view.Refused, it)
				if !a.asJSON {
					p.Line("refused  %s: %s", out.entry.what(), out.refusal.Message)
				}
				return
			}
			it.Number = out.number
			view.Sent = append(view.Sent, it)
			if !a.asJSON {
				p.Line("sent     %s", sentWhat(out))
			}
		})
		rest, err := o.read()
		if err != nil {
			return err
		}
		for _, e := range rest {
			switch {
			case e.raw != "":
			case !mine(e):
				view.Others++
			case !refusedNow[e.ID]:
				view.Left++
				if e.Refused != nil {
					earlier++
				}
			}
		}
	}
	if se, ok := errors.AsType[*stopError](stop); ok {
		fmt.Fprintf(a.errOut(), "lorvik: %v\n", se)
		stop = &cmdutil.ExitError{Code: cmdutil.ExitCode(se.err), Err: errors.Join(cmdutil.ErrSilent, se)}
	}
	if a.asJSON {
		if err := cmdutil.WriteJSON(a.out(), view); err != nil {
			return err
		}
		return stop
	}
	if stop != nil {
		return stop
	}
	if earlier > 0 {
		what := "1 refused entry"
		if earlier > 1 {
			what = fmt.Sprintf("%d refused entries", earlier)
		}
		p.Line("kept     %s; lorvik sync --list shows them, --drop removes one", what)
	}
	if view.Others > 0 {
		what := "1 entry"
		if view.Others > 1 {
			what = fmt.Sprintf("%d entries", view.Others)
		}
		p.Line("waiting  %s of other agents or people", what)
	}
	if len(view.Sent)+len(view.Refused)+view.Left+view.Others == 0 {
		p.Line("nothing queued")
	}
	return nil
}
