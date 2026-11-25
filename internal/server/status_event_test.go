package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

// Every door that moves a card records the move the same way: from, to and
// kind, with to the status the card landed in. Here a direct move, a block,
// an unblock, a duplicate drop and a cascade drop.
func TestStatusChangedEventsCarryTheMove(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	blocker := e.readyCard(userSecret, "blocker")
	target := e.readyCard(userSecret, "target")
	e.claim(userSecret, target.Number)
	call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(blocker.Number, "/links"), apitypes.LinkCards{Kind: "blocks", To: target.Number}, http.StatusOK)
	e.claim(userSecret, blocker.Number)
	e.move(userSecret, blocker.Number, apitypes.TransitionCard{Status: "done"})
	dup := e.readyCard(userSecret, "copy of target")
	survivor := target.Number
	e.move(userSecret, dup.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "duplicate", Duplicate: &survivor})
	story := e.newCard(userSecret, apitypes.CreateCard{Title: "story", Kind: "story", Status: "ready"})
	e.newCard(userSecret, apitypes.CreateCard{Title: "child", Status: "ready", Parent: &story.Number})
	e.move(userSecret, story.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "obsolete", Cascade: true})

	type move struct{ from, to string }
	want := map[string]move{
		"blocked_by": {"working", "waiting"}, "unblocked_by": {"waiting", "ready"},
		"duplicate_of": {"ready", "dropped"}, "cascade_of": {"ready", "dropped"},
	}
	seen := map[string]bool{}
	for _, ev := range call[apitypes.EventList](e, userSecret, http.MethodGet, "/events?limit=200", nil, http.StatusOK).Items {
		if ev.Type != "card.status_changed" {
			continue
		}
		var pl map[string]any
		if err := json.Unmarshal(ev.Payload, &pl); err != nil {
			t.Fatalf("payload of %s: %v", ev.ID, err)
		}
		for _, k := range []string{"from", "to", "kind"} {
			if s, _ := pl[k].(string); s == "" {
				t.Errorf("event on #%d lacks %s: %v", ev.Subject.Number, k, pl)
			}
		}
		for cause, m := range want {
			if _, ok := pl[cause]; ok {
				seen[cause] = true
				if pl["from"] != m.from || pl["to"] != m.to {
					t.Errorf("%s moved %v to %v, want %s to %s", cause, pl["from"], pl["to"], m.from, m.to)
				}
			}
		}
	}
	for cause := range want {
		if !seen[cause] {
			t.Errorf("no status change recorded with %s", cause)
		}
	}
}

// A cursor is an event of the caller's org. Any other id is refused on its
// field rather than read as the start of the feed, which would replay
// everything, or as a position, which no longer exists for an unknown id.
func TestUnknownEventCursorIsRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.readyCard(userSecret, "an event to page over")
	for _, path := range []string{"/events?after=123456789", "/events?before=123456789", "/inbox?cursor=123456789", "/events/stream?after=123456789"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			pr := e.problem(userSecret, http.MethodGet, path, nil, http.StatusUnprocessableEntity)
			if !strings.Contains(pr.Detail+fmt.Sprint(pr.Errors), "no such event") {
				t.Errorf("problem = %+v, want the unknown-cursor message", pr)
			}
		})
	}
}

// A stream answers 200 once it opens, so its start is checked first: a bad
// project is a problem response too, not an empty stream.
func TestEventStreamRefusesAnUnknownProjectBeforeOpening(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	rec := e.do(userSecret, http.MethodGet, "/events/stream?project=nope", nil)
	if rec.Code != http.StatusNotFound || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/problem+json") {
		t.Errorf("stream on an unknown project = %d %q: %s, want a 404 problem", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
}
