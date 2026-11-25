package server

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

func TestResolveRefsKeepsOnlyTargetsThatExist(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	c1 := e.readyCard(agentSecret, "one")
	other := call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "web", Name: "Web"}, http.StatusCreated)
	webCard := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/web/cards", apitypes.CreateCard{Title: "web one"}, http.StatusCreated)
	globalNote := call[apitypes.Note](e, userSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "global", Slug: "handbook", Title: "Handbook", Body: "org level"}, http.StatusCreated)
	projNote := call[apitypes.Note](e, userSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "lorvik", Slug: "arch", Title: "Arch", Body: "project level"}, http.StatusCreated)
	_ = other

	text := "[[card:1]] [[card:web/1]] [[card:99]] [[note:arch]] [[note:handbook]] [[note:global/handbook]] [[card:" + webCard.ID + "]] [[note:nope]]"
	source := e.ids.New()
	links, err := e.svc.resolveRefs(ctx, e.fs, e.org.ID, &e.proj.ID, domain.SubjectNote, source, domain.ParseRefs(text))
	if err != nil {
		t.Fatal(err)
	}
	var targets []string
	for _, l := range links {
		targets = append(targets, l.TargetType.String()+":"+l.TargetID.String())
	}
	want := []string{"card:" + c1.ID, "card:" + webCard.ID, "note:" + projNote.ID, "note:" + globalNote.ID}
	if !slices.Equal(targets, want) {
		t.Errorf("resolved = %v\nwant       %v (missing card and missing note dropped; the bare handbook falls back to the global note; the id form of web/1 deduplicated)", targets, want)
	}

	// A source with no project of its own; bare card numbers and
	// note slugs resolve in the global project.
	orgLinks, err := e.svc.resolveRefs(ctx, e.fs, e.org.ID, nil, domain.SubjectCard, source, domain.ParseRefs("[[card:1]] [[card:lorvik/1]] [[note:handbook]]"))
	if err != nil {
		t.Fatal(err)
	}
	if len(orgLinks) != 2 || orgLinks[0].TargetID.String() != c1.ID || orgLinks[1].TargetID.String() != globalNote.ID {
		t.Errorf("org-level resolution = %+v, want lorvik/1 and the global note only", orgLinks)
	}
}

func TestNoteRefResolvesInProjectBeforeGlobal(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	globalNote := call[apitypes.Note](e, userSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "global", Slug: "style", Title: "Style", Body: "global"}, http.StatusCreated)
	onlyGlobal := call[apitypes.Note](e, userSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "global", Slug: "deploy", Title: "Deploy", Body: "global"}, http.StatusCreated)
	projNote := call[apitypes.Note](e, userSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "lorvik", Slug: "style", Title: "Style", Body: "project"}, http.StatusCreated)

	links, err := e.svc.resolveRefs(ctx, e.fs, e.org.ID, &e.proj.ID, domain.SubjectCard, e.ids.New(), domain.ParseRefs("[[note:style]] [[note:deploy]]"))
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 || links[0].TargetID.String() != projNote.ID || links[1].TargetID.String() != onlyGlobal.ID {
		t.Errorf("resolved = %+v, want the project's own style note and the global deploy note", links)
	}
	if projNote.ID == globalNote.ID {
		t.Fatal("test setup: the two style notes must differ")
	}
}

func TestBacklinksHideSourcesTheCallerCannotSee(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	target := call[apitypes.Note](e, userSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "global", Slug: "handbook", Title: "Handbook", Body: "org level"}, http.StatusCreated)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "secret", Name: "Secret", Visibility: "private"}, http.StatusCreated)
	call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/secret/cards", apitypes.CreateCard{Title: "hidden", Body: "see [[note:handbook]]"}, http.StatusCreated)
	visible := e.newCard(agentSecret, apitypes.CreateCard{Title: "shown", Body: "see [[note:handbook]]"})

	asOwner := call[apitypes.Note](e, userSecret, http.MethodGet, "/notes/"+target.ID, nil, http.StatusOK)
	if len(asOwner.Backlinks) != 2 {
		t.Errorf("owner backlinks = %+v, want both cards", asOwner.Backlinks)
	}
	asAgent := call[apitypes.Note](e, agentSecret, http.MethodGet, "/notes/"+target.ID, nil, http.StatusOK)
	if len(asAgent.Backlinks) != 1 || asAgent.Backlinks[0].Number != visible.Number || asAgent.Backlinks[0].Project == nil || *asAgent.Backlinks[0].Project != "lorvik" {
		t.Errorf("agent backlinks = %+v, want only the lorvik card", asAgent.Backlinks)
	}
}

func TestNumericRefsResolveOnlyToTheirTypeInTheOrg(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	card := e.readyCard(agentSecret, "here")
	note := call[apitypes.Note](e, userSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "lorvik", Slug: "arch", Title: "Arch", Body: "x"}, http.StatusCreated)
	otherOrg, _ := e.fs.CreateOrg(ctx, domain.Org{ID: e.ids.New(), Name: "Other", Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	otherProj, _ := e.fs.CreateProject(ctx, domain.Project{ID: e.ids.New(), OrgID: otherOrg.ID, Slug: "lorvik", Name: "Theirs", Visibility: domain.VisibilityOrg,
		Concurrency: domain.ConcurrencyParallel, Scope: domain.ProjectScopeProject, Lease: time.Hour, CreatedBy: e.user, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	foreign, err := e.fs.CreateCard(ctx, domain.Card{ID: e.ids.New(), OrgID: otherOrg.ID, ProjectID: otherProj.ID, Number: 1, Kind: domain.KindTask, Title: "elsewhere",
		CreatedBy: e.user, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	text := "[[card:" + card.ID + "]] [[note:" + note.ID + "]] [[note:" + card.ID + "]] [[card:" + note.ID + "]] [[card:" + e.ids.New().String() + "]] [[card:" + foreign.ID.String() + "]]"
	links, err := e.svc.resolveRefs(ctx, e.fs, e.org.ID, &e.proj.ID, domain.SubjectNote, e.ids.New(), domain.ParseRefs(text))
	if err != nil {
		t.Fatal(err)
	}
	var targets []string
	for _, l := range links {
		targets = append(targets, l.TargetType.String()+":"+l.TargetID.String())
	}
	if want := []string{"card:" + card.ID, "note:" + note.ID}; !slices.Equal(targets, want) {
		t.Errorf("resolved = %v, want %v (a type that does not match, an unknown id and another org's card dropped)", targets, want)
	}
}
