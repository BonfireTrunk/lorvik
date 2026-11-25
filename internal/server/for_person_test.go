package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

const handOverNote = "about spending, and no decision covers it"

func (e *testEnv) markForPerson(secret string, c apitypes.Card, in apitypes.MarkForPerson) *httptest.ResponseRecorder {
	e.t.Helper()
	in.Version = e.show(userSecret, c.Number).Version
	return e.do(secret, http.MethodPost, cardPathOf(c.Number, "/for-person"), in)
}

func TestAQuestionAskedForAPersonIsNotAnAgentsToAnswer(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightWork | domain.RightAnswer)
	q := inChat[apitypes.Card](e, agentSecret, "chat-a", http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Kind: "question", Title: "Buy the bigger plan?", ForPerson: true}, http.StatusCreated)
	if !q.ForPerson {
		t.Fatal("the created question does not carry for_person")
	}
	if ev := e.eventsOf("card.created"); len(ev) != 1 || ev[0]["for_person"] != true {
		t.Errorf("created events = %+v, want for_person", ev)
	}
	if brief := call[apitypes.CardList](e, memberSecret, http.MethodGet, "/projects/lorvik/cards?kind=question", nil, http.StatusOK); len(brief.Items) != 1 || !brief.Items[0].ForPerson {
		t.Errorf("list = %+v, want the question flagged for a person", brief.Items)
	}
	rec := e.do(agentSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: q.Version, Answer: "yes", Note: behalfReason}, apitypes.HeaderSession, "chat-b")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "for a person") || !strings.Contains(rec.Body.String(), "--relayed") {
		t.Fatalf("an agent's own answer = %d %s, want 403 naming the mark and the relay", rec.Code, rec.Body)
	}
	if got := e.show(userSecret, q.Number); got.Status != "ready" {
		t.Fatalf("question = %s, want still open", got.Status)
	}
	relayed := call[apitypes.Card](e, agentSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: q.Version, Answer: "yes", Relayed: true, Note: relayNote}, http.StatusOK)
	if relayed.Status != "done" || !relayed.Relayed || !relayed.ForPerson {
		t.Errorf("relayed = %+v, want done, relayed and still marked", relayed)
	}

	byPerson := e.newCard(memberSecret, apitypes.CreateCard{Kind: "question", Title: "Rotate the keys?", ForPerson: true})
	call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(byPerson.Number, "/answer"), apitypes.AnswerCard{Version: byPerson.Version, Answer: "yes"}, http.StatusOK)

	p := e.problem(memberSecret, http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Title: "a task", ForPerson: true}, http.StatusUnprocessableEntity)
	if !hasField(p, "for_person") {
		t.Errorf("a task for a person: errors = %+v, want for_person", p.Errors)
	}
}

func TestWhoHandsAQuestionToAPerson(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		rights domain.Rights
		asker  string
		caller string
		note   string
		want   int
		field  string
	}{
		{"the asking agent without the answer right", domain.RightWork, agentSecret, agentSecret, handOverNote, http.StatusOK, ""},
		{"an agent holding the answer right", domain.RightAnswer, memberSecret, agentSecret, handOverNote, http.StatusOK, ""},
		{"an agent holding it gives no reason", domain.RightAnswer, memberSecret, agentSecret, " ", http.StatusUnprocessableEntity, "note"},
		{"another agent without the answer right", domain.RightWork | domain.RightReady | domain.RightTriage, memberSecret, agentSecret, handOverNote, http.StatusForbidden, ""},
		{"the person who asked", 0, memberSecret, memberSecret, "", http.StatusOK, ""},
		{"a person who may not answer and did not ask", 0, userSecret, memberSecret, "", http.StatusForbidden, ""},
		{"a person who may answer", 0, memberSecret, userSecret, "mine", http.StatusOK, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.giveRights(tt.rights)
			q := e.newCard(tt.asker, apitypes.CreateCard{Kind: "question", Title: "Buy the bigger plan?"})
			rec := e.markForPerson(tt.caller, q, apitypes.MarkForPerson{Note: tt.note})
			if rec.Code != tt.want {
				t.Fatalf("mark = %d %s, want %d", rec.Code, rec.Body, tt.want)
			}
			got := e.show(userSecret, q.Number)
			events := e.eventsOf("card.for_person")
			comments := call[[]apitypes.Comment](e, userSecret, http.MethodGet, cardPathOf(q.Number, "/comments"), nil, http.StatusOK)
			if tt.want != http.StatusOK {
				if got.ForPerson || len(events) != 0 || len(comments) != 0 {
					t.Errorf("refused, yet for_person = %v, events %+v, comments %+v", got.ForPerson, events, comments)
				}
				if tt.field != "" && !strings.Contains(rec.Body.String(), `"`+tt.field+`"`) {
					t.Errorf("body = %s, want field %s", rec.Body, tt.field)
				}
				return
			}
			if !got.ForPerson || got.Status != "ready" || len(events) != 1 || events[0]["set"] != true {
				t.Errorf("marked = %+v, events %+v; want an open question for a person and one event", got, events)
			}
			wantComments := 0
			if strings.TrimSpace(tt.note) != "" {
				wantComments = 1
			}
			if len(comments) != wantComments || (wantComments == 1 && comments[0].Body != tt.note) {
				t.Errorf("comments = %+v, want the note posted once when given", comments)
			}
		})
	}
}

func TestAQuestionIsHandedOverOnceAndOnlyWhileOpen(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	q := e.newCard(memberSecret, apitypes.CreateCard{Kind: "question", Title: "Buy the bigger plan?"})
	if rec := e.do(userSecret, http.MethodPost, cardPathOf(q.Number, "/for-person"), apitypes.MarkForPerson{Version: q.Version + 1}); rec.Code != http.StatusConflict {
		t.Errorf("stale version = %d, want 409", rec.Code)
	}
	if rec := e.markForPerson(userSecret, q, apitypes.MarkForPerson{}); rec.Code != http.StatusOK {
		t.Fatalf("mark = %d %s", rec.Code, rec.Body)
	}
	if rec := e.markForPerson(userSecret, q, apitypes.MarkForPerson{}); rec.Code != http.StatusConflict {
		t.Errorf("marking twice = %d, want 409", rec.Code)
	}
	answered := e.newCard(memberSecret, apitypes.CreateCard{Kind: "question", Title: "answered"})
	call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(answered.Number, "/answer"), apitypes.AnswerCard{Version: answered.Version, Answer: "A"}, http.StatusOK)
	if rec := e.markForPerson(userSecret, answered, apitypes.MarkForPerson{}); rec.Code != http.StatusConflict {
		t.Errorf("marking an answered question = %d, want 409", rec.Code)
	}
	task := e.newCard(memberSecret, apitypes.CreateCard{Title: "a task"})
	if rec := e.markForPerson(userSecret, task, apitypes.MarkForPerson{}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("marking a task = %d, want 422", rec.Code)
	}
}

func TestOnlyAPersonWhoMayAnswerClearsTheMark(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightWork | domain.RightAnswer | domain.RightTriage)
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "Buy the bigger plan?", ForPerson: true})
	for name, secret := range map[string]string{"the asking agent with the answer right": agentSecret, "a person who may not answer": memberSecret} {
		if rec := e.markForPerson(secret, q, apitypes.MarkForPerson{Clear: true}); rec.Code != http.StatusForbidden {
			t.Errorf("%s clears = %d %s, want 403", name, rec.Code, rec.Body)
		}
	}
	if rec := e.markForPerson(userSecret, q, apitypes.MarkForPerson{Clear: true, Note: "why"}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("clearing with a note = %d, want 422", rec.Code)
	}
	if got := e.show(userSecret, q.Number); !got.ForPerson || len(e.eventsOf("card.for_person")) != 0 {
		t.Fatalf("after the refusals for_person = %v, events %+v; want untouched", got.ForPerson, e.eventsOf("card.for_person"))
	}
	if rec := e.markForPerson(userSecret, q, apitypes.MarkForPerson{Clear: true}); rec.Code != http.StatusOK {
		t.Fatalf("the owner clears = %d %s", rec.Code, rec.Body)
	}
	if ev := e.eventsOf("card.for_person"); len(ev) != 1 || ev[0]["set"] != false {
		t.Errorf("events = %+v, want one with set false", ev)
	}
	if rec := e.markForPerson(userSecret, q, apitypes.MarkForPerson{Clear: true}); rec.Code != http.StatusConflict {
		t.Errorf("clearing an unmarked question = %d, want 409", rec.Code)
	}
	cur := e.show(userSecret, q.Number)
	rec := e.do(agentSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: cur.Version, Answer: "yes", Note: behalfReason}, apitypes.HeaderSession, "chat-b")
	if rec.Code != http.StatusOK {
		t.Errorf("an agent answering once cleared = %d %s, want 200", rec.Code, rec.Body)
	}
}
