package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// cursor is the opaque paging token handed to clients. It names the list it
// belongs to, the direction to continue in and the sort key of the edge row.
// The client holds it; the server keeps nothing.
type cursor struct {
	List string `json:"l"`
	Back bool   `json:"b,omitempty"`
	// Card keys.
	Status   int16 `json:"s,omitempty"`
	Priority int16 `json:"p,omitempty"`
	Number   int64 `json:"n,omitempty"`
	// Id keys (the org-wide card list, events) and the tiebreak for notes and
	// the unfolded list.
	ID int64 `json:"i,omitempty"`
	// Unfolded list: when the card closed, unix nanoseconds.
	ClosedAt int64 `json:"k,omitempty"`
	// Notes, and cards sorted by updated.
	UpdatedAt int64 `json:"u,omitempty"` // unix nanoseconds
	// Cards sorted by created.
	CreatedAt int64 `json:"c,omitempty"` // unix nanoseconds
	// Ranked lists: rank, then type (search only), then number and ID.
	Rank float64 `json:"r,omitempty"`
	Type int16   `json:"t,omitempty"`
	// Card lists: the sort name and direction the position belongs to. Empty
	// Sort is board order.
	Sort string `json:"o,omitempty"`
	Desc bool   `json:"d,omitempty"`
	// Member lists: role, then CreatedAt, then this kind and ID as the actor.
	Role      int16 `json:"ro,omitempty"`
	ActorKind int16 `json:"ak,omitempty"`
	// Map edges: ID is the source card, Peer the target (a card or a note) and
	// Type the link kind.
	Peer int64 `json:"pe,omitempty"`
	// Map group lists: the keys of the group the page ended at and, for group
	// links, the group its last link ran to; Type is the link kind.
	GroupFrom string `json:"gf,omitempty"`
	GroupTo   string `json:"gt,omitempty"`
	// Projects sort by scope, then slug held in Name. Locks sort by Name.
	Scope int16  `json:"sc,omitempty"`
	Name  string `json:"na,omitempty"`
}

func (c cursor) encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor parses a client token for the named list. A malformed or
// foreign token is a 400, not a silent restart from the top.
func decodeCursor(list, token string) (*cursor, error) {
	if token == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, badCursor()
	}
	var c cursor
	if err := json.Unmarshal(b, &c); err != nil || c.List != list {
		return nil, badCursor()
	}
	return &c, nil
}

func badCursor() error {
	return newProblem(http.StatusBadRequest, "invalid cursor", "pass the next or prev value from a previous page, or omit it to start over")
}

// page builds the Next and Prev tokens of a fetched page. first and last are
// the edge rows' keys; the tokens are omitted when the page is empty.
func page[T any](list string, items []T, key func(T) cursor, remaining int) apitypes.Page {
	if len(items) == 0 {
		return apitypes.Page{Remaining: remaining}
	}
	next := key(items[len(items)-1])
	next.List, next.Back = list, false
	prev := key(items[0])
	prev.List, prev.Back = list, true
	return apitypes.Page{Next: next.encode(), Prev: prev.encode(), Remaining: remaining}
}

// forwardPage builds the page of a list that only goes forward: Next names the
// last row while more follow, so a client looping on Next stops by itself.
func forwardPage[T any](list string, items []T, key func(T) cursor, remaining int) apitypes.Page {
	if len(items) == 0 || remaining == 0 {
		return apitypes.Page{Remaining: remaining}
	}
	next := key(items[len(items)-1])
	next.List = list
	return apitypes.Page{Next: next.encode(), Remaining: remaining}
}

// cardKeyFor returns the cursor key of a card under the named sort. The key
// carries the sort and direction so a token from another ordering is refused.
func cardKeyFor(sort string, desc bool) func(domain.Card) cursor {
	return func(c domain.Card) cursor {
		k := cursor{Sort: sort, Desc: desc, Number: c.Number}
		switch sort {
		case "":
			k.Status, k.Priority = int16(c.Status), int16(c.Priority)
		case "created":
			k.CreatedAt = c.CreatedAt.UnixNano()
		case "updated":
			k.UpdatedAt = c.UpdatedAt.UnixNano()
		case "priority":
			k.Priority = int16(c.Priority)
		}
		return k
	}
}

// forSort reports whether the token was issued for this card ordering. A nil
// cursor (first page) fits any.
func (c *cursor) forSort(sort string, desc bool) bool {
	return c == nil || (c.Sort == sort && c.Desc == desc)
}

func (c *cursor) cardCursor() *query.CardCursor {
	if c == nil {
		return nil
	}
	return &query.CardCursor{
		Status: domain.Status(c.Status), Priority: domain.Priority(c.Priority), Number: c.Number,
		CreatedAt: time.Unix(0, c.CreatedAt).UTC(), UpdatedAt: time.Unix(0, c.UpdatedAt).UTC(), ID: domain.ID(c.ID), Rank: c.Rank,
	}
}

func noteKey(n domain.Note) cursor {
	return cursor{UpdatedAt: n.UpdatedAt.UnixNano(), ID: int64(n.ID)}
}

func (c *cursor) noteCursor() *query.NoteCursor {
	if c == nil {
		return nil
	}
	return &query.NoteCursor{UpdatedAt: time.Unix(0, c.UpdatedAt).UTC(), ID: domain.ID(c.ID), Rank: c.Rank}
}

func idKey(id domain.ID) cursor { return cursor{ID: int64(id)} }

func memberCursorKey(role domain.Role, created time.Time, a domain.ActorRef) cursor {
	return cursor{Role: int16(role), CreatedAt: created.UnixNano(), ActorKind: int16(a.Kind), ID: int64(a.ID)}
}

func (c *cursor) memberCursor() *query.MemberCursor {
	if c == nil {
		return nil
	}
	return &query.MemberCursor{
		Role: domain.Role(c.Role), CreatedAt: time.Unix(0, c.CreatedAt).UTC(),
		Actor: domain.ActorRef{Kind: domain.ActorKind(c.ActorKind), ID: domain.ID(c.ID)},
	}
}

// pageSize is the page a list cut in the service returns; the store applies
// the same bounds to the lists it reads with a limit.
func pageSize(limit int) int {
	if limit <= 0 {
		return apitypes.DefaultPageSize
	}
	return min(limit, apitypes.MaxPageSize)
}

// sortRelevance marks a cursor as issued for a full-text list, so that it is
// refused without q, and a plain list's cursor is refused with it.
const sortRelevance = "relevance"

// orderOf is the ordering a cursor must have been issued for: relevance with a
// full-text query, else the named sort.
func orderOf(q, sort string) string {
	if q != "" {
		return sortRelevance
	}
	return sort
}

// rankedPage builds the page of a full-text list, which only goes forward.
// edge is the key of the page's last row, nil when the page is empty. With
// nothing beyond the page there is nothing to continue, so the rank is not read.
func (s *Service) rankedPage(ctx context.Context, list string, subject domain.SubjectType, q string, edge *cursor, remaining int) (apitypes.Page, error) {
	if edge == nil || remaining == 0 {
		return apitypes.Page{Remaining: remaining}, nil
	}
	rank, err := s.store.SearchRank(ctx, subject, domain.ID(edge.ID), q)
	if err != nil {
		return apitypes.Page{}, fmt.Errorf("search rank: %w", err)
	}
	edge.List, edge.Sort, edge.Rank = list, sortRelevance, rank
	return apitypes.Page{Next: edge.encode(), Remaining: remaining}, nil
}

// rankedCardEdge is the key of the last card of a full-text page.
func rankedCardEdge(cards []domain.Card) *cursor {
	if len(cards) == 0 {
		return nil
	}
	last := cards[len(cards)-1]
	return &cursor{Number: last.Number, ID: int64(last.ID)}
}

func (c *cursor) backward() bool { return c != nil && c.Back }

// eventPosition is the wire form of a feed position: the xid and the id,
// each zero-padded to its widest value, so positions compare as strings in
// feed order. Clients treat it as opaque and compare it only as a string.
func eventPosition(p query.EventPosition) string {
	return fmt.Sprintf("%020d-%019d", p.Xid, int64(p.ID))
}

func positionOf(e domain.Event) query.EventPosition {
	return query.EventPosition{Xid: e.Xid, ID: e.ID}
}

// cursorString is p as a client sent it: a position, or the bare event id an
// older client saved.
func cursorString(p query.EventPosition) string {
	if p.Xid == 0 {
		return p.ID.String()
	}
	return eventPosition(p)
}

// parseEventCursor reads an event cursor sent as a header or query value
// named field: a position, or an event id saved by a client that predates
// positions. Empty or "0" is the start of the feed. A malformed value is the
// caller's 422 on that field.
func parseEventCursor(field, s string) (query.EventPosition, error) {
	if s == "" || s == "0" {
		return query.EventPosition{}, nil
	}
	xs, is, isPosition := strings.Cut(s, "-")
	if !isPosition {
		id, err := domain.ParseID(s)
		if err != nil {
			return query.EventPosition{}, invalidField(field, "must be an event position")
		}
		return query.EventPosition{ID: id}, nil
	}
	x, xerr := strconv.ParseUint(xs, 10, 64)
	id, ierr := domain.ParseID(is)
	if xerr != nil || ierr != nil || x == 0 {
		return query.EventPosition{}, invalidField(field, "must be an event position")
	}
	return query.EventPosition{Xid: x, ID: id}, nil
}
