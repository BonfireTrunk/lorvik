// Package query holds the value types a list or search call is made of:
// filters, page requests, cursors, sort orders and result counts. The server
// builds them, the store consumes them, and neither imports the other.
package query

import (
	"cmp"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// RemainingCap bounds every Remaining count so that paging never scans more
// than one extra page. Clients show "100+" at the cap.
const RemainingCap = 100

// Page is the keyset paging request shared by every list filter.
type Page struct {
	// Limit is the page size; the store applies its default and maximum.
	Limit int
	// Backward pages toward older or lower-sorted rows. The page is still
	// returned in forward order.
	Backward bool
}

// HistoryPage asks for part of a card's or note's history, newest first:
// entries older than Before (a comment id or a revision version, 0 for the
// newest) and at most Limit of them (0 for the store's default page).
type HistoryPage struct {
	Before int64
	Limit  int
}

// CardCounts summarises a project.
type CardCounts struct {
	ByStatus      map[domain.Status]int
	OpenQuestions int
}

// TimeRange bounds a timestamp: After is inclusive, Before exclusive. A zero
// bound is open.
type TimeRange struct {
	After, Before time.Time
}

// IsZero reports whether the range has no bounds.
func (r TimeRange) IsZero() bool { return r.After.IsZero() && r.Before.IsZero() }

// Contains reports whether t lies in the range.
func (r TimeRange) Contains(t time.Time) bool {
	return (r.After.IsZero() || !t.Before(r.After)) && (r.Before.IsZero() || t.Before(r.Before))
}

// CardSort names a card list order. The zero value is board order: status,
// priority descending, number.
type CardSort int

// CardSort values. Every sort breaks ties by number, in the same direction.
const (
	SortBoard CardSort = iota
	SortCreated
	SortUpdated
	SortPriority
	SortNumber
)

// CardCursor is the sort key of a card for the sort in use: board order uses
// Status, Priority and Number; created and updated use their time and
// Number; priority uses Priority and Number; number uses Number.
type CardCursor struct {
	Status    domain.Status
	Priority  domain.Priority
	Number    int64
	CreatedAt time.Time
	UpdatedAt time.Time
	// ID is the whole key of the org-wide list, which orders by id.
	ID domain.ID
	// Rank is the relevance of the row a full-text page continues from.
	Rank float64
}

// UnfoldedCursor is the sort key of the unfolded list: closed_at desc, id desc.
type UnfoldedCursor struct {
	ClosedAt time.Time
	ID       domain.ID
}

// CardFilter selects cards for listing. Zero values mean no constraint.
type CardFilter struct {
	Statuses []domain.Status
	Kind     domain.CardKind
	ParentID *domain.ID
	// NoStory restricts to cards that belong to no story: no parent, and not
	// a story themselves. It excludes ParentID.
	NoStory bool
	// Lane restricts to cards carrying this lane; NoLane to cards with none.
	// They exclude each other.
	Lane   string
	NoLane bool
	// Actor restricts to cards claimed by or assigned to this actor.
	Actor *domain.ActorRef
	Open  bool
	// AwaitsKeepBy restricts to cards an agent settled for this user and
	// the user has not kept.
	AwaitsKeepBy *domain.ID
	// Relayed restricts to questions whose answer an agent relayed.
	Relayed bool
	// Priorities restricts to these priorities when not empty.
	Priorities []domain.Priority
	// Assignee restricts to cards assigned to or claimed by this actor id;
	// Unassigned restricts to cards with neither. They are exclusive.
	Assignee   *domain.ID
	Unassigned bool
	// Created, Updated and Closed bound the timestamps. A closed bound
	// excludes cards that were never closed.
	Created, Updated, Closed TimeRange
	// Sort and Desc choose the order; ignored when Query is set.
	Sort CardSort
	Desc bool
	// Query is a web-search style full-text query; with it the order is
	// relevance, and Cursor's Rank, Number and ID continue a page.
	Query  string
	Page   Page
	Cursor *CardCursor
}

// LaneCount is how many otherwise pullable cards wait behind a working card
// in one lane.
type LaneCount struct {
	Lane  string
	Cards int
}

// NoteCursor is the listing sort key of a note: updated_at desc, id desc.
type NoteCursor struct {
	UpdatedAt time.Time
	ID        domain.ID
	// Rank is the relevance of the row a full-text page continues from.
	Rank float64
}

// MemberCursor continues a member list after a row.
type MemberCursor struct {
	Role      domain.Role
	CreatedAt time.Time
	Actor     domain.ActorRef
}

// MemberFilter selects the members of an org or a project.
type MemberFilter struct {
	// All includes pending and removed members.
	All    bool
	Page   Page
	Cursor *MemberCursor
}

// AgentCursor continues an agent list after a row.
type AgentCursor struct {
	Status    domain.RecordStatus
	CreatedAt time.Time
	ID        domain.ID
}

// AgentFilter selects an org's agents.
type AgentFilter struct {
	// All includes revoked agents.
	All    bool
	Page   Page
	Cursor *AgentCursor
}

// TokenCursor continues a token list after a row.
type TokenCursor struct {
	CreatedAt time.Time
	ID        domain.ID
}

// NoteFilter selects notes.
type NoteFilter struct {
	ProjectID *domain.ID
	// ProjectIDs, when not nil, limits to these projects; the server passes
	// the caller's visible ones. An empty list matches nothing.
	ProjectIDs []domain.ID
	// Archived includes archived notes.
	Archived bool
	// Author restricts to notes created by this actor id.
	Author           *domain.ID
	Created, Updated TimeRange
	Query            string
	Page             Page
	Cursor           *NoteCursor
}

// EventPosition is an event's place in feed order: the xid of the
// transaction that wrote it, then its id. The zero value is the start of the
// feed. A position with an id and no xid names the event by id alone; the
// store looks its xid up.
type EventPosition struct {
	Xid uint64
	ID  domain.ID
}

// IsZero reports whether p is the start of the feed.
func (p EventPosition) IsZero() bool { return p.ID == 0 }

// Compare orders positions as the feed does, by xid and then id. A position
// without an xid sorts before every position that has one.
func (p EventPosition) Compare(q EventPosition) int {
	return cmp.Or(cmp.Compare(p.Xid, q.Xid), cmp.Compare(p.ID, q.ID))
}

// EventFilter selects events for ListEvents and CountEventsAfter.
type EventFilter struct {
	// ProjectIDs limits to these projects plus org-level events; nil means
	// every project. The server passes the actor's visible projects.
	ProjectIDs []domain.ID
	// After returns the events past After in feed order, oldest first.
	// Before, when set, returns the newest events ahead of Before instead;
	// the two are exclusive.
	After  EventPosition
	Before EventPosition
	Limit  int
	// ExcludeActor drops events written by this actor in ExcludeSession. An
	// event is dropped only when both match, so the same actor's other
	// sessions stay visible. Ignored when nil.
	ExcludeActor   *domain.ActorRef
	ExcludeSession string
	// Limited is the reader's reach into audience-limited events
	// (domain.EventType.Limited). The zero value reaches none, so a reader
	// that forgets it shows nothing it should not.
	Limited LimitedReach
}

// LimitedReach says which audience-limited events a reader gets: all of
// them (an org admin, or the server's own counting), or those that concern
// one user (domain.Event.Concerns). The zero value gets none.
type LimitedReach struct {
	All  bool
	User domain.ID
}

// Reaches reports whether the reader gets e.
func (r LimitedReach) Reaches(e domain.Event) bool {
	return !e.Type.Limited() || r.All || e.Concerns(r.User)
}

// SearchCursor is the sort key of a search hit: rank descending, then type,
// then id.
type SearchCursor struct {
	Rank float64
	Type domain.SubjectType
	ID   domain.ID
}

// SearchHit is one search match.
type SearchHit struct {
	Type      domain.SubjectType
	ID        domain.ID
	Kind      domain.CardKind // cards only
	Number    int64           // cards
	Slug      string
	ProjectID *domain.ID
	Title     string
	Status    string // card status word; empty for notes
	Snippet   string
	Rank      float64
}

// LinkEdge is one card link with the number and project of both cards.
type LinkEdge struct {
	FromID, ToID           domain.ID
	FromNumber, ToNumber   int64
	FromProject, ToProject domain.ID
	Kind                   domain.CardLinkKind
}

// LinkCursor is the sort key of a link list: from card id, to card id, kind.
type LinkCursor struct {
	From, To domain.ID
	Kind     domain.CardLinkKind
}

// LiveClaim is one live claim with the card it holds.
type LiveClaim struct {
	CardID    domain.ID
	ProjectID domain.ID
	Number    int64
	Title     string
	Kind      domain.CardKind
	Status    domain.Status
	Actor     domain.ActorRef
	Session   string
	ClaimedAt time.Time
	ExpiresAt time.Time
}

// SubscribedCard is a card a chat is subscribed to, with when the subscription began.
type SubscribedCard struct {
	CardID       domain.ID
	ProjectID    domain.ID
	ProjectSlug  string
	Number       int64
	Title        string
	Status       domain.Status
	SubscribedAt time.Time
}

// LiveClaimCursor is the sort key of a live claim list: holder, session,
// card id.
type LiveClaimCursor struct {
	Actor   domain.ActorRef
	Session string
	Card    domain.ID
}

// MentionEdge is a card's reference to a note.
type MentionEdge struct {
	CardID     domain.ID
	CardNumber int64
	NoteID     domain.ID
	NoteSlug   string
}

// MentionCursor is the sort key of a mention list: card id, note id.
type MentionCursor struct {
	Card, Note domain.ID
}

// GroupKind says what a map group is made of.
type GroupKind int16

// GroupKind values. Groups sort by kind, so stories come before lanes.
const (
	GroupStory GroupKind = 1
	GroupLane  GroupKind = 2
)

// MapGroupRef names a map group: a story by its card number, or a lane by its
// name. The lane group with the empty name holds the cards outside any story
// that have no lane.
type MapGroupRef struct {
	Kind   GroupKind
	Number int64
	Lane   string
}

// Less orders refs as the group lists are ordered: kind, story number, lane
// in byte order.
func (r MapGroupRef) Less(o MapGroupRef) bool {
	if r.Kind != o.Kind {
		return r.Kind < o.Kind
	}
	if r.Number != o.Number {
		return r.Number < o.Number
	}
	return r.Lane < o.Lane
}

// MapGroup is one box of the collapsed map: a story with the status counts of
// all its children, or a lane with the counts of the cards in it.
type MapGroup struct {
	Ref MapGroupRef
	// Title and StoryStatus are set for a story group only.
	Title       string
	StoryStatus domain.Status
	Counts      CardCounts
}

// MapGroupLink counts the links of one kind that run from a card in one group
// to a card in another.
type MapGroupLink struct {
	From, To MapGroupRef
	Kind     domain.CardLinkKind
	Count    int
}

// MapGroupLinkCursor is the sort key of a group link list: from group, to
// group, kind.
type MapGroupLinkCursor struct {
	From, To MapGroupRef
	Kind     domain.CardLinkKind
}
