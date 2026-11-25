package server

import (
	"net/http"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

func askAs(t *testing.T, e *testEnv, secret, session, slug string, in apitypes.CreateCard) apitypes.Card {
	t.Helper()
	in.Kind = "question"
	return inChat[apitypes.Card](e, secret, session, http.MethodPost, "/projects/"+slug+"/cards", in, http.StatusCreated)
}

func TestAQuestionAChatMayAnswerWakesIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightWork | domain.RightAnswer)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "docs", Name: "Docs"}, http.StatusCreated)

	byPerson := askAs(t, e, memberSecret, "", "lorvik", apitypes.CreateCard{Title: "asked by a person"})
	byOtherChat := askAs(t, e, agentSecret, "worker", "lorvik", apitypes.CreateCard{Title: "asked by another chat"})
	bySubagent := askAs(t, e, agentSecret, "duty-c2", "lorvik", apitypes.CreateCard{Title: "asked by this chat's subagent"})
	forPerson := askAs(t, e, memberSecret, "", "lorvik", apitypes.CreateCard{Title: "for a person", ForPerson: true})
	answered := askAs(t, e, memberSecret, "", "lorvik", apitypes.CreateCard{Title: "already answered"})
	call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(answered.Number, "/answer"), apitypes.AnswerCard{Version: answered.Version, Answer: "A"}, http.StatusOK)
	inDocs := askAs(t, e, userSecret, "", "docs", apitypes.CreateCard{Title: "asked in docs"})
	task := e.newCard(memberSecret, apitypes.CreateCard{Title: "a task"})
	cleared := askAs(t, e, memberSecret, "", "lorvik", apitypes.CreateCard{Title: "handed back", ForPerson: true})
	if rec := e.markForPerson(userSecret, cleared, apitypes.MarkForPerson{Clear: true}); rec.Code != http.StatusOK {
		t.Fatalf("clear = %d %s", rec.Code, rec.Body)
	}

	tests := []struct {
		name, secret, session, query string
		want                         map[string]bool
	}{
		{"a chat working in lorvik", agentSecret, "duty", "&chat_project=lorvik", map[string]bool{
			byPerson.ID: true, byOtherChat.ID: true, bySubagent.ID: false, forPerson.ID: false, answered.ID: false, inDocs.ID: false, task.ID: false, cleared.ID: true}},
		{"a chat that named no project", agentSecret, "duty", "", map[string]bool{byPerson.ID: true, inDocs.ID: true, bySubagent.ID: false}},
		{"a chat working in docs", agentSecret, "duty", "&chat_project=docs", map[string]bool{byPerson.ID: false, inDocs.ID: true}},
		{"a chat of an agent without the answer right", agent2Secret, "duty", "&chat_project=lorvik", map[string]bool{byPerson.ID: false, byOtherChat.ID: false, cleared.ID: false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := inboxAs(t, e, tt.secret, tt.session, "?cursor=0"+tt.query)
			wake := wakeByCard(in, "card.created")
			for id, want := range tt.want {
				if got, ok := wake[id]; !ok || got != want {
					t.Errorf("card %s: wake = %v (listed %v), want %v", id, got, ok, want)
				}
			}
			for _, ev := range in.Events {
				if ev.Type == "card.created" && ev.Subject.ID == byPerson.ID && ev.Mine {
					t.Errorf("a person's question reads as the caller's own card: %+v", ev)
				}
			}
		})
	}
	if got, ok := wakeByCard(inboxAs(t, e, agentSecret, "duty", "?cursor=0&chat_project=lorvik"), "card.for_person")[cleared.ID]; !ok || !got {
		t.Errorf("the cleared mark: wake = %v (listed %v), want true", got, ok)
	}
	for _, ev := range inboxAs(t, e, userSecret, "", "?cursor=0").Events {
		if ev.Wake != ev.Mine {
			t.Errorf("a person's inbox %s on %s: wake %v, mine %v; want wake to follow mine", ev.Type, ev.Subject.ID, ev.Wake, ev.Mine)
		}
	}
}

func TestTheChatThatSettledAnAnswerIsToldWhatThePersonDidWithIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.RightAnswer)
	kept := e.settle("kept", "A")
	e.keep(kept)
	changed := e.settle("changed", "A")
	call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(changed.Number, "/answer"), apitypes.AnswerCard{Version: changed.Version, Answer: "B"}, http.StatusOK)
	plain := e.newCard(memberSecret, apitypes.CreateCard{Kind: "question", Title: "a person's own answer"})
	call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(plain.Number, "/answer"), apitypes.AnswerCard{Version: plain.Version, Answer: "A"}, http.StatusOK)

	type flags struct{ mine, wake bool }
	of := func(in apitypes.Inbox, eventType, card string) (flags, bool) {
		for _, ev := range in.Events {
			if ev.Type == eventType && ev.Subject.ID == card && ev.Actor.Kind == "user" {
				return flags{ev.Mine, ev.Wake}, true
			}
		}
		return flags{}, false
	}
	for _, tt := range []struct {
		name, session string
		want          flags
	}{
		{"the chat that answered", "chat-a", flags{true, true}},
		{"its subagent's parent label", "chat-a-c2", flags{true, true}},
		{"another chat of the agent", "chat-b", flags{true, false}},
	} {
		in := inboxAs(t, e, agentSecret, tt.session, "?cursor=0&chat_project=lorvik")
		for _, c := range []struct {
			eventType string
			card      apitypes.Card
		}{{"card.kept", kept}, {"card.answered", changed}} {
			if got, ok := of(in, c.eventType, c.card.ID); !ok || got != tt.want {
				t.Errorf("%s, %s on %q: %+v (listed %v), want %+v", tt.name, c.eventType, c.card.Title, got, ok, tt.want)
			}
		}
		if got, ok := of(in, "card.answered", plain.ID); !ok || got != (flags{}) {
			t.Errorf("%s, a person's own answer: %+v (listed %v), want neither mine nor wake", tt.name, got, ok)
		}
	}
	if got, ok := of(inboxAs(t, e, agent2Secret, "chat-a", "?cursor=0&chat_project=lorvik"), "card.kept", kept.ID); !ok || got != (flags{}) {
		t.Errorf("another agent's chat with the same label: %+v (listed %v), want neither", got, ok)
	}
}
