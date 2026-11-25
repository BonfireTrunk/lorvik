package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

const (
	followMinBackoff = time.Second
	followMaxBackoff = 30 * time.Second
	// followStable is how long a connection must last for the next failure to
	// count as new rather than part of the same outage.
	followStable = 30 * time.Second
)

var errStreamClosed = errors.New("event stream closed")

// follower reads the inbox from an in-memory cursor and rereads it only when
// the event stream shows something new, so a quiet board costs no requests
// and the stored cursor stays where other commands left it.
type follower struct {
	c   *client.Client
	now func() time.Time
	// project is the one this chat works in, empty when none is bound.
	project string
	limit   int
	once    bool
	errOut  io.Writer
	print   func(ctx context.Context, wake []apitypes.Event) error
	// held, when set, ends a read that printed wake events with what the
	// chat holds. It never prints on its own: every line a listener prints
	// starts a turn in the chat.
	held    func(apitypes.Holding) error
	minWait time.Duration
	maxWait time.Duration
	stable  time.Duration
	reset   bool
	// renew keeps the token alive across a follow longer than it lives.
	renew func() error
}

func (a *app) followInbox(ctx context.Context, c *client.Client, limit int, once bool) error {
	c.Listener = true
	f := &follower{
		c: c, now: a.f.Time, project: a.optSlug(), limit: limit, once: once, errOut: a.errOut(),
		renew:   func() error { return a.renewAgent(c) },
		minWait: followMinBackoff, maxWait: followMaxBackoff, stable: followStable,
		print: func(ctx context.Context, wake []apitypes.Event) error {
			return a.printWake(ctx, wake)
		},
		held: a.printHeld,
	}
	return f.run(ctx)
}

// printWake writes one line per event and returns only once they are all out,
// because a harness reads stdout line by line.
func (a *app) printWake(ctx context.Context, wake []apitypes.Event) error {
	if a.asJSON {
		enc := json.NewEncoder(a.out())
		enc.SetEscapeHTML(false)
		for _, e := range wake {
			if err := enc.Encode(e); err != nil {
				return fmt.Errorf("write json: %w", err)
			}
		}
		return nil
	}
	a.resolve(ctx, eventsActors(wake)...)
	p := a.printer()
	for _, e := range wake {
		p.Line("%s", eventLine(p, e))
	}
	return nil
}

// run returns nil on interrupt or after --once has printed, and an error only
// when retrying cannot help (a rejected token, a bad request).
func (f *follower) run(ctx context.Context) error {
	cursor := f.c.Cursor
	if cursor == "" {
		head, err := f.c.EventHead(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		cursor = head
	}
	delay := f.minWait
	for {
		began := f.now()
		done, err := f.session(ctx, &cursor)
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err == nil {
			err = errStreamClosed
		}
		if permanent(err) {
			return err
		}
		if f.now().Sub(began) >= f.stable {
			delay = f.minWait
		}
		fmt.Fprintf(f.errOut, "lorvik: inbox --follow: %v; retrying in %s\n", err, delay)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		delay = min(delay*2, f.maxWait)
	}
}

// permanent reports a client error that a retry would repeat.
func permanent(err error) bool {
	ae, ok := errors.AsType[*client.APIError](err)
	return ok && ae.Status >= 400 && ae.Status < 500 &&
		ae.Status != http.StatusRequestTimeout && ae.Status != http.StatusTooManyRequests
}

// alreadyRead reports whether a streamed event lies at or before the cursor the
// catch-up reached. It compares positions, not event ids: the feed is ordered
// by transaction, so a later event can have a lower id. An event without a
// position is never treated as read.
func alreadyRead(cursor string, e apitypes.Event) bool {
	return e.Position != "" && newerCursor(cursor, e.Position) == cursor
}

// session catches up from cursor, then waits on one stream connection and
// rereads whenever an event newer than cursor arrives. The stream starts after
// cursor so an event written between the catch-up and the connection is
// replayed rather than lost.
func (f *follower) session(ctx context.Context, cursor *string) (bool, error) {
	var pos atomic.Pointer[string]
	track := func() {
		c := *cursor
		pos.Store(&c)
	}
	if done, err := f.drain(ctx, cursor); done || err != nil {
		return done, err
	}
	track()
	after := *cursor
	if after == "0" {
		after = ""
	}
	sctx, cancel := context.WithCancel(ctx)
	wake := make(chan struct{}, 1)
	finished := make(chan struct{})
	var streamErr error
	go func() {
		defer close(finished)
		streamErr = f.c.StreamEvents(sctx, "", after, func(e apitypes.Event) error {
			if seen := pos.Load(); seen != nil && alreadyRead(*seen, e) {
				return nil
			}
			select {
			case wake <- struct{}{}:
			default:
			}
			return nil
		})
	}()
	defer func() {
		cancel()
		<-finished
	}()
	for {
		select {
		case <-ctx.Done():
			return false, nil
		case <-finished:
			return false, streamErr
		case <-wake:
			if done, err := f.drain(ctx, cursor); done || err != nil {
				return done, err
			}
			track()
		}
	}
}

// drain reads every page after cursor and prints the wake events, then what
// the chat holds when any was printed. It reports true once --once has
// printed.
func (f *follower) drain(ctx context.Context, cursor *string) (bool, error) {
	if f.renew != nil {
		if err := f.renew(); err != nil {
			return false, err
		}
	}
	done, woke, err := f.pages(ctx, cursor)
	if err != nil || !woke || f.held == nil {
		return done, err
	}
	if h := f.c.LastUpdates().Holding; h != nil {
		return done, f.held(*h)
	}
	return done, nil
}

// pages is the loop of drain: done as drain reports it, woke when a wake
// event was printed.
func (f *follower) pages(ctx context.Context, cursor *string) (done, woke bool, err error) {
	for {
		in, err := f.c.Inbox(ctx, client.InboxParams{ChatProject: f.project, Cursor: *cursor, Limit: f.limit})
		if !f.reset && *cursor != "" && isFieldError(err, "cursor") {
			f.reset = true
			head, herr := f.c.EventHead(ctx)
			if herr != nil {
				return false, woke, herr
			}
			fmt.Fprintf(f.errOut, "lorvik: the stored inbox cursor %s names no event on this server; following from the newest event\n", *cursor)
			*cursor = head
			continue
		}
		if err != nil {
			return false, woke, err
		}
		var wake []apitypes.Event
		for _, e := range in.Events {
			if e.Wake {
				wake = append(wake, e)
			}
		}
		if len(wake) > 0 {
			if err := f.print(ctx, wake); err != nil {
				return false, woke, err
			}
			woke = true
		}
		moved := in.Cursor != "" && in.Cursor != *cursor
		if moved {
			*cursor = in.Cursor
		}
		if len(wake) > 0 && f.once {
			return true, woke, nil
		}
		if in.Remaining == 0 || !moved {
			return false, woke, nil
		}
	}
}
