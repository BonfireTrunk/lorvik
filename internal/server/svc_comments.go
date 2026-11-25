package server

import (
	"context"
	"fmt"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// CreateComment leaves a comment on a card.
func (s *Service) CreateComment(ctx context.Context, slug string, number int64, body, key string) (apitypes.Comment, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Comment{}, err
	}
	var out domain.Comment
	err = s.store.WithTx(ctx, func(tx Store) error {
		proj, a, c, lerr := s.loadCard(ctx, tx, p, slug, number)
		if lerr != nil {
			return lerr
		}
		id := s.ids.New()
		first, repeat, kerr := s.claimRequestKey(ctx, tx, p, key, domain.RequestComment, proj, c.ID, id)
		if kerr != nil {
			return kerr
		}
		if repeat {
			h, gerr := tx.GetComment(ctx, first.SubjectID)
			out = h
			return gerr
		}
		if err := a.require(p, domain.PermCommentWrite); err != nil {
			return err
		}
		h, aerr := s.addComment(ctx, tx, p, proj, c, body, id)
		if aerr != nil {
			return aerr
		}
		out = h
		return s.markSeen(ctx, tx, p, c)
	})
	if err != nil {
		return apitypes.Comment{}, err
	}
	return toComment(out, number), nil
}

// addComment stores a comment by the caller on c and emits its event. The
// caller has checked the permission and made the id.
func (s *Service) addComment(ctx context.Context, tx Store, p Principal, proj domain.Project, c domain.Card, body string, id domain.ID) (domain.Comment, error) {
	h := domain.Comment{ID: id, CardID: c.ID, Actor: p.Actor, Body: body}
	if err := h.Validate(); err != nil {
		return domain.Comment{}, err
	}
	if err := tx.AddComment(ctx, h); err != nil {
		return domain.Comment{}, fmt.Errorf("add comment: %w", err)
	}
	if err := s.emitP(ctx, tx, p, &proj.ID, domain.EventCardComment, cardSubject(c), payload(c.Title, "comment", h.ID.String())); err != nil {
		return domain.Comment{}, err
	}
	return h, nil
}

// ListComments returns one page of a card's comments, newest first: those
// older than the comment id before, at most limit of them.
func (s *Service) ListComments(ctx context.Context, slug string, number int64, before string, limit int) ([]apitypes.Comment, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return nil, err
	}
	pg := query.HistoryPage{Limit: limit}
	if before != "" {
		id, perr := domain.ParseID(before)
		if perr != nil {
			return nil, invalidField("before", "must be a comment id")
		}
		pg.Before = int64(id)
	}
	_, _, c, err := s.loadCard(ctx, s.store, p, slug, number)
	if err != nil {
		return nil, err
	}
	hs, err := s.store.ListComments(ctx, c.ID, pg)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	out := make([]apitypes.Comment, 0, len(hs))
	for _, h := range hs {
		out = append(out, toComment(h, c.Number))
	}
	return out, nil
}
