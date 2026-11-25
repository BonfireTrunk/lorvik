package server

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

const relayNote = "said in chat on Friday afternoon"

func (e *testEnv) relay(q apitypes.Card, answer string) apitypes.Card {
	e.t.Helper()
	return call[apitypes.Card](e, agentSecret, http.MethodPost, cardPathOf(q.Number, "/answer"),
		apitypes.AnswerCard{Version: q.Version, Answer: answer, Relayed: true, Note: relayNote}, http.StatusOK)
}

func TestAgentRelayClosesTheQuestionAndPostsTheNote(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task := e.readyCard(agentSecret, "Choose storage")
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "Postgres or SQLite?", Blocks: []int64{task.Number}})
	got := e.relay(q, "Postgres")
	if got.Status != "done" || !got.Relayed || got.Question.Answer != "Postgres" || got.Question.AnsweredBy == nil ||
		got.Question.AnsweredBy.ID != e.agent.ID.String() || got.Question.AnsweredAt == nil {
		t.Errorf("relayed question = %+v, want done, relayed, answered by the agent", got)
	}
	if t2 := e.show(agentSecret, task.Number); t2.Status != "ready" || len(t2.BlockedBy) != 0 {
		t.Errorf("blocked task = %+v, want ready", t2)
	}
	cs := call[[]apitypes.Comment](e, agentSecret, http.MethodGet, cardPathOf(q.Number, "/comments"), nil, http.StatusOK)
	if len(cs) != 1 || cs[0].Body != relayNote || cs[0].Actor.ID != e.agent.ID.String() {
		t.Errorf("comments = %+v, want the note by the agent", cs)
	}
	ans := e.eventsOf("card.answered")
	if len(ans) != 1 || ans[0]["relayed"] != true || ans[0]["answer"] != "Postgres" || ans[0]["confirmed"] != nil {
		t.Errorf("answered events = %+v, want one relayed", ans)
	}
	if n := len(e.eventsOf("card.comment")); n != 1 {
		t.Errorf("comment events = %d, want 1", n)
	}
	if brief := call[apitypes.CardList](e, agentSecret, http.MethodGet, "/projects/lorvik/cards?relayed=true", nil, http.StatusOK); len(brief.Items) != 1 ||
		brief.Items[0].Number != q.Number || !brief.Items[0].Relayed {
		t.Errorf("relayed list = %+v, want the question flagged", brief.Items)
	}
}

func TestAgentAnswerWithoutRelayedIsForbiddenWithAHint(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "q"})
	p := e.problem(agentSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: q.Version, Answer: "x", Note: relayNote}, http.StatusForbidden)
	if !strings.Contains(p.Hint, "--relayed") {
		t.Errorf("hint = %q, want the relay command", p.Hint)
	}
	if got := e.show(agentSecret, q.Number); got.Status != "ready" {
		t.Errorf("question = %q, want untouched", got.Status)
	}
}

func TestAgentRelayRefusals(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "q"})
	path := cardPathOf(q.Number, "/answer")
	tests := []struct {
		name  string
		body  apitypes.AnswerCard
		field string
	}{
		{"missing note", apitypes.AnswerCard{Version: q.Version, Answer: "x", Relayed: true}, "note"},
		{"blank note", apitypes.AnswerCard{Version: q.Version, Answer: "x", Relayed: true, Note: "  "}, "note"},
		{"decide", apitypes.AnswerCard{Version: q.Version, Answer: "x", Relayed: true, Note: relayNote, Decide: true}, "decide"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := e.problem(agentSecret, http.MethodPost, path, tt.body, http.StatusUnprocessableEntity)
			if !hasField(p, tt.field) {
				t.Errorf("problem = %+v, want field %s", p, tt.field)
			}
		})
	}
	if got := e.show(agentSecret, q.Number); got.Status != "ready" {
		t.Errorf("question = %q, want untouched", got.Status)
	}
}

func TestUserCannotRelay(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "q"})
	p := e.problem(userSecret, http.MethodPost, cardPathOf(q.Number, "/answer"),
		apitypes.AnswerCard{Version: q.Version, Answer: "x", Relayed: true, Note: relayNote}, http.StatusUnprocessableEntity)
	if !hasField(p, "relayed") {
		t.Errorf("problem = %+v, want field relayed", p)
	}
	p = e.problem(userSecret, http.MethodPost, cardPathOf(q.Number, "/answer"),
		apitypes.AnswerCard{Version: q.Version, Answer: "x", Relayed: true}, http.StatusUnprocessableEntity)
	if !hasField(p, "relayed") {
		t.Errorf("problem without a note = %+v, want field relayed, not the agent's note rule", p)
	}
}

func TestUserConfirmsARelayedAnswer(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "q"})
	r := e.relay(q, "Postgres")
	got := call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: r.Version, Answer: "Postgres"}, http.StatusOK)
	if got.Status != "done" || got.Relayed || got.Question.Answer != "Postgres" || got.Question.AnsweredBy.ID != e.user.ID.String() {
		t.Errorf("confirmed question = %+v, want done and answered by the user", got)
	}
	ev := e.eventsOf("card.answered")
	last := ev[len(ev)-1]
	if last["confirmed"] != true || last["previous"] != nil || last["relayed"] != nil {
		t.Errorf("confirm event = %+v, want confirmed only", last)
	}
	if list := call[apitypes.CardList](e, agentSecret, http.MethodGet, "/projects/lorvik/cards?relayed=true", nil, http.StatusOK); len(list.Items) != 0 {
		t.Errorf("relayed list = %+v, want empty once confirmed", list.Items)
	}
}

func TestUserChangesARelayedAnswer(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "q"})
	r := e.relay(q, "SQLite")
	rec := e.do(userSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: r.Version, Answer: "Postgres", Decide: true})
	if rec.Code != http.StatusOK {
		t.Fatalf("answer = %d: %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Location") == "" {
		t.Error("decide on a relayed question must record the decision")
	}
	got := e.show(agentSecret, q.Number)
	if got.Question.Answer != "Postgres" || got.Question.AnsweredBy.ID != e.user.ID.String() || got.Relayed {
		t.Errorf("changed question = %+v, want the user's answer", got)
	}
	ev := e.eventsOf("card.answered")
	last := ev[len(ev)-1]
	if last["confirmed"] != true || last["previous"] != "SQLite" || last["answer"] != "Postgres" || last["decided"] != true {
		t.Errorf("change event = %+v, want confirmed with the previous answer", last)
	}
}

func TestUserAnswerOnAUserAnsweredQuestionStaysRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "q"})
	a := call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: q.Version, Answer: "A"}, http.StatusOK)
	e.problem(userSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: a.Version, Answer: "B"}, http.StatusConflict)
}

func TestAgentClaimStartOrDoneOnAQuestionIsRefusedWithTheRelayHint(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "q"})
	want := `relay the human's answer: lorvik answer ` + itoa(q.Number) + ` "<answer>" --relayed -m "<where they said it>"`
	refusals := []apitypes.Problem{
		e.problem(agentSecret, http.MethodPost, cardPathOf(q.Number, "/claim"), apitypes.ClaimCard{}, http.StatusConflict),
		e.moveFails(agentSecret, q.Number, apitypes.TransitionCard{Status: "working"}, http.StatusConflict),
		e.moveFails(agentSecret, q.Number, apitypes.TransitionCard{Status: "done"}, http.StatusConflict),
	}
	for i, p := range refusals {
		if p.Hint != want {
			t.Errorf("refusal %d hint = %q, want %q", i, p.Hint, want)
		}
	}
	if got := e.show(agentSecret, q.Number); got.Status != "ready" || got.Claim != nil {
		t.Errorf("question = %q claimed %v, want ready and unclaimed, still in the human's list", got.Status, got.Claim != nil)
	}
	if got := e.move(agentSecret, q.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "obsolete"}); got.Status != "dropped" {
		t.Errorf("dropped question = %q, want dropping unchanged", got.Status)
	}
}

func TestUserDoneOnAQuestionIsUnchanged(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "q"})
	e.claim(userSecret, q.Number)
	if got := e.move(userSecret, q.Number, apitypes.TransitionCard{Status: "done"}); got.Status != "done" {
		t.Errorf("question = %q, want done", got.Status)
	}
}

func TestUserOpenQuestionsDoNotCountRelayedAnswers(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "q"})
	count := func(secret string) int {
		return call[apitypes.Inbox](e, secret, http.MethodGet, "/inbox", nil, http.StatusOK).OpenQuestions
	}
	if u := count(userSecret); u != 1 {
		t.Errorf("before the relay: user %d, want 1", u)
	}
	e.relay(q, "A")
	if u, a := count(userSecret), count(agentSecret); u != 0 || a != 0 {
		t.Errorf("after the relay: user %d, agent %d; want 0 and 0", u, a)
	}
}

func TestRelayedFilterListsOnlyRelayedQuestions(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	relayed := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "relayed"})
	e.relay(relayed, "A")
	open := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "open"})
	byUser := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "by user"})
	call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(byUser.Number, "/answer"), apitypes.AnswerCard{Version: byUser.Version, Answer: "B"}, http.StatusOK)
	for _, path := range []string{"/projects/lorvik/cards?relayed=true", "/cards?relayed=true"} {
		got := call[apitypes.CardList](e, userSecret, http.MethodGet, path, nil, http.StatusOK)
		if !slices.Equal(cardNumbers(got), []int64{relayed.Number}) {
			t.Errorf("%s = %v, want only #%d (not #%d or #%d)", path, cardNumbers(got), relayed.Number, open.Number, byUser.Number)
		}
	}
}

func TestAgentCannotParkOrDropAQuestionThatStillBlocksWork(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task := e.readyCard(agentSecret, "Choose storage")
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "Postgres or SQLite?", Blocks: []int64{task.Number}})
	for _, to := range []string{"backlog", "waiting"} {
		e.moveFails(agentSecret, q.Number, apitypes.TransitionCard{Status: to}, http.StatusConflict)
	}
	p := e.moveFails(agentSecret, q.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "obsolete"}, http.StatusConflict)
	if !strings.Contains(p.Detail, "#"+itoa(task.Number)) || !strings.Contains(p.Hint, "--relayed") {
		t.Errorf("problem = %+v, want the blocked card named and the relay hint", p)
	}
	if got := e.show(agentSecret, task.Number); got.Status != "waiting" {
		t.Errorf("blocked task = %q, want still waiting for the answer", got.Status)
	}
	e.move(agentSecret, task.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "obsolete"})
	if got := e.move(agentSecret, q.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "obsolete"}); got.Status != "dropped" {
		t.Errorf("question = %q, want dropped once the blocked work is gone", got.Status)
	}
}

func TestReopeningARelayedQuestionClearsTheAnswer(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "q"})
	e.relay(q, "Postgres")
	got := e.move(userSecret, q.Number, apitypes.TransitionCard{Status: "ready"})
	if got.Status != "ready" || got.Relayed || got.Question == nil || got.Question.Answer != "" || got.Question.AnsweredBy != nil {
		t.Errorf("reopened question = %+v, want open with no answer", got)
	}
}
