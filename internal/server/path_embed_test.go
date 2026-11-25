package server

import (
	"net/http"
	"slices"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

// Route inputs embed path structs. huma reads only exported fields, and an
// embedded unexported type is an unexported field, so such an input never
// receives its path values and answers 422 for every request. These pin the
// routes whose inputs embed IDPath and ProjectMemberPath.
func TestBindAndUnbindAgentReadPathParams(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	created := call[apitypes.AgentCreated](e, userSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "bound", Projects: []string{"lorvik"}}, http.StatusCreated)
	path := "/agents/" + created.Agent.ID + "/projects/lorvik"

	bound := call[apitypes.Agent](e, userSecret, http.MethodPut, path, apitypes.PutMembership{Rights: []string{"work", "ready"}}, http.StatusOK)
	if len(bound.Projects) != 1 || !slices.Equal(bound.Projects[0].Rights, []string{"work", "ready"}) {
		t.Fatalf("bind: projects = %+v, want lorvik with work and ready", bound.Projects)
	}

	if rec := e.do(userSecret, http.MethodDelete, path, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unbind: status %d body %s", rec.Code, rec.Body.String())
	}
	after := call[apitypes.Agent](e, userSecret, http.MethodGet, "/agents/"+created.Agent.ID, nil, http.StatusOK)
	if len(after.Projects) != 0 {
		t.Fatalf("after unbind: projects = %+v, want none", after.Projects)
	}
}

func TestPutAndRemoveProjectMemberReadPathParams(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := "/projects/lorvik/members/user/" + e.member.ID.String()

	m := call[apitypes.ProjectMember](e, userSecret, http.MethodPut, path, apitypes.PutMembership{Role: "reviewer"}, http.StatusOK)
	if m.Role != "reviewer" {
		t.Fatalf("put member: role = %q, want reviewer", m.Role)
	}
	if rec := e.do(userSecret, http.MethodDelete, path, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("remove member: status %d body %s", rec.Code, rec.Body.String())
	}
}
