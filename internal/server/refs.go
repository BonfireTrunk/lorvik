package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// syncRefs replaces the stored [[...]] references of an entity's text with
// the targets that resolve. projectID is the source's project, nil for an
// org-level source.
func (s *Service) syncRefs(ctx context.Context, tx Store, orgID domain.ID, projectID *domain.ID, srcType domain.SubjectType, sourceID domain.ID, text string) error {
	links, err := s.resolveRefs(ctx, tx, orgID, projectID, srcType, sourceID, domain.ParseRefs(text))
	if err != nil {
		return fmt.Errorf("resolve references of %s %s: %w", srcType, sourceID, err)
	}
	if err := tx.ReplaceRefs(ctx, sourceID, links); err != nil {
		return fmt.Errorf("store references of %s %s: %w", srcType, sourceID, err)
	}
	return nil
}

// resolveRefs turns parsed references into (source, target) id pairs. A
// reference to something that does not exist is dropped; only resolved
// targets are stored, so backlinks never dangle. Bare card and note
// references resolve in the source's project, then in the global project.
func (s *Service) resolveRefs(ctx context.Context, tx Store, orgID domain.ID, srcProject *domain.ID, srcType domain.SubjectType, sourceID domain.ID, refs []domain.Ref) ([]domain.RefLink, error) {
	slugToID := map[string]*domain.ID{}
	projectOf := func(slug string) (*domain.ID, error) {
		if id, ok := slugToID[slug]; ok {
			return id, nil
		}
		p, err := tx.GetProjectBySlug(ctx, orgID, slug)
		if errors.Is(err, domain.ErrNotFound) {
			slugToID[slug] = nil
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("look up project %q: %w", slug, err)
		}
		slugToID[slug] = &p.ID
		return &p.ID, nil
	}

	seen := map[domain.ID]bool{}
	var out []domain.RefLink
	add := func(typ domain.SubjectType, id domain.ID) {
		if id == 0 || id == sourceID || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, domain.RefLink{SourceType: srcType, SourceID: sourceID, TargetType: typ, TargetID: id})
	}
	for _, r := range refs {
		if r.ID != 0 {
			// An id is looked up in the table its type names, so a card id
			// written as a note, an unknown id or another org's id is dropped.
			var org domain.ID
			var err error
			if r.Type == domain.RefCard {
				var c domain.Card
				c, err = tx.GetCard(ctx, r.ID)
				org = c.OrgID
			} else {
				var n domain.Note
				n, err = tx.GetNote(ctx, r.ID)
				org = n.OrgID
			}
			if errors.Is(err, domain.ErrNotFound) || (err == nil && org != orgID) {
				continue
			}
			if err != nil {
				return nil, err
			}
			add(r.Type, r.ID)
			continue
		}
		// Candidates in order: an explicit project prefix alone, otherwise the
		// source's own project and then the global project.
		var scopes []domain.ID
		if r.Project != "" {
			id, err := projectOf(r.Project)
			if err != nil {
				return nil, err
			}
			if id == nil {
				continue
			}
			scopes = append(scopes, *id)
		} else {
			if srcProject != nil {
				scopes = append(scopes, *srcProject)
			}
			g, err := projectOf(domain.GlobalSlug)
			if err != nil {
				return nil, err
			}
			if g != nil && (srcProject == nil || *g != *srcProject) {
				scopes = append(scopes, *g)
			}
		}
		var id domain.ID
		var err error
		for _, sc := range scopes {
			if r.Type == domain.RefCard {
				var c domain.Card
				c, err = tx.GetCardByNumber(ctx, sc, r.Number)
				id = c.ID
			} else {
				var n domain.Note
				n, err = tx.GetNoteBySlug(ctx, sc, r.Key)
				id = n.ID
			}
			if !errors.Is(err, domain.ErrNotFound) {
				break
			}
		}
		if len(scopes) == 0 {
			err = domain.ErrNotFound
		}
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		add(r.Type, id)
	}
	return out, nil
}

// backlinks lists the sources referencing target that the caller may see.
func (s *Service) backlinks(ctx context.Context, st Store, p Principal, targetID domain.ID) ([]apitypes.Backlink, error) {
	links, err := st.Backlinks(ctx, targetID)
	if err != nil {
		return nil, fmt.Errorf("backlinks: %w", err)
	}
	if len(links) == 0 {
		return nil, nil
	}
	_, roles, err := s.visibleProjects(ctx, st, p)
	if err != nil {
		return nil, err
	}
	visible := func(projectID *domain.ID) bool {
		if projectID == nil {
			return p.HasOrgRole
		}
		_, ok := roles[*projectID]
		return ok
	}
	by := map[domain.SubjectType][]domain.ID{}
	for _, l := range links {
		by[l.SourceType] = append(by[l.SourceType], l.SourceID)
	}
	lk := s.lookup(st, p)
	var out []apitypes.Backlink
	addOne := func(typ domain.SubjectType, id domain.ID, projectID *domain.ID, number int64, slug, title string) error {
		if !visible(projectID) {
			return nil
		}
		ps, err := lk.projectSlugPtr(ctx, projectID)
		if err != nil {
			return err
		}
		out = append(out, apitypes.Backlink{Type: typ.String(), ID: id.String(), Project: ps, Number: number, Slug: slug, Title: title})
		return nil
	}
	if ids := by[domain.SubjectCard]; len(ids) > 0 {
		cards, err := st.GetCards(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("load referencing cards: %w", err)
		}
		for _, c := range cards {
			if err := addOne(domain.SubjectCard, c.ID, &c.ProjectID, c.Number, "", c.Title); err != nil {
				return nil, err
			}
		}
	}
	if ids := by[domain.SubjectNote]; len(ids) > 0 {
		ns, err := st.GetNotes(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("load referencing notes: %w", err)
		}
		for _, n := range ns {
			if err := addOne(domain.SubjectNote, n.ID, &n.ProjectID, 0, n.Slug, n.Title); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
