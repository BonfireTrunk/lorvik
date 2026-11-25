package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// CreateNote creates a note in a project; org-wide notes go in the global project.
func (s *Service) CreateNote(ctx context.Context, in apitypes.CreateNote) (apitypes.Note, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Note{}, err
	}
	var out domain.Note
	err = s.store.WithTx(ctx, func(tx Store) error {
		proj, a, perr := s.noteProject(ctx, tx, p, in.Project)
		if perr != nil {
			return perr
		}
		if err := a.require(p, domain.PermNoteWrite); err != nil {
			return err
		}
		now := s.now()
		n := domain.Note{ID: s.ids.New(), OrgID: p.OrgID, ProjectID: proj.ID, Status: domain.RecordActive, Slug: in.Slug,
			Title: strings.TrimSpace(in.Title), Body: in.Body, PageBytes: len(in.Page), CreatedBy: p.Actor, UpdatedBy: p.Actor, CreatedAt: now, UpdatedAt: now}
		if verr := errors.Join(n.Validate(), domain.ValidatePage(in.Page)); verr != nil {
			return verr
		}
		if _, gerr := tx.GetNoteBySlug(ctx, n.ProjectID, n.Slug); gerr == nil {
			return fmt.Errorf("note %q: %w", n.Slug, errExists)
		} else if !errors.Is(gerr, domain.ErrNotFound) {
			return fmt.Errorf("check note slug: %w", gerr)
		}
		created, cerr := tx.CreateNote(ctx, n)
		if cerr != nil {
			return fmt.Errorf("create note: %w", cerr)
		}
		out = created
		if in.Page != "" {
			if perr := saveNotePage(ctx, tx, out.ID, in.Page); perr != nil {
				return perr
			}
		}
		return s.afterNoteSave(ctx, tx, p, out, domain.EventNoteCreated)
	})
	if err != nil {
		return apitypes.Note{}, err
	}
	return s.lookup(s.store, p).note(ctx, out)
}

// afterNoteSave refreshes references and appends the note event.
func (s *Service) afterNoteSave(ctx context.Context, tx Store, p Principal, n domain.Note, typ domain.EventType) error {
	if err := s.syncRefs(ctx, tx, p.OrgID, &n.ProjectID, domain.SubjectNote, n.ID, n.Body); err != nil {
		return err
	}
	return s.emitP(ctx, tx, p, &n.ProjectID, typ, noteSubject(n), payload(n.Title, "slug", n.Slug, "status", recordStatus(n.Status, "archived"), "version", n.Version))
}

const noteList = "notes"

// ListNotes returns one page of note briefs, most recently updated first.
func (s *Service) ListNotes(ctx context.Context, in apitypes.ListNotesParams) (apitypes.NoteList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.NoteList{}, err
	}
	cur, err := decodeCursor(noteList, in.Cursor)
	if err != nil {
		return apitypes.NoteList{}, err
	}
	if !cur.forSort(orderOf(in.Query, ""), false) {
		return apitypes.NoteList{}, badCursor()
	}
	f := query.NoteFilter{Query: in.Query, Archived: in.Archived, Page: query.Page{Limit: in.Limit, Backward: cur.backward()}, Cursor: cur.noteCursor()}
	if f.Created, err = parseRange("created", in.CreatedAfter, in.CreatedBefore); err != nil {
		return apitypes.NoteList{}, err
	}
	if f.Updated, err = parseRange("updated", in.UpdatedAfter, in.UpdatedBefore); err != nil {
		return apitypes.NoteList{}, err
	}
	if in.Author != "" {
		id, aerr := parseActorID(p, "author", in.Author)
		if aerr != nil {
			return apitypes.NoteList{}, aerr
		}
		f.Author = &id
	}
	if f.ProjectIDs, err = s.projectScope(ctx, p, in.Project); err != nil {
		return apitypes.NoteList{}, err
	}
	ns, remaining, err := s.store.ListNotes(ctx, p.OrgID, f)
	if err != nil {
		return apitypes.NoteList{}, fmt.Errorf("list notes: %w", err)
	}
	lk := s.lookup(s.store, p)
	items := make([]apitypes.NoteBrief, 0, len(ns))
	for _, n := range ns {
		b, berr := lk.noteBrief(ctx, n)
		if berr != nil {
			return apitypes.NoteList{}, berr
		}
		items = append(items, b)
	}
	out := apitypes.NoteList{Items: items, Page: page(noteList, ns, noteKey, remaining)}
	if in.Query != "" {
		var edge *cursor
		if len(ns) > 0 {
			edge = &cursor{ID: int64(ns[len(ns)-1].ID)}
		}
		if out.Page, err = s.rankedPage(ctx, noteList, domain.SubjectNote, in.Query, edge, remaining); err != nil {
			return apitypes.NoteList{}, err
		}
	}
	return out, nil
}

// noteProject resolves the project slug a note is addressed by. There is no
// project-less note: org-wide notes are addressed as the global project.
func (s *Service) noteProject(ctx context.Context, st Store, p Principal, slug string) (domain.Project, access, error) {
	if slug == "" {
		return domain.Project{}, access{}, invalidField("project", "required; use \"global\" for org-wide notes")
	}
	return s.project(ctx, st, p, slug)
}

// loadNote resolves a note by slug within a project, or by id from anywhere
// the caller may see.
func (s *Service) loadNote(ctx context.Context, st Store, p Principal, projectSlug, key string) (access, domain.Note, error) {
	if id, err := domain.ParseID(key); err == nil && id.Time().Year() >= 2026 {
		n, err := st.GetNote(ctx, id)
		if err != nil || n.OrgID != p.OrgID {
			return access{}, domain.Note{}, fmt.Errorf("note %s: %w", key, domain.ErrNotFound)
		}
		a, err := s.accessByID(ctx, st, p, n.ProjectID)
		if err != nil {
			return access{}, domain.Note{}, err
		}
		return a, n, nil
	}
	proj, a, err := s.noteProject(ctx, st, p, projectSlug)
	if err != nil {
		return access{}, domain.Note{}, err
	}
	n, err := st.GetNoteBySlug(ctx, proj.ID, key)
	if err != nil {
		return access{}, domain.Note{}, fmt.Errorf("note %q: %w", key, err)
	}
	return a, n, nil
}

// GetNote returns a note with its backlinks.
func (s *Service) GetNote(ctx context.Context, projectSlug, key string) (apitypes.Note, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Note{}, err
	}
	_, n, err := s.loadNote(ctx, s.store, p, projectSlug, key)
	if err != nil {
		return apitypes.Note{}, err
	}
	out, err := s.lookup(s.store, p).note(ctx, n)
	if err != nil {
		return apitypes.Note{}, err
	}
	if out.Backlinks, err = s.backlinks(ctx, s.store, p, n.ID); err != nil {
		return apitypes.Note{}, err
	}
	return out, nil
}

// UpdateNote applies a versioned edit, keeping the previous text as a
// revision, and refreshes the note's references.
func (s *Service) UpdateNote(ctx context.Context, projectSlug, key string, in apitypes.UpdateNote) (apitypes.Note, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Note{}, err
	}
	var out domain.Note
	err = s.store.WithTx(ctx, func(tx Store) error {
		a, n, lerr := s.loadNote(ctx, tx, p, projectSlug, key)
		if lerr != nil {
			return lerr
		}
		if err := a.require(p, domain.PermNoteWrite); err != nil {
			return err
		}
		if verr := versionCheck(n.Version, in.Version, fmt.Sprintf("note %q", n.Slug)); verr != nil {
			return verr
		}
		if in.Title != nil {
			n.Title = strings.TrimSpace(*in.Title)
		}
		if in.Body != nil {
			n.Body = *in.Body
		}
		if in.Status != nil && *in.Status != "" {
			switch *in.Status {
			case "active":
				n.Status = domain.RecordActive
			case "archived":
				n.Status = domain.RecordArchived
			default:
				return invalidField("status", "must be active or archived")
			}
		}
		var pageErr error
		pageChanged := false
		if in.Page != nil {
			if pageErr = domain.ValidatePage(*in.Page); pageErr == nil {
				old, perr := s.currentNotePage(ctx, tx, n)
				if perr != nil {
					return perr
				}
				pageChanged = old != *in.Page
				n.PageBytes = len(*in.Page)
			}
		}
		n.UpdatedBy, n.UpdatedAt = p.Actor, s.now()
		if verr := errors.Join(n.Validate(), pageErr); verr != nil {
			return verr
		}
		updated, uerr := s.updateNote(ctx, tx, n, in.Version, pageChanged)
		if uerr != nil {
			return uerr
		}
		out = updated
		if pageChanged {
			if perr := saveNotePage(ctx, tx, out.ID, *in.Page); perr != nil {
				return perr
			}
		}
		return s.afterNoteSave(ctx, tx, p, out, domain.EventNoteUpdated)
	})
	if err != nil {
		return apitypes.Note{}, err
	}
	return s.lookup(s.store, p).note(ctx, out)
}

// ListNoteRevisions returns one page of a note's past versions, newest
// first, without bodies.
func (s *Service) ListNoteRevisions(ctx context.Context, projectSlug, key string, pg query.HistoryPage) ([]apitypes.NoteRevision, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	_, n, err := s.loadNote(ctx, s.store, p, projectSlug, key)
	if err != nil {
		return nil, err
	}
	revs, err := s.store.ListNoteRevisions(ctx, n.ID, pg)
	if err != nil {
		return nil, fmt.Errorf("list revisions: %w", err)
	}
	out := make([]apitypes.NoteRevision, 0, len(revs))
	for _, r := range revs {
		out = append(out, toRevision(r))
	}
	return out, nil
}

// GetNoteRevision returns one past version with its body.
func (s *Service) GetNoteRevision(ctx context.Context, projectSlug, key string, version int64) (apitypes.NoteRevision, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.NoteRevision{}, err
	}
	_, n, err := s.loadNote(ctx, s.store, p, projectSlug, key)
	if err != nil {
		return apitypes.NoteRevision{}, err
	}
	r, err := s.store.GetNoteRevision(ctx, n.ID, version)
	if err != nil {
		return apitypes.NoteRevision{}, fmt.Errorf("revision %d of note %q: %w", version, n.Slug, err)
	}
	return toRevision(r), nil
}
