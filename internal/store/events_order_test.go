//go:build integration

package store

import (
	"math"
	"slices"
	"strconv"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// Feed order is by transaction id as a number. The feed queries hand the id
// out as text, and as text "9" sorts after "10": a feed whose ids differ in
// length must still page in number order and name its newest event as the
// latest. The ids are set by hand, far below any a live transaction holds,
// because a database reaches the next length only every tenfold of its age.
func TestFeedOrdersTransactionIdsOfDifferentLengthsAsNumbers(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	subj := domain.Subject{Type: domain.SubjectCard, ID: gen.New(), Number: 1}
	want := []uint64{8, 9, 10, 11}
	for _, xid := range want {
		ev, err := domain.NewEvent(gen.New(), e.org.ID, &e.project.ID, domain.EventCardUpdated, e.agent, subj, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.st.AppendEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
		if _, err := e.st.db.Exec(ctx, "UPDATE events SET xid = $1::text::xid8 WHERE org_id = $2 AND id = $3", strconv.FormatUint(xid, 10), int64(e.org.ID), int64(ev.ID)); err != nil {
			t.Fatalf("set xid %d: %v", xid, err)
		}
	}
	xids := func(evs []domain.Event) []uint64 {
		out := make([]uint64, len(evs))
		for i, ev := range evs {
			out[i] = ev.Xid
		}
		return out
	}
	page := func(name string, f query.EventFilter, want []uint64, wantRemaining int) []domain.Event {
		t.Helper()
		evs, remaining, err := e.st.ListEvents(ctx, e.org.ID, f)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := xids(evs); !slices.Equal(got, want) || remaining != wantRemaining {
			t.Errorf("%s = xids %v and %d remaining, want %v and %d", name, got, remaining, want, wantRemaining)
		}
		return evs
	}
	position := func(ev domain.Event) query.EventPosition { return query.EventPosition{Xid: ev.Xid, ID: ev.ID} }

	page("the whole feed", query.EventFilter{Limit: 10}, want, 0)
	first := page("the first page", query.EventFilter{Limit: 2}, want[:2], 2)
	if len(first) == 2 {
		page("the page after it", query.EventFilter{After: position(first[1]), Limit: 2}, want[2:], 0)
	}
	newest := page("the newest page", query.EventFilter{Before: query.EventPosition{ID: math.MaxInt64}, Limit: 3}, want[1:], 1)
	if len(newest) == 3 {
		page("the page before it", query.EventFilter{Before: position(newest[0]), Limit: 3}, want[:1], 0)
	}
	if latest, err := e.st.LatestEventPosition(ctx, e.org.ID, query.EventFilter{}); err != nil || latest.Xid != 11 {
		t.Errorf("latest = %+v, %v; want the event at xid 11", latest, err)
	}
}
