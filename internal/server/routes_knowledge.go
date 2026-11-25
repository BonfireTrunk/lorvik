package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

// NotePath addresses a note by slug within a project, or by id from anywhere.
type NotePath struct {
	Key     string `path:"key" doc:"note slug or id"`
	Project string `query:"project" doc:"project slug (global for org-wide notes); required for a slug, ignored for an id"`
}

type listNotesIn struct {
	apitypes.ListNotesParams
}

func (r *routes) registerNotes() {
	one := "/notes/{key}"
	huma.Register(r.api, operation("list-notes", http.MethodGet, "/notes", "notes", "List notes, most recently updated first", http.StatusOK),
		handle(r, func(ctx context.Context, in *listNotesIn) (apitypes.NoteList, error) {
			return r.svc.ListNotes(ctx, in.ListNotesParams)
		}))
	huma.Register(r.api, carryingPage(operation("create-note", http.MethodPost, "/notes", "notes", "Create a note", http.StatusCreated)),
		handle(r, func(ctx context.Context, in *struct{ Body apitypes.CreateNote }) (apitypes.Note, error) {
			return r.svc.CreateNote(ctx, in.Body)
		}))
	huma.Register(r.api, operation("get-note", http.MethodGet, one, "notes", "Get a note with backlinks", http.StatusOK),
		handle(r, func(ctx context.Context, in *NotePath) (apitypes.Note, error) {
			return r.svc.GetNote(ctx, in.Project, in.Key)
		}))
	huma.Register(r.api, carryingPage(operation("update-note", http.MethodPatch, one, "notes", "Edit or archive a note (version required)", http.StatusOK)),
		handle(r, func(ctx context.Context, in *struct {
			NotePath
			Body apitypes.UpdateNote
		}) (apitypes.Note, error) {
			return r.svc.UpdateNote(ctx, in.Project, in.Key, in.Body)
		}))
	huma.Register(r.api, operation("get-note-page", http.MethodGet, one+"/page", "notes", "The note's HTML page as JSON, or 404 when it has none", http.StatusOK),
		handle(r, func(ctx context.Context, in *NotePath) (apitypes.HTMLPage, error) {
			return r.svc.GetNotePage(ctx, in.Project, in.Key)
		}))
	huma.Register(r.api, operation("list-note-revisions", http.MethodGet, one+"/revisions", "notes", "Past versions of a note, newest first, without bodies", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct {
			NotePath
			RevisionPage
		}) ([]apitypes.NoteRevision, error) {
			return r.svc.ListNoteRevisions(ctx, in.Project, in.Key, in.page())
		}))
	type revisionIn struct {
		NotePath
		Version int64 `path:"version" minimum:"1"`
	}
	huma.Register(r.api, operation("get-note-revision", http.MethodGet, one+"/revisions/{version}", "notes", "One past version with its body", http.StatusOK),
		handle(r, func(ctx context.Context, in *revisionIn) (apitypes.NoteRevision, error) {
			return r.svc.GetNoteRevision(ctx, in.Project, in.Key, in.Version)
		}))
}
