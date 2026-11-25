package server

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// holding lists the cards the calling chat holds, the soonest to end first.
// A chat that works several cards forgets some, and a forgotten card stays
// claimed for as long as the chat makes requests, so the CLI repeats this
// list after every command.
func (s *Service) holding(ctx context.Context, p Principal) (apitypes.Holding, error) {
	claims, err := s.store.ListClaimsByActor(ctx, p.OrgID, p.Actor)
	if err != nil {
		return apitypes.Holding{}, fmt.Errorf("list claims: %w", err)
	}
	now := s.now()
	claims = slices.DeleteFunc(claims, func(cl domain.Claim) bool { return cl.Expired(now) || !cl.OwnedBy(p.Actor, p.Session) })
	slices.SortFunc(claims, func(a, b domain.Claim) int {
		return cmp.Or(a.ExpiresAt.Compare(b.ExpiresAt), cmp.Compare(a.CardID, b.CardID))
	})
	out := apitypes.Holding{Cards: []apitypes.HeldCard{}, More: max(len(claims)-apitypes.HoldingCap, 0)}
	claims = claims[:min(len(claims), apitypes.HoldingCap)]
	if len(claims) == 0 {
		return out, nil
	}
	ids := make([]domain.ID, len(claims))
	for i, cl := range claims {
		ids[i] = cl.CardID
	}
	cards, err := s.store.GetCards(ctx, ids)
	if err != nil {
		return apitypes.Holding{}, fmt.Errorf("load held cards: %w", err)
	}
	byID := make(map[domain.ID]domain.Card, len(cards))
	for _, c := range cards {
		byID[c.ID] = c
	}
	lk := s.lookup(s.store, p)
	for _, cl := range claims {
		c, ok := byID[cl.CardID]
		if !ok {
			continue
		}
		slug, err := lk.projectSlug(ctx, c.ProjectID)
		if err != nil {
			return apitypes.Holding{}, err
		}
		title := c.Title
		if r := []rune(title); len(r) > apitypes.HoldingTitle {
			title = string(r[:apitypes.HoldingTitle]) + "..."
		}
		out.Cards = append(out.Cards, apitypes.HeldCard{Project: slug, Number: c.Number, Title: title, ExpiresAt: cl.ExpiresAt})
	}
	return out, nil
}

// wantsHolding reports whether the response should say what the chat holds:
// the client asked, and the response is not the event stream, whose headers
// go out once for a connection that lasts.
func wantsHolding(r *http.Request) bool {
	return r.Header.Get(apitypes.HeaderHolding) != "" && r.URL.Path != apiPrefix+"/events/stream"
}

// holdingWriter sets HeaderHolding when the response starts, not when the
// request arrives: a claim, a release or a done changes what the chat holds,
// and the list has to be the one after it.
type holdingWriter struct {
	http.ResponseWriter
	stamp   func(http.Header)
	stamped bool
}

// WriteHeader stamps the header before the first status that is not
// informational: a 1xx is not the start of the answer.
func (w *holdingWriter) WriteHeader(code int) {
	if !w.stamped && code >= http.StatusOK {
		w.stamped = true
		w.stamp(w.Header())
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *holdingWriter) Write(b []byte) (int, error) {
	if !w.stamped {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (w *holdingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// withHolding wraps w so the response carries what p's chat holds. A failure
// is logged and leaves the header out; it never fails the request.
func (a *authenticator) withHolding(ctx context.Context, w http.ResponseWriter, p Principal) http.ResponseWriter {
	return &holdingWriter{ResponseWriter: w, stamp: func(h http.Header) {
		held, err := a.svc.holding(ctx, p)
		if err != nil {
			a.logger.WarnContext(ctx, "list held cards", "actor", p.Actor.ID, "error", err)
			return
		}
		v, err := holdingHeader(held)
		if err != nil {
			a.logger.WarnContext(ctx, "encode held cards", "error", err)
			return
		}
		h.Set(apitypes.HeaderHolding, v)
	}}
}

// holdingHeaderMax bounds the header value. A reverse proxy refuses a
// response whose headers overflow its buffer, 4 KiB by default in nginx, and
// eight cards with long titles outside ASCII would come close.
const holdingHeaderMax = 2 << 10

// holdingHeader encodes held for HeaderHolding, naming fewer cards and
// counting them in More while the value is over holdingHeaderMax.
func holdingHeader(held apitypes.Holding) (string, error) {
	for {
		raw, err := json.Marshal(held)
		if err != nil {
			return "", err
		}
		v := base64.StdEncoding.EncodeToString(raw)
		if len(v) <= holdingHeaderMax || len(held.Cards) == 0 {
			return v, nil
		}
		held.Cards = held.Cards[:len(held.Cards)-1]
		held.More++
	}
}
