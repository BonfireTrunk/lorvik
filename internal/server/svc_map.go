package server

import (
	"context"
	"fmt"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

const (
	linkList    = "project-links"
	mentionList = "project-mentions"
)

// ListProjectLinks lists the card links with an end in the project, in both
// directions. A link whose other card is in a project the caller cannot read
// is left out.
func (s *Service) ListProjectLinks(ctx context.Context, slug string, limit int, token string) (apitypes.ProjectLinkList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.ProjectLinkList{}, err
	}
	proj, _, err := s.project(ctx, s.store, p, slug)
	if err != nil {
		return apitypes.ProjectLinkList{}, err
	}
	cur, err := decodeCursor(linkList, token)
	if err != nil {
		return apitypes.ProjectLinkList{}, err
	}
	scope, err := s.projectScope(ctx, p, "")
	if err != nil {
		return apitypes.ProjectLinkList{}, err
	}
	var after *query.LinkCursor
	if cur != nil {
		after = &query.LinkCursor{From: domain.ID(cur.ID), To: domain.ID(cur.Peer), Kind: domain.CardLinkKind(cur.Type)}
	}
	edges, remaining, err := s.store.ListProjectLinks(ctx, proj.ID, scope, pageSize(limit), after)
	if err != nil {
		return apitypes.ProjectLinkList{}, fmt.Errorf("list project links: %w", err)
	}
	lk := s.lookup(s.store, p)
	end := func(number int64, projectID domain.ID) (apitypes.LinkEnd, error) {
		out := apitypes.LinkEnd{Number: number}
		if projectID == proj.ID {
			return out, nil
		}
		var err error
		out.Project, err = lk.projectSlug(ctx, projectID)
		return out, err
	}
	items := make([]apitypes.ProjectLink, len(edges))
	for i, e := range edges {
		item := apitypes.ProjectLink{Kind: e.Kind.String()}
		if item.From, err = end(e.FromNumber, e.FromProject); err != nil {
			return apitypes.ProjectLinkList{}, err
		}
		if item.To, err = end(e.ToNumber, e.ToProject); err != nil {
			return apitypes.ProjectLinkList{}, err
		}
		items[i] = item
	}
	key := func(e query.LinkEdge) cursor {
		return cursor{ID: int64(e.FromID), Peer: int64(e.ToID), Type: int16(e.Kind)}
	}
	return apitypes.ProjectLinkList{Items: items, Page: forwardPage(linkList, edges, key, remaining)}, nil
}

// ListProjectMentions lists the notes the project's cards reference, leaving
// out notes in projects the caller cannot read.
func (s *Service) ListProjectMentions(ctx context.Context, slug string, limit int, token string) (apitypes.ProjectMentionList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.ProjectMentionList{}, err
	}
	proj, _, err := s.project(ctx, s.store, p, slug)
	if err != nil {
		return apitypes.ProjectMentionList{}, err
	}
	cur, err := decodeCursor(mentionList, token)
	if err != nil {
		return apitypes.ProjectMentionList{}, err
	}
	scope, err := s.projectScope(ctx, p, "")
	if err != nil {
		return apitypes.ProjectMentionList{}, err
	}
	var after *query.MentionCursor
	if cur != nil {
		after = &query.MentionCursor{Card: domain.ID(cur.ID), Note: domain.ID(cur.Peer)}
	}
	edges, remaining, err := s.store.ListProjectMentions(ctx, proj.ID, scope, pageSize(limit), after)
	if err != nil {
		return apitypes.ProjectMentionList{}, fmt.Errorf("list project mentions: %w", err)
	}
	items := make([]apitypes.ProjectMention, len(edges))
	for i, e := range edges {
		items[i] = apitypes.ProjectMention{Card: e.CardNumber, Note: apitypes.NoteKey{ID: e.NoteID.String(), Slug: e.NoteSlug}}
	}
	key := func(e query.MentionEdge) cursor { return cursor{ID: int64(e.CardID), Peer: int64(e.NoteID)} }
	return apitypes.ProjectMentionList{Items: items, Page: forwardPage(mentionList, edges, key, remaining)}, nil
}
