package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func TestAnswerRelayedUsageErrorsSendNothing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{"relayed without a note", []string{"answer", "17", "postgres", "--relayed"}},
		{"relayed with a blank note", []string{"answer", "17", "postgres", "--relayed", "-m", "  "}},
		{"a blank reason", []string{"answer", "17", "postgres", "-m", "  "}},
		{"relayed with decide", []string{"answer", "17", "postgres", "--relayed", "-m", "in chat", "--decide"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{}
			h := newHarness(t, serve(t, rec.wrapAll(map[string]http.HandlerFunc{})))
			err := h.run(t, append([]string{"-p", "demo"}, tt.args...)...)
			if got := cmdutil.ExitCode(err); got != 2 {
				t.Errorf("exit = %d (%v), want 2", got, err)
			}
			if calls := rec.calls(); len(calls) != 0 {
				t.Errorf("requests sent: %v", calls)
			}
		})
	}
}

func TestAnswerRelayedSendsTheNote(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	h := newHarness(t, serve(t, rec.wrapAll(map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/17":         reply(200, fixtureCard()),
		"POST /api/v1/projects/demo/cards/17/answer": reply(200, fixtureCard()),
		"GET /api/v1/actors":                         reply(200, fixtureActors()),
	})))
	if err := h.run(t, "-p", "demo", "answer", "17", "postgres", "--relayed", "-m", "Ana in chat, 14:05"); err != nil {
		t.Fatal(err)
	}
	want := `{"version":7,"answer":"postgres","relayed":true,"note":"Ana in chat, 14:05"}`
	if got := rec.last(t, "POST").Body; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestAnswerSendsAnAgentsReason(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	h := newHarness(t, serve(t, rec.wrapAll(map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/17":         reply(200, fixtureCard()),
		"POST /api/v1/projects/demo/cards/17/answer": reply(200, fixtureCard()),
		"GET /api/v1/actors":                         reply(200, fixtureActors()),
	})))
	if err := h.run(t, "-p", "demo", "answer", "17", "postgres", "-m", "decision #7 names postgres"); err != nil {
		t.Fatal(err)
	}
	want := `{"version":7,"answer":"postgres","note":"decision #7 names postgres"}`
	if got := rec.last(t, "POST").Body; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestShowSaysForWhomAnAgentAnswered(t *testing.T) {
	t.Parallel()
	kept := fixedNow.Add(-3 * time.Minute)
	tests := []struct {
		name string
		kept *time.Time
		want string
	}{
		{"waiting", nil, "for @Ana Souza, not looked at yet: decision #7 names postgres"},
		{"kept", &kept, "for @Ana Souza, kept 3m ago: decision #7 names postgres"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := fixtureCard()
			c.Kind = "question"
			c.Question = &apitypes.Question{Answer: "postgres", AnsweredBy: &claude}
			c.Behalf = &apitypes.Behalf{For: ana, Reason: "decision #7 names postgres", KeptAt: tt.kept}
			h := newHarness(t, serve(t, map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/42": reply(200, c),
				"GET /api/v1/actors":                 reply(200, fixtureActors()),
			}))
			if err := h.run(t, "-p", "demo", "show", "42"); err != nil {
				t.Fatal(err)
			}
			if want := "answer: postgres (@claude-code (agent))\n" + tt.want + "\n"; !strings.Contains(h.out.String(), want) {
				t.Errorf("output lacks %q:\n%s", want, h.out.String())
			}
		})
	}
}

func TestLsAnsweredForAsksTheServer(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	h := newHarness(t, serve(t, rec.wrapAll(map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards": reply(200, apitypes.CardList{}),
	})))
	if err := h.run(t, "-p", "demo", "ls", "--answered-for", "me"); err != nil {
		t.Fatal(err)
	}
	if got := rec.last(t, "GET /api/v1/projects/demo/cards").URI; !strings.Contains(got, "answered_for=me") {
		t.Errorf("request = %q, want answered_for=me", got)
	}
}

func TestShowMarksARelayedAnswer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		by   *apitypes.Actor
		want string
	}{
		{"by an agent", &claude, "answer: postgres (relayed by @claude-code (agent))"},
		{"agent unknown", nil, "answer: postgres (relayed)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := fixtureCard()
			c.Kind = "question"
			c.Relayed = true
			c.Question = &apitypes.Question{Answer: "postgres", AnsweredBy: tt.by}
			h := newHarness(t, serve(t, map[string]http.HandlerFunc{
				"GET /api/v1/projects/demo/cards/42": reply(200, c),
				"GET /api/v1/actors":                 reply(200, fixtureActors()),
			}))
			if err := h.run(t, "-p", "demo", "show", "42"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(h.out.String(), tt.want+"\n") {
				t.Errorf("output lacks %q:\n%s", tt.want, h.out.String())
			}
		})
	}
}

func TestAskPersonAndEscalateSendTheMark(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	marked := fixtureCard()
	marked.Kind, marked.ForPerson = "question", true
	h := newHarness(t, serve(t, rec.wrapAll(map[string]http.HandlerFunc{
		"POST /api/v1/projects/demo/cards":               reply(201, marked),
		"GET /api/v1/projects/demo/cards/17":             reply(200, fixtureCard()),
		"POST /api/v1/projects/demo/cards/17/for-person": reply(200, marked),
		"GET /api/v1/actors":                             reply(200, fixtureActors()),
	})))
	if err := h.run(t, "-p", "demo", "ask", "Buy the bigger plan?", "--person"); err != nil {
		t.Fatal(err)
	}
	if got, want := rec.last(t, "POST").Body, `{"kind":"question","title":"Buy the bigger plan?","for_person":true}`; got != want {
		t.Errorf("ask body = %s, want %s", got, want)
	}
	h.out.Reset()
	if err := h.run(t, "-p", "demo", "escalate", "17", "-m", "about spending"); err != nil {
		t.Fatal(err)
	}
	if got, want := rec.last(t, "POST").Body, `{"version":7,"note":"about spending"}`; got != want {
		t.Errorf("escalate body = %s, want %s", got, want)
	}
	h.out.Reset()
	if err := h.run(t, "-p", "demo", "show", "17"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.out.String(), "for a person") {
		t.Errorf("an unmarked card reads as for a person:\n%s", h.out.String())
	}
}

func TestShowSaysAQuestionIsForAPerson(t *testing.T) {
	t.Parallel()
	c := fixtureCard()
	c.Kind, c.ForPerson = "question", true
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards/42": reply(200, c),
		"GET /api/v1/actors":                 reply(200, fixtureActors()),
	}))
	if err := h.run(t, "-p", "demo", "show", "42"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "  for a person") {
		t.Errorf("output lacks the mark:\n%s", h.out.String())
	}
}

func TestEscalateNeedsAReason(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"escalate", "17"}, {"escalate", "17", "-m", " "}} {
		rec := &recorder{}
		h := newHarness(t, serve(t, rec.wrapAll(map[string]http.HandlerFunc{})))
		err := h.run(t, append([]string{"-p", "demo"}, args...)...)
		if got := cmdutil.ExitCode(err); got != 2 {
			t.Errorf("%v: exit = %d (%v), want 2", args, got, err)
		}
		if calls := rec.calls(); len(calls) != 0 {
			t.Errorf("%v: requests sent: %v", args, calls)
		}
	}
}
