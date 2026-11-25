package server

import (
	"errors"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// access.can is the one place a role or rights decide a permission in a
// project: people by role, agents by rights, and an agent's role never counts.
func TestAccessCan(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a    access
		perm domain.Permission
		want bool
	}{
		{"member writes", access{role: domain.RoleMember}, domain.PermCardWrite, true},
		{"member does not ready", access{role: domain.RoleMember}, domain.PermCardReady, false},
		{"reviewer readies", access{role: domain.RoleReviewer}, domain.PermCardReady, true},
		{"agent reads with no rights", access{agent: true, role: domain.RoleViewer}, domain.PermRead, true},
		{"agent without work cannot write", access{agent: true, role: domain.RoleViewer}, domain.PermCardWrite, false},
		{"agent with work writes", access{agent: true, rights: domain.RightWork}, domain.PermCardWrite, true},
		{"agent with work cannot ready", access{agent: true, rights: domain.RightWork}, domain.PermCardReady, false},
		{"agent with ready readies", access{agent: true, rights: domain.RightReady}, domain.PermCardReady, true},
		{"agent with triage triages", access{agent: true, rights: domain.RightTriage}, domain.PermCardTriage, true},
		{"agent with answer answers", access{agent: true, rights: domain.RightAnswer}, domain.PermQuestionAnswer, true},
		{"agent never accepts a decision", access{agent: true, rights: domain.AllRights}, domain.PermDecisionAccept, false},
		{"agent never manages projects", access{agent: true, rights: domain.AllRights}, domain.PermProjectManage, false},
		{"agent never registers agents", access{agent: true, rights: domain.AllRights}, domain.PermAgentRegister, false},
		{"a high role on an agent row counts for nothing", access{agent: true, role: domain.RoleOwner}, domain.PermCardWrite, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.a.can(tt.perm); got != tt.want {
				t.Errorf("can(%s) = %v, want %v", tt.perm, got, tt.want)
			}
		})
	}
}

// Overriding another actor's claim stays human, whatever an agent holds.
func TestMayOverrideIsHumanOnly(t *testing.T) {
	t.Parallel()
	human := Principal{Actor: domain.ActorRef{Kind: domain.ActorUser, ID: 1}}
	agent := Principal{Actor: domain.ActorRef{Kind: domain.ActorAgent, ID: 2}}
	if !(access{role: domain.RoleReviewer}).mayOverride(human) {
		t.Error("a reviewer may override")
	}
	if (access{role: domain.RoleMember}).mayOverride(human) {
		t.Error("a member may not override")
	}
	if (access{agent: true, rights: domain.AllRights}).mayOverride(agent) {
		t.Error("an agent with every right may not override")
	}
}

// An agent that lacks a right hears which one and where its owner can give it;
// a permission no right grants keeps the role message.
func TestRefusalNamesTheRightAndTheChat(t *testing.T) {
	t.Parallel()
	agent := Principal{Actor: domain.ActorRef{Kind: domain.ActorAgent, ID: 2}, AgentName: "codex", Session: "swift-heron-1a2b-c3"}
	a := access{agent: true, role: domain.RoleViewer, slug: "web"}
	err := a.require(agent, domain.PermCardReady)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err = %v, want forbidden", err)
	}
	if got, want := toProblem(err).Detail, "forbidden: agent codex lacks the ready right in web"; got != want {
		t.Errorf("detail = %q, want %q", got, want)
	}
	if got, want := toProblem(err).Hint, "its owner can give it on the Access page, to the agent or to this chat (swift-heron-1a2b)"; got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}
	agent.Session = ""
	if got, want := toProblem(a.require(agent, domain.PermCardWrite)).Hint, "its owner can give it on the Access page"; got != want {
		t.Errorf("hint without a session = %q, want %q", got, want)
	}
	if got := toProblem(a.require(agent, domain.PermDecisionAccept)).Detail; got != "forbidden: viewer cannot accept decisions" {
		t.Errorf("detail for a permission no right grants = %q", got)
	}
}
