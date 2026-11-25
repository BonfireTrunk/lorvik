package server

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// cardMutation loads a card in a transaction, lets fn change it, and returns
// the result. fn persists what it changes and may append events.
func (s *Service) cardMutation(ctx context.Context, slug string, number int64, fn func(tx Store, p Principal, proj domain.Project, a access, c *domain.Card) error) (apitypes.Card, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Card{}, err
	}
	var out domain.Card
	err = s.store.WithTx(ctx, func(tx Store) error {
		proj, a, c, lerr := s.loadCard(ctx, tx, p, slug, number)
		if lerr != nil {
			return lerr
		}
		if ferr := fn(tx, p, proj, a, &c); ferr != nil {
			return ferr
		}
		out = c
		return nil
	})
	if err != nil {
		return apitypes.Card{}, err
	}
	return s.lookup(s.store, p).card(ctx, out)
}

// currentClaim returns the card's claim, nil when none.
func currentClaim(ctx context.Context, st Store, cardID domain.ID) (*domain.Claim, error) {
	cl, err := st.GetClaim(ctx, cardID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load claim: %w", err)
	}
	return &cl, nil
}

// claimFor gives the caller the card's lease: a fresh claim, or a renewal of
// their own. Someone else's live claim is refused unless the caller may
// triage, in which case it is taken over. stated is the length the caller
// asked for, zero for none.
func (s *Service) claimFor(ctx context.Context, tx Store, p Principal, a access, proj domain.Project, c domain.Card, stated time.Duration) (domain.Claim, *domain.Claim, error) {
	existing, err := currentClaim(ctx, tx, c.ID)
	if err != nil {
		return domain.Claim{}, nil, err
	}
	now := s.now()
	cl, err := domain.NewClaim(c, existing, p.Actor, p.Session, now, proj.Lease, stated)
	if errors.Is(err, domain.ErrClaimedByOther) && a.mayOverride(p) {
		cl, err = domain.NewClaim(c, nil, p.Actor, p.Session, now, proj.Lease, stated)
	}
	if errors.Is(err, domain.ErrClaimedByOther) {
		return domain.Claim{}, nil, withHint(fmt.Errorf("%w: #%d is held by %s until %s", domain.ErrClaimedByOther, c.Number,
			holder(*existing), existing.ExpiresAt.UTC().Format(time.RFC3339)), "pick another card with `lorvik next`, or wait for the lease to expire")
	}
	if err != nil {
		return domain.Claim{}, nil, err
	}
	if err := tx.PutClaim(ctx, cl); err != nil {
		return domain.Claim{}, nil, fmt.Errorf("claim #%d: %w", c.Number, err)
	}
	return cl, existing, nil
}

// holder names a claim's holder for refusals; the session tells apart the
// chats that share one agent token.
func holder(cl domain.Claim) string {
	if cl.Session == "" {
		return fmt.Sprintf("%s %s", cl.Actor.Kind, cl.Actor.ID)
	}
	return fmt.Sprintf("%s %s in session %s", cl.Actor.Kind, cl.Actor.ID, cl.Session)
}

func optionalVersion(c domain.Card, version int64) error {
	if version != 0 {
		return versionCheck(c.Version, version, fmt.Sprintf("card #%d", c.Number))
	}
	return nil
}

// statedLease parses the length a caller asks for a claim; empty is none.
// A claim keeps its length in whole seconds, so a fraction is cut here and
// the first expiry is the one every renewal repeats.
func statedLease(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	d, err := parseLease(raw)
	if err != nil {
		return 0, err
	}
	d = d.Truncate(time.Second)
	return d, domain.ValidLease(d)
}

// Claim leases a card to the caller and moves it to working. lease is the
// length the caller states for this claim, empty for the project's.
func (s *Service) Claim(ctx context.Context, slug string, number, version int64, lease string) (apitypes.Card, error) {
	stated, err := statedLease(lease)
	if err != nil {
		return apitypes.Card{}, err
	}
	return s.cardMutation(ctx, slug, number, func(tx Store, p Principal, proj domain.Project, a access, c *domain.Card) error {
		if err := a.require(p, domain.PermCardWrite); err != nil {
			return err
		}
		if err := optionalVersion(*c, version); err != nil {
			return err
		}
		if err := notWorked(*c); err != nil {
			return err
		}
		if err := s.agentOnQuestion(ctx, tx, p, *c, domain.StatusWorking); err != nil {
			return err
		}
		blockers, err := tx.ListBlockers(ctx, c.ID)
		if err != nil {
			return fmt.Errorf("list blockers: %w", err)
		}
		if len(blockers) > 0 {
			return withHint(fmt.Errorf("%w: card #%d is blocked by #%d: %s", domain.ErrInvalidTransition, c.Number, blockers[0].Number, blockers[0].Title),
				"resolve or answer the blocker first, or pick another card with `lorvik next`")
		}
		if c.Status == domain.StatusBacklog {
			return withHint(fmt.Errorf("%w: card #%d is still in the backlog", domain.ErrInvalidTransition, c.Number),
				"move it to ready first with `lorvik ready <n>`")
		}
		cl, previous, err := s.claimFor(ctx, tx, p, a, proj, *c, stated)
		if err != nil {
			return err
		}
		expected := c.Version
		if err := c.Start(s.now()); err != nil {
			return err
		}
		updated, err := updateCard(ctx, tx, *c, expected)
		if err != nil {
			return err
		}
		*c = updated
		pl := payload(updated.Title, "expires_at", cl.ExpiresAt)
		if previous != nil && !previous.OwnedBy(p.Actor, p.Session) {
			pl["taken_from"] = toActor(previous.Actor)
			if previous.Session != "" {
				pl["taken_from_session"] = previous.Session
			}
		}
		if err := s.emitP(ctx, tx, p, &proj.ID, domain.EventCardClaimed, cardSubject(updated), pl); err != nil {
			return err
		}
		return s.storyFollows(ctx, tx, p, updated)
	})
}

// Renew extends the caller's lease, by the length it states or else by the
// claim's own. Renewal is not an event: it would drown the inbox.
func (s *Service) Renew(ctx context.Context, slug string, number, version int64, lease string) (apitypes.Card, error) {
	stated, err := statedLease(lease)
	if err != nil {
		return apitypes.Card{}, err
	}
	return s.cardMutation(ctx, slug, number, func(tx Store, p Principal, proj domain.Project, a access, c *domain.Card) error {
		if err := a.require(p, domain.PermCardWrite); err != nil {
			return err
		}
		if err := optionalVersion(*c, version); err != nil {
			return err
		}
		cl, err := currentClaim(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		if cl == nil || !cl.OwnedBy(p.Actor, p.Session) {
			return withHint(fmt.Errorf("%w: #%d", domain.ErrNotClaimant, c.Number), "claim the card first with `lorvik claim <n>`")
		}
		cl.Renew(s.now(), proj.Lease, stated)
		if err := tx.PutClaim(ctx, *cl); err != nil {
			return fmt.Errorf("renew claim: %w", err)
		}
		s.logger.DebugContext(ctx, "lease renewed", "project", proj.Slug, "card", c.Number, "actor", p.Actor.ID, "expires_at", cl.ExpiresAt)
		return nil
	})
}

// Release gives a claim back; a working card returns to ready.
func (s *Service) Release(ctx context.Context, slug string, number, version int64) (apitypes.Card, error) {
	return s.cardMutation(ctx, slug, number, func(tx Store, p Principal, proj domain.Project, a access, c *domain.Card) error {
		if err := a.require(p, domain.PermCardWrite); err != nil {
			return err
		}
		if err := optionalVersion(*c, version); err != nil {
			return err
		}
		cl, err := currentClaim(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		if cl == nil {
			return withHint(fmt.Errorf("%w: #%d is not claimed", domain.ErrNotClaimant, c.Number), "nothing to release")
		}
		if cl.HeldAgainst(p.Actor, p.Session, s.now()) && !a.mayOverride(p) {
			return withHint(fmt.Errorf("%w: #%d is held by %s", domain.ErrNotClaimant, c.Number, holder(*cl)), "only the claimant or a reviewer can release it")
		}
		if err := releaseClaim(ctx, tx, *c); err != nil {
			return err
		}
		if c.Released(s.now()) {
			updated, err := updateCard(ctx, tx, *c, c.Version)
			if err != nil {
				return err
			}
			*c = updated
		}
		pl := payload(c.Title, "status", c.Status.String())
		if !cl.OwnedBy(p.Actor, p.Session) {
			pl["released_from"] = toActor(cl.Actor)
			if cl.Session != "" {
				pl["released_from_session"] = cl.Session
			}
		}
		return s.emitP(ctx, tx, p, &proj.ID, domain.EventCardReleased, cardSubject(*c), pl)
	})
}

// notWorked is the 409 for claiming or starting a card that is never
// worked by hand, with the advice for its kind.
func notWorked(c domain.Card) error {
	err := c.Workable()
	switch {
	case err == nil:
		return nil
	case c.Kind == domain.KindDecision:
		return withHint(err, "a human accepts or rejects it with `lorvik decisions accept` or `reject`")
	}
	return withHint(err, "a story is not worked: start one of its children")
}

// agentOnQuestion is the 409 for an agent moving an open question anywhere but
// dropped, or dropping one that still blocks open cards: the human answers a
// question, an agent only relays that answer, and a withdrawn question must
// not release the work that waited for the answer.
func (s *Service) agentOnQuestion(ctx context.Context, tx Store, p Principal, c domain.Card, to domain.Status) error {
	if !p.IsAgent() || c.Kind != domain.KindQuestion || !c.Status.Open() {
		return nil
	}
	if to != domain.StatusDropped {
		return withHint(fmt.Errorf("%w: question #%d is closed by the human's answer; an agent never works, parks or closes it", domain.ErrInvalidTransition, c.Number),
			relayAdvice(c.Number))
	}
	links, err := tx.ListCardLinks(ctx, c.ID)
	if err != nil {
		return fmt.Errorf("list links of #%d: %w", c.Number, err)
	}
	for _, l := range links {
		if l.Kind != domain.LinkBlocks || l.FromCardID != c.ID {
			continue
		}
		t, err := tx.GetCard(ctx, l.ToCardID)
		if err != nil {
			return fmt.Errorf("load blocked card: %w", err)
		}
		if t.Status.Open() {
			return withHint(fmt.Errorf("%w: question #%d still blocks #%d", domain.ErrInvalidTransition, c.Number, t.Number),
				relayAdvice(c.Number)+"; or, when the blocked work itself is gone, drop that card first")
		}
	}
	return nil
}

// foldAdvice is the advice for why CanFold refused the card; empty when the
// reason is not one CanFold names.
func foldAdvice(err error) string {
	switch {
	case errors.Is(err, domain.ErrFoldNotDone):
		return "finish the card first"
	case errors.Is(err, domain.ErrFoldInStory):
		return "fold the story instead"
	case errors.Is(err, domain.ErrAlreadyFolded):
		return "nothing to do"
	}
	return ""
}

// foldRefused attaches the advice for why CanFold refused the card.
func foldRefused(err error) error {
	if hint := foldAdvice(err); hint != "" {
		return withHint(err, hint)
	}
	return err
}

// decisionAuthorPerm is what reworking a decision's proposal takes: its
// proposer needs decision-propose; anyone else needs decision-accept, or for
// an agent the triage right.
func decisionAuthorPerm(p Principal, c domain.Card) domain.Permission {
	switch {
	case c.CreatedBy == p.Actor:
		return domain.PermDecisionPropose
	case p.IsAgent():
		return domain.PermCardTriage
	}
	return domain.PermDecisionAccept
}

// requireMove checks the caller may make this status change. A decision's
// verdicts (accept, reject, revoke) are a human's with decision-accept, and
// accepting or rejecting a proposal is also an agent's under the decide right
// (agentVerdict); proposing it again is its proposer's with decision-propose,
// or a reviewer's. Making a card ready from the backlog is authorising it: an
// agent needs the ready right even on its own card, and a person needs ready
// on one they do not own. Every other card follows the ordinary edit rules.
func (s *Service) requireMove(ctx context.Context, tx Store, p Principal, a access, c domain.Card, to domain.Status) (*domain.Claim, error) {
	if c.Kind != domain.KindDecision && c.Status == domain.StatusBacklog && to == domain.StatusReady {
		claim, err := currentClaim(ctx, tx, c.ID)
		if err != nil {
			return nil, err
		}
		perm := domain.PermCardWrite
		if p.IsAgent() || !ownsCard(p, c, claim) {
			perm = domain.PermCardReady
		}
		return claim, a.require(p, perm)
	}
	if c.Kind != domain.KindDecision {
		return s.requireCardEdit(ctx, tx, p, a, c, false)
	}
	if c.Status == domain.StatusDropped && to == domain.StatusReady {
		return nil, a.require(p, decisionAuthorPerm(p, c))
	}
	if agentVerdictOn(p, c, to) {
		return nil, s.agentVerdict(ctx, tx, p, a, c)
	}
	if err := requireHuman(p); err != nil {
		return nil, err
	}
	return nil, a.require(p, domain.PermDecisionAccept)
}

// agentVerdictOn reports whether the move is an agent accepting or rejecting
// a proposed decision: the one thing the decide right is for.
func agentVerdictOn(p Principal, c domain.Card, to domain.Status) bool {
	return p.IsAgent() && c.Kind == domain.KindDecision && c.Status == domain.StatusReady && to.Terminal()
}

// agentVerdict checks an agent may accept or reject the proposed decision d
// for its owner: it holds the decide right there and nothing bars this
// proposal to it (verdictBarred).
func (s *Service) agentVerdict(ctx context.Context, tx Store, p Principal, a access, d domain.Card) error {
	const person = "a person accepts or rejects it on the dashboard"
	if err := p.requireWritable(); err != nil {
		return err
	}
	if !a.rights.Has(domain.RightDecide) {
		return p.lacksRight(domain.RightDecide, a.slug, person)
	}
	why, err := s.verdictBarred(ctx, tx, p, a.slug, d)
	if err != nil || why == "" {
		return err
	}
	return withHint(fmt.Errorf("%w: %s", domain.ErrForbidden, why), person)
}

// verdictBarred says why the calling chat gives no verdict on the proposed
// decision d whatever rights it holds, or "" when nothing bars it. A decision
// of the global project binds every project; a chat does not ratify what it
// or its subagents proposed; and replacing a decision in force is changing
// what a person put there. Labels are the agent's own claim, so the second
// bar catches mistakes, not a chat that lies. slug is d's project.
func (s *Service) verdictBarred(ctx context.Context, st Store, p Principal, slug string, d domain.Card) (string, error) {
	if slug == domain.GlobalSlug {
		return fmt.Sprintf("#%d is in the global project, where only a person gives the verdict", d.Number), nil
	}
	own, err := askedByChat(ctx, st, p, d)
	if err != nil {
		return "", err
	}
	if own {
		chat := "a chat without a session label"
		if base := domain.BaseSession(p.Session); base != "" {
			chat = "chat " + base
		}
		return fmt.Sprintf("%s proposed #%d, so it gives no verdict on it", chat, d.Number), nil
	}
	links, err := st.ListCardLinks(ctx, d.ID)
	if err != nil {
		return "", fmt.Errorf("list links of #%d: %w", d.Number, err)
	}
	for _, l := range links {
		if l.Kind != domain.LinkSupersedes || l.FromCardID != d.ID {
			continue
		}
		old, err := st.GetCard(ctx, l.ToCardID)
		if err != nil {
			return "", fmt.Errorf("load superseded decision: %w", err)
		}
		if old.Status == domain.StatusDone {
			return fmt.Sprintf("#%d would replace accepted decision #%d, which only a person does", d.Number, old.Number), nil
		}
	}
	return "", nil
}

// openChildren lists the children that are not done or dropped.
func openChildren(children []domain.Card) []domain.Card {
	var open []domain.Card
	for _, ch := range children {
		if ch.Status.Open() {
			open = append(open, ch)
		}
	}
	return open
}

func numbers(cs []domain.Card) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = fmt.Sprintf("#%d", c.Number)
	}
	return strings.Join(parts, ", ")
}

// foldOf reads the fold of a request: exactly one of a document path or
// none. field names the request field in the error.
func foldOf(field, into string, none bool) (string, error) {
	into = strings.TrimSpace(into)
	switch {
	case into != "" && none:
		return "", invalidField(field, "give a document path or none, not both")
	case into == "" && !none:
		return "", invalidField(field, "required: give the document the result was folded into, or none when nothing durable changed")
	}
	return into, nil
}

// storyFollows moves the story of a child that started working to working.
func (s *Service) storyFollows(ctx context.Context, tx Store, p Principal, child domain.Card) error {
	if child.ParentID == nil || child.Status != domain.StatusWorking {
		return nil
	}
	story, err := tx.GetCard(ctx, *child.ParentID)
	if err != nil {
		return fmt.Errorf("load story of #%d: %w", child.Number, err)
	}
	from := story.Status
	if !story.WorkBegan(s.now()) {
		return nil
	}
	updated, err := updateCard(ctx, tx, story, story.Version)
	if err != nil {
		return err
	}
	return s.emitStatusChanged(ctx, tx, p, updated, from, "by_child", child.Number)
}

// closeStory checks a story may move to done and returns the fold path:
// every child is done or dropped, and the request carries a fold.
func closeStory(c domain.Card, children []domain.Card, in apitypes.TransitionCard) (string, error) {
	if open := openChildren(children); len(open) > 0 {
		return "", conflict(fmt.Sprintf("story #%d has open cards: %s", c.Number, numbers(open)),
			"finish or drop them first, or drop the story with cascade")
	}
	if in.Fold == nil {
		return "", invalidField("fold", "required to close a story: give the document the result was folded into, or none when nothing durable changed")
	}
	return foldOf("fold", in.Fold.Into, in.Fold.None)
}

// dropChildren drops the open children of a story that is being dropped,
// releasing their claims. They take the story's reason, except that a
// duplicate story's children are obsolete (they duplicate nothing, and a
// duplicate drop needs a survivor), and a proposed decision cannot be dropped
// as obsolete; it is rejected.
func (s *Service) dropChildren(ctx context.Context, tx Store, p Principal, story domain.Card, open []domain.Card, reason domain.DropReason) error {
	now := s.now()
	for _, ch := range open {
		r := reason
		switch {
		case ch.Kind == domain.KindDecision:
			r = domain.DropRejected
		case reason == domain.DropDuplicate:
			r = domain.DropObsolete
		}
		from := ch.Status
		if err := ch.Transition(domain.StatusDropped, r, now); err != nil {
			return err
		}
		updated, err := updateCard(ctx, tx, ch, ch.Version)
		if err != nil {
			return err
		}
		if err := releaseClaim(ctx, tx, updated); err != nil {
			return err
		}
		if err := s.emitStatusChanged(ctx, tx, p, updated, from, "drop_reason", r.String(), "cascade_of", story.Number); err != nil {
			return err
		}
		if err := s.unblockDependents(ctx, tx, p, updated); err != nil {
			return err
		}
	}
	return nil
}

// emitCardFolded records that the card was folded into the document at into.
func (s *Service) emitCardFolded(ctx context.Context, tx Store, p Principal, proj domain.Project, c domain.Card, into string) error {
	return s.emitP(ctx, tx, p, &proj.ID, domain.EventCardFolded, cardSubject(c), payload(c.Title, "into", into))
}

// foldChildren stamps the fold on every child of a story that is closing.
func (s *Service) foldChildren(ctx context.Context, tx Store, children []domain.Card, f domain.Fold) error {
	for _, ch := range children {
		ch.Fold = &f
		ch.UpdatedAt = f.At
		if _, err := updateCard(ctx, tx, ch, ch.Version); err != nil {
			return err
		}
	}
	return nil
}

// Transition changes status. Dropping needs a reason; a duplicate drop also
// needs the surviving card and records a duplicates link. Decisions, stories
// and their children follow the extra rules of the story and decision model.
func (s *Service) Transition(ctx context.Context, slug string, number int64, in apitypes.TransitionCard, key string) (apitypes.Card, error) {
	return s.cardMutation(ctx, slug, number, func(tx Store, p Principal, proj domain.Project, a access, c *domain.Card) error {
		if _, repeat, err := s.claimRequestKey(ctx, tx, p, key, domain.RequestTransition, proj, c.ID, c.ID); err != nil {
			return err
		} else if repeat {
			// The claim may have waited for the first request to commit: the
			// card loaded before it is then the card before the move.
			moved, gerr := tx.GetCard(ctx, c.ID)
			*c = moved
			return gerr
		}
		to, err := domain.ParseStatus(in.Status)
		if err != nil {
			return invalidField("status", err.Error())
		}
		claim, err := s.requireMove(ctx, tx, p, a, *c, to)
		if err != nil {
			return err
		}
		note := strings.TrimSpace(in.Note)
		var behalf *domain.Behalf
		if agentVerdictOn(p, *c, to) {
			if note == "" {
				return invalidField("note", "required: say what the verdict rests on (a decision, a requirements section, a person's words on a card)")
			}
			behalf = &domain.Behalf{For: p.Owner, By: p.Actor.ID, Reason: note, Session: p.Session}
		}
		if err := versionCheck(c.Version, in.Version, fmt.Sprintf("card #%d", c.Number)); err != nil {
			return err
		}
		reason := domain.DropNone
		if in.DropReason != "" {
			if reason, err = domain.ParseDropReason(in.DropReason); err != nil {
				return invalidField("drop_reason", err.Error())
			}
		}
		if to == domain.StatusDropped && c.Kind != domain.KindDecision && !ownsCard(p, *c, claim) {
			if err := a.require(p, domain.PermCardTriage); err != nil {
				return err
			}
		}
		if to == domain.StatusWorking {
			if err := notWorked(*c); err != nil {
				return err
			}
		}
		if err := s.agentOnQuestion(ctx, tx, p, *c, to); err != nil {
			return err
		}
		if c.Status.Terminal() && to == domain.StatusReady {
			if err := s.checkReopen(ctx, tx, *c); err != nil {
				return err
			}
		}
		survivor, err := s.checkTransition(ctx, tx, p, a, *c, claim, to, reason, in.Duplicate)
		if err != nil {
			return err
		}
		plan, err := s.planStory(ctx, tx, *c, to, in)
		if err != nil {
			return err
		}
		from := c.Status
		settled := c.Behalf
		now := s.now()
		switch {
		case to == domain.StatusWorking:
			if _, _, err = s.claimFor(ctx, tx, p, a, proj, *c, 0); err != nil {
				return err
			}
			err = c.Start(now)
		case c.Kind == domain.KindDecision && to == domain.StatusDone:
			err = c.Accept(p.Actor, now)
		default:
			err = c.Transition(to, reason, now)
		}
		if err != nil {
			return err
		}
		if plan.cascade {
			if err := s.dropChildren(ctx, tx, p, *c, plan.open, reason); err != nil {
				return err
			}
		}
		if plan.folds {
			if err := c.FoldInto(plan.into, p.Actor, now); err != nil {
				return foldRefused(err)
			}
		}
		if to.Terminal() {
			c.ClosingNote = note
		}
		if behalf != nil {
			c.Behalf = behalf
		}
		updated, err := updateCard(ctx, tx, *c, in.Version)
		if err != nil {
			return err
		}
		*c = updated
		if plan.close {
			if err := s.foldChildren(ctx, tx, plan.children, *updated.Fold); err != nil {
				return err
			}
		}
		if to != domain.StatusWorking && claim != nil {
			if err := releaseClaim(ctx, tx, *c); err != nil {
				return err
			}
		}
		if survivor != nil {
			if err := s.addLink(ctx, tx, p, updated, *survivor, domain.LinkDuplicates); err != nil {
				return err
			}
		}
		if to.Terminal() {
			if err := s.unblockDependents(ctx, tx, p, updated); err != nil {
				return err
			}
		}
		var extra []any
		if reason != domain.DropNone {
			extra = append(extra, "drop_reason", reason.String())
		}
		if survivor != nil {
			extra = append(extra, "duplicate_of", survivor.Number)
		}
		if note != "" {
			extra = append(extra, "note", note)
		}
		if behalf != nil {
			extra = append(extra, "for", toActor(domain.ActorRef{Kind: domain.ActorUser, ID: behalf.For}))
		}
		if settled != nil && settled.KeptAt == nil && !p.IsAgent() {
			extra = append(extra, settledBy(*settled)...)
		}
		if err := s.emitStatusChanged(ctx, tx, p, updated, from, extra...); err != nil {
			return err
		}
		if plan.folds {
			if err := s.emitCardFolded(ctx, tx, p, proj, updated, plan.into); err != nil {
				return err
			}
		}
		if updated.Kind == domain.KindDecision && to == domain.StatusDone {
			if err := s.supersedeOld(ctx, tx, p, updated); err != nil {
				return err
			}
		}
		if err := s.storyFollows(ctx, tx, p, updated); err != nil {
			return err
		}
		return s.markSeen(ctx, tx, p, updated)
	})
}

// checkReopen refuses to reopen a card of a closed story, and a closed story
// itself: closed is closed.
func (s *Service) checkReopen(ctx context.Context, tx Store, c domain.Card) error {
	if c.Kind == domain.KindStory {
		return withHint(fmt.Errorf("%w: story #%d is %s and cannot be reopened", domain.ErrInvalidTransition, c.Number, c.Status),
			"closed is closed: start a new story and link it with relates")
	}
	if c.ParentID == nil {
		return nil
	}
	story, err := tx.GetCard(ctx, *c.ParentID)
	if err != nil {
		return fmt.Errorf("load story of #%d: %w", c.Number, err)
	}
	if story.Status.Terminal() {
		return conflict(fmt.Sprintf("story #%d is closed, so #%d stays %s", story.Number, c.Number, c.Status),
			"closed is closed: start a new story and link it with relates")
	}
	return nil
}

// storyPlan is what a story's move needs beyond the status change itself.
type storyPlan struct {
	close, cascade bool
	folds          bool
	into           string
	children, open []domain.Card
}

// planStory checks the request-level rules of a story closing or dropping,
// and refuses cascade on any other move. A fold is taken from a story closing
// and from a card without a parent moving to done; any other move refuses it.
func (s *Service) planStory(ctx context.Context, tx Store, c domain.Card, to domain.Status, in apitypes.TransitionCard) (storyPlan, error) {
	var plan storyPlan
	closing := c.Kind == domain.KindStory && to == domain.StatusDone && domain.CanTransition(c.Kind, c.Status, to)
	dropping := c.Kind == domain.KindStory && to == domain.StatusDropped && domain.CanTransition(c.Kind, c.Status, to)
	if in.Fold != nil && !closing {
		into, err := standaloneFold(c, to, *in.Fold)
		if err != nil {
			return plan, err
		}
		plan.folds, plan.into = true, into
	}
	if in.Cascade && !dropping {
		return plan, invalidField("cascade", "only dropping a story with open cards cascades")
	}
	if !closing && !dropping {
		return plan, nil
	}
	children, err := tx.ListChildren(ctx, c.ID)
	if err != nil {
		return plan, fmt.Errorf("list children of #%d: %w", c.Number, err)
	}
	plan.children, plan.open = children, openChildren(children)
	if closing {
		plan.into, err = closeStory(c, children, in)
		plan.close = err == nil
		plan.folds = plan.close
		return plan, err
	}
	if len(plan.open) > 0 {
		if !in.Cascade {
			return plan, conflict(fmt.Sprintf("story #%d has open cards: %s", c.Number, numbers(plan.open)),
				"drop them first, or drop the story with cascade to drop them with it")
		}
		plan.cascade = true
	}
	return plan, nil
}

// standaloneFold returns the fold path of a non-story card moving to done.
// A card that CanFold would refuse once done, such as one with a parent, is
// refused the fold here too.
func standaloneFold(c domain.Card, to domain.Status, f apitypes.Fold) (string, error) {
	if c.Kind == domain.KindStory || to != domain.StatusDone || !domain.CanTransition(c.Kind, c.Status, to) {
		return "", invalidField("fold", "a fold is taken only when a card moves to done")
	}
	done := c
	done.Status = domain.StatusDone
	if err := done.CanFold(); err != nil {
		return "", withHint(invalidField("fold", err.Error()), foldAdvice(err))
	}
	return foldOf("fold", f.Into, f.None)
}

// FoldCard folds a done card that has no parent into a document, or into
// nothing when no durable change was made.
func (s *Service) FoldCard(ctx context.Context, slug string, number int64, in apitypes.FoldCard) (apitypes.Card, error) {
	return s.cardMutation(ctx, slug, number, func(tx Store, p Principal, proj domain.Project, a access, c *domain.Card) error {
		if _, err := s.requireCardEdit(ctx, tx, p, a, *c, false); err != nil {
			return err
		}
		if err := versionCheck(c.Version, in.Version, fmt.Sprintf("card #%d", c.Number)); err != nil {
			return err
		}
		into, err := foldOf("into", in.Into, in.None)
		if err != nil {
			return err
		}
		if err := c.FoldInto(into, p.Actor, s.now()); err != nil {
			return foldRefused(err)
		}
		updated, err := updateCard(ctx, tx, *c, in.Version)
		if err != nil {
			return err
		}
		*c = updated
		if err := s.emitCardFolded(ctx, tx, p, proj, updated, into); err != nil {
			return err
		}
		return s.markSeen(ctx, tx, p, updated)
	})
}

// checkTransition validates the request-level rules the domain cannot see:
// reason and duplicate pairing, claim ownership, and the surviving card.
func (s *Service) checkTransition(ctx context.Context, tx Store, p Principal, a access, c domain.Card, claim *domain.Claim, to domain.Status, reason domain.DropReason, dup *int64) (*domain.Card, error) {
	switch {
	case reason != domain.DropNone && to != domain.StatusDropped:
		return nil, invalidField("drop_reason", "only allowed when status is dropped")
	case to == domain.StatusDropped && reason == domain.DropDuplicate && dup == nil:
		return nil, invalidField("duplicate", "required when drop_reason is duplicate: give the surviving card number")
	case dup != nil && reason != domain.DropDuplicate:
		return nil, invalidField("duplicate", "only allowed when drop_reason is duplicate")
	}
	if claim != nil && claim.HeldAgainst(p.Actor, p.Session, s.now()) && to != domain.StatusWorking {
		if !a.mayOverride(p) {
			return nil, withHint(fmt.Errorf("%w: %s holds #%d", domain.ErrClaimedByOther, holder(*claim), c.Number),
				"wait for the claimant, or ask a reviewer")
		}
	}
	if dup == nil {
		return nil, nil
	}
	survivor, err := refCard(ctx, tx, c.ProjectID, *dup, "duplicate")
	if err != nil {
		return nil, err
	}
	if survivor.ID == c.ID {
		return nil, invalidField("duplicate", "a card cannot duplicate itself")
	}
	return &survivor, nil
}

// unblockDependents moves waiting cards that have no open blockers left back
// to ready, or to backlog as unblockIfClear says. The blocker counts as
// answered when it completed: a question closed with an answer, or any other
// card done. A dropped blocker, or a question closed unanswered, did not.
func (s *Service) unblockDependents(ctx context.Context, tx Store, p Principal, blocker domain.Card) error {
	answered := blocker.Status == domain.StatusDone && (blocker.Kind != domain.KindQuestion || blocker.Question != nil && blocker.Question.AnsweredBy != nil)
	links, err := tx.ListCardLinks(ctx, blocker.ID)
	if err != nil {
		return fmt.Errorf("list links of #%d: %w", blocker.Number, err)
	}
	for _, l := range links {
		if l.Kind != domain.LinkBlocks || l.FromCardID != blocker.ID {
			continue
		}
		if err := s.unblockIfClear(ctx, tx, p, l.ToCardID, blocker, answered); err != nil {
			return err
		}
	}
	return nil
}

// unblockIfClear moves a waiting card with no open blockers left back to
// ready. A card a question parked from backlog is readied only by an answer
// or a blocker that completed: when its last blocker is unlinked, dropped or
// a question closed unanswered, it goes back to backlog (#283).
func (s *Service) unblockIfClear(ctx context.Context, tx Store, p Principal, cardID domain.ID, blocker domain.Card, answered bool) error {
	t, err := tx.GetCard(ctx, cardID)
	if err != nil {
		return fmt.Errorf("load blocked card: %w", err)
	}
	if t.Status != domain.StatusWaiting {
		return nil
	}
	open, err := tx.ListBlockers(ctx, t.ID)
	if err != nil {
		return fmt.Errorf("list blockers of #%d: %w", t.Number, err)
	}
	if len(open) > 0 {
		return nil
	}
	back, err := s.backToBacklog(ctx, tx, t, answered)
	if err != nil {
		return err
	}
	if err := t.Transition(domain.StatusReady, domain.DropNone, s.now()); err != nil {
		return err
	}
	if back {
		if err := t.Transition(domain.StatusBacklog, domain.DropNone, s.now()); err != nil {
			return err
		}
	}
	updated, err := updateCard(ctx, tx, t, t.Version)
	if err != nil {
		return err
	}
	in, err := s.sharedProject(ctx, tx, updated, blocker)
	if err != nil {
		return err
	}
	return s.emitStatusChangedIn(ctx, tx, p, in, updated, domain.StatusWaiting, "unblocked_by", blocker.Number, "blocker_title", blocker.Title)
}

// backToBacklog reports whether a waiting card whose blockers cleared goes
// back to backlog: it was parked from backlog and nothing answered it.
func (s *Service) backToBacklog(ctx context.Context, tx Store, t domain.Card, answered bool) (bool, error) {
	if answered {
		return false, nil
	}
	from, err := tx.CardParkedFrom(ctx, t.OrgID, t.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load where #%d was parked from: %w", t.Number, err)
	}
	return from == domain.StatusBacklog, nil
}

// Answer records an answer to a question. With decide it also records the
// answer as an accepted decision card in the same project, linked to the
// question, then unblocks what the question held.
//
// An agent with the answer right answers like a person with it. Any other
// agent may only relay the answer a human gave elsewhere: the question closes
// with it and the note is posted as the agent's comment. A user can answer
// again to record their own answer over the relayed one.
func (s *Service) Answer(ctx context.Context, slug string, number int64, in apitypes.AnswerCard) (apitypes.Card, string, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Card{}, "", err
	}
	var out domain.Card
	var loc string
	err = s.store.WithTx(ctx, func(tx Store) error {
		proj, a, c, lerr := s.loadCard(ctx, tx, p, slug, number)
		if lerr != nil {
			return lerr
		}
		note := strings.TrimSpace(in.Note)
		entitled := p.IsAgent() && !in.Relayed && a.can(domain.PermQuestionAnswer)
		switch {
		case p.IsAgent() && in.Decide:
			return invalidField("decide", "a decision needs a human: ask them to answer on the dashboard")
		case p.IsAgent() && !entitled:
			if err := p.requireWritable(); err != nil {
				return err
			}
			if !in.Relayed {
				return a.answerRefusal(p, c.Number)
			}
			if _, err := s.requireCardEdit(ctx, tx, p, a, c, false); err != nil {
				return err
			}
		default:
			if err := a.require(p, domain.PermQuestionAnswer); err != nil {
				return err
			}
			if in.Decide {
				if err := a.require(p, domain.PermDecisionAccept); err != nil {
					return err
				}
			}
			if in.Relayed {
				return invalidField("relayed", "relaying is for agents passing on a human's answer")
			}
			if !entitled && note != "" {
				return invalidField("note", "only an agent gives a note")
			}
			if entitled {
				if err := s.refuseOwnQuestion(ctx, tx, p, c); err != nil {
					return err
				}
				if note == "" {
					return invalidField("note", "required: say what the answer rests on (a decision, a requirements section, a person's words on a card)")
				}
			}
		}
		if in.Relayed && note == "" {
			return invalidField("note", "required with relayed: say where and when the human gave the answer")
		}
		if verr := versionCheck(c.Version, in.Version, fmt.Sprintf("card #%d", c.Number)); verr != nil {
			return verr
		}
		var behalf *domain.Behalf
		if entitled {
			behalf = &domain.Behalf{For: p.Owner, By: p.Actor.ID, Reason: note, Session: p.Session}
		}
		settled := c.Behalf
		re, aerr := c.Answer(in.Answer, p.Actor, in.Relayed, behalf, s.now())
		if errors.Is(aerr, domain.ErrForbidden) {
			return withHint(aerr, relayAdvice(c.Number))
		}
		if aerr != nil {
			return aerr
		}
		q, uerr := updateCard(ctx, tx, c, in.Version)
		if uerr != nil {
			return uerr
		}
		out = q
		if err := releaseClaim(ctx, tx, q); err != nil {
			return err
		}
		extra := []any{"answer", in.Answer, "decided", in.Decide}
		if in.Relayed {
			extra = append(extra, "relayed", true)
		}
		if re.Confirmed {
			extra = append(extra, "confirmed", true)
		}
		if re.Previous != "" {
			extra = append(extra, "previous", re.Previous)
		}
		if re.Confirmed && settled != nil {
			extra = append(extra, settledBy(*settled)...)
		}
		if behalf != nil {
			extra = append(extra, "for", toActor(domain.ActorRef{Kind: domain.ActorUser, ID: behalf.For}), "reason", behalf.Reason)
		}
		if eerr := s.emitP(ctx, tx, p, &proj.ID, domain.EventCardAnswered, cardSubject(q), payload(q.Title, extra...)); eerr != nil {
			return eerr
		}
		if in.Relayed {
			if _, cerr := s.addComment(ctx, tx, p, proj, q, note, s.ids.New()); cerr != nil {
				return cerr
			}
		}
		if in.Decide {
			d, derr := s.decideFromAnswer(ctx, tx, p, proj, q)
			if derr != nil {
				return derr
			}
			loc = fmt.Sprintf("%s/projects/%s/cards/%d", apiPrefix, proj.Slug, d.Number)
		}
		if err := s.unblockDependents(ctx, tx, p, q); err != nil {
			return err
		}
		return s.markSeen(ctx, tx, p, q)
	})
	if err != nil {
		return apitypes.Card{}, "", err
	}
	detail, err := s.cardDetail(ctx, s.store, p, out)
	return detail, loc, err
}

// Keep records that the caller has looked at what an agent settled for them
// on the card and lets it stand. Only that person can; nothing else about
// the card changes.
func (s *Service) Keep(ctx context.Context, slug string, number, version int64) (apitypes.Card, error) {
	return s.cardMutation(ctx, slug, number, func(tx Store, p Principal, proj domain.Project, _ access, c *domain.Card) error {
		if err := p.requireWritable(); err != nil {
			return err
		}
		if err := versionCheck(c.Version, version, fmt.Sprintf("card #%d", c.Number)); err != nil {
			return err
		}
		if err := c.Keep(p.Actor, s.now()); err != nil {
			return err
		}
		updated, err := updateCard(ctx, tx, *c, version)
		if err != nil {
			return err
		}
		*c = updated
		return s.emitP(ctx, tx, p, &proj.ID, domain.EventCardKept, cardSubject(*c), payload(c.Title, append([]any{"kind", c.Kind.String()}, settledBy(*c.Behalf)...)...))
	})
}

// settledBy is the part of an event's payload that names the agent and the
// chat whose settling a person kept or replaced: that chat is told.
func settledBy(b domain.Behalf) []any {
	return []any{"settled_by", toActor(domain.ActorRef{Kind: domain.ActorAgent, ID: b.By}), "settled_by_session", b.Session}
}

// ForPerson marks an open question as one only a person answers, or with
// Clear takes the mark off. The asker, an agent holding the answer right and
// a person who may answer questions can mark it; an agent says why, and that
// note is posted as its comment. Only a person who may answer clears it.
func (s *Service) ForPerson(ctx context.Context, slug string, number int64, in apitypes.MarkForPerson) (apitypes.Card, error) {
	return s.cardMutation(ctx, slug, number, func(tx Store, p Principal, proj domain.Project, a access, c *domain.Card) error {
		if err := p.requireWritable(); err != nil {
			return err
		}
		note := strings.TrimSpace(in.Note)
		switch {
		case in.Clear:
			if err := requireHuman(p); err != nil {
				return err
			}
			if err := a.require(p, domain.PermQuestionAnswer); err != nil {
				return err
			}
			if note != "" {
				return invalidField("note", "a note goes with marking, not with clearing")
			}
		default:
			if c.CreatedBy != p.Actor {
				if err := a.require(p, domain.PermQuestionAnswer); err != nil {
					return err
				}
			}
			if p.IsAgent() && note == "" {
				return invalidField("note", "required: say why a person must answer this")
			}
		}
		if err := versionCheck(c.Version, in.Version, fmt.Sprintf("card #%d", c.Number)); err != nil {
			return err
		}
		var err error
		if in.Clear {
			err = c.ClearForPerson(p.Actor, s.now())
		} else {
			err = c.MarkForPerson(s.now())
		}
		if err != nil {
			return err
		}
		updated, err := updateCard(ctx, tx, *c, in.Version)
		if err != nil {
			return err
		}
		*c = updated
		if err := s.emitP(ctx, tx, p, &proj.ID, domain.EventCardForPerson, cardSubject(*c), payload(c.Title, "set", !in.Clear)); err != nil {
			return err
		}
		if note != "" {
			if _, err := s.addComment(ctx, tx, p, proj, *c, note, s.ids.New()); err != nil {
				return err
			}
		}
		return s.markSeen(ctx, tx, p, *c)
	})
}

// refuseOwnQuestion stops a chat answering a question that it or one of its
// subagents asked: another chat or a person settles it. Labels are the
// agent's own claim, so this catches mistakes, not a chat that lies.
func (s *Service) refuseOwnQuestion(ctx context.Context, tx Store, p Principal, q domain.Card) error {
	own, err := askedByChat(ctx, tx, p, q)
	if err != nil || !own {
		return err
	}
	chat := "a chat without a session label"
	if base := domain.BaseSession(p.Session); base != "" {
		chat = "chat " + base
	}
	return withHint(fmt.Errorf("%w: %s asked #%d, so it cannot answer it", domain.ErrForbidden, chat, q.Number),
		"another chat with the answer right or a person answers it; or "+relayAdvice(q.Number))
}

// askedByChat reports whether the calling chat or one of its subagents asked
// q: the same actor under the same base label. A card of the caller whose
// creation event is missing counts as its own, so the unknown case is left to
// another chat or a person.
func askedByChat(ctx context.Context, st Store, p Principal, q domain.Card) (bool, error) {
	if q.CreatedBy != p.Actor {
		return false, nil
	}
	asked, err := st.CardCreatedSession(ctx, p.OrgID, q.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("session that asked #%d: %w", q.Number, err)
	}
	return domain.BaseSession(asked) == domain.BaseSession(p.Session), nil
}

// answerRefusal is the 403 for an agent that answers without the answer right
// and without relaying: it names the right and the way an agent without it
// passes on a human's answer.
func (a access) answerRefusal(p Principal, number int64) error {
	return p.lacksRight(domain.RightAnswer, a.slug, relayAdvice(number))
}

// relayAdvice is the advice to an agent that tried to close or answer a
// question without a human's word.
func relayAdvice(number int64) string {
	return fmt.Sprintf(`relay the human's answer: lorvik answer %d "<answer>" --relayed -m "<where they said it>"`, number)
}

// decideFromAnswer records the accepted decision card for an answered
// question: its body is the question and the answer, it relates to the
// question, and it joins the question's story while that story is open.
func (s *Service) decideFromAnswer(ctx context.Context, tx Store, p Principal, proj domain.Project, q domain.Card) (domain.Card, error) {
	now := s.now()
	page, err := currentCardPage(ctx, tx, q)
	if err != nil {
		return domain.Card{}, err
	}
	ctxText := q.Body
	if strings.TrimSpace(ctxText) == "" && page != "" {
		ctxText = "See the page."
	}
	d := domain.Card{ID: s.ids.New(), OrgID: p.OrgID, ProjectID: proj.ID, Kind: domain.KindDecision, Status: domain.StatusDone,
		Priority: domain.PriorityNormal, Title: q.Title, CreatedBy: p.Actor, ClosedAt: &now, CreatedAt: now, UpdatedAt: now,
		Body:       fmt.Sprintf("## Context\n\n%s\n\n## Decision\n\n%s\n", ctxText, q.Question.Answer),
		PageBytes:  len(page),
		Acceptance: &domain.Acceptance{By: p.Actor, At: now}}
	if q.ParentID != nil {
		story, err := tx.GetCard(ctx, *q.ParentID)
		if err != nil {
			return domain.Card{}, fmt.Errorf("load story of #%d: %w", q.Number, err)
		}
		if story.Status.Open() {
			d.ParentID = &story.ID
		}
	}
	if err := d.Validate(); err != nil {
		return domain.Card{}, err
	}
	created, err := tx.CreateCard(ctx, d)
	if err != nil {
		return domain.Card{}, fmt.Errorf("create decision: %w", err)
	}
	if page != "" {
		if err := saveCardPage(ctx, tx, created.ID, page); err != nil {
			return domain.Card{}, err
		}
	}
	if err := s.syncRefs(ctx, tx, p.OrgID, &created.ProjectID, domain.SubjectCard, created.ID, created.Body); err != nil {
		return domain.Card{}, err
	}
	if err := s.emitP(ctx, tx, p, &proj.ID, domain.EventCardCreated, cardSubject(created),
		payload(created.Title, "kind", created.Kind.String(), "status", created.Status.String(), "priority", created.Priority.String(), "from_question", q.Number)); err != nil {
		return domain.Card{}, err
	}
	if err := s.addLink(ctx, tx, p, created, q, domain.LinkRelates); err != nil {
		return domain.Card{}, err
	}
	err = s.emitP(ctx, tx, p, &proj.ID, domain.EventCardLinked, cardSubject(created),
		payload(created.Title, "kind", domain.LinkRelates.String(), "to", q.Number, "to_title", q.Title))
	return created, err
}

// crossLinkable are the link kinds allowed between the global project and
// another project.
var crossLinkable = []domain.CardLinkKind{domain.LinkBlocks, domain.LinkRelates, domain.LinkApplies}

// linkTarget resolves the card a link points at: in the named project (the
// caller must see it) or in the source's own. Between two projects one must
// be the global project.
func (s *Service) linkTarget(ctx context.Context, tx Store, p Principal, proj domain.Project, kind domain.CardLinkKind, slug string, number int64) (domain.Card, domain.Project, access, error) {
	target, ta := proj, access{}
	if slug != "" && slug != proj.Slug {
		var err error
		target, ta, err = s.project(ctx, tx, p, slug)
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Card{}, domain.Project{}, access{}, invalidField("project", fmt.Sprintf("no project %q you can see", slug))
		}
		if err != nil {
			return domain.Card{}, domain.Project{}, access{}, err
		}
		if proj.Scope != domain.ProjectScopeGlobal && target.Scope != domain.ProjectScopeGlobal {
			return domain.Card{}, domain.Project{}, access{}, invalidField("project", "cards link across projects only through the global project")
		}
		if !slices.Contains(crossLinkable, kind) {
			return domain.Card{}, domain.Project{}, access{}, invalidField("kind", "between projects, cards link as blocks, relates or applies")
		}
	}
	to, err := refCard(ctx, tx, target.ID, number, "to")
	return to, target, ta, err
}

// LinkCards adds a link from this card to another, in the same project or
// across the global project.
func (s *Service) LinkCards(ctx context.Context, slug string, number int64, in apitypes.LinkCards) (apitypes.Card, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Card{}, err
	}
	var out domain.Card
	err = s.store.WithTx(ctx, func(tx Store) error {
		proj, a, c, lerr := s.loadCard(ctx, tx, p, slug, number)
		if lerr != nil {
			return lerr
		}
		if err := a.require(p, domain.PermCardWrite); err != nil {
			return err
		}
		out = c
		kind, err := domain.ParseCardLinkKind(in.Kind)
		if err != nil {
			return invalidField("kind", err.Error())
		}
		switch {
		case kind == domain.LinkSupersedes:
			return invalidField("kind", "a supersedes link is made by recording a decision with supersedes")
		case kind == domain.LinkApplies && c.Kind != domain.KindDecision:
			return invalidField("kind", "only a decision applies to other cards")
		}
		to, target, ta, rerr := s.linkTarget(ctx, tx, p, proj, kind, in.Project, in.To)
		if rerr != nil {
			return rerr
		}
		if target.ID == proj.ID {
			ta = a
		} else if kind == domain.LinkBlocks {
			if err := ta.require(p, domain.PermCardWrite); err != nil {
				return err
			}
		}
		if kind == domain.LinkBlocks && !c.Status.Terminal() {
			if berr := s.blockCard(ctx, tx, p, ta, c, to); berr != nil {
				return berr
			}
		} else {
			if aerr := s.addLink(ctx, tx, p, c, to, kind); aerr != nil {
				return aerr
			}
		}
		pl := payload(c.Title, "kind", kind.String(), "to", to.Number, "to_title", to.Title)
		if target.ID != proj.ID {
			pl["to_project"] = target.Slug
		}
		in, err := s.sharedProject(ctx, tx, c, to)
		if err != nil {
			return err
		}
		return s.emitP(ctx, tx, p, &in, domain.EventCardLinked, cardSubject(c), pl)
	})
	if err != nil {
		return apitypes.Card{}, err
	}
	fresh, err := s.store.GetCard(ctx, out.ID)
	if err != nil {
		return apitypes.Card{}, fmt.Errorf("reload card: %w", err)
	}
	return s.cardDetail(ctx, s.store, p, fresh)
}

// UnlinkCards removes a link and releases a target that has no blockers left.
// project names the target's project when it is not the card's.
func (s *Service) UnlinkCards(ctx context.Context, slug string, number int64, kind string, to int64, project string) error {
	p, err := s.principal(ctx)
	if err != nil {
		return err
	}
	return s.store.WithTx(ctx, func(tx Store) error {
		proj, a, c, lerr := s.loadCard(ctx, tx, p, slug, number)
		if lerr != nil {
			return lerr
		}
		if err := a.require(p, domain.PermCardWrite); err != nil {
			return err
		}
		k, err := domain.ParseCardLinkKind(kind)
		if err != nil {
			return invalidField("kind", err.Error())
		}
		if k == domain.LinkSupersedes {
			return invalidField("kind", "a supersedes link is part of the decision's history and is not removed")
		}
		target, _, _, rerr := s.linkTarget(ctx, tx, p, proj, k, project, to)
		if rerr != nil {
			return rerr
		}
		if err := tx.RemoveCardLink(ctx, c.ID, target.ID, k); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return fmt.Errorf("no %s link from #%d to #%d: %w", kind, c.Number, to, err)
			}
			return fmt.Errorf("remove link: %w", err)
		}
		if k == domain.LinkBlocks {
			if err := s.unblockIfClear(ctx, tx, p, target.ID, c, false); err != nil {
				return err
			}
		}
		in, err := s.sharedProject(ctx, tx, c, target)
		if err != nil {
			return err
		}
		return s.emitP(ctx, tx, p, &in, domain.EventCardUnlinked, cardSubject(c),
			payload(c.Title, "kind", kind, "to", to, "to_title", target.Title))
	})
}
