package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

const (
	orgViewerSecret     = "lvk_u_orgviewer-secret"
	projectViewerSecret = "lvk_u_projviewer-secret"
)

// pageAuthzEnv adds to newEnv: a private project "secret" holding a card and
// a note with pages, an org viewer, and a member of the org who is also a
// viewer in "secret" only.
func pageAuthzEnv(t *testing.T) (e *testEnv, secretCard apitypes.Card) {
	t.Helper()
	e = newEnv(t)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "secret", Name: "Secret", Visibility: "private"}, http.StatusCreated)
	secretCard = call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/secret/cards", apitypes.CreateCard{Title: "hidden", Status: "ready", Page: pageOne}, http.StatusCreated)
	call[apitypes.Note](e, userSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "secret", Slug: "hidden-note", Title: "hidden", Body: "b", Page: pageOne}, http.StatusCreated)

	orgViewer := e.seedUser("ann", domain.RoleViewer)
	e.seedToken(orgViewerSecret, orgViewer, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
	pv := e.seedUser("pat", domain.RoleMember)
	e.seedToken(projectViewerSecret, pv, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
	secret, err := e.fs.GetProjectBySlug(context.Background(), e.org.ID, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.fs.PutProjectMember(context.Background(), domain.ProjectMember{ProjectID: secret.ID, Actor: pv, Role: domain.RoleViewer, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	return e, secretCard
}

func TestPageRoutesHideAPrivateProjectWith404(t *testing.T) {
	t.Parallel()
	e, c := pageAuthzEnv(t)
	tests := []struct {
		name, secret, path string
	}{
		{"card page, org member outside the project", memberSecret, secretCardPath(c.Number, "/page")},
		{"card page, org viewer outside the project", orgViewerSecret, secretCardPath(c.Number, "/page")},
		{"card page, project-bound agent of another project", agent2Secret, secretCardPath(c.Number, "/page")},
		{"note page, org member outside the project", memberSecret, "/notes/hidden-note/page?project=secret"},
		{"note page, org viewer outside the project", orgViewerSecret, "/notes/hidden-note/page?project=secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e.problem(tt.secret, http.MethodGet, tt.path, nil, http.StatusNotFound)
		})
	}
	t.Run("a project viewer reads both pages", func(t *testing.T) {
		t.Parallel()
		for _, path := range []string{secretCardPath(c.Number, "/page"), "/notes/hidden-note/page?project=secret"} {
			if got := call[apitypes.HTMLPage](e, projectViewerSecret, http.MethodGet, path, nil, http.StatusOK); got.HTML != pageOne {
				t.Errorf("%s: html = %q, want the page", path, got.HTML)
			}
		}
	})
}

func TestViewersCannotSetOrRemoveAPageButMembersCan(t *testing.T) {
	t.Parallel()
	e, secretCard := pageAuthzEnv(t)
	open := e.newCard(userSecret, apitypes.CreateCard{Title: "open", Page: pageOne})
	note := call[apitypes.Note](e, userSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "lorvik", Slug: "open-note", Title: "n", Body: "b", Page: pageOne}, http.StatusCreated)
	secretNote := call[apitypes.Note](e, userSecret, http.MethodGet, "/notes/hidden-note?project=secret", nil, http.StatusOK)

	cardPatch := func(version int64, page string) apitypes.UpdateCard {
		return apitypes.UpdateCard{Version: version, Page: &page}
	}
	notePatch := func(version int64, page string) apitypes.UpdateNote {
		body := "b"
		return apitypes.UpdateNote{Version: version, Body: &body, Page: &page}
	}
	tests := []struct {
		name, secret, path string
		body               any
		want               int
	}{
		{"org viewer sets a card page", orgViewerSecret, cardPathOf(open.Number, ""), cardPatch(open.Version, pageTwo), http.StatusForbidden},
		{"org viewer removes a card page", orgViewerSecret, cardPathOf(open.Number, ""), cardPatch(open.Version, ""), http.StatusForbidden},
		{"project viewer sets a card page", projectViewerSecret, secretCardPath(secretCard.Number, ""), cardPatch(secretCard.Version, pageTwo), http.StatusForbidden},
		{"project viewer removes a card page", projectViewerSecret, secretCardPath(secretCard.Number, ""), cardPatch(secretCard.Version, ""), http.StatusForbidden},
		{"org viewer sets a note page", orgViewerSecret, "/notes/open-note?project=lorvik", notePatch(note.Version, pageTwo), http.StatusForbidden},
		{"org viewer removes a note page", orgViewerSecret, "/notes/open-note?project=lorvik", notePatch(note.Version, ""), http.StatusForbidden},
		{"project viewer sets a note page", projectViewerSecret, "/notes/hidden-note?project=secret", notePatch(secretNote.Version, pageTwo), http.StatusForbidden},
		{"member sets a card page on an open card", memberSecret, cardPathOf(open.Number, ""), cardPatch(open.Version, pageTwo), http.StatusOK},
		{"member sets a note page", memberSecret, "/notes/open-note?project=lorvik", notePatch(note.Version, pageTwo), http.StatusOK},
	}
	for _, tt := range tests {
		// Sequential: the member cases bump the version the viewer cases read.
		if tt.want == http.StatusForbidden {
			e.problem(tt.secret, http.MethodPatch, tt.path, tt.body, tt.want)
			continue
		}
		if rec := e.do(tt.secret, http.MethodPatch, tt.path, tt.body); rec.Code != tt.want {
			t.Errorf("%s: status = %d, want %d: %s", tt.name, rec.Code, tt.want, rec.Body)
		}
	}

	if got := call[apitypes.HTMLPage](e, userSecret, http.MethodGet, cardPathOf(open.Number, "/page"), nil, http.StatusOK); got.HTML != pageTwo {
		t.Errorf("open card page = %q, want the member's page", got.HTML)
	}
	for _, path := range []string{secretCardPath(secretCard.Number, "/page"), "/notes/hidden-note/page?project=secret"} {
		if got := call[apitypes.HTMLPage](e, userSecret, http.MethodGet, path, nil, http.StatusOK); got.HTML != pageOne {
			t.Errorf("%s = %q, want the page untouched by the refused viewer edits", path, got.HTML)
		}
	}
}

func secretCardPath(number int64, suffix string) string {
	return "/projects/secret/cards/" + itoa(number) + suffix
}
