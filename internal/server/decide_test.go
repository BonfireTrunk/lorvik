package server

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

const verdictReason = "docs/requirements/board.md 2.3 already says so"

// verdict gives a verdict on a decision from one chat of the agent.
func (e *testEnv) verdict(session string, n int64, in apitypes.TransitionCard) apitypes.Card {
	e.t.Helper()
	in.Version = e.show(userSecret, n).Version
	return inChat[apitypes.Card](e, agentSecret, session, http.MethodPost, cardPathOf(n, "/transition"), in, http.StatusOK)
}

func accept(note string) apitypes.TransitionCard {
	return apitypes.TransitionCard{Status: "done", Note: note}
}

func reject(note string) apitypes.TransitionCard {
	return apitypes.TransitionCard{Status: "dropped", DropReason: "rejected", Note: note}
}

func TestAnAgentGivesAVerdictOnlyWithTheDecideRight(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.AllRights &^ domain.RightDecide)
	prop := e.decision(memberSecret, "Use pgx", "")
	for name, in := range map[string]apitypes.TransitionCard{"accept": accept(verdictReason), "reject": reject(verdictReason)} {
		pr := e.moveFails(agentSecret, prop.Number, in, http.StatusForbidden)
		if !strings.Contains(pr.Detail, "decide") {
			t.Errorf("%s without the right: detail = %q, want it to name the decide right", name, pr.Detail)
		}
	}

	e.giveRights(domain.RightDecide)
	for name, in := range map[string]apitypes.TransitionCard{"accept": accept(" "), "reject": reject("")} {
		if pr := e.moveFails(agentSecret, prop.Number, in, http.StatusUnprocessableEntity); !hasField(pr, "note") {
			t.Errorf("%s without a reason: errors = %+v, want note", name, pr.Errors)
		}
	}
	if got := e.show(userSecret, prop.Number); got.Status != "ready" || got.Behalf != nil {
		t.Fatalf("proposal = %s behalf %+v, want untouched", got.Status, got.Behalf)
	}

	acc := e.verdict("duty", prop.Number, accept(verdictReason))
	b := acc.Behalf
	if acc.Status != "done" || acc.Decision.AcceptedBy == nil || acc.Decision.AcceptedBy.ID != e.agent.ID.String() || acc.Decision.AcceptedBy.Kind != "agent" {
		t.Fatalf("accepted = %s by %+v, want done and accepted by the agent", acc.Status, acc.Decision.AcceptedBy)
	}
	if b == nil || b.For.ID != e.user.ID.String() || b.For.Kind != "user" || b.By.ID != e.agent.ID.String() || b.Reason != verdictReason || b.Session != "duty" || b.KeptAt != nil {
		t.Errorf("behalf = %+v, want for the agent's owner, by the agent, with the reason and the chat, not kept", b)
	}
	moves := e.eventsOf("card.status_changed")
	last := moves[len(moves)-1]
	if who, _ := last["for"].(map[string]any); who["id"] != e.user.ID.String() || last["note"] != verdictReason || last["to"] != "done" {
		t.Errorf("event = %+v, want the owner in for, the reason in note, to done", last)
	}

	other := e.decision(memberSecret, "Use sqlite", "")
	rej := e.verdict("duty", other.Number, reject(verdictReason))
	if rej.Status != "dropped" || rej.DropReason != "rejected" || rej.Behalf == nil || rej.Behalf.Reason != verdictReason || rej.Behalf.KeptAt != nil {
		t.Errorf("rejected = %s %s behalf %+v, want dropped as rejected and recorded for the owner", rej.Status, rej.DropReason, rej.Behalf)
	}

	// Everything else a person does with decisions stays a person's.
	e.moveFails(agentSecret, prop.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "revoked", Note: verdictReason}, http.StatusForbidden)
	e.problem(agentSecret, http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Kind: "decision", Title: "recorded by an agent", Status: "done"}, http.StatusForbidden)
	if got := e.show(userSecret, prop.Number); got.Status != "done" {
		t.Errorf("after an agent's revoke: %s, want still done", got.Status)
	}
}

func TestWhatBarsAnAgentsVerdictWhateverItsRights(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightWork | domain.RightDecide)
	propose := func(secret, session, slug string, in apitypes.CreateCard) apitypes.Card {
		in.Kind, in.Body = "decision", "## Decision\n\nd\n"
		return inChat[apitypes.Card](e, secret, session, http.MethodPost, "/projects/"+slug+"/cards", in, http.StatusCreated)
	}

	own := propose(agentSecret, "duty", "lorvik", apitypes.CreateCard{Title: "proposed by this chat"})
	for _, session := range []string{"duty", "duty-c3"} {
		for name, in := range map[string]apitypes.TransitionCard{"accept": accept(verdictReason), "reject": reject(verdictReason)} {
			in.Version = own.Version
			rec := e.do(agentSecret, http.MethodPost, cardPathOf(own.Number, "/transition"), in, apitypes.HeaderSession, session)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "proposed #") {
				t.Errorf("%s of its own proposal from %s = %d %s, want 403 naming who proposed it", name, session, rec.Code, rec.Body)
			}
		}
	}
	if got := e.verdict("other-chat", own.Number, accept(verdictReason)); got.Status != "done" {
		t.Errorf("another chat of the agent: %s, want it accepted", got.Status)
	}

	inForce := e.decision(userSecret, "Use Go", "done")
	successor := propose(memberSecret, "", "lorvik", apitypes.CreateCard{Title: "Use Rust", Supersedes: &inForce.Number})
	for name, in := range map[string]apitypes.TransitionCard{"accept": accept(verdictReason), "reject": reject(verdictReason)} {
		pr := e.moveFails(agentSecret, successor.Number, in, http.StatusForbidden)
		if !strings.Contains(pr.Detail, "replace accepted decision") {
			t.Errorf("%s of a successor: detail = %q, want it to name the decision in force", name, pr.Detail)
		}
	}
	if got := e.show(userSecret, inForce.Number); got.Status != "done" {
		t.Fatalf("the decision in force = %s, want done", got.Status)
	}
	e.move(userSecret, inForce.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "revoked"})
	if got := e.verdict("duty", successor.Number, accept(verdictReason)); got.Status != "done" {
		t.Errorf("a successor of a revoked decision: %s, want it accepted: nothing in force is replaced", got.Status)
	}

	global := propose(memberSecret, "", "global", apitypes.CreateCard{Title: "org-wide"})
	for name, in := range map[string]apitypes.TransitionCard{"accept": accept(verdictReason), "reject": reject(verdictReason)} {
		in.Version = global.Version
		pr := e.problem(agentSecret, http.MethodPost, "/projects/global/cards/"+strconv.FormatInt(global.Number, 10)+"/transition", in, http.StatusForbidden)
		if !strings.Contains(pr.Detail, "global project") {
			t.Errorf("%s in the global project: detail = %q, want it to name the global project", name, pr.Detail)
		}
	}
}

func TestAVerdictAnAgentGaveWaitsForItsOwner(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightDecide)
	settle := func(title string, in apitypes.TransitionCard) apitypes.Card {
		return e.verdict("duty", e.decision(memberSecret, title, "").Number, in)
	}
	waiting := settle("waiting", accept(verdictReason))
	kept := e.keep(settle("kept", accept(verdictReason)))
	revoked := settle("revoked", accept(verdictReason))
	reproposed := settle("proposed again", reject(verdictReason))

	if kept.Behalf == nil || kept.Behalf.KeptAt == nil || kept.Status != "done" {
		t.Errorf("kept = %s behalf %+v, want the decision in force and looked at", kept.Status, kept.Behalf)
	}
	if ev := e.eventsOf("card.kept"); len(ev) != 1 || ev[0]["kind"] != "decision" || ev[0]["settled_by_session"] != "duty" {
		t.Errorf("kept events = %+v, want one on a decision naming the chat", ev)
	}
	e.problem(memberSecret, http.MethodPost, cardPathOf(waiting.Number, "/keep"), apitypes.KeepCard{Version: waiting.Version}, http.StatusForbidden)

	after := e.move(userSecret, revoked.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "revoked"})
	again := e.move(userSecret, reproposed.Number, apitypes.TransitionCard{Status: "ready"})
	if after.Behalf != nil || again.Behalf != nil {
		t.Errorf("behalf after a person's revoke = %+v, after proposing again = %+v; want none", after.Behalf, again.Behalf)
	}
	e.move(userSecret, kept.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "revoked"})

	settledBy := map[float64]any{}
	for _, ev := range e.eventsOf("card.status_changed") {
		if by, ok := ev["settled_by"].(map[string]any); ok {
			settledBy[ev["_subject"].(float64)] = by["id"]
			if ev["settled_by_session"] != "duty" {
				t.Errorf("event on #%v names chat %v, want duty", ev["_subject"], ev["settled_by_session"])
			}
		}
	}
	want := map[float64]any{float64(revoked.Number): e.agent.ID.String(), float64(reproposed.Number): e.agent.ID.String()}
	if len(settledBy) != len(want) || settledBy[float64(revoked.Number)] != want[float64(revoked.Number)] || settledBy[float64(reproposed.Number)] != want[float64(reproposed.Number)] {
		t.Errorf("moves that name the agent whose verdict they replaced = %v, want only #%d and #%d (not the kept #%d)", settledBy, revoked.Number, reproposed.Number, kept.Number)
	}

	var listed []int64
	for _, c := range call[apitypes.CardList](e, userSecret, http.MethodGet, "/projects/lorvik/cards?status=done,dropped,ready&answered_for=me", nil, http.StatusOK).Items {
		listed = append(listed, c.Number)
	}
	if len(listed) != 1 || listed[0] != waiting.Number {
		t.Errorf("answered_for=me = %v, want only #%d", listed, waiting.Number)
	}

	in := inboxAs(t, e, agentSecret, "duty", "?cursor=0&chat_project=lorvik")
	woke := map[string]bool{}
	for _, ev := range in.Events {
		if ev.Actor.Kind == "user" && (ev.Type == "card.status_changed" || ev.Type == "card.kept") {
			woke[ev.Subject.ID] = woke[ev.Subject.ID] || ev.Wake
		}
	}
	if !woke[revoked.ID] || !woke[reproposed.ID] || !woke[kept.ID] {
		t.Errorf("woken for = %v, want the chat told of the revoke (#%d), the new proposal (#%d) and the keep (#%d)", woke, revoked.Number, reproposed.Number, kept.Number)
	}
}

func TestAProposedDecisionWakesAChatThatMayDecide(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightWork | domain.RightDecide)
	propose := func(secret, session, slug string, in apitypes.CreateCard) apitypes.Card {
		in.Kind, in.Body = "decision", "## Decision\n\nd\n"
		return inChat[apitypes.Card](e, secret, session, http.MethodPost, "/projects/"+slug+"/cards", in, http.StatusCreated)
	}
	byPerson := propose(memberSecret, "", "lorvik", apitypes.CreateCard{Title: "proposed by a person"})
	byOtherChat := propose(agentSecret, "worker", "lorvik", apitypes.CreateCard{Title: "proposed by another chat"})
	bySubagent := propose(agentSecret, "duty-c2", "lorvik", apitypes.CreateCard{Title: "proposed by this chat's subagent"})
	inForce := e.decision(userSecret, "in force", "done")
	successor := propose(memberSecret, "", "lorvik", apitypes.CreateCard{Title: "replaces one in force", Supersedes: &inForce.Number})
	accepted := propose(memberSecret, "", "lorvik", apitypes.CreateCard{Title: "already accepted"})
	e.move(userSecret, accepted.Number, apitypes.TransitionCard{Status: "done"})
	global := propose(memberSecret, "", "global", apitypes.CreateCard{Title: "org-wide"})
	again := propose(memberSecret, "", "lorvik", apitypes.CreateCard{Title: "proposed again"})
	e.move(userSecret, again.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "rejected"})
	e.move(userSecret, again.Number, apitypes.TransitionCard{Status: "ready"})

	tests := []struct {
		name, secret, query string
		want                map[string]bool
	}{
		{"a chat working in lorvik", agentSecret, "&chat_project=lorvik", map[string]bool{
			byPerson.ID: true, byOtherChat.ID: true, bySubagent.ID: false, successor.ID: false, accepted.ID: false, inForce.ID: false}},
		{"a chat that named no project", agentSecret, "", map[string]bool{byPerson.ID: true, global.ID: false}},
		{"a chat of an agent without the decide right", agent2Secret, "&chat_project=lorvik", map[string]bool{byPerson.ID: false, byOtherChat.ID: false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wake := wakeByCard(inboxAs(t, e, tt.secret, "duty", "?cursor=0"+tt.query), "card.created")
			for id, want := range tt.want {
				if got, ok := wake[id]; !ok || got != want {
					t.Errorf("card %s: wake = %v (listed %v), want %v", id, got, ok, want)
				}
			}
		})
	}
	var back, away bool
	for _, ev := range inboxAs(t, e, agentSecret, "duty", "?cursor=0&chat_project=lorvik").Events {
		if ev.Type != "card.status_changed" || ev.Subject.ID != again.ID {
			continue
		}
		if strings.Contains(string(ev.Payload), `"to":"ready"`) {
			back = ev.Wake
		} else {
			away = away || ev.Wake
		}
	}
	if !back || away {
		t.Errorf("a decision proposed again: wake on the move back to ready = %v, on its rejection = %v; want true and false", back, away)
	}
}

// dropCreation removes a card's creation event, as if the event log had lost
// it.
func (e *testEnv) dropCreation(c apitypes.Card) {
	e.fs.mu.Lock()
	defer e.fs.mu.Unlock()
	e.fs.events = slices.DeleteFunc(e.fs.events, func(ev domain.Event) bool {
		return ev.Type == domain.EventCardCreated && ev.Subject.ID.String() == c.ID
	})
}

// Which chat made a card is read from its creation event. When that event is
// missing the agent's own card counts as the calling chat's, so the unknown
// case goes to another judge instead of letting a chat settle its own card.
func TestACardOfTheAgentWithNoCreationEventIsNotItsChatsToSettle(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightWork | domain.RightAnswer | domain.RightDecide)
	prop := inChat[apitypes.Card](e, agentSecret, "worker", http.MethodPost, "/projects/lorvik/cards",
		apitypes.CreateCard{Kind: "decision", Title: "proposed by another chat", Body: "## Decision\n\nd\n"}, http.StatusCreated)
	q := inChat[apitypes.Card](e, agentSecret, "worker", http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Kind: "question", Title: "asked by another chat"}, http.StatusCreated)
	e.dropCreation(prop)
	e.dropCreation(q)

	in := accept(verdictReason)
	in.Version = prop.Version
	if rec := e.do(agentSecret, http.MethodPost, cardPathOf(prop.Number, "/transition"), in, apitypes.HeaderSession, "duty"); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "proposed #") {
		t.Errorf("verdict on the agent's proposal with no creation event = %d %s, want 403 as its own proposal", rec.Code, rec.Body)
	}
	ans := apitypes.AnswerCard{Version: q.Version, Answer: "yes", Note: verdictReason}
	if rec := e.do(agentSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), ans, apitypes.HeaderSession, "duty"); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "asked #") {
		t.Errorf("answer to the agent's question with no creation event = %d %s, want 403 as its own question", rec.Code, rec.Body)
	}

	byPerson := e.decision(memberSecret, "proposed by a person", "")
	e.dropCreation(byPerson)
	if got := e.verdict("duty", byPerson.Number, accept(verdictReason)); got.Status != "done" {
		t.Errorf("a person's proposal with no creation event: %s, want it accepted", got.Status)
	}
}

// What a chat settles is settled for the agent's owner, whoever gave the
// chat the right: an admin who manages the agent hands on a right of their
// own, and the owner is still the one who looks at the result.
func TestWhatAChatSettlesIsForTheAgentsOwnerWhoeverGaveTheRight(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	const adminSecret = "lvk_u_alex-secret"
	admin := e.seedUser("alex", domain.RoleAdmin)
	e.seedToken(adminSecret, admin, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
	call[apitypes.Agent](e, adminSecret, http.MethodPut, "/agents/"+e.agent2.ID.String()+"/sessions/duty/projects/lorvik",
		apitypes.AgentSessionGrantBody{Rights: []string{"decide"}}, http.StatusOK)
	prop := e.decision(memberSecret, "Use pgx", "")
	in := accept(verdictReason)
	in.Version = prop.Version
	got := inChat[apitypes.Card](e, agent2Secret, "duty", http.MethodPost, cardPathOf(prop.Number, "/transition"), in, http.StatusOK)
	if got.Behalf == nil || got.Behalf.For.ID != e.user.ID.String() {
		t.Fatalf("behalf = %+v, want it for the agent's owner, not the admin who gave the right", got.Behalf)
	}
	e.problem(adminSecret, http.MethodPost, cardPathOf(prop.Number, "/keep"), apitypes.KeepCard{Version: got.Version}, http.StatusForbidden)
	e.keep(got)
}

// The decide right is handed on only by someone whose own role accepts
// decisions: a member cannot give it to a chat of their own agent.
func TestAMemberCannotGiveAChatTheDecideRight(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.membersAgent(domain.RightWork)
	grant := "/agents/" + e.agentNamed("sam-bot").String() + "/sessions/duty/projects/lorvik"
	for _, rights := range [][]string{{"decide"}, {"answer"}, {"work", "decide"}} {
		if p := e.problem(memberSecret, http.MethodPut, grant, apitypes.AgentSessionGrantBody{Rights: rights}, http.StatusUnprocessableEntity); !hasField(p, "rights") {
			t.Errorf("a member gives %v: errors = %+v, want rights", rights, p.Errors)
		}
	}
	e.setOrgRole(e.member, domain.RoleReviewer, domain.RecordActive)
	call[apitypes.Agent](e, memberSecret, http.MethodPut, grant, apitypes.AgentSessionGrantBody{Rights: []string{"answer", "decide"}}, http.StatusOK)
}

// A rejection an agent gave stops waiting for its owner's look once the
// decision is proposed again, by a person or by an agent: the proposal is
// open again and gets a new verdict.
func TestAnAgentProposingAgainClearsTheVerdictAnAgentGave(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightWork | domain.RightDecide)
	prop := inChat[apitypes.Card](e, agentSecret, "worker", http.MethodPost, "/projects/lorvik/cards",
		apitypes.CreateCard{Kind: "decision", Title: "Use pgx", Body: "## Decision\n\nd\n"}, http.StatusCreated)
	rej := e.verdict("duty", prop.Number, reject(verdictReason))
	if rej.Behalf == nil {
		t.Fatal("the rejection carries no behalf")
	}
	again := inChat[apitypes.Card](e, agentSecret, "worker", http.MethodPost, cardPathOf(prop.Number, "/transition"), apitypes.TransitionCard{Version: rej.Version, Status: "ready"}, http.StatusOK)
	if again.Status != "ready" || again.Behalf != nil {
		t.Errorf("proposed again = %s behalf %+v, want an open proposal with nothing waiting for a look", again.Status, again.Behalf)
	}
	if got := e.verdict("duty", prop.Number, accept(verdictReason)); got.Behalf == nil || got.Behalf.KeptAt != nil {
		t.Errorf("the second verdict's behalf = %+v, want it waiting for the owner", got.Behalf)
	}
}
