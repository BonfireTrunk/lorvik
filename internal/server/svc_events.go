package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// streamPoll bounds how long a stream waits without a wake-up before it
// checks the log anyway.
const streamPoll = 15 * time.Second

// projectScope is the project filter for the caller's events, notes and
// search: one project, or every project they may see (nil only for admins,
// who see everything). Readers pass it to the store so that paging, ranking
// and counts never see a project the caller cannot.
func (s *Service) projectScope(ctx context.Context, p Principal, projectSlug string) ([]domain.ID, error) {
	if projectSlug != "" {
		proj, _, err := s.project(ctx, s.store, p, projectSlug)
		if err != nil {
			return nil, err
		}
		return []domain.ID{proj.ID}, nil
	}
	if p.HasOrgRole && p.OrgRole.SeesEveryProject() {
		return nil, nil
	}
	ps, _, err := s.visibleProjects(ctx, s.store, p)
	if err != nil {
		return nil, err
	}
	ids := make([]domain.ID, 0, len(ps))
	for _, pr := range ps {
		ids = append(ids, pr.ID)
	}
	return ids, nil
}

// unread counts events by others after the cursor, up to the cap.
func (s *Service) unread(ctx context.Context, p Principal, after query.EventPosition) (int, error) {
	ids, err := s.projectScope(ctx, p, "")
	if err != nil {
		return 0, err
	}
	actor := p.Actor
	return s.store.CountEventsAfter(ctx, p.OrgID, query.EventFilter{ProjectIDs: ids, After: after, ExcludeActor: &actor, ExcludeSession: p.Session, Limited: p.limitedReach(), Limit: apitypes.UnreadCap + 1})
}

// ListEvents returns events after a position, oldest first. With before it
// returns the newest events ahead of that position instead, still oldest
// first.
func (s *Service) ListEvents(ctx context.Context, projectSlug, after, before string, limit int) (apitypes.EventList, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.EventList{}, err
	}
	a, err := parseEventCursor("after", after)
	if err != nil {
		return apitypes.EventList{}, err
	}
	b, err := parseEventCursor("before", before)
	if err != nil {
		return apitypes.EventList{}, err
	}
	if !a.IsZero() && !b.IsZero() {
		return apitypes.EventList{}, newProblem(http.StatusBadRequest, "after and before cannot be combined", "pass either after (newer events) or before (older events)")
	}
	ids, err := s.projectScope(ctx, p, projectSlug)
	if err != nil {
		return apitypes.EventList{}, err
	}
	evs, remaining, err := s.store.ListEvents(ctx, p.OrgID, query.EventFilter{ProjectIDs: ids, After: a, Before: b, Limit: limit, Limited: p.limitedReach()})
	if errors.Is(err, domain.ErrNotFound) {
		field := "after"
		if !b.IsZero() {
			field = "before"
		}
		return apitypes.EventList{}, unknownCursor(field)
	}
	if err != nil {
		return apitypes.EventList{}, fmt.Errorf("list events: %w", err)
	}
	items, err := s.lookup(s.store, p).events(ctx, evs)
	if err != nil {
		return apitypes.EventList{}, err
	}
	out := apitypes.EventList{Items: items, Remaining: remaining}
	if len(evs) > 0 {
		out.Next = eventPosition(positionOf(evs[len(evs)-1]))
		out.Prev = eventPosition(positionOf(evs[0]))
	}
	return out, nil
}

// Inbox returns what others did after the cursor the caller sent, the
// caller's claims and the open question count. The cursor to store next is
// the newest event examined, including the caller's own. chatSlug names the
// project the calling chat works in and only narrows which events wake it.
func (s *Service) Inbox(ctx context.Context, projectSlug, chatSlug, cursor string, limit int) (apitypes.Inbox, error) {
	p, err := s.principal(ctx)
	if err != nil {
		return apitypes.Inbox{}, err
	}
	after, err := parseEventCursor("cursor", cursor)
	if err != nil {
		return apitypes.Inbox{}, err
	}
	ids, err := s.projectScope(ctx, p, projectSlug)
	if err != nil {
		return apitypes.Inbox{}, err
	}
	actor := p.Actor
	// The page and the latest id come from one snapshot: an event that became
	// visible between two separate reads would sit behind the cursor unread.
	var (
		evs       []domain.Event
		remaining int
		latest    query.EventPosition
	)
	err = s.store.WithSnapshot(ctx, func(tx Store) error {
		var lerr error
		evs, remaining, lerr = tx.ListEvents(ctx, p.OrgID, query.EventFilter{ProjectIDs: ids, After: after, Limit: limit, ExcludeActor: &actor, ExcludeSession: p.Session, Limited: p.limitedReach()})
		if errors.Is(lerr, domain.ErrNotFound) {
			return unknownCursor("cursor")
		}
		if lerr != nil {
			return fmt.Errorf("list inbox events: %w", lerr)
		}
		if remaining == 0 && projectSlug == "" {
			if latest, lerr = tx.LatestEventPosition(ctx, p.OrgID, query.EventFilter{ProjectIDs: ids, Limited: p.limitedReach()}); lerr != nil {
				return fmt.Errorf("latest event: %w", lerr)
			}
		}
		return nil
	})
	if err != nil {
		return apitypes.Inbox{}, err
	}
	lk := s.lookup(s.store, p)
	out := apitypes.Inbox{Remaining: remaining}
	if !after.IsZero() {
		out.Cursor = cursorString(after)
	}
	if out.Events, err = lk.events(ctx, evs); err != nil {
		return apitypes.Inbox{}, err
	}
	switch {
	case len(evs) > 0 && (remaining > 0 || projectSlug != ""):
		// More to read, or a single project: the cursor stops at the last
		// event returned, so events of other projects are never skipped.
		out.Cursor = eventPosition(positionOf(evs[len(evs)-1]))
	case projectSlug != "":
		// Nothing new in this project; the cursor stays where it was.
	case !latest.IsZero() && after.Compare(latest) < 0:
		// Everything was read; skip past the caller's own events too. In feed
		// order the latest visible event is at or past every event returned.
		// A cursor already past it stays where it is: it names an event the
		// caller got before losing the reach to it, and a cursor never moves
		// back. A bare event id sorts first and so becomes a position here.
		out.Cursor = eventPosition(latest)
	}
	claims, err := s.store.ListClaimsByActor(ctx, p.OrgID, p.Actor)
	if err != nil {
		return apitypes.Inbox{}, fmt.Errorf("list claims: %w", err)
	}
	now := s.now()
	claims = slices.DeleteFunc(claims, func(cl domain.Claim) bool { return cl.Expired(now) })
	scope, err := s.chatScope(ctx, p, chatSlug)
	if err != nil {
		return apitypes.Inbox{}, err
	}
	if err := s.markMineWake(ctx, p, evs, claims, scope, out.Events); err != nil {
		return apitypes.Inbox{}, err
	}
	if out.Claims, out.ClaimsOtherSessions, err = s.claimBriefs(ctx, lk, p, claims); err != nil {
		return apitypes.Inbox{}, err
	}
	if out.OpenQuestions, err = s.openQuestions(ctx, p); err != nil {
		return apitypes.Inbox{}, err
	}
	if out.Locks, err = s.liveLocks(ctx, p); err != nil {
		return apitypes.Inbox{}, err
	}
	return out, nil
}

// claimBriefs loads the cards of the caller's claims and splits them by
// Claim.OwnedBy: the calling chat's own, and the same agent's claims under
// other sessions, which this chat neither holds nor renewed. A human owns
// its claims in every session, so the second list is empty for humans.
func (s *Service) claimBriefs(ctx context.Context, lk *lookup, p Principal, claims []domain.Claim) (mine, other []apitypes.CardBrief, err error) {
	mine, other = []apitypes.CardBrief{}, []apitypes.CardBrief{}
	if len(claims) == 0 {
		return mine, other, nil
	}
	cardIDs := make([]domain.ID, 0, len(claims))
	owned := make(map[domain.ID]bool, len(claims))
	for _, cl := range claims {
		cardIDs = append(cardIDs, cl.CardID)
		owned[cl.CardID] = cl.OwnedBy(p.Actor, p.Session)
	}
	cards, err := s.store.GetCards(ctx, cardIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("load claimed cards: %w", err)
	}
	briefs, err := lk.briefs(ctx, cards)
	if err != nil {
		return nil, nil, err
	}
	for i, c := range cards {
		if owned[c.ID] {
			mine = append(mine, briefs[i])
		} else {
			other = append(other, briefs[i])
		}
	}
	return mine, other, nil
}

// chatScope is the project a chat named as the one it works in: nil when it
// named none. A slug that names no project of the org is not an error, since
// a chat may listen before its project exists or before its agent has joined
// it; it gives an id no card has, so no assignment wakes the chat.
func (s *Service) chatScope(ctx context.Context, p Principal, slug string) (*domain.ID, error) {
	if slug == "" {
		return nil, nil
	}
	proj, err := s.store.GetProjectBySlug(ctx, p.OrgID, slug)
	if errors.Is(err, domain.ErrNotFound) {
		return new(domain.ID), nil
	}
	if err != nil {
		return nil, fmt.Errorf("project %q: %w", slug, err)
	}
	return &proj.ID, nil
}

// markMineWake sets Mine and Wake on the inbox events whose subject is a card
// the caller holds, is assigned to or created. Mine covers every claim of the
// caller; Wake narrows it to the calling chat: the card is held by this chat,
// was created by it, or is assigned to the caller and no other chat's. A
// human is one chat in every session, so for a human Wake is Mine; an agent's
// chats share one token, so its claims and creations count only in the
// session that made them, and an assignment names the agent, not a chat: it
// wakes a chat only in the project the chat works in (scope, nil for any),
// and not for a card another chat of the agent holds or created, which is
// that chat's card. An event that says a claim of the caller is gone is Mine
// too and wakes the chat that held it (lostClaim). So is a person keeping or
// replacing what the caller settled for them, for the chat that settled it
// (judged). A question the chat may answer, and a proposed decision it may
// give the verdict on, wake it without being Mine: they are nobody's cards
// (wakeToSettle). A card an agent's chat is subscribed to is Mine and wakes that
// chat (subscribedByChat).
// Cards are loaded once, and only when a claim does not already answer. out
// parallels evs.
func (s *Service) markMineWake(ctx context.Context, p Principal, evs []domain.Event, claims []domain.Claim, scope *domain.ID, out []apitypes.Event) error {
	held := make(map[domain.ID]bool, len(claims))
	owned := make(map[domain.ID]bool, len(claims))
	for _, cl := range claims {
		held[cl.CardID] = true
		if cl.OwnedBy(p.Actor, p.Session) {
			owned[cl.CardID] = true
		}
	}
	var unknown []domain.ID
	seen := map[domain.ID]bool{}
	for _, e := range evs {
		if e.Subject.Type == domain.SubjectCard && !owned[e.Subject.ID] && !seen[e.Subject.ID] {
			seen[e.Subject.ID] = true
			unknown = append(unknown, e.Subject.ID)
		}
	}
	mine := maps.Clone(held)
	assigned := map[domain.ID]bool{}
	loaded := map[domain.ID]domain.Card{}
	if len(unknown) > 0 {
		cards, err := s.store.GetCards(ctx, unknown)
		if err != nil {
			return fmt.Errorf("load event cards: %w", err)
		}
		for _, c := range cards {
			loaded[c.ID] = c
			if c.Assignee != nil && *c.Assignee == p.Actor {
				mine[c.ID] = true
				// held is true here only for another chat's claim: this
				// chat's own cards never reach the loop. A card the agent
				// created is left to the created-in-session check below.
				assigned[c.ID] = !held[c.ID] && c.CreatedBy != p.Actor && (scope == nil || c.ProjectID == *scope)
			}
			if c.CreatedBy == p.Actor {
				mine[c.ID] = true
			}
		}
	}
	for i, e := range evs {
		lost, here := lostClaim(p, e)
		settled, bySelf := judged(p, e)
		granted := chatGrantChanged(p, e)
		out[i].Mine = lost || granted || settled || e.Subject.Type == domain.SubjectCard && mine[e.Subject.ID]
		out[i].Wake = here || granted || bySelf
	}
	if !p.IsAgent() {
		for i := range out {
			out[i].Wake = out[i].Mine
		}
		return nil
	}
	subscribed, err := s.subscribedByChat(ctx, p, evs)
	if err != nil {
		return err
	}
	for i, e := range evs {
		if e.Subject.Type == domain.SubjectCard && subscribed[e.Subject.ID] {
			out[i].Mine = true
		}
	}
	wake := map[domain.ID]bool{}
	var candidates []domain.ID
	seen = map[domain.ID]bool{}
	for i, e := range evs {
		id := e.Subject.ID
		switch {
		case !out[i].Mine:
		case owned[id] || assigned[id] || subscribed[id]:
			wake[id] = true
		case !seen[id]:
			seen[id] = true
			candidates = append(candidates, id)
		}
	}
	if len(candidates) > 0 {
		created, err := s.store.CardsCreatedInSession(ctx, p.OrgID, candidates, p.Actor, p.Session)
		if err != nil {
			return fmt.Errorf("cards created in session: %w", err)
		}
		for _, id := range created {
			wake[id] = true
		}
	}
	for i, e := range evs {
		out[i].Wake = out[i].Wake || out[i].Mine && wake[e.Subject.ID]
	}
	return s.wakeToSettle(ctx, p, evs, loaded, scope, out)
}

// subscribedByChat returns the cards among the subjects of evs that the calling
// chat is subscribed to; a chat's subscriptions
// are shared by its subagents.
func (s *Service) subscribedByChat(ctx context.Context, p Principal, evs []domain.Event) (map[domain.ID]bool, error) {
	if p.Session == "" {
		return nil, nil
	}
	var cards []domain.ID
	seen := map[domain.ID]bool{}
	for _, e := range evs {
		if e.Subject.Type == domain.SubjectCard && !seen[e.Subject.ID] {
			seen[e.Subject.ID] = true
			cards = append(cards, e.Subject.ID)
		}
	}
	got, err := s.store.SubscribedCardsAmong(ctx, p.OrgID, cards, p.Actor.ID, domain.BaseSession(p.Session))
	if err != nil {
		return nil, fmt.Errorf("subscribed cards: %w", err)
	}
	out := make(map[domain.ID]bool, len(got))
	for _, id := range got {
		out[id] = true
	}
	return out, nil
}

// wakeToSettle wakes an agent's chat for what it may settle for its owner.
// A question it may answer: one that was just asked, or whose for-a-person
// mark a person just took off; still open, not for a person, in a project
// where the caller holds the answer right, and not asked by this chat or its
// subagents. A decision it may give the verdict on: one that was just
// proposed, or proposed again; still a proposal, in a project where the
// caller holds the decide right, and not barred to this chat
// (verdictBarred). Both only in the project the chat works in (scope, nil for
// any). cards are the event cards by id.
func (s *Service) wakeToSettle(ctx context.Context, p Principal, evs []domain.Event, cards map[domain.ID]domain.Card, scope *domain.ID, out []apitypes.Event) error {
	standing := map[domain.ID]access{}
	for i, e := range evs {
		if out[i].Wake {
			continue
		}
		c, ok := cards[e.Subject.ID]
		if !ok || scope != nil && c.ProjectID != *scope {
			continue
		}
		question := c.Kind == domain.KindQuestion && !c.Status.Terminal() && !c.ForPerson() &&
			(e.Type == domain.EventCardCreated || e.Type == domain.EventCardForPerson)
		proposal := c.Kind == domain.KindDecision && c.Status == domain.StatusReady && proposes(e)
		if !question && !proposal {
			continue
		}
		a, known := standing[c.ProjectID]
		if !known {
			var err error
			if a, err = s.accessByID(ctx, s.store, p, c.ProjectID); err != nil {
				return err
			}
			standing[c.ProjectID] = a
		}
		if question {
			if !a.can(domain.PermQuestionAnswer) {
				continue
			}
			own, err := askedByChat(ctx, s.store, p, c)
			if err != nil {
				return err
			}
			out[i].Wake = !own
			continue
		}
		if !a.rights.Has(domain.RightDecide) {
			continue
		}
		why, err := s.verdictBarred(ctx, s.store, p, a.slug, c)
		if err != nil {
			return err
		}
		out[i].Wake = why == ""
	}
	return nil
}

// proposes reports whether e put a decision up as a proposal: its creation,
// or a rejected one proposed again.
func proposes(e domain.Event) bool {
	if e.Type == domain.EventCardCreated {
		return true
	}
	var pl struct {
		To string `json:"to"`
	}
	return e.Type == domain.EventCardStatusChanged && json.Unmarshal(e.Payload, &pl) == nil && pl.To == domain.StatusReady.String()
}

// judged reports whether e says a person kept or replaced what the caller
// settled for them, and whether the calling chat or one of its subagents was
// the one that settled it: that chat is the one to learn from it.
func judged(p Principal, e domain.Event) (settled, here bool) {
	if e.Type != domain.EventCardKept && e.Type != domain.EventCardAnswered && e.Type != domain.EventCardStatusChanged || !p.IsAgent() {
		return false, false
	}
	var pl struct {
		SettledBy        *apitypes.Actor `json:"settled_by"`
		SettledBySession string          `json:"settled_by_session"`
	}
	if json.Unmarshal(e.Payload, &pl) != nil || pl.SettledBy == nil || *pl.SettledBy != toActor(p.Actor) {
		return false, false
	}
	return true, domain.BaseSession(pl.SettledBySession) == domain.BaseSession(p.Session)
}

// chatGrantChanged reports whether e says what the calling chat was given
// changed: its owner gave it rights or took them back, or its grant reached
// its end. The chat acts on those rights, so it is the one to be told.
func chatGrantChanged(p Principal, e domain.Event) bool {
	if e.Type != domain.EventAgentSessionRights || !p.IsAgent() || p.Session == "" || e.Subject.ID != p.Actor.ID {
		return false
	}
	var pl struct {
		Session string `json:"session"`
	}
	return json.Unmarshal(e.Payload, &pl) == nil && pl.Session == domain.BaseSession(p.Session)
}

// lostClaim reports whether e tells the caller that a claim of theirs is
// gone: its lease ran out (the sweeper names the former holder as actor), or
// someone took the card over or released it. here says the claim was this
// chat's: a human's in any session, an agent's in the session the event
// names. The card is no longer the caller's by any other rule, so without
// this the one chat that must stop working it would not be told.
func lostClaim(p Principal, e domain.Event) (lost, here bool) {
	var pl struct {
		Session             string          `json:"session"`
		TakenFrom           *apitypes.Actor `json:"taken_from"`
		TakenFromSession    string          `json:"taken_from_session"`
		ReleasedFrom        *apitypes.Actor `json:"released_from"`
		ReleasedFromSession string          `json:"released_from_session"`
	}
	switch e.Type {
	case domain.EventCardClaimExpired, domain.EventCardClaimed, domain.EventCardReleased:
		if json.Unmarshal(e.Payload, &pl) != nil {
			return false, false
		}
	default:
		return false, false
	}
	me := toActor(p.Actor)
	var session string
	switch {
	case e.Type == domain.EventCardClaimExpired && e.Actor == p.Actor:
		session = pl.Session
	case e.Type == domain.EventCardClaimed && pl.TakenFrom != nil && *pl.TakenFrom == me:
		session = pl.TakenFromSession
	case e.Type == domain.EventCardReleased && pl.ReleasedFrom != nil && *pl.ReleasedFrom == me:
		session = pl.ReleasedFromSession
	default:
		return false, false
	}
	return true, !p.Actor.SessionScoped() || session == p.Session
}

// openQuestions counts unanswered questions in the caller's projects: all of
// them for users, only the ones the agent asked for agents.
func (s *Service) openQuestions(ctx context.Context, p Principal) (int, error) {
	projects, _, err := s.visibleProjects(ctx, s.store, p)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, proj := range projects {
		if !p.IsAgent() {
			counts, cerr := s.store.CountCards(ctx, proj.ID)
			if cerr != nil {
				return 0, fmt.Errorf("count cards: %w", cerr)
			}
			total += counts.OpenQuestions
			continue
		}
		n, cerr := s.store.CountOpenQuestionsBy(ctx, proj.ID, p.Actor)
		if cerr != nil {
			return 0, fmt.Errorf("count questions: %w", cerr)
		}
		total += n
	}
	return total, nil
}

// StreamStart is the position a stream opens at, with the caller and the
// project checked. An empty after is the newest event the caller may get.
// A stream answers 200 as soon as it opens, so the route resolves this
// first: a bad start becomes a problem response, and what is written once
// the client has seen the stream open comes after the start.
func (s *Service) StreamStart(ctx context.Context, projectSlug, after string) (query.EventPosition, error) {
	var none query.EventPosition
	p, err := s.principal(ctx)
	if err != nil {
		return none, err
	}
	ids, err := s.projectScope(ctx, p, projectSlug)
	if err != nil {
		return none, err
	}
	if after == "" {
		head, err := s.store.LatestEventPosition(ctx, p.OrgID, query.EventFilter{ProjectIDs: ids, Limited: p.limitedReach()})
		if err != nil {
			return none, fmt.Errorf("latest event: %w", err)
		}
		return head, nil
	}
	cursor, err := parseEventCursor("after", after)
	if err != nil {
		return none, err
	}
	if !cursor.IsZero() {
		if _, err := s.store.CountEventsAfter(ctx, p.OrgID, query.EventFilter{After: cursor, Limit: 1}); errors.Is(err, domain.ErrNotFound) {
			return none, unknownCursor("after")
		} else if err != nil {
			return none, fmt.Errorf("check cursor: %w", err)
		}
	}
	return cursor, nil
}

// StreamEvents sends every event after cursor, a position from StreamStart,
// to send until ctx ends or send fails. It wakes on hub signals and polls as
// a fallback.
func (s *Service) StreamEvents(ctx context.Context, projectSlug string, cursor query.EventPosition, send func(apitypes.Event) error, heartbeat func() error) error {
	p, err := s.principal(ctx)
	if err != nil {
		return err
	}
	p, ids, expires, ok, err := s.streamStanding(ctx, p, projectSlug)
	if err != nil || !ok {
		return err
	}
	expiry := time.NewTimer(expires.Sub(s.now()))
	defer expiry.Stop()
	wake, leave := s.hub.subscribe(p.OrgID)
	defer leave()
	lk := s.lookup(s.store, p)
	// The poll ticks on its own clock and only what was sent resets the
	// heartbeat count: a wake-up for an event this caller may not get must
	// not show in when the next heartbeat arrives.
	poll := time.NewTicker(streamPoll)
	defer poll.Stop()
	idle := 0
	for {
		evs, _, err := s.store.ListEvents(ctx, p.OrgID, query.EventFilter{ProjectIDs: ids, After: cursor, Limit: 100, Limited: p.limitedReach()})
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("poll events: %w", err)
		}
		for _, e := range evs {
			ae, err := lk.event(ctx, e)
			if err != nil {
				return err
			}
			if err := send(ae); err != nil {
				return fmt.Errorf("send event: %w", err)
			}
			cursor = positionOf(e)
			idle = 0
		}
		// A full page is drained before waiting, but never before the
		// caller is looked at again: what they may get can change mid-drain.
		if len(evs) < 100 {
			select {
			case <-ctx.Done():
				return nil
			case <-wake:
			case <-expiry.C:
			case <-poll.C:
				if idle++; idle%2 == 0 {
					if err := heartbeat(); err != nil {
						return fmt.Errorf("send heartbeat: %w", err)
					}
				}
			}
		}
		was := p
		if p, ids, _, ok, err = s.streamStanding(ctx, p, projectSlug); err != nil || !ok {
			return err
		}
		if p != was {
			lk = s.lookup(s.store, p)
		}
	}
}

// streamStanding is an open stream's caller and projects now, and when its
// token expires. ok is false once the caller has lost the stream: the token
// is revoked or expired, the user or agent or the agent's owner lost their
// standing, or the project went out of reach. Only a failure to look is an
// error.
func (s *Service) streamStanding(ctx context.Context, p Principal, projectSlug string) (Principal, []domain.ID, time.Time, bool, error) {
	tok, err := s.store.GetToken(ctx, p.TokenID)
	if errors.Is(err, domain.ErrNotFound) {
		return p, nil, time.Time{}, false, nil
	}
	if err != nil {
		return p, nil, time.Time{}, false, fmt.Errorf("load stream token: %w", err)
	}
	np, prob := s.standing(ctx, tok, p.Session, s.now())
	if prob != nil {
		if isInternal(prob) {
			return p, nil, time.Time{}, false, errors.New("check stream caller")
		}
		return p, nil, time.Time{}, false, nil
	}
	ids, err := s.projectScope(ctx, np, projectSlug)
	if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrForbidden) {
		return p, nil, time.Time{}, false, nil
	}
	if err != nil {
		return p, nil, time.Time{}, false, err
	}
	return np, ids, tok.ExpiresAt, true, nil
}

// unknownCursor is the 422 for an event cursor that names no event of the
// caller's org.
func unknownCursor(field string) error {
	return invalidField(field, "no such event; start without it, or pass a position from a previous page")
}
