package server

import (
	"context"
	"fmt"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// markSeen records that the caller has just written on or opened the card.
// The mark is a fresh id rather than a comment id so it also covers comments
// that land between the caller's read and this write. It raises no event:
// reading is not activity other actors need to hear about.
func (s *Service) markSeen(ctx context.Context, tx Store, p Principal, c domain.Card) error {
	r := domain.CardRead{CardID: c.ID, ProjectID: c.ProjectID, Actor: p.Actor, SeenID: s.ids.New()}
	if err := tx.MarkCardSeen(ctx, r); err != nil {
		return fmt.Errorf("mark card #%d seen: %w", c.Number, err)
	}
	return nil
}

// MarkCardSeen records that the caller has opened a card, for any actor kind.
func (s *Service) MarkCardSeen(ctx context.Context, slug string, number int64) error {
	p, err := s.principal(ctx)
	if err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx Store) error {
		_, _, c, lerr := s.loadCard(ctx, tx, p, slug, number)
		if lerr != nil {
			return lerr
		}
		return s.markSeen(ctx, tx, p, c)
	})
}

const replyList = "replies"

// markRepliesPage is how many replies MarkRepliesSeen reads at a time.
const markRepliesPage = 200

// MarkRepliesSeen marks every card with replies to the caller in the project
// seen, as opening each one would.
func (s *Service) MarkRepliesSeen(ctx context.Context, slug string) error {
	p, err := s.principal(ctx)
	if err != nil {
		return err
	}
	proj, _, err := s.project(ctx, s.store, p, slug)
	if err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx Store) error {
		var before domain.ID
		for {
			rs, remaining, err := tx.ListReplies(ctx, p.Actor, proj.ID, before, markRepliesPage)
			if err != nil {
				return fmt.Errorf("list replies: %w", err)
			}
			for _, r := range rs {
				if err := tx.MarkCardSeen(ctx, domain.CardRead{CardID: r.Comment.CardID, ProjectID: proj.ID, Actor: p.Actor, SeenID: s.ids.New()}); err != nil {
					return fmt.Errorf("mark card %s seen: %w", r.Comment.CardID, err)
				}
			}
			if remaining == 0 || len(rs) == 0 {
				return nil
			}
			before = rs[len(rs)-1].Comment.ID
		}
	})
}

// ListReplies returns the cards in the project that carry comments by others
// newer than the caller last saw, newest first, a page at a time from the
// cursor token.
func (s *Service) ListReplies(ctx context.Context, slug, token string, limit int32) (apitypes.ReplyList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.ReplyList{}, err
	}
	proj, _, err := s.project(ctx, s.store, p, slug)
	if err != nil {
		return apitypes.ReplyList{}, err
	}
	cur, err := decodeCursor(replyList, token)
	if err != nil {
		return apitypes.ReplyList{}, err
	}
	var before domain.ID
	if cur != nil {
		before = domain.ID(cur.ID)
	}
	rs, remaining, err := s.store.ListReplies(ctx, p.Actor, proj.ID, before, limit)
	if err != nil {
		return apitypes.ReplyList{}, fmt.Errorf("list replies: %w", err)
	}
	l := s.lookup(s.store, p)
	out := make([]apitypes.Reply, 0, len(rs))
	for _, r := range rs {
		c, err := s.store.GetCard(ctx, r.Comment.CardID)
		if err != nil {
			return apitypes.ReplyList{}, fmt.Errorf("card %s: %w", r.Comment.CardID, err)
		}
		brief, err := l.brief(ctx, c)
		if err != nil {
			return apitypes.ReplyList{}, err
		}
		out = append(out, apitypes.Reply{Card: brief, Comment: toComment(r.Comment, c.Number), Unseen: r.Unseen})
	}
	pg := page(replyList, rs, func(r domain.Reply) cursor { return cursor{ID: int64(r.Comment.ID)} }, remaining)
	pg.Prev = ""
	return apitypes.ReplyList{Items: out, Page: pg}, nil
}
