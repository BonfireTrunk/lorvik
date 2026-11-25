package server

import (
	"math"
	"net/http"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

func TestEventPositionsCompareAsStringsInFeedOrder(t *testing.T) {
	t.Parallel()
	ordered := []query.EventPosition{
		{Xid: 1, ID: 900},
		{Xid: 2, ID: 5},
		{Xid: 2, ID: 6},
		{Xid: 10, ID: 1},
		{Xid: 10, ID: math.MaxInt64},
		{Xid: math.MaxUint64, ID: 1},
	}
	for i := 1; i < len(ordered); i++ {
		a, b := eventPosition(ordered[i-1]), eventPosition(ordered[i])
		if a >= b {
			t.Errorf("%q >= %q, want %+v before %+v", a, b, ordered[i-1], ordered[i])
		}
	}
}

func TestParseEventCursor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want query.EventPosition
		ok   bool
	}{
		{"", query.EventPosition{}, true},
		{"0", query.EventPosition{}, true},
		{"112288221999765368", query.EventPosition{ID: 112288221999765368}, true},
		{eventPosition(query.EventPosition{Xid: 42, ID: 7}), query.EventPosition{Xid: 42, ID: 7}, true},
		{"42-7", query.EventPosition{Xid: 42, ID: 7}, true},
		{"abc", query.EventPosition{}, false},
		{"-7", query.EventPosition{}, false},
		{"0-7", query.EventPosition{}, false},
		{"42-0", query.EventPosition{}, false},
		{"42-", query.EventPosition{}, false},
		{"42-7-1", query.EventPosition{}, false},
	} {
		got, err := parseEventCursor("cursor", tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("parseEventCursor(%q) = %+v, %v; want %+v, ok %v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

// A transaction that took its xid early can write a lower event id and
// become visible after events with higher ids. Clients that kept the highest
// id would skip it; positions deliver it once.
func TestFeedDeliversALaterEventWithALowerIDOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.readyCard(userSecret, "first")
	in := call[apitypes.Inbox](e, agentSecret, http.MethodGet, "/inbox", nil, http.StatusOK)
	first := e.lastEvent()
	late := first
	late.ID = 1
	if err := e.fs.AppendEvent(t.Context(), late); err != nil {
		t.Fatal(err)
	}
	next := call[apitypes.Inbox](e, agentSecret, http.MethodGet, "/inbox?cursor="+in.Cursor, nil, http.StatusOK)
	if len(next.Events) != 1 || next.Events[0].ID != late.ID.String() {
		t.Fatalf("inbox after %q = %+v, want only the late event", in.Cursor, next.Events)
	}
	if next.Cursor != next.Events[0].Position || next.Cursor <= in.Cursor {
		t.Errorf("cursor %q after %q, want the late event's position, past the old cursor", next.Cursor, in.Cursor)
	}
	listed := call[apitypes.EventList](e, userSecret, http.MethodGet, "/events?after="+eventPosition(positionOf(first)), nil, http.StatusOK)
	if len(listed.Items) != 1 || listed.Items[0].ID != late.ID.String() {
		t.Errorf("events after the first = %+v, want only the late event", listed.Items)
	}
	again := call[apitypes.Inbox](e, agentSecret, http.MethodGet, "/inbox?cursor="+next.Cursor, nil, http.StatusOK)
	if len(again.Events) != 0 {
		t.Errorf("inbox after the late event = %+v, want nothing", again.Events)
	}
}

// A cursor saved before positions existed is a bare event id; the server
// still reads it and answers with positions from then on.
func TestInboxAcceptsAnEventIDCursorFromAnOlderClient(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.readyCard(userSecret, "first")
	old := e.lastEvent().ID.String()
	e.readyCard(userSecret, "second")
	in := call[apitypes.Inbox](e, agentSecret, http.MethodGet, "/inbox?cursor="+old, nil, http.StatusOK)
	if len(in.Events) != 1 || in.Cursor != in.Events[0].Position {
		t.Errorf("inbox after id %s = %+v cursor %q, want the second event and its position", old, in.Events, in.Cursor)
	}
	quiet := call[apitypes.Inbox](e, userSecret, http.MethodGet, "/inbox?project=lorvik&cursor="+old, nil, http.StatusOK)
	if len(quiet.Events) != 0 || quiet.Cursor != old {
		t.Errorf("own events only = %+v cursor %q, want nothing new and the id echoed", quiet.Events, quiet.Cursor)
	}
}

// A position saved against another database names no event here: the server
// refuses it instead of treating it as a bound past the whole feed.
func TestInboxRefusesAPositionThatNamesNoEventOfTheOrg(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.readyCard(userSecret, "first")
	real := positionOf(e.lastEvent())
	for _, bad := range []query.EventPosition{
		{Xid: real.Xid + 1000, ID: real.ID},
		{Xid: real.Xid, ID: real.ID + 1},
	} {
		e.problem(agentSecret, http.MethodGet, "/inbox?cursor="+eventPosition(bad), nil, http.StatusUnprocessableEntity)
		e.problem(userSecret, http.MethodGet, "/events?after="+eventPosition(bad), nil, http.StatusUnprocessableEntity)
	}
	call[apitypes.Inbox](e, agentSecret, http.MethodGet, "/inbox?cursor="+eventPosition(real), nil, http.StatusOK)
}
