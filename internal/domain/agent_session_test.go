package domain

import (
	"strings"
	"testing"
	"time"
)

func TestBaseSession(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"mellow-comet-020d", "mellow-comet-020d"},
		{"mellow-comet-020d-c271", "mellow-comet-020d"},
		{"mellow-comet-020d-c271-c3", "mellow-comet-020d"},
		{"witty-lynx-c123", "witty-lynx-c123"},
		{"witty-lynx-c123-c5", "witty-lynx-c123"},
		{"witty-lynx-c123-c5-c6", "witty-lynx-c123"},
		{"chat-a-c5", "chat-a"},
		{"a-c", "a-c"},
		{"a-c12x", "a-c12x"},
		{"a-c12-cx", "a-c12-cx"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := BaseSession(tt.in); got != tt.want {
			t.Errorf("BaseSession(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRightsValidationOnMembers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		actor   ActorRef
		rights  Rights
		wantErr bool
	}{
		{"agent with rights", bot, RightWork | RightReady, false},
		{"agent without rights", bot, 0, false},
		{"user without rights", alice, 0, false},
		{"user with rights", alice, RightWork, true},
		{"unknown bit", bot, Rights(32), true},
	}
	for _, tt := range tests {
		pm := ProjectMember{Actor: tt.actor, Role: RoleViewer, Rights: tt.rights}
		m := Membership{Actor: tt.actor, Role: RoleViewer, Rights: tt.rights}
		if err := pm.Validate(); (err != nil) != tt.wantErr {
			t.Errorf("%s: ProjectMember.Validate() = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
		if err := m.Validate(); (err != nil) != tt.wantErr {
			t.Errorf("%s: Membership.Validate() = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}

func TestAgentSessionGrantValidate(t *testing.T) {
	t.Parallel()
	ok := AgentSessionGrant{Session: "mellow-comet", Rights: RightWork, Status: RecordActive}
	tests := []struct {
		name    string
		mutate  func(*AgentSessionGrant)
		wantErr bool
	}{
		{"valid", func(*AgentSessionGrant) {}, false},
		{"removed", func(g *AgentSessionGrant) { g.Status = RecordRemoved }, false},
		{"empty session", func(g *AgentSessionGrant) { g.Session = "" }, true},
		{"subagent session", func(g *AgentSessionGrant) { g.Session = "mellow-comet-c2" }, true},
		{"no rights", func(g *AgentSessionGrant) { g.Rights = 0 }, true},
		{"unknown right", func(g *AgentSessionGrant) { g.Rights = RightWork | 64 }, true},
		{"pending", func(g *AgentSessionGrant) { g.Status = RecordPending }, true},
		{"instructions at the limit", func(g *AgentSessionGrant) { g.Instructions = strings.Repeat("é", MaxGrantInstructions) }, false},
		{"instructions past the limit", func(g *AgentSessionGrant) { g.Instructions = strings.Repeat("é", MaxGrantInstructions+1) }, true},
	}
	for _, tt := range tests {
		g := ok
		tt.mutate(&g)
		if err := g.Validate(); (err != nil) != tt.wantErr {
			t.Errorf("%s: Validate() = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}

func TestAgentSessionGrantLive(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { return new(now.Add(d)) }
	tests := []struct {
		name string
		g    AgentSessionGrant
		want bool
	}{
		{"no end", AgentSessionGrant{Status: RecordActive}, true},
		{"ends later", AgentSessionGrant{Status: RecordActive, EndsAt: at(time.Second)}, true},
		{"ends now", AgentSessionGrant{Status: RecordActive, EndsAt: at(0)}, false},
		{"ended", AgentSessionGrant{Status: RecordActive, EndsAt: at(-time.Second)}, false},
		{"taken back before its end", AgentSessionGrant{Status: RecordRemoved, EndsAt: at(time.Hour)}, false},
		{"taken back, no end", AgentSessionGrant{Status: RecordRemoved}, false},
	}
	for _, tt := range tests {
		if got := tt.g.Live(now); got != tt.want {
			t.Errorf("%s: Live = %v, want %v", tt.name, got, tt.want)
		}
	}
}
