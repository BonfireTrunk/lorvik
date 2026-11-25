package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

func decodeBody[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return v
}

func subscriptionPath(n int64) string { return cardPathOf(n, "/subscription") }

func subscribe(e *testEnv, session string, n int64) int {
	e.t.Helper()
	return e.do(agentSecret, http.MethodPut, subscriptionPath(n), nil, apitypes.HeaderSession, session).Code
}

func unsubscribe(e *testEnv, session string, n int64) int {
	e.t.Helper()
	return e.do(agentSecret, http.MethodDelete, subscriptionPath(n), nil, apitypes.HeaderSession, session).Code
}

func subscriptionList(e *testEnv, session string) []apitypes.Subscription {
	e.t.Helper()
	rec := e.do(agentSecret, http.MethodGet, "/me/subscriptions", nil, apitypes.HeaderSession, session)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("GET /me/subscriptions = %d: %s", rec.Code, rec.Body)
	}
	return decodeBody[apitypes.SubscriptionList](e.t, rec.Body.Bytes()).Items
}

// wakesOnComment reports whether the chat's inbox, read from the start, wakes
// it for the comment on card n.
func wakesOnComment(t *testing.T, e *testEnv, session string, n int64) (wake, mine bool) {
	t.Helper()
	for _, ev := range inboxAs(t, e, agentSecret, session, "?cursor=0").Events {
		if ev.Type == "card.comment" && ev.Subject.Number == n {
			return ev.Wake, ev.Mine
		}
	}
	t.Fatalf("no card.comment on #%d in the inbox of %q", n, session)
	return false, false
}

func TestASubscribedCardsCommentWakesOnlyTheSubscribedChatAndItsSubagents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		session string
		want    bool
	}{
		{"the chat that is subscribed", "lead", true},
		{"its subagent shares the subscription", "lead-c2", true},
		{"another chat of the same agent", "other", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			card := e.readyCard(userSecret, "waiting on the owner")
			if code := subscribe(e, "lead", card.Number); code != http.StatusNoContent {
				t.Fatalf("subscribe = %d, want 204", code)
			}
			comment(e, userSecret, card.Number, "go ahead")
			wake, mine := wakesOnComment(t, e, tt.session, card.Number)
			if wake != tt.want || mine != tt.want {
				t.Errorf("chat %q: wake %v mine %v, want both %v", tt.session, wake, mine, tt.want)
			}
		})
	}
}

func TestUnsubscribeStopsTheWake(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	card := e.readyCard(userSecret, "waiting on the owner")
	subscribe(e, "lead", card.Number)
	for range 2 {
		if code := unsubscribe(e, "lead", card.Number); code != http.StatusNoContent {
			t.Fatalf("unsubscribe = %d, want 204 also when nothing is subscribed", code)
		}
	}
	comment(e, userSecret, card.Number, "go ahead")
	if wake, _ := wakesOnComment(t, e, "lead", card.Number); wake {
		t.Error("a chat that unsubscribed was woken")
	}
}

func TestKeepChatSeenTouchesTheChatAtMostOncePerInterval(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	card := e.readyCard(userSecret, "waiting on the owner")
	subscribe(e, "lead-c1", card.Number)
	key := sessionKey{e.agent.ID, "lead"}
	if got := e.fs.sessions[key]; got == nil || !got.LastSeenAt.Equal(t0) {
		t.Fatalf("chat row after the subscription = %+v, want the base label seen at t0", got)
	}
	tok := e.seedToken("lvk_a_stream", e.agent, domain.ScopeWrite, t0.Add(domain.AgentTokenTTL))
	e.renewed = append(e.renewed, tok.ID)
	e.advance(30 * time.Minute)
	tok = *e.fs.tokens[tok.ID]
	p, prob := e.svc.standing(t.Context(), tok, "lead-c1", e.clock())
	if prob != nil {
		t.Fatal(prob)
	}
	ctx := WithPrincipal(t.Context(), p)
	e.svc.KeepChatSeen(ctx)
	if got := e.fs.sessions[key].LastSeenAt; !got.Equal(t0.Add(30 * time.Minute)) {
		t.Errorf("last seen after a stream keepalive = %v, want 30m after t0", got)
	}
	before := e.fs.sessionTouches
	e.advance(10 * time.Second)
	e.svc.KeepChatSeen(ctx)
	if e.fs.sessionTouches != before {
		t.Errorf("a second keepalive within the touch interval wrote again (%d touches, want %d)", e.fs.sessionTouches, before)
	}
	e.advance(touchInterval)
	e.svc.KeepChatSeen(ctx)
	if e.fs.sessionTouches != before+1 {
		t.Errorf("a keepalive past the touch interval wrote %d times, want once", e.fs.sessionTouches-before)
	}
}

func TestOnlyAnAgentsChatCanSubscribe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		method  string
		path    string
		secret  string
		session string
		detail  string
	}{
		{"a person subscribing", http.MethodPut, "", userSecret, "", "subscribing is for an agent's chat; people see replies on the dashboard"},
		{"a person unsubscribing", http.MethodDelete, "", userSecret, "", "subscribing is for an agent's chat; people see replies on the dashboard"},
		{"a person listing", http.MethodGet, "/me/subscriptions", userSecret, "", "subscribing is for an agent's chat; people see replies on the dashboard"},
		{"an agent without a session subscribing", http.MethodPut, "", agentSecret, "", "send the chat's session label"},
		{"an agent without a session listing", http.MethodGet, "/me/subscriptions", agentSecret, "", "send the chat's session label"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			card := e.readyCard(userSecret, "a card")
			path := tt.path
			if path == "" {
				path = subscriptionPath(card.Number)
			}
			var hdr []string
			if tt.session != "" {
				hdr = []string{apitypes.HeaderSession, tt.session}
			}
			rec := e.do(tt.secret, tt.method, path, nil, hdr...)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%s %s = %d, want 422: %s", tt.method, path, rec.Code, rec.Body)
			}
			if got := decodeBody[apitypes.Problem](t, rec.Body.Bytes()).Detail; got != tt.detail {
				t.Errorf("detail = %q, want %q", got, tt.detail)
			}
		})
	}
}

func TestAChatIsSubscribedToAtMostFiftyOpenCards(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	var cards []apitypes.Card
	for i := range domain.MaxChatSubscriptions + 1 {
		cards = append(cards, e.readyCard(userSecret, fmt.Sprintf("card %d", i)))
	}
	for _, c := range cards[:domain.MaxChatSubscriptions] {
		if code := subscribe(e, "lead", c.Number); code != http.StatusNoContent {
			t.Fatalf("subscribe #%d = %d, want 204", c.Number, code)
		}
	}
	last := cards[domain.MaxChatSubscriptions]
	rec := e.do(agentSecret, http.MethodPut, subscriptionPath(last.Number), nil, apitypes.HeaderSession, "lead")
	if rec.Code != http.StatusConflict {
		t.Fatalf("subscribe past the cap = %d, want 409: %s", rec.Code, rec.Body)
	}
	if got, want := decodeBody[apitypes.Problem](t, rec.Body.Bytes()).Detail, "this chat is subscribed to 50 cards; unsubscribe from one first"; got != want {
		t.Errorf("detail = %q, want %q", got, want)
	}
	if code := subscribe(e, "lead", cards[0].Number); code != http.StatusNoContent {
		t.Errorf("subscribing to a card already subscribed to at the cap = %d, want 204", code)
	}
	if code := subscribe(e, "other", last.Number); code != http.StatusNoContent {
		t.Errorf("another chat's first subscribe = %d, want 204: the cap is per chat", code)
	}
	unsubscribe(e, "lead", cards[1].Number)
	if code := subscribe(e, "lead", last.Number); code != http.StatusNoContent {
		t.Errorf("subscribe after making room = %d, want 204", code)
	}
}

func TestAClosedCardCannotBeSubscribedTo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		move apitypes.TransitionCard
		want string
	}{
		{"dropped", apitypes.TransitionCard{Status: "dropped", DropReason: "wontfix"}, "card #%d is dropped"},
		{"done", apitypes.TransitionCard{Status: "done"}, "card #%d is done"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			card := e.readyCard(userSecret, "closing")
			e.claim(userSecret, card.Number)
			e.move(userSecret, card.Number, tt.move)
			rec := e.do(agentSecret, http.MethodPut, subscriptionPath(card.Number), nil, apitypes.HeaderSession, "lead")
			if rec.Code != http.StatusConflict {
				t.Fatalf("subscribe = %d, want 409: %s", rec.Code, rec.Body)
			}
			if got, want := decodeBody[apitypes.Problem](t, rec.Body.Bytes()).Detail, fmt.Sprintf(tt.want, card.Number); got != want {
				t.Errorf("detail = %q, want %q", got, want)
			}
		})
	}
}

func TestListSubscriptionsHoldsOnlyOpenCardsOfTheCallingChat(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	open := e.readyCard(userSecret, "still open")
	closing := e.readyCard(userSecret, "closes later")
	theirs := e.readyCard(userSecret, "another chat's")
	subscribe(e, "lead", open.Number)
	subscribe(e, "lead-c1", closing.Number)
	subscribe(e, "other", theirs.Number)
	e.move(userSecret, closing.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "wontfix"})
	got := subscriptionList(e, "lead")
	if len(got) != 1 || got[0].Number != open.Number || got[0].Project != "lorvik" || got[0].Title != "still open" ||
		got[0].Status != "ready" || !got[0].SubscribedAt.Equal(t0) {
		t.Errorf("subscriptions of lead = %+v, want only #%d with its project, title, status and start", got, open.Number)
	}
	if got := subscriptionList(e, "nobody"); got == nil || len(got) != 0 {
		t.Errorf("subscriptions of a chat with none = %#v, want an empty list", got)
	}
}

func TestSubscriptionsOfLostChatsAndClosedCardsAreSweptAndLiveOnesKept(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	shared := e.readyCard(userSecret, "shared")
	closing := e.readyCard(userSecret, "closes")
	subscribe(e, "gone", shared.Number)
	subscribe(e, "live", shared.Number)
	subscribe(e, "live", closing.Number)
	e.advance(subscriptionLostAfter + time.Hour)
	inboxAs(t, e, agentSecret, "live", "")
	e.move(userSecret, closing.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "wontfix"})

	// Just closed: the listener may still be on its way to the closing event.
	lost, closed, err := e.svc.sweepSubscriptions(t.Context(), e.clock())
	if err != nil {
		t.Fatal(err)
	}
	if lost != 1 || closed != 0 {
		t.Errorf("first sweep took %d of lost chats and %d of closed cards, want 1 and 0", lost, closed)
	}
	e.advance(closedSubscriptionKept + time.Minute)
	inboxAs(t, e, agentSecret, "live", "")
	if _, closed, err = e.svc.sweepSubscriptions(t.Context(), e.clock()); err != nil || closed != 1 {
		t.Errorf("sweep an hour after closing took %d of closed cards (%v), want 1", closed, err)
	}
	if got := subscriptionList(e, "live"); len(got) != 1 || got[0].Number != shared.Number {
		t.Errorf("subscriptions of the live chat = %+v, want only #%d", got, shared.Number)
	}
	if n := len(e.fs.subscriptions); n != 1 {
		t.Errorf("%d subscriptions left, want the live chat's one", n)
	}
}

func TestSubscribingTwiceKeepsOneSubscription(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	card := e.readyCard(userSecret, "twice")
	subscribe(e, "lead", card.Number)
	subscribe(e, "lead-c1", card.Number)
	if n := len(e.fs.subscriptions); n != 1 {
		t.Errorf("%d rows after two subscriptions by one chat, want 1", n)
	}
}
