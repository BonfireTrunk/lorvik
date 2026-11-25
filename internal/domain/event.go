package domain

import (
	"encoding/json"
	"slices"
	"time"
)

// EventType names what happened. The set is closed and documented here
// because clients render each type into a sentence and agents branch on it.
type EventType int16

// EventType values, grouped by subject with room between groups.
const (
	EventCardCreated       EventType = 1
	EventCardUpdated       EventType = 2
	EventCardStatusChanged EventType = 3
	EventCardClaimed       EventType = 4
	EventCardReleased      EventType = 5
	EventCardClaimExpired  EventType = 6
	EventCardAnswered      EventType = 7
	EventCardLinked        EventType = 8
	EventCardUnlinked      EventType = 9
	EventCardComment       EventType = 10
	EventCardFolded        EventType = 11
	// EventCardClaimEnding is the last call before a claim lapses, written
	// by the sweeper in UnpromptedSession with the holder as actor (payload
	// expires_at, and session when the claim has one).
	EventCardClaimEnding EventType = 12
	// EventCardKept says a person looked at what an agent settled for them
	// on the card and let it stand.
	EventCardKept EventType = 13
	// EventCardForPerson says a question was marked as one only a person
	// answers, or that a person cleared the mark (payload set).
	EventCardForPerson EventType = 14

	EventNoteCreated EventType = 30
	EventNoteUpdated EventType = 31

	EventProjectCreated  EventType = 40
	EventProjectUpdated  EventType = 41
	EventProjectArchived EventType = 42

	EventAgentRegistered EventType = 50
	EventAgentApproved   EventType = 51
	EventAgentRevoked    EventType = 52
	// EventAgentJoinRequested: an agent asked to join a project and its
	// owner has to decide.
	EventAgentJoinRequested EventType = 53
	// EventAgentJoined: an agent joined a project, allowed by its owner or
	// by the owner's settings (payload auto).
	EventAgentJoined     EventType = 54
	EventAgentJoinDenied EventType = 55
	// EventAgentUpdated: an agent's own say over joining projects changed.
	EventAgentUpdated EventType = 56
	// EventAgentJoinSetting: a user changed their own say over their agents
	// joining projects: the account switch, or with a project their rule there.
	EventAgentJoinSetting EventType = 57
	// EventAgentSessionRights: an agent's owner gave one chat of the agent
	// rights in a project, or took them back (empty rights).
	EventAgentSessionRights EventType = 58

	EventMembershipGranted EventType = 60
	EventMembershipChanged EventType = 61
	EventMembershipRemoved EventType = 62
	// EventMembershipRequested: a sign-in created a pending membership row.
	EventMembershipRequested EventType = 63
	// EventMembershipInvited: an admin invited a GitHub user ahead of their first sign-in.
	EventMembershipInvited EventType = 64

	EventTokenRotated       EventType = 70
	EventTokenReuseDetected EventType = 71

	EventLockAcquired EventType = 80
	EventLockReleased EventType = 81
	EventLockExpired  EventType = 82

	// EventIdentityLinked: a user added a way to sign in to their account
	// (payload provider and login). A first sign-in that creates the user
	// is not a link.
	EventIdentityLinked EventType = 90
	// EventIdentityUnlinked: a user removed a way to sign in from their account.
	EventIdentityUnlinked EventType = 91
	// EventIdentitySignInFailed: a wrong password was given for a user's
	// login. Nobody is signed in, so the user it was tried against is both
	// actor and subject, in UnpromptedSession. No payload: nothing about the
	// caller is kept.
	EventIdentitySignInFailed EventType = 92
	// EventIdentitySignInRefused: the wrong passwords for a user's login
	// reached a sign-in limit, so further tries are refused for a while.
	// Actor, subject, session and payload as for EventIdentitySignInFailed.
	EventIdentitySignInRefused EventType = 93

	// The fifties are full, so agent events go on here.
	// EventAgentRestored: a revoked agent was brought back (payload status).
	EventAgentRestored EventType = 100
	// EventAgentRemoved: an agent was removed for good.
	EventAgentRemoved EventType = 101
	// EventUserDeleted: a person deleted their own account. One per org they
	// were a member of, the person as actor and subject; no payload.
	EventUserDeleted EventType = 102
)

var eventTypes = names[EventType]{"type",
	[]EventType{
		EventCardCreated, EventCardUpdated, EventCardStatusChanged, EventCardClaimed, EventCardReleased,
		EventCardClaimExpired, EventCardAnswered, EventCardLinked, EventCardUnlinked, EventCardComment,
		EventCardFolded, EventCardClaimEnding, EventCardKept, EventCardForPerson,
		EventNoteCreated, EventNoteUpdated,
		EventProjectCreated, EventProjectUpdated, EventProjectArchived,
		EventAgentRegistered, EventAgentApproved, EventAgentRevoked, EventAgentJoinRequested, EventAgentJoined, EventAgentJoinDenied, EventAgentUpdated, EventAgentJoinSetting, EventAgentSessionRights,
		EventMembershipGranted, EventMembershipChanged, EventMembershipRemoved, EventMembershipRequested, EventMembershipInvited,
		EventTokenRotated, EventTokenReuseDetected,
		EventLockAcquired, EventLockReleased, EventLockExpired,
		EventIdentityLinked, EventIdentityUnlinked, EventIdentitySignInFailed, EventIdentitySignInRefused,
		EventAgentRestored, EventAgentRemoved,
		EventUserDeleted,
	},
	[]string{
		"card.created", "card.updated", "card.status_changed", "card.claimed", "card.released",
		"card.claim_expired", "card.answered", "card.linked", "card.unlinked", "card.comment",
		"card.folded", "card.claim_ending", "card.kept", "card.for_person",
		"note.created", "note.updated",
		"project.created", "project.updated", "project.archived",
		"agent.registered", "agent.approved", "agent.revoked", "agent.join_requested", "agent.joined", "agent.join_denied", "agent.updated", "agent.join_setting", "agent.session_rights",
		"membership.granted", "membership.changed", "membership.removed", "membership.requested", "membership.invited",
		"token.rotated", "token.reuse_detected",
		"lock.acquired", "lock.released", "lock.expired",
		"identity.linked", "identity.unlinked", "identity.signin_failed", "identity.signin_refused",
		"agent.restored", "agent.removed",
		"user.deleted",
	}}

func (t EventType) String() string { return eventTypes.name(t) }

// ParseEventType reads a dotted event name.
func ParseEventType(s string) (EventType, error) { return eventTypes.parse(s) }

// AllEventTypes in definition order.
var AllEventTypes = eventTypes.values

// limitedEvents are the events about one person's account: who was invited
// or asked to join, which sign-ins someone added or removed, wrong
// passwords against a login, a token that was rotated or used after it was
// replaced. They are shown only to the person they concern (Event.Concerns)
// and to org admins, never to other members or to agents (#314 A, #333 A).
// An agent's token events concern no person, so only admins get them.
var limitedEvents = []EventType{
	EventMembershipInvited, EventMembershipRequested,
	EventIdentityLinked, EventIdentityUnlinked, EventIdentitySignInFailed, EventIdentitySignInRefused,
	EventTokenRotated, EventTokenReuseDetected,
}

// LimitedEventTypes lists the audience-limited event types.
func LimitedEventTypes() []EventType { return slices.Clone(limitedEvents) }

// Limited reports whether events of type t have a limited audience.
func (t EventType) Limited() bool { return slices.Contains(limitedEvents, t) }

// UnpromptedSession is the session of an event the server wrote about an
// actor who did nothing, such as a wrong password someone tried against
// their login. A reader's inbox and unread count leave out what the reader
// did in their own session, and for a person on the dashboard that session
// is empty; this value is no label a request can carry (see
// apitypes.ValidSession), so such an event is never taken for the actor's
// own doing.
const UnpromptedSession = "@unprompted"

// Subject identifies the entity an event is about. Number is set for cards
// so a client can print "#42" without a lookup.
type Subject struct {
	Type   SubjectType
	ID     ID
	Number int64
}

// Event is one append-only record. Its id is the position clients page
// through; its time is in the id. The payload holds the facts a client needs
// to render the event, including the subject's title at the time.
type Event struct {
	OrgID ID
	ID    ID
	// Xid is the transaction that wrote the event, set when the store reads
	// it back. Feed order is (Xid, ID).
	Xid       uint64
	ProjectID *ID
	Type      EventType
	Actor     ActorRef
	// Session is the label of the session that wrote the event; empty when
	// the request sent none or the system wrote it, UnpromptedSession when
	// the actor did nothing.
	Session string
	Subject Subject
	Payload json.RawMessage
}

// Concerns reports whether the event is about user's own account: they are
// its subject (a membership subject carries the user's id) or they wrote it.
// The store's event queries apply the same rule in SQL.
func (e Event) Concerns(user ID) bool {
	if user == 0 {
		return false
	}
	return e.Subject.Type == SubjectMembership && e.Subject.ID == user ||
		e.Actor == ActorRef{Kind: ActorUser, ID: user}
}

// CreatedAt is the millisecond the event was written.
func (e Event) CreatedAt() time.Time { return e.ID.Time() }

// NewEvent builds an event with an encoded payload. Payload may be nil. The
// id is assigned by the caller's generator.
func NewEvent(id ID, orgID ID, projectID *ID, typ EventType, actor ActorRef, subject Subject, payload any) (Event, error) {
	e := Event{OrgID: orgID, ID: id, ProjectID: projectID, Type: typ, Actor: actor, Subject: subject}
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return Event{}, errorf("encode event payload: %w", err)
		}
		e.Payload = b
	}
	return e, nil
}
