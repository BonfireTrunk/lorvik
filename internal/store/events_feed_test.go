//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// A transaction that takes its xid early but writes its event late gets the
// higher event id and can commit first. A reader that pages past it must
// still receive the lower-id event of the transaction that commits after.
// Other tests run in parallel and hold transactions open, which delays
// visibility, so every read polls until it settles.
func TestFeedNeverSkipsAnEventThatCommitsLater(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	subj := domain.Subject{Type: domain.SubjectCard, ID: gen.New(), Number: 1}
	newEvent := func() domain.Event {
		ev, err := domain.NewEvent(gen.New(), e.org.ID, &e.project.ID, domain.EventCardUpdated, e.agent, subj, nil)
		if err != nil {
			t.Fatal(err)
		}
		return ev
	}
	takeXid := func(tx *Store) error {
		_, err := tx.db.Exec(ctx, "SELECT pg_current_xact_id()")
		return err
	}

	aHasXid, bAppended, bCommit := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var early, late domain.Event
	bDone := make(chan error, 1)
	go func() {
		<-aHasXid
		bDone <- e.st.WithTx(ctx, func(tx *Store) error {
			if err := takeXid(tx); err != nil {
				return err
			}
			late = newEvent()
			if err := tx.AppendEvent(ctx, late); err != nil {
				return err
			}
			close(bAppended)
			select {
			case <-bCommit:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	err := e.st.WithTx(ctx, func(tx *Store) error {
		if err := takeXid(tx); err != nil {
			return err
		}
		close(aHasXid)
		select {
		case <-bAppended:
		case <-ctx.Done():
			return ctx.Err()
		}
		early = newEvent()
		return tx.AppendEvent(ctx, early)
	})
	if err != nil {
		t.Fatalf("first transaction: %v", err)
	}
	if early.ID <= late.ID {
		t.Fatalf("setup: the first transaction's event %v should have the higher id than %v", early.ID, late.ID)
	}

	read := func(after query.EventPosition, want int) []domain.Event {
		t.Helper()
		for {
			evs, _, err := e.st.ListEvents(ctx, e.org.ID, query.EventFilter{After: after, Limit: 10})
			if err != nil {
				t.Fatalf("list events after %v: %v", after, err)
			}
			if len(evs) >= want {
				return evs
			}
			select {
			case <-ctx.Done():
				t.Fatalf("after %v: got %d events, want %d before the deadline", after, len(evs), want)
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	first := read(query.EventPosition{}, 1)
	if len(first) != 1 || first[0].ID != early.ID || first[0].Xid == 0 {
		t.Fatalf("while the second transaction is open the feed = %v, want only %v with its xid", first, early.ID)
	}
	close(bCommit)
	if err := <-bDone; err != nil {
		t.Fatalf("second transaction: %v", err)
	}
	next := read(query.EventPosition{Xid: first[0].Xid, ID: first[0].ID}, 1)
	if next[0].ID != late.ID {
		t.Errorf("after the cursor the feed = %v, want the lower-id event %v that committed later", next[0].ID, late.ID)
	}
	if latest, err := e.st.LatestEventPosition(ctx, e.org.ID, query.EventFilter{}); err != nil || latest.ID != late.ID || latest.Xid <= first[0].Xid {
		t.Errorf("latest = %v, %v; want %v, the last event in feed order", latest, err, late.ID)
	}
}
