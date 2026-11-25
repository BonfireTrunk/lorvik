package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

// ListNotes calls GET /notes.
func (c *Client) ListNotes(ctx context.Context, p apitypes.ListNotesParams) (apitypes.NoteList, error) {
	v := timeFilterValues(p.CreatedAfter, p.CreatedBefore, p.UpdatedAfter, p.UpdatedBefore)
	setIf(v, "project", p.Project)
	setBool(v, "archived", p.Archived)
	setIf(v, "q", p.Query)
	setIf(v, "author", p.Author)
	setLimit(v, p.Limit, p.Cursor)
	return call[apitypes.NoteList](ctx, c, http.MethodGet, withQuery("/notes", v), nil)
}

// CreateNote calls POST /notes.
func (c *Client) CreateNote(ctx context.Context, in apitypes.CreateNote) (apitypes.Note, error) {
	return call[apitypes.Note](ctx, c, http.MethodPost, "/notes", in)
}

// notePath builds /notes/{key}rest?project=. The key is a slug or an id; the
// project is ignored for an id.
func notePath(key, project, rest string) string {
	v := url.Values{}
	setIf(v, "project", project)
	return withQuery("/notes/"+seg(key)+rest, v)
}

// GetNote calls GET /notes/{key}?project=.
func (c *Client) GetNote(ctx context.Context, key, project string) (apitypes.Note, error) {
	return call[apitypes.Note](ctx, c, http.MethodGet, notePath(key, project, ""), nil)
}

// GetNotePage calls GET /notes/{key}/page?project=. A note without a page is
// a 404.
func (c *Client) GetNotePage(ctx context.Context, key, project string) (apitypes.HTMLPage, error) {
	return call[apitypes.HTMLPage](ctx, c, http.MethodGet, notePath(key, project, "/page"), nil)
}

// UpdateNote calls PATCH /notes/{key}?project=. Status "archived" archives.
func (c *Client) UpdateNote(ctx context.Context, key, project string, in apitypes.UpdateNote) (apitypes.Note, error) {
	return call[apitypes.Note](ctx, c, http.MethodPatch, notePath(key, project, ""), in)
}

// ListNoteRevisions calls GET /notes/{key}/revisions?project=, newest first
// and without bodies.
func (c *Client) ListNoteRevisions(ctx context.Context, key, project string, p HistoryParams) ([]apitypes.NoteRevision, error) {
	v := historyValues(p)
	setIf(v, "project", project)
	return call[[]apitypes.NoteRevision](ctx, c, http.MethodGet, withQuery("/notes/"+seg(key)+"/revisions", v), nil)
}

// GetNoteRevision calls GET /notes/{key}/revisions/{version}?project=.
func (c *Client) GetNoteRevision(ctx context.Context, key, project string, version int64) (apitypes.NoteRevision, error) {
	return call[apitypes.NoteRevision](ctx, c, http.MethodGet, notePath(key, project, "/revisions/"+num(version)), nil)
}

// Search calls GET /search. project may be empty to search everything.
func (c *Client) Search(ctx context.Context, q, project, cursor string, limit int) (apitypes.SearchResult, error) {
	v := url.Values{}
	v.Set("q", q)
	setIf(v, "project", project)
	setIf(v, "cursor", cursor)
	setLimit(v, limit, "")
	return call[apitypes.SearchResult](ctx, c, http.MethodGet, withQuery("/search", v), nil)
}
