package server

import (
	"net/http"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// The domain owns the role rules, so every door that grants standing refuses
// the same things with 422: an agent above viewer or a person with rights, and a
// project role above reviewer.
func TestRoleCapsHoldOnEveryDoor(t *testing.T) {
	t.Parallel()
	agentPath := func(e *testEnv) string { return "/agents/" + e.agent.ID.String() + "/projects/lorvik" }
	tests := []struct {
		name  string
		path  func(e *testEnv) string
		bad   apitypes.PutMembership
		field string
		ok    apitypes.PutMembership
	}{
		{"org role for an agent", func(e *testEnv) string { return "/members/agent/" + e.agent.ID.String() },
			apitypes.PutMembership{Role: "admin"}, "role", apitypes.PutMembership{Rights: []string{"work"}}},
		{"org member role for an agent", func(e *testEnv) string { return "/members/agent/" + e.agent.ID.String() },
			apitypes.PutMembership{Role: "member"}, "role", apitypes.PutMembership{Role: "viewer", Rights: []string{"work", "ready"}}},
		{"agent bound to a project", agentPath,
			apitypes.PutMembership{Role: "reviewer"}, "role", apitypes.PutMembership{Rights: []string{"work", "answer"}}},
		{"project role for an agent", func(e *testEnv) string { return "/projects/lorvik/members/agent/" + e.agent.ID.String() },
			apitypes.PutMembership{Role: "reviewer"}, "role", apitypes.PutMembership{Rights: []string{"triage"}}},
		{"viewer alone for an org agent", func(e *testEnv) string { return "/members/agent/" + e.agent.ID.String() },
			apitypes.PutMembership{Role: "viewer"}, "rights", apitypes.PutMembership{}},
		{"viewer alone for a bound agent", agentPath,
			apitypes.PutMembership{Role: "viewer"}, "rights", apitypes.PutMembership{}},
		{"viewer alone for a project agent", func(e *testEnv) string { return "/projects/lorvik/members/agent/" + e.agent.ID.String() },
			apitypes.PutMembership{Role: "viewer"}, "rights", apitypes.PutMembership{}},
		{"project role for a user", func(e *testEnv) string { return "/projects/lorvik/members/user/" + e.member.ID.String() },
			apitypes.PutMembership{Role: "admin"}, "role", apitypes.PutMembership{Role: "reviewer"}},
		{"rights for a user in a project", func(e *testEnv) string { return "/projects/lorvik/members/user/" + e.member.ID.String() },
			apitypes.PutMembership{Role: "member", Rights: []string{"work"}}, "rights", apitypes.PutMembership{Role: "member"}},
		{"rights for a user in the org", func(e *testEnv) string { return "/members/user/" + e.member.ID.String() },
			apitypes.PutMembership{Role: "member", Rights: []string{"work"}}, "rights", apitypes.PutMembership{Role: "member"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			p := e.problem(userSecret, http.MethodPut, tt.path(e), tt.bad, http.StatusUnprocessableEntity)
			if len(p.Errors) != 1 || p.Errors[0].Field != tt.field {
				t.Errorf("problem = %+v, want one error on %s", p, tt.field)
			}
			if rec := e.do(userSecret, http.MethodPut, tt.path(e), tt.ok); rec.Code != http.StatusOK {
				t.Errorf("%+v = %d, want 200: %s", tt.ok, rec.Code, rec.Body)
			}
		})
	}
}

// A granter hands on only the rights their own role holds, wherever they
// grant: a reviewer who owns an agent binds it with all four, and once demoted
// to member can give it work and no more.
func TestGrantedRightsAreCappedByTheGranter(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.setOrgRole(e.member, domain.RoleReviewer, domain.RecordActive)
	mine := call[apitypes.AgentCreated](e, memberSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "mine"}, http.StatusCreated)
	path := "/agents/" + mine.Agent.ID + "/projects/lorvik"
	call[apitypes.Agent](e, memberSecret, http.MethodPut, path, apitypes.PutMembership{Rights: []string{"work", "ready", "triage", "answer"}}, http.StatusOK)
	e.setOrgRole(e.member, domain.RoleMember, domain.RecordActive)
	p := e.problem(memberSecret, http.MethodPut, path, apitypes.PutMembership{Rights: []string{"work", "ready"}}, http.StatusUnprocessableEntity)
	if !hasField(p, "rights") {
		t.Errorf("errors = %+v, want rights: a member cannot give ready", p.Errors)
	}
	call[apitypes.Agent](e, memberSecret, http.MethodPut, path, apitypes.PutMembership{Rights: []string{"work"}}, http.StatusOK)
}
