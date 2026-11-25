package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

// cursorView is the --json shape when --from now only moves the cursor.
type cursorView struct {
	Cursor string `json:"cursor"`
}

func newInbox(a *app) *cobra.Command {
	var (
		from      string
		limit     int
		peek      bool
		follow    bool
		once      bool
		exitOnEOF bool
	)
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Show what others did since your last look",
		Long: `Print events by other actors since your cursor, your own live claims
(renewed by this call), the number of open questions and every live lock. Each event is one
line: time, then a sentence naming who did what.

An agent's claims are split: "my claims (renewed)" are this session's;
"other sessions' claims (not renewed)" were taken by other chats
under the same agent token and are not yours to continue. A human has one list.
Each claim line ends with the time left on its lease.

The server keeps no read position. The cursor lives in your user config,
keyed by token, and is sent with every request; the "N unread" line other
commands print on stderr counts events after it. A normal run advances it past
what it examined, your own events included. --peek lists without advancing.
Each chat keeps its own position; a chat with none starts from the newest
position any chat of your token has stored. Positions idle for 7 days are
forgotten.

--from picks where to read from instead of the stored cursor:
  now      set the cursor to the newest event and list nothing
  oldest   read from the very beginning
  <id>     read events after this event id or position
Passing --project narrows the listing to one project and never advances the
cursor, because events from other projects would be skipped unseen.

With -Q and nothing new, inbox prints nothing at all, which keeps hook output
out of an agent's context when there is nothing to say.

--follow keeps running and prints one line per event that concerns this chat,
as it happens: an event on a card this chat holds or created,
or on a card assigned to you in this chat's project (--project, LORVIK_PROJECT
or .lorvik.toml; any project when none is bound) unless another of your chats
holds or created it. It starts at the stored cursor without moving it (at the newest event
when there is no usable one), keeps its own position in memory, and rereads the inbox only when the event stream shows a new event,
never on a timer. If the stream or a request fails it says so on stderr, retries
with a pause that doubles from 1s to 30s and, once back, reads what it missed.
Nothing it does renews this chat's claims: a listener running is not the chat
working. A batch that printed an event ends with the cards this chat holds,
one line each ("holding  #42  demo  Fix login  12m left"); it prints them at no
other time. --once exits 0 after the first batch that held such an event. With --json each line is one
Event object, and the held cards are one last line {"holding": ...}. Ctrl-C exits 0. --follow does not take --from.
--exit-on-eof also exits 0 when stdin reaches end of file: an adapter that
starts the listener keeps a pipe to its stdin open, so the listener ends with
the adapter even when the adapter is killed.

Examples:
  lorvik inbox
  lorvik inbox --peek -L 30
  lorvik inbox --from now
  lorvik inbox -Q -L 5          (from a harness hook)
  lorvik inbox --follow --once  (block until something concerns this chat)`,
		Annotations: map[string]string{annotationNoNotice: "1"},
		Args:        usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if once && !follow {
				return cmdutil.Usagef("--once needs --follow")
			}
			if exitOnEOF && !follow {
				return cmdutil.Usagef("--exit-on-eof needs --follow")
			}
			if follow && from != "" {
				return cmdutil.Usagef("--follow reads from the stored cursor; drop --from")
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if follow {
				if exitOnEOF {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					defer cancel()
					go func() {
						_, _ = io.Copy(io.Discard, a.f.IOStreams.In)
						cancel()
					}()
				}
				return a.followInbox(ctx, c, limit, once)
			}
			if from == "now" {
				return a.inboxHead(ctx, c)
			}
			cursor := c.Cursor
			switch {
			case from == "oldest":
				cursor = "0"
			case from != "":
				if !isID(from) && !isPosition(from) {
					return cmdutil.Usagef("--from %q: want now, oldest, an event id or a position", from)
				}
				cursor = from
			}
			chat := a.optSlug()
			in, err := c.Inbox(ctx, client.InboxParams{Project: a.project, ChatProject: chat, Cursor: cursor, Limit: limit})
			if from == "" && isFieldError(err, "cursor") {
				// A stored cursor from another database (a reset, another server)
				// names no event here; only the oldest position is still meaningful.
				fmt.Fprintf(a.errOut(), "lorvik: the stored inbox cursor %s names no event on this server; reading from the oldest\n", cursor)
				cursor = "0"
				in, err = c.Inbox(ctx, client.InboxParams{Project: a.project, ChatProject: chat, Cursor: cursor, Limit: limit})
			}
			if err != nil {
				return err
			}
			store := !peek && a.project == "" && in.Cursor != "" && in.Cursor != cursor
			if store {
				if err := a.saveCursor(in.Cursor); err != nil {
					return err
				}
			}
			if a.quiet && !a.asJSON && len(in.Events) == 0 {
				return nil
			}
			a.resolve(ctx, inboxActors(in)...)
			return a.emit(in, func(p *cmdutil.Printer) { printInbox(p, in) })
		},
	}
	f := cmd.Flags()
	f.StringVar(&from, "from", "", "Read from now, oldest or after this event id or position instead of the stored cursor")
	f.IntVarP(&limit, "limit", "L", defaultLimit, "Maximum events to print")
	f.BoolVar(&peek, "peek", false, "List without advancing the stored cursor")
	f.BoolVar(&follow, "follow", false, "Stay running and print only events that concern this chat, as they happen")
	f.BoolVar(&once, "once", false, "With --follow: exit after the first batch that held such an event")
	f.BoolVar(&exitOnEOF, "exit-on-eof", false, "With --follow: exit when stdin reaches end of file")
	return cmd
}

// inboxHead stores the position of the newest event as the cursor without listing
// anything, so later runs report only what happens from now on.
func (a *app) inboxHead(ctx context.Context, c *client.Client) error {
	head, err := c.EventHead(ctx)
	if err != nil {
		return err
	}
	if err := a.saveCursor(head); err != nil {
		return err
	}
	return a.emit(cursorView{Cursor: head}, func(p *cmdutil.Printer) { p.Line("cursor set to event %s", head) })
}

func inboxActors(in apitypes.Inbox) []apitypes.Actor {
	return append(append(append(eventsActors(in.Events), briefsActors(in.Claims)...), briefsActors(in.ClaimsOtherSessions)...), lockActors(in.Locks)...)
}

func printInbox(p *cmdutil.Printer, in apitypes.Inbox) {
	if len(in.Events) == 0 {
		p.Line("no new events")
	}
	var mine, others []apitypes.Event
	for _, e := range in.Events {
		if e.Mine {
			mine = append(mine, e)
		} else {
			others = append(others, e)
		}
	}
	for _, g := range []struct {
		heading string
		events  []apitypes.Event
	}{{"on your cards:", mine}, {"others:", others}} {
		if len(g.events) == 0 {
			continue
		}
		p.Line("%s", g.heading)
		for _, e := range g.events {
			p.Line("%s", eventLine(p, e))
		}
	}
	if in.Remaining > 0 {
		n := cmdutil.RemainingText(in.Remaining)
		p.Line("remaining %s, run lorvik inbox again", n)
	}
	if len(in.Claims) > 0 {
		p.Line("my claims (renewed):")
		for _, c := range in.Claims {
			p.Line("  %s", claimLine(p, c))
		}
	}
	if len(in.ClaimsOtherSessions) > 0 {
		p.Line("other sessions' claims (not renewed):")
		for _, c := range in.ClaimsOtherSessions {
			p.Line("  %s", claimLine(p, c))
		}
	}
	if in.OpenQuestions > 0 {
		p.Line("open questions: %d", in.OpenQuestions)
	}
	printLocks(p, in.Locks)
}
