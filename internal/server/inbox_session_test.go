package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func inboxAs(t *testing.T, e *testEnv, secret, session, query string) apitypes.Inbox {
	t.Helper()
	var hdr []string
	if session != "" {
		hdr = []string{apitypes.HeaderSession, session}
	}
	rec := e.do(secret, http.MethodGet, "/inbox"+query, nil, hdr...)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /inbox = %d: %s", rec.Code, rec.Body)
	}
	var in apitypes.Inbox
	if err := json.Unmarshal(rec.Body.Bytes(), &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func createAs(t *testing.T, e *testEnv, secret, session, title string) apitypes.Card {
	t.Helper()
	var hdr []string
	if session != "" {
		hdr = []string{apitypes.HeaderSession, session}
	}
	rec := e.do(secret, http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Title: title, Status: "ready"}, hdr...)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create card = %d: %s", rec.Code, rec.Body)
	}
	var c apitypes.Card
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestInboxExcludesOnlyTheCallersOwnSession(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		writerSession string
		callerSession string
		wantListed    bool
	}{
		{"same agent, other session", "lead-c1", "lead", true},
		{"same agent, same session", "lead", "lead", false},
		{"same agent, no session on both sides", "", "", false},
		{"same agent, writer had a session, caller none", "lead", "", true},
		{"same agent, writer had none, caller has one", "", "lead", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			createAs(t, e, agentSecret, tt.writerSession, "written by the agent")
			in := inboxAs(t, e, agentSecret, tt.callerSession, "")
			if got := len(in.Events) == 1; got != tt.wantListed {
				t.Errorf("events = %d, listed = %v, want %v", len(in.Events), got, tt.wantListed)
			}
		})
	}
}

func TestInboxMarksEventsOnCardsTheCallerHolds(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	held := createAs(t, e, agent2Secret, "", "held by the caller")
	other := createAs(t, e, agent2Secret, "", "unrelated")
	e.claim(agentSecret, held.Number)

	in := inboxAs(t, e, agentSecret, "lead", "?cursor=0")
	mine := map[string]bool{}
	for _, ev := range in.Events {
		mine[ev.Subject.ID] = ev.Mine
	}
	if !mine[held.ID] {
		t.Errorf("event on the held card: mine = false, want true")
	}
	if v, ok := mine[other.ID]; !ok || v {
		t.Errorf("event on the unrelated card: mine = %v (present %v), want false", v, ok)
	}
}

func TestInboxWakesOnlyTheChatAnEventConcerns(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	claimAs := func(n int64, session string) {
		t.Helper()
		if rec := e.do(agentSecret, http.MethodPost, cardPathOf(n, "/claim"), apitypes.ClaimCard{}, apitypes.HeaderSession, session); rec.Code != http.StatusOK {
			t.Fatalf("claim = %d: %s", rec.Code, rec.Body)
		}
	}
	createdHere := createAs(t, e, agentSecret, "lead", "asked by this chat")
	createdThere := createAs(t, e, agentSecret, "other", "asked by another chat")
	heldHere := createAs(t, e, agent2Secret, "", "held by this chat")
	claimAs(heldHere.Number, "lead")
	heldThere := createAs(t, e, agent2Secret, "", "held by another chat")
	claimAs(heldThere.Number, "other")
	assigned := createAs(t, e, userSecret, "", "assigned to the agent")
	unrelated := createAs(t, e, agent2Secret, "", "unrelated")
	agentID := e.agent.ID.String()
	for _, c := range []apitypes.Card{createdHere, createdThere, heldHere, heldThere, assigned, unrelated} {
		upd := apitypes.UpdateCard{Version: e.show(userSecret, c.Number).Version, Title: new(c.Title + ", edited")}
		if c.ID == assigned.ID {
			upd.Assignee = &apitypes.Actor{Kind: "agent", ID: agentID}
		}
		call[apitypes.Card](e, userSecret, http.MethodPatch, cardPathOf(c.Number, ""), upd, http.StatusOK)
	}

	in := inboxAs(t, e, agentSecret, "lead", "?cursor=0")
	wake := map[string]bool{}
	for _, ev := range in.Events {
		if ev.Type == "card.updated" {
			wake[ev.Subject.ID] = ev.Wake
		}
	}
	for _, tt := range []struct {
		name string
		card apitypes.Card
		want bool
	}{
		{"created by this chat", createdHere, true},
		{"created by another chat of the agent", createdThere, false},
		{"held by this chat", heldHere, true},
		{"held by another chat of the agent", heldThere, false},
		{"assigned to the agent", assigned, true},
		{"unrelated", unrelated, false},
	} {
		got, ok := wake[tt.card.ID]
		if !ok || got != tt.want {
			t.Errorf("%s: wake = %v (listed %v), want %v", tt.name, got, ok, tt.want)
		}
	}

	human := inboxAs(t, e, userSecret, "laptop", "?cursor=0")
	for _, ev := range human.Events {
		if ev.Wake != ev.Mine {
			t.Errorf("human inbox %s on %s: wake %v, mine %v; want wake to follow mine", ev.Type, ev.Subject.ID, ev.Wake, ev.Mine)
		}
	}
}

// wakeByCard maps each card to the wake flag of its events of one type.
func wakeByCard(in apitypes.Inbox, eventType string) map[string]bool {
	wake := map[string]bool{}
	for _, ev := range in.Events {
		if ev.Type == eventType {
			wake[ev.Subject.ID] = ev.Wake
		}
	}
	return wake
}

func TestAnAssignmentToTheAgentDoesNotWakeAChatForAnotherChatsCard(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	agent := &apitypes.Actor{Kind: "agent", ID: e.agent.ID.String()}
	create := func(secret, session, title string) apitypes.Card {
		t.Helper()
		var hdr []string
		if session != "" {
			hdr = []string{apitypes.HeaderSession, session}
		}
		rec := e.do(secret, http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Title: title, Status: "ready", Assignee: agent}, hdr...)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create card = %d: %s", rec.Code, rec.Body)
		}
		var c apitypes.Card
		if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	selfHere := create(agentSecret, "lead", "this chat assigned it to itself")
	selfThere := create(agentSecret, "other", "another chat assigned it to itself")
	handed := create(userSecret, "", "a person handed it to the agent")
	handedHeldThere := create(userSecret, "", "handed over, picked up by another chat")
	e.claimAs(agentSecret, "lorvik", handedHeldThere.Number, "other")
	ownHeldThere := create(agentSecret, "lead", "this chat's card, worked by another chat")
	e.claimAs(agentSecret, "lorvik", ownHeldThere.Number, "other")
	cards := []apitypes.Card{selfHere, selfThere, handed, handedHeldThere, ownHeldThere}
	for _, c := range cards {
		upd := apitypes.UpdateCard{Version: e.show(userSecret, c.Number).Version, Title: new(c.Title + ", edited")}
		call[apitypes.Card](e, userSecret, http.MethodPatch, cardPathOf(c.Number, ""), upd, http.StatusOK)
	}

	wake := wakeByCard(inboxAs(t, e, agentSecret, "lead", "?cursor=0"), "card.updated")
	for _, tt := range []struct {
		card apitypes.Card
		want bool
	}{
		{selfHere, true},
		{selfThere, false},
		{handed, true},
		{handedHeldThere, false},
		{ownHeldThere, true},
	} {
		got, ok := wake[tt.card.ID]
		if !ok || got != tt.want {
			t.Errorf("%s: wake = %v (listed %v), want %v", tt.card.Title, got, ok, tt.want)
		}
	}
}

func TestAnAssignmentWakesAChatOnlyInTheProjectItWorksIn(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "docs", Name: "Docs"}, http.StatusCreated)
	agent := &apitypes.Actor{Kind: "agent", ID: e.agent.ID.String()}
	create := func(slug, title string, assignee *apitypes.Actor) apitypes.Card {
		t.Helper()
		return call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/"+slug+"/cards", apitypes.CreateCard{Title: title, Status: "ready", Assignee: assignee}, http.StatusCreated)
	}
	here := create("lorvik", "assigned in lorvik", agent)
	there := create("docs", "assigned in docs", agent)
	heldThere := create("docs", "held by this chat in docs", nil)
	e.claimAs(agentSecret, "docs", heldThere.Number, "lead")
	rec := e.do(agentSecret, http.MethodPost, "/projects/docs/cards", apitypes.CreateCard{Title: "created by this chat in docs", Status: "ready", Assignee: agent}, apitypes.HeaderSession, "lead")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create card = %d: %s", rec.Code, rec.Body)
	}
	var ownThere apitypes.Card
	if err := json.Unmarshal(rec.Body.Bytes(), &ownThere); err != nil {
		t.Fatal(err)
	}
	call[apitypes.Comment](e, userSecret, http.MethodPost, "/projects/docs/cards/"+strconv.FormatInt(ownThere.Number, 10)+"/comments", apitypes.CreateComment{Body: "a person's note"}, http.StatusCreated)

	for _, tt := range []struct {
		name, query            string
		here, there, heldThere bool
	}{
		{"a chat working in lorvik", "&chat_project=lorvik", true, false, true},
		{"a chat working in docs", "&chat_project=docs", false, true, true},
		{"a chat that named no project", "", true, true, true},
		{"a chat whose project does not exist", "&chat_project=nope", false, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := inboxAs(t, e, agentSecret, "lead", "?cursor=0"+tt.query)
			wake := wakeByCard(in, "card.created")
			for _, c := range []struct {
				card apitypes.Card
				want bool
			}{{here, tt.here}, {there, tt.there}, {heldThere, tt.heldThere}} {
				got, ok := wake[c.card.ID]
				if !ok || got != c.want {
					t.Errorf("%s: wake = %v (listed %v), want %v", c.card.Title, got, ok, c.want)
				}
			}
			if got, ok := wakeByCard(in, "card.comment")[ownThere.ID]; !ok || !got {
				t.Errorf("%s: wake = %v (listed %v), want true in any project", ownThere.Title, got, ok)
			}
		})
	}

	human := inboxAs(t, e, userSecret, "laptop", "?cursor=0&chat_project=lorvik")
	woken := false
	for _, ev := range human.Events {
		if ev.Wake != ev.Mine {
			t.Errorf("human inbox %s on %s: wake %v, mine %v; want wake to follow mine whatever project is named", ev.Type, ev.Subject.ID, ev.Wake, ev.Mine)
		}
		woken = woken || ev.Wake && ev.Project != nil && *ev.Project == "docs"
	}
	if !woken {
		t.Error("human inbox: no event in docs wakes, want the person's own cards there to wake them")
	}
}

func TestInboxSplitsClaimsByOwningSession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	claimAs := func(secret string, n int64, session string) {
		t.Helper()
		if rec := e.do(secret, http.MethodPost, cardPathOf(n, "/claim"), apitypes.ClaimCard{}, apitypes.HeaderSession, session); rec.Code != http.StatusOK {
			t.Fatalf("claim = %d: %s", rec.Code, rec.Body)
		}
	}
	numbers := func(cards []apitypes.CardBrief) []int64 {
		out := []int64{}
		for _, c := range cards {
			out = append(out, c.Number)
		}
		slices.Sort(out)
		return out
	}
	here := createAs(t, e, agent2Secret, "", "held by this chat")
	there := createAs(t, e, agent2Secret, "", "held by another chat of the agent")
	claimAs(agentSecret, here.Number, "lead")
	claimAs(agentSecret, there.Number, "other")
	humanHere := createAs(t, e, agent2Secret, "", "held by the human at the laptop")
	humanThere := createAs(t, e, agent2Secret, "", "held by the human elsewhere")
	claimAs(userSecret, humanHere.Number, "laptop")
	claimAs(userSecret, humanThere.Number, "phone")

	tests := []struct {
		name      string
		secret    string
		session   string
		wantMine  []int64
		wantOther []int64
	}{
		{"agent chat", agentSecret, "lead", []int64{here.Number}, []int64{there.Number}},
		{"agent chat that holds nothing", agentSecret, "third", []int64{}, []int64{here.Number, there.Number}},
		{"human keeps one list", userSecret, "laptop", []int64{humanHere.Number, humanThere.Number}, []int64{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := inboxAs(t, e, tt.secret, tt.session, "")
			mine, other := numbers(in.Claims), numbers(in.ClaimsOtherSessions)
			if !slices.Equal(mine, tt.wantMine) || !slices.Equal(other, tt.wantOther) {
				t.Errorf("claims = %v, other sessions = %v; want %v, %v", mine, other, tt.wantMine, tt.wantOther)
			}
		})
	}
}

func TestInboxListsAreAlwaysPresent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	rec := e.do(agentSecret, http.MethodGet, "/inbox", nil, apitypes.HeaderSession, "idle")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /inbox = %d: %s", rec.Code, rec.Body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"claims", "claims_other_sessions", "locks"} {
		if got := string(raw[field]); got != "[]" {
			t.Errorf("%s = %q, want [] for a caller with no claims and no locks", field, got)
		}
	}
}

func TestInboxLeavesOutClaimsWhoseLeaseHasEnded(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		session string
	}{
		{"claim of another session of the agent", "lead"},
		{"claim of the calling session", "other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			c := createAs(t, e, agent2Secret, "", "lease lapses before the sweep")
			if rec := e.do(agentSecret, http.MethodPost, cardPathOf(c.Number, "/claim"), apitypes.ClaimCard{}, apitypes.HeaderSession, "other"); rec.Code != http.StatusOK {
				t.Fatalf("claim = %d: %s", rec.Code, rec.Body)
			}
			e.advance(31 * time.Minute)
			if n := len(e.fs.claims); n != 1 {
				t.Fatalf("store holds %d claim rows, want the expired one still there", n)
			}
			in := inboxAs(t, e, agentSecret, tt.session, "?cursor=0")
			if len(in.Claims) != 0 || len(in.ClaimsOtherSessions) != 0 {
				t.Errorf("claims = %d, other sessions = %d; want none, the lease has ended", len(in.Claims), len(in.ClaimsOtherSessions))
			}
			for _, ev := range in.Events {
				if ev.Subject.ID == c.ID && ev.Mine {
					t.Errorf("event %s on the card is marked mine after the lease ended", ev.Type)
				}
			}
		})
	}
}

// A chat is told when a card stops being its own: the lease ran out, another
// actor took the lapsed claim before the sweeper settled it, or a reviewer
// released it. Each such event wakes the chat that held the card and no
// other chat of the agent, though the card is by then nobody's or someone
// else's.
func TestTheChatThatLosesAClaimIsWoken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// lose takes the claim of chat "lead" on card n away.
		lose func(t *testing.T, e *testEnv, n int64)
		typ  string
	}{
		{"the lease ran out", func(t *testing.T, e *testEnv, n int64) {
			e.advance(31 * time.Minute)
			if swept, err := e.svc.sweepOnce(context.Background(), e.clock()); err != nil || swept != 1 {
				t.Fatalf("sweepOnce = %d, %v; want 1", swept, err)
			}
		}, "card.claim_expired"},
		{"taken over after the lease ran out", func(t *testing.T, e *testEnv, n int64) {
			e.advance(31 * time.Minute)
			e.claim(memberSecret, n)
		}, "card.claimed"},
		{"released by a reviewer", func(t *testing.T, e *testEnv, n int64) {
			call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(n, "/release"), apitypes.ClaimCard{}, http.StatusOK)
		}, "card.released"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			c := e.readyCard(memberSecret, "someone else's card, held by a chat")
			e.claimAs(agentSecret, "lorvik", c.Number, "lead")
			tt.lose(t, e, c.Number)

			here := inboxAs(t, e, agentSecret, "lead", "?cursor=0")
			if got, ok := wakeByCard(here, tt.typ)[c.ID]; !ok || !got {
				t.Errorf("the chat that held it: wake = %v (listed %v), want woken", got, ok)
			}
			there := inboxAs(t, e, agentSecret, "other", "?cursor=0")
			if got, ok := wakeByCard(there, tt.typ)[c.ID]; !ok || got {
				t.Errorf("another chat of the agent: wake = %v (listed %v), want listed and not woken", got, ok)
			}
			for _, ev := range inboxAs(t, e, agent2Secret, "lead", "?cursor=0").Events {
				if ev.Type == tt.typ && (ev.Mine || ev.Wake) {
					t.Errorf("another agent in a session of the same label: mine %v, wake %v; want neither", ev.Mine, ev.Wake)
				}
			}
		})
	}
}

// A person holds a claim in every session and the dashboard sends no label:
// the expiry is written in a session no request can carry, so it is not
// dropped as the reader's own doing.
func TestAPersonIsToldTheirClaimRanOut(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(userSecret, "held from the dashboard")
	e.claim(memberSecret, c.Number)
	e.advance(31 * time.Minute)
	if swept, err := e.svc.sweepOnce(context.Background(), e.clock()); err != nil || swept != 1 {
		t.Fatalf("sweepOnce = %d, %v; want 1", swept, err)
	}
	for _, session := range []string{"", "laptop"} {
		if got, ok := wakeByCard(inboxAs(t, e, memberSecret, session, "?cursor=0"), "card.claim_expired")[c.ID]; !ok || !got {
			t.Errorf("session %q: wake = %v (listed %v), want the expiry in the inbox, waking", session, got, ok)
		}
	}
}
