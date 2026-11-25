package domain

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Claim records who holds a card and until when. It lives in its own table so
// renewals never rewrite the card. A card with a claim is "picked"; the
// card's own status says whether work has started.
type Claim struct {
	CardID    ID
	OrgID     ID
	ProjectID ID
	Actor     ActorRef
	// Session labels the client session that took the card; empty when the
	// client sent none.
	Session   string
	ExpiresAt time.Time
	CreatedAt time.Time
	// Lease is the length the holder stated for this claim; zero follows the
	// project's lease, whatever it is when the claim is renewed.
	Lease time.Duration
}

// ClaimLastCall is how long before a claim ends its holder gets a last call.
const ClaimLastCall = 5 * time.Minute

// LastCallAt is when the claim's last call is due: ClaimLastCall before it
// ends, or a quarter of its lease when that is shorter. Renewal starts at
// half the lease, so a holder that is making requests never gets one. The
// store's ListEndingClaims applies the same rule in SQL.
func (c Claim) LastCallAt(project time.Duration) time.Time {
	return c.ExpiresAt.Add(-min(ClaimLastCall, c.LeaseOr(project)/4))
}

// LeaseOr is the length the claim renews by: its own, or the project's.
func (c Claim) LeaseOr(project time.Duration) time.Duration { return cmp.Or(c.Lease, project) }

// Expired reports whether the lease has lapsed.
func (c Claim) Expired(now time.Time) bool { return !now.Before(c.ExpiresAt) }

// OwnedBy reports whether the claim is the caller's, live or not. An agent
// token is shared by every chat that runs under it, so an agent's claim
// belongs to the session that took it; a human's belongs to the human in
// any session.
func (c Claim) OwnedBy(actor ActorRef, session string) bool {
	return c.Actor == actor && (!actor.SessionScoped() || c.Session == session)
}

// HeldAgainst reports whether the claim binds the caller: it is live and
// someone else, or another session of the same agent, holds it. An expired
// claim binds nobody, even before the sweeper deletes it.
func (c Claim) HeldAgainst(actor ActorRef, session string, now time.Time) bool {
	return !c.Expired(now) && !c.OwnedBy(actor, session)
}

// HeldBy reports whether the caller holds a live claim.
func (c Claim) HeldBy(actor ActorRef, session string, now time.Time) bool {
	return c.OwnedBy(actor, session) && !c.Expired(now)
}

// Renew extends the lease from now by the claim's own length, or the
// project's. A newly stated length replaces the claim's and sets the expiry
// exactly, so a holder can also give time back.
func (c *Claim) Renew(now time.Time, project, stated time.Duration) {
	if stated != 0 {
		c.Lease = stated
		c.ExpiresAt = now.Add(stated)
		return
	}
	if next := now.Add(c.LeaseOr(project)); next.After(c.ExpiresAt) {
		c.ExpiresAt = next
	}
}

// Question holds the fields a question card adds.
type Question struct {
	Choices    []string
	Answer     string
	AnsweredBy *ActorRef
	AnsweredAt *time.Time
	// Relayed marks an answer an agent passed on from a human.
	Relayed bool
	// ForPerson marks a question only a person may answer: an agent can
	// still relay what a person said, never answer it itself.
	ForPerson bool
}

// Behalf records that an agent settled a card for a person under a right
// that person gave it: who the person is, which agent it was, what the agent
// said it relied on, and the chat it acted from. Until the person keeps it,
// it counts as not looked at.
type Behalf struct {
	For     ID // user id
	By      ID // agent id
	Reason  string
	Session string
	KeptAt  *time.Time
}

// Acceptance records who accepted a decision and when. It is kept when the
// decision is later revoked or superseded: it says who put it in force.
type Acceptance struct {
	By ActorRef
	At time.Time
}

// Fold records that a closed card's result was folded into the requirements
// documents. Into is the document path; empty means nothing durable changed.
type Fold struct {
	Into string
	At   time.Time
	By   ActorRef
}

// Card is a unit of work, a question for a human, a story or a decision.
type Card struct {
	ID         ID
	OrgID      ID
	ProjectID  ID
	Number     int64
	ParentID   *ID
	Kind       CardKind
	Status     Status
	DropReason DropReason
	Priority   Priority
	Title      string
	Body       string
	Assignee   *ActorRef
	CreatedBy  ActorRef
	Question   *Question
	// Branch is the git branch the work is on; empty when unset.
	Branch string
	// Lane names a resource that cannot take two concurrent edits; empty when
	// unset. Only one card per lane is handed out while another is working.
	Lane string
	// Behalf is set while the card stands as an agent settled it for a
	// person; a person settling it again clears it.
	Behalf *Behalf
	// Acceptance is set on a decision that is or was in force.
	Acceptance *Acceptance
	// Fold is set once the card is folded; see Card.FoldInto.
	Fold *Fold
	// ClosingNote is the note given with the move that closed the card; empty
	// while the card is open. It is what a fold brief quotes.
	ClosingNote string
	ClosedAt    *time.Time
	Version     int64
	// PageBytes is the size of the card's HTML page; 0 when it has none. The
	// page itself is kept apart and read only on request.
	PageBytes int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// transitions lists the allowed next statuses. Reopening from a terminal state
// goes to ready, never directly to working.
var transitions = map[Status][]Status{
	StatusBacklog: {StatusReady, StatusDropped},
	StatusReady:   {StatusBacklog, StatusWorking, StatusWaiting, StatusDropped},
	StatusWorking: {StatusReady, StatusWaiting, StatusDone, StatusDropped},
	StatusWaiting: {StatusReady, StatusWorking, StatusDropped},
	StatusDone:    {StatusReady},
	StatusDropped: {StatusReady},
}

// storyTransitions is the table without the reopen edges: a closed story is
// closed, and related later work is a new story. A story closes from ready and
// waiting as well as working: it only goes working (WorkBegan, never by hand)
// when a task or question child starts, so one whose children are decisions, or were settled by humans
// without being started, would otherwise never close.
var storyTransitions = map[Status][]Status{
	StatusBacklog: transitions[StatusBacklog],
	StatusReady:   {StatusBacklog, StatusWaiting, StatusDropped, StatusDone},
	StatusWorking: transitions[StatusWorking],
	StatusWaiting: {StatusReady, StatusDropped, StatusDone},
}

// decisionTransitions: proposed (ready) is accepted (done) or rejected
// (dropped); accepted is revoked (dropped); only a rejected decision comes
// back, as a new proposal. Which drop reason goes with each edge is checked in
// Transition.
var decisionTransitions = map[Status][]Status{
	StatusReady:   {StatusDone, StatusDropped},
	StatusDone:    {StatusDropped},
	StatusDropped: {StatusReady},
}

func transitionTable(k CardKind) map[Status][]Status {
	switch k {
	case KindStory:
		return storyTransitions
	case KindDecision:
		return decisionTransitions
	}
	return transitions
}

// CanTransition reports whether from -> to is allowed for a card of kind k.
func CanTransition(k CardKind, from, to Status) bool {
	return slices.Contains(transitionTable(k)[from], to)
}

// Validate checks a card's own fields. Relationships (parent exists, no
// cycles) are checked by the store.
func (c Card) Validate() error {
	var errs []error
	if strings.TrimSpace(c.Title) == "" {
		errs = append(errs, invalid("title", "required"))
	}
	if len(c.Title) > 200 {
		errs = append(errs, invalid("title", "at most 200 characters"))
	}
	if len(c.Body) > 64*1024 {
		errs = append(errs, invalid("body", "at most 64 KiB"))
	}
	if !cardKinds.valid(c.Kind) {
		errs = append(errs, invalid("kind", "must be task, question, story or decision"))
	}
	if !statuses.valid(c.Status) {
		errs = append(errs, invalid("status", "unknown status"))
	}
	if !priorities.valid(c.Priority) {
		errs = append(errs, invalid("priority", "must be low, normal, high or urgent"))
	}
	if c.Status == StatusDropped && c.DropReason == DropNone {
		errs = append(errs, invalid("drop_reason", "required when status is dropped"))
	}
	if c.Status != StatusDropped && c.DropReason != DropNone {
		errs = append(errs, invalid("drop_reason", "only allowed when status is dropped"))
	}
	if c.DropReason != DropNone && !dropReasons.valid(c.DropReason) {
		errs = append(errs, invalid("drop_reason", "must be one of: "+joinLabels(dropReasons.labels)))
	}
	if c.DropReason.DecisionOnly() && c.Kind != KindDecision {
		errs = append(errs, invalid("drop_reason", "rejected, revoked and superseded apply to decisions only"))
	}
	if c.Kind == KindDecision && c.Status == StatusDone && c.Acceptance == nil {
		errs = append(errs, invalid("status", "an accepted decision records who accepted it"))
	}
	if c.ParentID != nil && c.ID != 0 && *c.ParentID == c.ID {
		errs = append(errs, invalid("parent_id", "a card cannot be its own parent"))
	}
	errs = append(errs, ValidateBranch(c.Branch), ValidateLane(c.Lane))
	if c.Kind == KindQuestion && c.Question != nil && len(c.Question.Choices) == 1 {
		errs = append(errs, invalid("choices", "give at least two choices or none"))
	}
	return errors.Join(errs...)
}

// Transition applies a status change and the bookkeeping that goes with it.
// It does not touch the claim; the caller releases it when the card closes.
// A decision's edges are refused as invalid fields, not as conflicts: its
// status is a verdict, not a place on a board. Superseded is the system's
// reason (see Supersede) and is never accepted here.
func (c *Card) Transition(to Status, reason DropReason, now time.Time) error {
	if !CanTransition(c.Kind, c.Status, to) {
		if c.Kind == KindDecision {
			return invalid("status", fmt.Sprintf("a decision cannot go from %s to %s: it is accepted, rejected or revoked, and only a rejected one is proposed again", c.Status, to))
		}
		return errorf("%w: %s -> %s", ErrInvalidTransition, c.Status, to)
	}
	if to == StatusDropped && reason == DropNone {
		return invalid("drop_reason", "required when dropping")
	}
	if err := c.checkReason(to, reason); err != nil {
		return err
	}
	if c.Kind == KindQuestion && c.Question != nil && c.Status.Terminal() && !to.Terminal() {
		// A reopened question is open again: its old answer stays in the events.
		c.Question.Answer, c.Question.AnsweredBy, c.Question.AnsweredAt, c.Question.Relayed = "", nil, nil, false
	}
	if c.Status.Terminal() {
		// What an agent settled is undone or replaced by this move, so nothing
		// of it waits for a look any more.
		c.Behalf = nil
	}
	c.Status = to
	c.DropReason = DropNone
	if to == StatusDropped {
		c.DropReason = reason
	}
	if to.Terminal() {
		t := now
		c.ClosedAt = &t
	} else {
		c.ClosedAt = nil
		c.ClosingNote = ""
	}
	c.UpdatedAt = now
	return nil
}

// checkReason holds the kind-specific rules on drop reasons and reopening.
func (c *Card) checkReason(to Status, reason DropReason) error {
	if to == StatusDropped && reason == DropSuperseded {
		return invalid("drop_reason", "superseded is set by recording a superseding decision")
	}
	if c.Kind != KindDecision {
		if reason.DecisionOnly() {
			return invalid("drop_reason", "rejected, revoked and superseded apply to decisions only")
		}
		return nil
	}
	switch {
	case to == StatusDropped && c.Status == StatusReady && reason != DropRejected:
		return invalid("drop_reason", "a proposed decision is dropped as rejected")
	case to == StatusDropped && c.Status == StatusDone && reason != DropRevoked:
		return invalid("drop_reason", "an accepted decision is dropped as revoked")
	case to == StatusReady && c.Status == StatusDropped && c.DropReason != DropRejected:
		return invalid("status", fmt.Sprintf("a %s decision is not proposed again; record a new decision", c.DropReason))
	}
	return nil
}

// Accept puts a proposed decision in force and records who did it.
func (c *Card) Accept(by ActorRef, now time.Time) error {
	if c.Kind != KindDecision {
		return invalid("kind", "only decisions are accepted")
	}
	if err := c.Transition(StatusDone, DropNone, now); err != nil {
		return err
	}
	c.Acceptance = &Acceptance{By: by, At: now}
	return nil
}

// Supersede drops an accepted decision because a successor was accepted.
func (c *Card) Supersede(now time.Time) error {
	if c.Kind != KindDecision || c.Status != StatusDone {
		return errorf("%w: only an accepted decision is superseded", ErrInvalidTransition)
	}
	t := now
	c.Status, c.DropReason, c.ClosedAt, c.UpdatedAt = StatusDropped, DropSuperseded, &t, now
	c.Behalf = nil
	return nil
}

// WorkBegan moves a story that is still backlog or ready to working because
// one of its children started. It reports whether the story changed; the
// edge is not in the manual table because nobody starts a story directly.
func (c *Card) WorkBegan(now time.Time) bool {
	if c.Kind != KindStory || (c.Status != StatusBacklog && c.Status != StatusReady) {
		return false
	}
	c.Status, c.UpdatedAt = StatusWorking, now
	return true
}

// FoldInto marks a closed card as folded into the document at path; an empty
// path means nothing durable changed. A card that is not done, has a parent
// or is already folded cannot be folded on its own.
func (c *Card) FoldInto(path string, by ActorRef, now time.Time) error {
	if err := c.CanFold(); err != nil {
		return err
	}
	c.Fold = &Fold{Into: path, At: now, By: by}
	c.UpdatedAt = now
	return nil
}

// ParkedBy reports whether a blocker of the given kind parks the card in
// waiting: an active (ready or working) card always, a backlog card only for
// a question, whose answer is what it waits for. A decision is never parked.
func (c Card) ParkedBy(blocker CardKind) bool {
	if c.Kind == KindDecision {
		return false
	}
	switch c.Status {
	case StatusReady, StatusWorking:
		return true
	case StatusBacklog:
		return blocker == KindQuestion
	}
	return false
}

// Why a card cannot be folded on its own. CanFold wraps ErrInvalidTransition
// and one of these, so a caller can pick its advice by reason.
var (
	ErrFoldNotDone   = errors.New("only a done card is folded")
	ErrFoldInStory   = errors.New("its story folds it when it closes")
	ErrAlreadyFolded = errors.New("already folded")
)

// CanFold reports whether FoldInto would accept the card.
func (c Card) CanFold() error {
	switch {
	case c.Status != StatusDone:
		return errorf("%w: card #%d is %s; %w", ErrInvalidTransition, c.Number, c.Status, ErrFoldNotDone)
	case c.ParentID != nil:
		return errorf("%w: card #%d belongs to a story; %w", ErrInvalidTransition, c.Number, ErrFoldInStory)
	case c.Fold != nil:
		return errorf("%w: card #%d is %w", ErrInvalidTransition, c.Number, ErrAlreadyFolded)
	}
	return nil
}

// Workable reports whether the card is ever claimed or started by hand. A
// decision is a verdict and a story's status follows its children, so
// neither is.
func (c Card) Workable() error {
	switch c.Kind {
	case KindDecision:
		return errorf("%w: decision #%d is proposed, accepted or rejected, never claimed or started", ErrInvalidTransition, c.Number)
	case KindStory:
		return errorf("%w: story #%d is never claimed or started", ErrInvalidTransition, c.Number)
	}
	return nil
}

// NewClaim gives actor the card for the length they stated, or for the
// project's lease when they stated none. A live claim by someone else, or by
// another session of the same agent, is refused; an expired or own claim is
// replaced, and replacing one's own live claim keeps the length stated for
// it. The card itself is not changed: claiming is "picked", starting is a
// status change.
func NewClaim(c Card, existing *Claim, actor ActorRef, session string, now time.Time, project, stated time.Duration) (Claim, error) {
	if c.Status.Terminal() {
		return Claim{}, errorf("%w: card is %s", ErrInvalidTransition, c.Status)
	}
	if err := c.Workable(); err != nil {
		return Claim{}, err
	}
	if existing != nil && existing.HeldAgainst(actor, session, now) {
		return Claim{}, errorf("%w: until %s", ErrClaimedByOther, existing.ExpiresAt.UTC().Format(time.RFC3339))
	}
	if stated == 0 && existing != nil && existing.HeldBy(actor, session, now) {
		stated = existing.Lease
	}
	return Claim{CardID: c.ID, OrgID: c.OrgID, ProjectID: c.ProjectID, Actor: actor, Session: session,
		ExpiresAt: now.Add(cmp.Or(stated, project)), CreatedAt: now, Lease: stated}, nil
}

// Start moves a picked card to working.
func (c *Card) Start(now time.Time) error {
	if c.Status == StatusWorking {
		return nil
	}
	return c.Transition(StatusWorking, DropNone, now)
}

// Released is the card-side bookkeeping when its claim goes away without the
// card closing: a working card returns to ready. It reports whether the card
// changed. A story is never released: it has no claim of its own and its
// status follows its children.
func (c *Card) Released(now time.Time) bool {
	if c.Kind == KindStory || c.Status != StatusWorking {
		return false
	}
	c.Status = StatusReady
	c.UpdatedAt = now
	return true
}

// Relayed reports whether the card is a question whose answer an agent passed
// on.
func (c Card) Relayed() bool {
	return c.Kind == KindQuestion && c.Status == StatusDone && c.Question != nil && c.Question.Relayed
}

// ForPerson reports whether the card is a question only a person may answer.
func (c Card) ForPerson() bool {
	return c.Kind == KindQuestion && c.Question != nil && c.Question.ForPerson
}

// MarkForPerson hands an open question to a person: from now on no agent
// answers it. Clearing the mark is ClearForPerson, a person's call.
func (c *Card) MarkForPerson(now time.Time) error {
	switch {
	case c.Kind != KindQuestion:
		return invalid("kind", "only a question can be marked for a person")
	case c.Status.Terminal():
		return errorf("%w: question is already %s", ErrInvalidTransition, c.Status)
	case c.ForPerson():
		return errorf("%w: the question is already marked for a person", ErrInvalidTransition)
	}
	if c.Question == nil {
		c.Question = &Question{}
	}
	c.Question.ForPerson = true
	c.UpdatedAt = now
	return nil
}

// ClearForPerson lets agents holding the answer right answer the question
// again. Only a person does this.
func (c *Card) ClearForPerson(by ActorRef, now time.Time) error {
	switch {
	case by.Kind != ActorUser:
		return errorf("%w: only a person clears the for-a-person mark", ErrForbidden)
	case !c.ForPerson():
		return errorf("%w: the question is not marked for a person", ErrInvalidTransition)
	}
	c.Question.ForPerson = false
	c.UpdatedAt = now
	return nil
}

// AwaitsKeep reports whether the card stands as an agent settled it for a
// person who has not kept it yet.
func (c Card) AwaitsKeep() bool {
	return c.Behalf != nil && c.Behalf.KeptAt == nil
}

// Keep records that the person an agent settled the card for has looked at
// it and lets it stand. Only that person keeps it, and only once.
func (c *Card) Keep(by ActorRef, now time.Time) error {
	switch {
	case !c.AwaitsKeep():
		return errorf("%w: nothing on this card waits to be kept", ErrInvalidTransition)
	case by.Kind != ActorUser || by.ID != c.Behalf.For:
		return errorf("%w: only the person it was settled for keeps it", ErrForbidden)
	}
	c.Behalf.KeptAt = &now
	c.UpdatedAt = now
	return nil
}

// Reanswer says what an answer did to one an agent gave and no person had
// settled: a user answered it again, and Previous is the agent's text when
// the user changed it.
type Reanswer struct {
	Confirmed bool
	Previous  string
}

// Answer records an answer on a question card. An open question is answered
// and closed. An agent only passes on a human's answer, so it must say it is
// relaying unless behalf says the caller checked its answer right and names
// the person it answers for; a user cannot claim to relay. An answer an
// agent relayed, or gave for a person who has not kept it, stays open to one
// more answer from a user, who answers it as their own.
func (c *Card) Answer(answer string, by ActorRef, relayed bool, behalf *Behalf, now time.Time) (Reanswer, error) {
	var re Reanswer
	entitled := behalf != nil
	if c.Kind != KindQuestion {
		return re, invalid("kind", "only question cards can be answered")
	}
	if strings.TrimSpace(answer) == "" {
		return re, invalid("answer", "required")
	}
	switch {
	case by.Kind == ActorAgent && !relayed && c.ForPerson():
		return re, errorf("%w: this question is for a person, an agent only relays a human's answer", ErrForbidden)
	case by.Kind == ActorAgent && !relayed && !entitled:
		return re, errorf("%w: an agent cannot answer a question, only relay a human's answer", ErrForbidden)
	case by.Kind != ActorAgent && relayed:
		return re, invalid("relayed", "relaying is for agents passing on a human's answer")
	}
	if c.Question == nil {
		c.Question = &Question{}
	}
	switch {
	case (c.Relayed() || c.Status == StatusDone && c.AwaitsKeep()) && by.Kind == ActorUser:
		re.Confirmed = true
		if c.Question.Answer != answer {
			re.Previous = c.Question.Answer
		}
	case c.Status.Terminal():
		return re, errorf("%w: question is already %s", ErrInvalidTransition, c.Status)
	}
	c.Question.Answer = answer
	c.Question.AnsweredBy = &by
	c.Question.AnsweredAt = &now
	c.Question.Relayed = relayed && !entitled
	c.Behalf = behalf
	if !re.Confirmed {
		c.Status = StatusDone
		c.ClosedAt = &now
	}
	c.UpdatedAt = now
	return re, nil
}

// CardLink is a directed edge: From blocks To, From duplicates To.
type CardLink struct {
	FromCardID ID
	ToCardID   ID
	Kind       CardLinkKind
	CreatedBy  ActorRef
	CreatedAt  time.Time
}

// Validate checks a link.
func (l CardLink) Validate() error {
	if !cardLinkKinds.valid(l.Kind) {
		return invalid("kind", "must be one of: "+joinLabels(cardLinkKinds.labels))
	}
	if l.FromCardID == l.ToCardID {
		return invalid("to", "a card cannot link to itself")
	}
	return nil
}

// Comment is the message left on a card for whoever picks it up next. It is
// append-only: never edited, never deleted while the card exists. Its time
// is in its id.
type Comment struct {
	ID     ID
	CardID ID
	Actor  ActorRef
	Body   string
}

// Validate checks a comment.
func (h Comment) Validate() error {
	if strings.TrimSpace(h.Body) == "" {
		return invalid("body", "required")
	}
	if len(h.Body) > 64*1024 {
		return invalid("body", "at most 64 KiB")
	}
	return nil
}

// CardRead records the last moment an actor wrote on or opened a card. A
// comment with a greater id than SeenID is a reply the actor has not seen.
type CardRead struct {
	CardID, ProjectID ID
	Actor             ActorRef
	SeenID            ID
}

// Reply is the newest comment by someone else on a card since the actor last
// saw it, with how many such comments there are.
type Reply struct {
	Comment Comment
	Unseen  int
}

// MaxBranchLen and MaxLaneLen bound the free-text tags on a card.
const (
	MaxBranchLen = 200
	MaxLaneLen   = 40
)

// ValidateBranch accepts an empty branch or one without whitespace.
func ValidateBranch(s string) error {
	switch {
	case len(s) > MaxBranchLen:
		return invalid("branch", "at most 200 characters")
	case strings.ContainsFunc(s, unicode.IsSpace):
		return invalid("branch", "must not contain whitespace")
	}
	return nil
}

// ValidateLane accepts an empty lane or a short tag without whitespace or
// commas, so lists of lanes stay unambiguous.
func ValidateLane(s string) error {
	switch {
	case len(s) > MaxLaneLen:
		return invalid("lane", "at most 40 characters")
	case strings.ContainsFunc(s, unicode.IsSpace) || strings.Contains(s, ","):
		return invalid("lane", "must not contain whitespace or commas")
	}
	return nil
}

// CardRevision is the title, body and page a card had before an edit replaced
// them. UpdatedBy is the actor who made that edit, and Version is the card
// version it replaced.
type CardRevision struct {
	ID        ID
	CardID    ID
	Version   int64
	UpdatedBy ActorRef
	CreatedAt time.Time
	Title     string
	Body      string
	// Page is the card's page at that version; empty when it had none.
	Page string
}
