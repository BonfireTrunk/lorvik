package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// pageRequestBytes is the body limit of the routes that carry a page. A page
// is at most 1 MiB, but JSON escaping can make it larger on the wire.
const pageRequestBytes = 3 << 20

// saveCardPage stores html as the page of a card, or removes the page when
// html is empty.
func saveCardPage(ctx context.Context, tx Store, id domain.ID, html string) error {
	var err error
	if html == "" {
		err = tx.DeleteCardPage(ctx, id)
	} else {
		err = tx.PutCardPage(ctx, id, html, domain.PageText(html))
	}
	if err != nil {
		return fmt.Errorf("save card page: %w", err)
	}
	return nil
}

// saveNotePage is saveCardPage for a note.
func saveNotePage(ctx context.Context, tx Store, id domain.ID, html string) error {
	var err error
	if html == "" {
		err = tx.DeleteNotePage(ctx, id)
	} else {
		err = tx.PutNotePage(ctx, id, html, domain.PageText(html))
	}
	if err != nil {
		return fmt.Errorf("save note page: %w", err)
	}
	return nil
}

// currentCardPage returns the page c has now, empty when it has none.
func currentCardPage(ctx context.Context, st Store, c domain.Card) (string, error) {
	if c.PageBytes == 0 {
		return "", nil
	}
	html, err := st.GetCardPage(ctx, c.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return "", fmt.Errorf("load page of card #%d: %w", c.Number, err)
	}
	return html, nil
}

// currentNotePage returns the page n has now, empty when it has none.
func (s *Service) currentNotePage(ctx context.Context, st Store, n domain.Note) (string, error) {
	if n.PageBytes == 0 {
		return "", nil
	}
	html, err := st.GetNotePage(ctx, n.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return "", fmt.Errorf("load page of note %q: %w", n.Slug, err)
	}
	return html, nil
}

// GetCardPage returns the card's HTML page, or a 404 when it has none.
func (s *Service) GetCardPage(ctx context.Context, slug string, number int64) (apitypes.HTMLPage, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.HTMLPage{}, err
	}
	_, _, c, err := s.loadCard(ctx, s.store, p, slug, number)
	if err != nil {
		return apitypes.HTMLPage{}, err
	}
	html, err := s.store.GetCardPage(ctx, c.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return apitypes.HTMLPage{}, fmt.Errorf("card #%d has no page: %w", c.Number, domain.ErrNotFound)
	}
	if err != nil {
		return apitypes.HTMLPage{}, fmt.Errorf("load page of card #%d: %w", c.Number, err)
	}
	return apitypes.HTMLPage{HTML: html, Bytes: len(html), Version: c.Version}, nil
}

// GetNotePage returns the note's HTML page, or a 404 when it has none.
func (s *Service) GetNotePage(ctx context.Context, projectSlug, key string) (apitypes.HTMLPage, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.HTMLPage{}, err
	}
	_, n, err := s.loadNote(ctx, s.store, p, projectSlug, key)
	if err != nil {
		return apitypes.HTMLPage{}, err
	}
	html, err := s.store.GetNotePage(ctx, n.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return apitypes.HTMLPage{}, fmt.Errorf("note %q has no page: %w", n.Slug, domain.ErrNotFound)
	}
	if err != nil {
		return apitypes.HTMLPage{}, fmt.Errorf("load page of note %q: %w", n.Slug, err)
	}
	return apitypes.HTMLPage{HTML: html, Bytes: len(html), Version: n.Version}, nil
}
