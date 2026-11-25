package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

const behalfReason = "docs/plan.md names Postgres"

// settle has the member ask a question and the agent, holding the answer
// right, answer it from chat-a with a reason.
func (e *testEnv) settle(title, answer string) apitypes.Card {
	e.t.Helper()
	q := e.newCard(memberSecret, apitypes.CreateCard{Kind: "question", Title: title})
	return inChat[apitypes.Card](e, agentSecret, "chat-a", http.MethodPost, cardPathOf(q.Number, "/answer"),
		apitypes.AnswerCard{Version: q.Version, Answer: answer, Note: behalfReason}, http.StatusOK)
}

func (e *testEnv) keep(c apitypes.Card) apitypes.Card {
	e.t.Helper()
	return call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(c.Number, "/keep"), apitypes.KeepCard{Version: c.Version}, http.StatusOK)
}

func TestAnAgentsOwnAnswerNeedsAReason(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightAnswer)
	q := e.newCard(memberSecret, apitypes.CreateCard{Kind: "question", Title: "Postgres or SQLite?"})
	for _, note := range []string{"", "  "} {
		p := e.problem(agentSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: q.Version, Answer: "Postgres", Note: note}, http.StatusUnprocessableEntity)
		if !hasField(p, "note") {
			t.Errorf("note %q: errors = %+v, want note", note, p.Errors)
		}
	}
	if got := e.show(memberSecret, q.Number); got.Status != "ready" || got.Behalf != nil {
		t.Errorf("question = %s behalf %+v, want untouched", got.Status, got.Behalf)
	}
	p := e.problem(userSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: q.Version, Answer: "Postgres", Note: behalfReason}, http.StatusUnprocessableEntity)
	if !hasField(p, "note") {
		t.Errorf("a person's note: errors = %+v, want note", p.Errors)
	}
}

func TestAnAgentsOwnAnswerIsRecordedForItsOwner(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightAnswer)
	got := e.settle("Postgres or SQLite?", "Postgres")
	b := got.Behalf
	if got.Status != "done" || b == nil || b.For.ID != e.user.ID.String() || b.For.Kind != "user" || b.By.ID != e.agent.ID.String() || b.By.Kind != "agent" || b.Reason != behalfReason || b.Session != "chat-a" || b.KeptAt != nil {
		t.Fatalf("answered = %s behalf %+v, want done, for the agent's owner, by the agent, with the reason and the chat, not kept", got.Status, b)
	}
	ans := e.eventsOf("card.answered")
	if len(ans) != 1 || ans[0]["reason"] != behalfReason {
		t.Fatalf("answered events = %+v, want one with the reason", ans)
	}
	if who, _ := ans[0]["for"].(map[string]any); who["id"] != e.user.ID.String() {
		t.Errorf("answered event for = %+v, want the agent's owner", ans[0]["for"])
	}
	if brief := call[apitypes.CardList](e, memberSecret, http.MethodGet, "/projects/lorvik/cards?status=done", nil, http.StatusOK); len(brief.Items) != 1 || brief.Items[0].Behalf == nil {
		t.Errorf("list = %+v, want the card with its behalf", brief.Items)
	}
}

func TestOnlyThePersonItWasSettledForKeepsIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightAnswer)
	admin := e.seedUser("alex", domain.RoleAdmin)
	const adminSecret = "lvk_u_alex-secret"
	e.seedToken(adminSecret, admin, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
	c := e.settle("Postgres or SQLite?", "Postgres")
	body := apitypes.KeepCard{Version: c.Version}
	for name, secret := range map[string]string{"another admin": adminSecret, "the agent": agentSecret, "a member": memberSecret} {
		e.problem(secret, http.MethodPost, cardPathOf(c.Number, "/keep"), body, http.StatusForbidden)
		if got := e.show(userSecret, c.Number); got.Behalf == nil || got.Behalf.KeptAt != nil {
			t.Fatalf("%s: behalf = %+v, want still waiting", name, got.Behalf)
		}
	}
	e.problem(userSecret, http.MethodPost, cardPathOf(c.Number, "/keep"), apitypes.KeepCard{Version: c.Version - 1}, http.StatusConflict)
	e.advance(time.Minute)
	kept := e.keep(c)
	if kept.Behalf == nil || kept.Behalf.KeptAt == nil || !kept.Behalf.KeptAt.Equal(t0.Add(time.Minute)) || kept.Behalf.Reason != behalfReason {
		t.Fatalf("kept behalf = %+v, want kept at the call's time with the reason still there", kept.Behalf)
	}
	if kept.Status != "done" || kept.Question.Answer != "Postgres" || kept.Question.AnsweredBy.ID != e.agent.ID.String() {
		t.Errorf("kept card = %+v, want the agent's answer standing", kept)
	}
	if ev := e.eventsOf("card.kept"); len(ev) != 1 || ev[0]["_subject"] != float64(c.Number) {
		t.Errorf("kept events = %+v, want one on the card", ev)
	}
	e.problem(userSecret, http.MethodPost, cardPathOf(c.Number, "/keep"), apitypes.KeepCard{Version: kept.Version}, http.StatusConflict)
	e.problem(userSecret, http.MethodPost, cardPathOf(c.Number, "/answer"), apitypes.AnswerCard{Version: kept.Version, Answer: "SQLite"}, http.StatusConflict)

	plain := e.newCard(memberSecret, apitypes.CreateCard{Kind: "question", Title: "answered by a person"})
	a := call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(plain.Number, "/answer"), apitypes.AnswerCard{Version: plain.Version, Answer: "A"}, http.StatusOK)
	e.problem(userSecret, http.MethodPost, cardPathOf(plain.Number, "/keep"), apitypes.KeepCard{Version: a.Version}, http.StatusConflict)
}

func TestAPersonChangesWhatAnAgentAnswered(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightAnswer)
	tests := []struct {
		name, answer string
		previous     any
	}{
		{"another answer", "SQLite", "Postgres"},
		{"the same answer", "Postgres", nil},
	}
	for _, tt := range tests {
		c := e.settle(tt.name, "Postgres")
		got := call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(c.Number, "/answer"), apitypes.AnswerCard{Version: c.Version, Answer: tt.answer}, http.StatusOK)
		if got.Status != "done" || got.Behalf != nil || got.Question.Answer != tt.answer || got.Question.AnsweredBy.ID != e.user.ID.String() {
			t.Errorf("%s: card = %+v, want the person's answer and no behalf", tt.name, got)
		}
		ev := e.eventsOf("card.answered")
		last := ev[len(ev)-1]
		if last["confirmed"] != true || last["previous"] != tt.previous || last["for"] != nil || last["reason"] != nil {
			t.Errorf("%s: event = %+v, want confirmed, previous %v, no for or reason", tt.name, last, tt.previous)
		}
	}
	c := e.settle("an agent again", "Postgres")
	e.problem(agentSecret, http.MethodPost, cardPathOf(c.Number, "/answer"), apitypes.AnswerCard{Version: c.Version, Answer: "SQLite", Note: behalfReason}, http.StatusConflict)
}

func TestAnsweredForListsWhatAPersonHasNotKept(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightAnswer)
	waiting := e.settle("waiting", "A")
	kept := e.keep(e.settle("kept", "A"))
	changed := e.settle("changed", "A")
	call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(changed.Number, "/answer"), apitypes.AnswerCard{Version: changed.Version, Answer: "B"}, http.StatusOK)
	numbers := func(secret, who string) []int64 {
		var out []int64
		for _, c := range call[apitypes.CardList](e, secret, http.MethodGet, "/projects/lorvik/cards?status=done&answered_for="+who, nil, http.StatusOK).Items {
			out = append(out, c.Number)
		}
		return out
	}
	if got := numbers(userSecret, "me"); len(got) != 1 || got[0] != waiting.Number {
		t.Errorf("answered_for=me for the owner = %v, want only #%d (not the kept #%d)", got, waiting.Number, kept.Number)
	}
	if got := numbers(memberSecret, "me"); len(got) != 0 {
		t.Errorf("answered_for=me for another person = %v, want none", got)
	}
	if got := numbers(memberSecret, e.user.ID.String()); len(got) != 1 || got[0] != waiting.Number {
		t.Errorf("answered_for=<owner id> = %v, want only #%d", got, waiting.Number)
	}
}
