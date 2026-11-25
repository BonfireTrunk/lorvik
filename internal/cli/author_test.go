package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func TestShowMarksAgentAuthorsOnly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		createdBy  apitypes.Actor
		answeredBy apitypes.Actor
		want       []string
		notWant    []string
	}{
		{"agent created, person answered", claude, ana, []string{"by claude-code (agent)", "answer: postgres (@Ana Souza)"}, []string{"Ana Souza (agent)"}},
		{"person created, agent answered", ana, claude, []string{"by Ana Souza  ", "answer: postgres (@claude-code (agent))"}, []string{"Ana Souza (agent)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := fixtureCard()
			c.Kind = "question"
			c.CreatedBy = tt.createdBy
			c.Question = &apitypes.Question{Answer: "postgres", AnsweredBy: &tt.answeredBy}
			h := newHarness(t, serve(t, map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/42": reply(200, c),
				"GET /api/v1/actors":                 reply(200, fixtureActors()),
			}))
			if err := h.run(t, "-p", "demo", "show", "42"); err != nil {
				t.Fatal(err)
			}
			out := h.out.String()
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(out, w) {
					t.Errorf("output has %q:\n%s", w, out)
				}
			}
		})
	}
}

func TestEventSentenceMarksAgentActors(t *testing.T) {
	t.Parallel()
	names := func(a apitypes.Actor) string { return map[string]string{ana.ID: "ana", claude.ID: "claude-code"}[a.ID] }
	card := apitypes.Subject{Type: "card", ID: "5001", Number: 12}
	tests := []struct {
		name string
		e    apitypes.Event
		want string
	}{
		{"agent actor", apitypes.Event{Type: "card.claimed", Actor: claude, Subject: card}, "claude-code (agent) claimed #12"},
		{"user actor", apitypes.Event{Type: "card.claimed", Actor: ana, Subject: card}, "ana claimed #12"},
		{"agent taken from", apitypes.Event{Type: "card.claimed", Actor: ana, Subject: card, Payload: json.RawMessage(`{"taken_from":{"kind":"agent","id":"` + claude.ID + `"}}`)}, "ana claimed #12 (taken over from claude-code (agent))"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := eventSentence(tt.e, names); got != tt.want {
				t.Errorf("eventSentence = %q, want %q", got, tt.want)
			}
		})
	}
}
