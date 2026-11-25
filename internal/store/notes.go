package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
	"github.com/BonfireTrunk/lorvik/internal/store/sqlcgen"
)

// CreateNote inserts a note at version 1.
func (s *Store) CreateNote(ctx context.Context, n domain.Note) (domain.Note, error) {
	row, err := s.q.InsertNote(ctx, sqlcgen.InsertNoteParams{
		ID: int64(n.ID), OrgID: int64(n.OrgID), ProjectID: int64(n.ProjectID), Status: int16(n.Status),
		CreatedByKind: int16(n.CreatedBy.Kind), CreatedByID: int64(n.CreatedBy.ID),
		UpdatedByKind: int16(n.UpdatedBy.Kind), UpdatedByID: int64(n.UpdatedBy.ID),
		CreatedAt: n.CreatedAt, Slug: n.Slug, Title: n.Title, Body: n.Body, PageBytes: int32(n.PageBytes),
	})
	if err != nil {
		return domain.Note{}, wrap("create note", err)
	}
	return noteFromRow(row), nil
}

// GetNote returns one note.
func (s *Store) GetNote(ctx context.Context, id domain.ID) (domain.Note, error) {
	row, err := s.q.GetNote(ctx, int64(id))
	if err != nil {
		return domain.Note{}, wrap("get note", err)
	}
	return noteFromRow(row), nil
}

// GetNotes returns the notes among ids that exist.
func (s *Store) GetNotes(ctx context.Context, in []domain.ID) ([]domain.Note, error) {
	return getMany(ctx, in, "get notes", s.q.GetNotes, noteFromRow)
}

// GetNoteBySlug resolves a slug within a project.
func (s *Store) GetNoteBySlug(ctx context.Context, projectID domain.ID, slug string) (domain.Note, error) {
	row, err := s.q.GetProjectNoteBySlug(ctx, sqlcgen.GetProjectNoteBySlugParams{ProjectID: int64(projectID), Slug: slug})
	if err != nil {
		return domain.Note{}, wrap("get note by slug", err)
	}
	return noteFromRow(row), nil
}

// noteKeyset lists most recently updated first; the cursor arguments are
// c_updated_at and c_id.
var noteKeyset = keyset[sqlcgen.Note]{
	order: "n.updated_at DESC, n.id DESC", reverse: "n.updated_at, n.id",
	after:  "(n.updated_at < @c_updated_at OR (n.updated_at = @c_updated_at AND n.id < @c_id))",
	before: "(n.updated_at > @c_updated_at OR (n.updated_at = @c_updated_at AND n.id > @c_id))",
	key:    func(r sqlcgen.Note) pgx.NamedArgs { return pgx.NamedArgs{"c_updated_at": r.UpdatedAt, "c_id": r.ID} },
}

const (
	noteRank      = noteRankValue + " DESC, n.id DESC"
	noteRankAfter = "(" + noteRankValue + " < @c_rank OR (" + noteRankValue + " = @c_rank AND n.id < @c_id))"
)

// ListNotes returns one page of notes, most recently updated first.
func (s *Store) ListNotes(ctx context.Context, orgID domain.ID, f query.NoteFilter) ([]domain.Note, int, error) {
	const op = "list notes"
	q := newFilter()
	q.add("n.org_id = @org_id", "org_id", int64(orgID))
	if f.ProjectID != nil {
		q.add("n.project_id = @project_id", "project_id", int64(*f.ProjectID))
	}
	if f.ProjectIDs != nil {
		q.add("n.project_id = ANY(@project_ids::bigint[])", "project_ids", ids(f.ProjectIDs))
	}
	if !f.Archived {
		q.add("n.status > 0")
	}
	if f.Author != nil {
		q.add("n.created_by_id = @author_id", "author_id", int64(*f.Author))
	}
	q.addRange("n.created_at", "created", f.Created)
	q.addRange("n.updated_at", "updated", f.Updated)
	if query := strings.TrimSpace(f.Query); query != "" {
		q.add(noteMatch, "query", query)
		var after pgx.NamedArgs
		if f.Cursor != nil {
			after = pgx.NamedArgs{"c_rank": f.Cursor.Rank, "c_id": int64(f.Cursor.ID)}
		}
		rows, remaining, err := listRanked[sqlcgen.Note](ctx, s.db, q, "n.*", "notes n", noteRank, noteRankAfter, after, f.Page.Limit)
		if err != nil {
			return nil, 0, wrap(op, err)
		}
		return mapRows(rows, noteFromRow), remaining, nil
	}
	var cursor pgx.NamedArgs
	if f.Cursor != nil {
		cursor = pgx.NamedArgs{"c_updated_at": f.Cursor.UpdatedAt, "c_id": int64(f.Cursor.ID)}
	}
	rows, remaining, err := listKeyset(ctx, s.db, q, "n.*", "notes n", noteKeyset, f.Page, cursor)
	if err != nil {
		return nil, 0, wrap(op, err)
	}
	return mapRows(rows, noteFromRow), remaining, nil
}

// PutNotePage stores the note's HTML page and its visible text for search,
// replacing any earlier page. The caller sets Note.PageBytes in the same
// transaction.
func (s *Store) PutNotePage(ctx context.Context, noteID domain.ID, html, text string) error {
	return wrap("put note page", s.q.PutNotePage(ctx, sqlcgen.PutNotePageParams{NoteID: int64(noteID), Html: html, Text: text}))
}

// DeleteNotePage removes the note's page; no error when it has none.
func (s *Store) DeleteNotePage(ctx context.Context, noteID domain.ID) error {
	return wrap("delete note page", s.q.DeleteNotePage(ctx, int64(noteID)))
}

// GetNotePage returns the note's HTML page, or ErrNotFound when it has none.
func (s *Store) GetNotePage(ctx context.Context, noteID domain.ID) (string, error) {
	html, err := s.q.GetNotePage(ctx, int64(noteID))
	if err != nil {
		return "", wrap("get note page", err)
	}
	return html, nil
}

// UpdateNote keeps the replaced title, body and page as a revision, then writes
// the new content where the version matches. The page is kept in the revision
// only when pageChanged. Both run in one transaction.
func (s *Store) UpdateNote(ctx context.Context, n domain.Note, expectedVersion int64, revisionID domain.ID, pageChanged bool) (domain.Note, error) {
	const op = "update note"
	var out domain.Note
	err := s.WithTx(ctx, func(tx *Store) error {
		snap, err := tx.q.SnapshotNoteRevision(ctx, sqlcgen.SnapshotNoteRevisionParams{
			RevisionID: int64(revisionID), ID: int64(n.ID), ExpectedVersion: expectedVersion, PageChanged: pageChanged,
		})
		if err != nil {
			return wrap(op+": snapshot", err)
		}
		if snap == 0 {
			return conflictOrNotFound(ctx, op, func(ctx context.Context) (bool, error) {
				return tx.q.NoteExists(ctx, int64(n.ID))
			})
		}
		row, err := tx.q.UpdateNote(ctx, sqlcgen.UpdateNoteParams{
			ID: int64(n.ID), ExpectedVersion: expectedVersion, Title: n.Title, Body: n.Body, PageBytes: int32(n.PageBytes), Status: int16(n.Status),
			UpdatedByKind: int16(n.UpdatedBy.Kind), UpdatedByID: int64(n.UpdatedBy.ID), UpdatedAt: n.UpdatedAt,
		})
		if err != nil {
			return wrap(op, err)
		}
		out = noteFromRow(row)
		return nil
	})
	return out, err
}

// ListNoteRevisions returns one page of a note's past versions, newest
// first, without bodies.
func (s *Store) ListNoteRevisions(ctx context.Context, noteID domain.ID, pg query.HistoryPage) ([]domain.NoteRevision, error) {
	rows, err := s.q.ListNoteRevisions(ctx, sqlcgen.ListNoteRevisionsParams{NoteID: int64(noteID), Before: pg.Before, MaxRows: historyLimit(pg)})
	if err != nil {
		return nil, wrap("list note revisions", err)
	}
	return mapRows(rows, func(r sqlcgen.ListNoteRevisionsRow) domain.NoteRevision {
		return domain.NoteRevision{ID: domain.ID(r.ID), NoteID: domain.ID(r.NoteID), Version: r.Version,
			UpdatedBy: actor(r.UpdatedByKind, r.UpdatedByID), Title: r.Title}
	}), nil
}

// GetNoteRevision returns one past version.
func (s *Store) GetNoteRevision(ctx context.Context, noteID domain.ID, version int64) (domain.NoteRevision, error) {
	row, err := s.q.GetNoteRevision(ctx, sqlcgen.GetNoteRevisionParams{NoteID: int64(noteID), Version: version})
	if err != nil {
		return domain.NoteRevision{}, wrap("get note revision", err)
	}
	return noteRevisionFromRow(row), nil
}

// ReplaceRefs stores the references found in one source's body, replacing
// the previous set.
func (s *Store) ReplaceRefs(ctx context.Context, sourceID domain.ID, links []domain.RefLink) error {
	const op = "replace refs"
	return s.WithTx(ctx, func(tx *Store) error {
		if err := tx.q.DeleteRefs(ctx, int64(sourceID)); err != nil {
			return wrap(op, err)
		}
		if len(links) == 0 {
			return nil
		}
		sourceType := links[0].SourceType
		types := make([]int16, 0, len(links))
		targets := make([]int64, 0, len(links))
		for _, l := range links {
			if l.SourceID != sourceID {
				return fmt.Errorf("%s: link source %d is not %d", op, l.SourceID, sourceID)
			}
			types = append(types, int16(l.TargetType))
			targets = append(targets, int64(l.TargetID))
		}
		return wrap(op, tx.q.InsertRefs(ctx, sqlcgen.InsertRefsParams{
			SourceType: int16(sourceType), SourceID: int64(sourceID), TargetTypes: types, TargetIds: targets,
		}))
	})
}

// Backlinks returns the sources that reference target.
func (s *Store) Backlinks(ctx context.Context, targetID domain.ID) ([]domain.RefLink, error) {
	rows, err := s.q.Backlinks(ctx, int64(targetID))
	if err != nil {
		return nil, wrap("backlinks", err)
	}
	return mapRows(rows, refFromRow), nil
}
