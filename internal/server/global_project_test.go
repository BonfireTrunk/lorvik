package server

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func TestGlobalProjectListedFirstWithScope(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "aaa", Name: "Aaa"}, http.StatusCreated)
	ps := call[apitypes.ProjectList](e, userSecret, http.MethodGet, "/projects", nil, http.StatusOK).Items
	if len(ps) != 3 || ps[0].Slug != "global" || ps[0].Scope != "global" {
		t.Fatalf("projects = %+v, want the global project first even though aaa sorts earlier", ps)
	}
	for _, p := range ps[1:] {
		if p.Scope != "project" {
			t.Errorf("project %s scope = %q, want project", p.Slug, p.Scope)
		}
	}
}

func TestProjectOnlyAgentReadsGlobalButCannotWriteThere(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	card := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/global/cards", apitypes.CreateCard{Title: "applies everywhere"}, http.StatusCreated)
	call[apitypes.Note](e, userSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "global", Slug: "rules", Title: "Rules", Body: "shared zebra rules"}, http.StatusCreated)

	got := call[apitypes.Project](e, agent2Secret, http.MethodGet, "/projects/global", nil, http.StatusOK)
	if got.Role != "viewer" {
		t.Errorf("project-only agent role in global = %q, want viewer", got.Role)
	}
	call[apitypes.Card](e, agent2Secret, http.MethodGet, "/projects/global/cards/"+strconv.FormatInt(card.Number, 10), nil, http.StatusOK)
	call[apitypes.Note](e, agent2Secret, http.MethodGet, "/notes/rules?project=global", nil, http.StatusOK)
	if l := call[apitypes.NoteList](e, agent2Secret, http.MethodGet, "/notes?project=global", nil, http.StatusOK); len(l.Items) != 1 {
		t.Errorf("project-only agent lists %d global notes, want 1", len(l.Items))
	}
	if r := call[apitypes.SearchResult](e, agent2Secret, http.MethodGet, "/search?q="+url.QueryEscape("zebra"), nil, http.StatusOK); len(r.Items) != 1 || r.Items[0].Project == nil || *r.Items[0].Project != "global" {
		t.Errorf("search hits = %+v, want the global note", r.Items)
	}

	e.problem(agent2Secret, http.MethodPost, "/projects/global/cards", apitypes.CreateCard{Title: "nope"}, http.StatusForbidden)
	e.problem(agent2Secret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "global", Slug: "mine", Title: "Mine", Body: "x"}, http.StatusForbidden)
}

func TestOrgMemberWritesInGlobal(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, secret := range []string{memberSecret, agentSecret} {
		call[apitypes.Card](e, secret, http.MethodPost, "/projects/global/cards", apitypes.CreateCard{Title: "from " + secret}, http.StatusCreated)
	}
	call[apitypes.Note](e, memberSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "global", Slug: "mine", Title: "Mine", Body: "x"}, http.StatusCreated)
}

func TestGlobalSlugIsReservedAndGlobalCannotBeArchived(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.problem(userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "global", Name: "Another"}, http.StatusUnprocessableEntity)
	p := e.problem(userSecret, http.MethodPatch, "/projects/global", apitypes.UpdateProject{Status: new("archived")}, http.StatusConflict)
	if p.Hint != "the global project cannot be archived" {
		t.Errorf("hint = %q, want the archive refusal", p.Hint)
	}
	call[apitypes.Project](e, userSecret, http.MethodPatch, "/projects/global", apitypes.UpdateProject{Name: new("Everything")}, http.StatusOK)
}

func TestNoteWithoutProjectIsRejected(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.problem(userSecret, http.MethodPost, "/notes", apitypes.CreateNote{Slug: "loose", Title: "Loose", Body: "x"}, http.StatusUnprocessableEntity)
	e.problem(userSecret, http.MethodGet, "/notes/loose", nil, http.StatusUnprocessableEntity)
}
