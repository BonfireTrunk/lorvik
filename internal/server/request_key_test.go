package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// keyed sends a write with an Idempotency-Key and decodes a body of the wanted status.
func keyed[T any](e *testEnv, secret, path, key string, body any, want int) T {
	e.t.Helper()
	rec := e.do(secret, http.MethodPost, path, body, apitypes.HeaderIdempotencyKey, key)
	if rec.Code != want {
		e.t.Fatalf("POST %s with key %q = %d, want %d: %s", path, key, rec.Code, want, rec.Body)
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		e.t.Fatalf("decode POST %s: %v: %s", path, err, rec.Body)
	}
	return v
}

func (e *testEnv) keyedProblem(secret, path, key string, body any, want int) apitypes.Problem {
	e.t.Helper()
	rec := e.do(secret, http.MethodPost, path, body, apitypes.HeaderIdempotencyKey, key)
	if rec.Code != want {
		e.t.Fatalf("POST %s with key %q = %d, want %d: %s", path, key, rec.Code, want, rec.Body)
	}
	var p apitypes.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		e.t.Fatalf("decode problem: %v: %s", err, rec.Body)
	}
	return p
}

func (e *testEnv) comments(secret string, n int64) []apitypes.Comment {
	e.t.Helper()
	return call[[]apitypes.Comment](e, secret, http.MethodGet, cardPathOf(n, "/comments"), nil, http.StatusOK)
}

func (e *testEnv) cardCount() int {
	e.t.Helper()
	return len(call[apitypes.CardList](e, userSecret, http.MethodGet, "/projects/lorvik/cards?limit=200", nil, http.StatusOK).Items)
}

func TestKeyedCommentSentTwiceIsStoredOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(userSecret, "Talk")
	path := cardPathOf(c.Number, "/comments")
	body := apitypes.CreateComment{Body: "first thoughts"}
	first := keyed[apitypes.Comment](e, userSecret, path, "k-comment-1", body, http.StatusCreated)
	again := keyed[apitypes.Comment](e, userSecret, path, "k-comment-1", body, http.StatusCreated)
	if again != first {
		t.Errorf("second response = %+v, want the first, %+v", again, first)
	}
	if cs := e.comments(userSecret, c.Number); len(cs) != 1 || cs[0].ID != first.ID {
		t.Errorf("comments = %+v, want only %s", cs, first.ID)
	}
	if n := len(e.eventsOf("card.comment")); n != 1 {
		t.Errorf("comment events = %d, want 1", n)
	}
}

func TestKeyedCreateSentTwiceMakesOneCard(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	in := apitypes.CreateCard{Title: "File once"}
	first := keyed[apitypes.Card](e, userSecret, "/projects/lorvik/cards", "k-create-1", in, http.StatusCreated)
	again := keyed[apitypes.Card](e, userSecret, "/projects/lorvik/cards", "k-create-1", in, http.StatusCreated)
	if again.Number != first.Number || again.ID != first.ID {
		t.Errorf("second card = #%d %s, want #%d %s", again.Number, again.ID, first.Number, first.ID)
	}
	if n := e.cardCount(); n != 1 {
		t.Errorf("cards = %d, want 1", n)
	}
	if n := len(e.eventsOf("card.created")); n != 1 {
		t.Errorf("created events = %d, want 1", n)
	}
}

func TestKeyedTransitionRepeatGetsTheDoneCardWhereAnUnkeyedOneConflicts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.claim(userSecret, e.readyCard(userSecret, "Finish").Number)
	path := cardPathOf(c.Number, "/transition")
	body := apitypes.TransitionCard{Version: c.Version, Status: "done"}
	first := keyed[apitypes.Card](e, userSecret, path, "k-done-1", body, http.StatusOK)
	if first.Status != "done" {
		t.Fatalf("first status = %q, want done", first.Status)
	}
	again := keyed[apitypes.Card](e, userSecret, path, "k-done-1", body, http.StatusOK)
	if again.Status != "done" || again.Version != first.Version {
		t.Errorf("repeat = %q v%d, want done v%d", again.Status, again.Version, first.Version)
	}
	if n := len(e.eventsOf("card.status_changed")); n != 1 {
		t.Errorf("status events = %d, want 1", n)
	}
	e.problem(userSecret, http.MethodPost, path, body, http.StatusConflict)
}

// memberBecomes changes the member's org role.
func (e *testEnv) memberBecomes(role domain.Role) {
	e.t.Helper()
	if _, err := e.fs.PutMembership(context.Background(), domain.Membership{OrgID: e.org.ID, Actor: e.member, Role: role,
		Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		e.t.Fatal(err)
	}
}

// A queued write is sent again under its key after a lost reply, perhaps long
// after. The repeat is answered with what the first request made before the
// caller's right is looked at: the write happened, and saying so gives the
// caller nothing it did not make.
func TestAKeyedRepeatIsAnsweredThoughTheCallerLostTheRight(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	made := keyed[apitypes.Card](e, memberSecret, "/projects/lorvik/cards", "k-made", apitypes.CreateCard{Title: "Filed"}, http.StatusCreated)
	held := e.claim(memberSecret, e.readyCard(memberSecret, "Finish").Number)
	comments, transition := cardPathOf(held.Number, "/comments"), cardPathOf(held.Number, "/transition")
	note := keyed[apitypes.Comment](e, memberSecret, comments, "k-note", apitypes.CreateComment{Body: "nearly"}, http.StatusCreated)
	closing := apitypes.TransitionCard{Version: e.show(memberSecret, held.Number).Version, Status: "done"}
	done := keyed[apitypes.Card](e, memberSecret, transition, "k-done", closing, http.StatusOK)

	e.memberBecomes(domain.RoleViewer)
	e.problem(memberSecret, http.MethodPost, comments, apitypes.CreateComment{Body: "more"}, http.StatusForbidden)

	if again := keyed[apitypes.Card](e, memberSecret, "/projects/lorvik/cards", "k-made", apitypes.CreateCard{Title: "Filed"}, http.StatusCreated); again.ID != made.ID {
		t.Errorf("repeated create = card %s, want %s", again.ID, made.ID)
	}
	if again := keyed[apitypes.Comment](e, memberSecret, comments, "k-note", apitypes.CreateComment{Body: "nearly"}, http.StatusCreated); again.ID != note.ID {
		t.Errorf("repeated comment = %s, want %s", again.ID, note.ID)
	}
	if again := keyed[apitypes.Card](e, memberSecret, transition, "k-done", closing, http.StatusOK); again.Status != "done" || again.Version != done.Version {
		t.Errorf("repeated done = %q v%d, want done v%d", again.Status, again.Version, done.Version)
	}
}

// A keyed write the server refuses leaves its key free. The queue relies on
// it: a done refused for the missing claim is sent again under the same key
// once the card is claimed, and that send must be judged, not answered as a
// repeat of the refusal.
func TestARefusedKeyedWriteLeavesItsKeyFree(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	held := e.claim(userSecret, e.readyCard(userSecret, "Finish").Number)
	comments, transition := cardPathOf(held.Number, "/comments"), cardPathOf(held.Number, "/transition")

	e.memberBecomes(domain.RoleViewer)
	e.keyedProblem(memberSecret, "/projects/lorvik/cards", "k-made", apitypes.CreateCard{Title: "Filed"}, http.StatusForbidden)
	e.keyedProblem(memberSecret, comments, "k-note", apitypes.CreateComment{Body: "nearly"}, http.StatusForbidden)
	e.memberBecomes(domain.RoleMember)
	if made := keyed[apitypes.Card](e, memberSecret, "/projects/lorvik/cards", "k-made", apitypes.CreateCard{Title: "Filed"}, http.StatusCreated); made.Title != "Filed" {
		t.Errorf("create after the refusal = %+v", made)
	}
	keyed[apitypes.Comment](e, memberSecret, comments, "k-note", apitypes.CreateComment{Body: "nearly"}, http.StatusCreated)
	if cs := e.comments(memberSecret, held.Number); len(cs) != 1 || cs[0].Body != "nearly" {
		t.Errorf("comments = %+v, want the one sent after the refusal", cs)
	}

	e.keyedProblem(userSecret, transition, "k-done", apitypes.TransitionCard{Version: held.Version + 5, Status: "done"}, http.StatusConflict)
	if done := keyed[apitypes.Card](e, userSecret, transition, "k-done", apitypes.TransitionCard{Version: held.Version, Status: "done"}, http.StatusOK); done.Status != "done" {
		t.Errorf("done after the refusal = %q, want done", done.Status)
	}
}

func TestKeyIsPerActor(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(userSecret, "Talk")
	path := cardPathOf(c.Number, "/comments")
	a := keyed[apitypes.Comment](e, userSecret, path, "shared-key", apitypes.CreateComment{Body: "from the owner"}, http.StatusCreated)
	b := keyed[apitypes.Comment](e, memberSecret, path, "shared-key", apitypes.CreateComment{Body: "from the member"}, http.StatusCreated)
	d := keyed[apitypes.Comment](e, agentSecret, path, "shared-key", apitypes.CreateComment{Body: "from the agent"}, http.StatusCreated)
	if a.ID == b.ID || b.ID == d.ID || a.ID == d.ID {
		t.Errorf("comment ids %s %s %s, want three different", a.ID, b.ID, d.ID)
	}
	if n := len(e.eventsOf("card.comment")); n != 3 {
		t.Errorf("comment events = %d, want 3", n)
	}
}

func TestKeyUsedForAnotherRequestIsRefusedAndWritesNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(userSecret, "One")
	other := e.readyCard(userSecret, "Two")
	keyed[apitypes.Comment](e, userSecret, cardPathOf(c.Number, "/comments"), "k-reuse", apitypes.CreateComment{Body: "hello"}, http.StatusCreated)
	cards := e.cardCount()
	tests := []struct {
		name string
		path string
		body any
	}{
		{"a create", "/projects/lorvik/cards", apitypes.CreateCard{Title: "Sneaky"}},
		{"a comment on another card", cardPathOf(other.Number, "/comments"), apitypes.CreateComment{Body: "elsewhere"}},
		{"a transition", cardPathOf(c.Number, "/transition"), apitypes.TransitionCard{Version: c.Version, Status: "done"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := e.keyedProblem(userSecret, tt.path, "k-reuse", tt.body, http.StatusUnprocessableEntity)
			if !strings.Contains(p.Detail, apitypes.HeaderIdempotencyKey) {
				t.Errorf("detail = %q, want it to name the header", p.Detail)
			}
		})
	}
	if n := e.cardCount(); n != cards {
		t.Errorf("cards = %d, want %d", n, cards)
	}
	if cs := e.comments(userSecret, other.Number); len(cs) != 0 {
		t.Errorf("comments on the other card = %+v, want none", cs)
	}
	if got := e.show(userSecret, c.Number); got.Status != "ready" {
		t.Errorf("card status = %q, want ready", got.Status)
	}
	if n := len(e.eventsOf("card.comment")); n != 1 {
		t.Errorf("comment events = %d, want 1", n)
	}
}

func TestInvalidKeyIsRefusedAndWritesNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(userSecret, "Talk")
	tests := []struct{ name, key string }{
		{"65 characters", strings.Repeat("a", 65)},
		{"a space", "has space"},
		{"a slash", "a/b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := e.keyedProblem(userSecret, cardPathOf(c.Number, "/comments"), tt.key, apitypes.CreateComment{Body: "x"}, http.StatusUnprocessableEntity)
			if !strings.Contains(p.Detail, apitypes.HeaderIdempotencyKey) {
				t.Errorf("detail = %q, want it to name the header", p.Detail)
			}
			e.keyedProblem(userSecret, "/projects/lorvik/cards", tt.key, apitypes.CreateCard{Title: "x"}, http.StatusUnprocessableEntity)
		})
	}
	if cs := e.comments(userSecret, c.Number); len(cs) != 0 {
		t.Errorf("comments = %+v, want none", cs)
	}
	if n := e.cardCount(); n != 1 {
		t.Errorf("cards = %d, want 1", n)
	}
}

func TestKeyIsIgnoredOutsideTheThreeRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(userSecret, "Take")
	path := cardPathOf(c.Number, "/claim")
	for _, key := range []string{"not a valid key", "k-claim"} {
		rec := e.do(userSecret, http.MethodPost, path, apitypes.ClaimCard{}, apitypes.HeaderIdempotencyKey, key)
		if rec.Code != http.StatusOK {
			t.Fatalf("claim with key %q = %d, want 200: %s", key, rec.Code, rec.Body)
		}
		call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(c.Number, "/release"), apitypes.ClaimCard{}, http.StatusOK)
	}
}
