package cmdutil

import (
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func TestAuthorMarksAgentsOnly(t *testing.T) {
	t.Parallel()
	names := NameFunc(func(a apitypes.Actor) string { return map[string]string{"1": "ana", "2": "claude-code"}[a.ID] })
	p := &Printer{Name: names}
	tests := []struct {
		name      string
		actor     apitypes.Actor
		wantPlain string
		wantWho   string
	}{
		{"user", apitypes.Actor{Kind: "user", ID: "1"}, "ana", "@ana"},
		{"agent", apitypes.Actor{Kind: "agent", ID: "2"}, "claude-code (agent)", "@claude-code (agent)"},
		{"unresolved agent", apitypes.Actor{Kind: "agent", ID: "9"}, "9 (agent)", "@9 (agent)"},
		{"unresolved user", apitypes.Actor{Kind: "user", ID: "8"}, "8", "@8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := p.Author(tt.actor); got != tt.wantPlain {
				t.Errorf("Author = %q, want %q", got, tt.wantPlain)
			}
			if got := p.Who(tt.actor); got != tt.wantWho {
				t.Errorf("Who = %q, want %q", got, tt.wantWho)
			}
		})
	}
}

func TestCardLineMarksAgentHolders(t *testing.T) {
	t.Parallel()
	agent := apitypes.Actor{Kind: "agent", ID: "2"}
	user := apitypes.Actor{Kind: "user", ID: "1"}
	tests := []struct {
		name string
		b    apitypes.CardBrief
		want string
	}{
		{"agent claimant", apitypes.CardBrief{Number: 1, Status: "working", Priority: "high", Title: "T", Claim: &apitypes.Claim{Actor: agent}}, "#1  working  high  T  @bot (agent)"},
		{"agent assignee", apitypes.CardBrief{Number: 1, Status: "ready", Priority: "high", Title: "T", Assignee: &agent}, "#1  ready  high  T  @bot (agent)"},
		{"user assignee", apitypes.CardBrief{Number: 1, Status: "ready", Priority: "high", Title: "T", Assignee: &user}, "#1  ready  high  T  @ana"},
	}
	names := NameFunc(func(a apitypes.Actor) string { return map[string]string{"1": "ana", "2": "bot"}[a.ID] })
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := CardLine(tt.b, false, names); got != tt.want {
				t.Errorf("CardLine = %q, want %q", got, tt.want)
			}
		})
	}
}
