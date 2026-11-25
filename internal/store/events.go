package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
	"github.com/BonfireTrunk/lorvik/internal/store/sqlcgen"
)

// notifyChannel is the LISTEN/NOTIFY channel; the payload is the org id.
const notifyChannel = "lorvik_events"

// AppendEvent stores e and queues a notification for the org, which
// Postgres delivers when the surrounding transaction commits.
func (s *Store) AppendEvent(ctx context.Context, e domain.Event) error {
	const op = "append event"
	var payload []byte
	if len(e.Payload) > 0 {
		payload = []byte(e.Payload)
	}
	err := s.q.InsertEvent(ctx, sqlcgen.InsertEventParams{
		OrgID: int64(e.OrgID), ID: int64(e.ID), ProjectID: idPtr(e.ProjectID), Type: int16(e.Type),
		ActorKind: int16(e.Actor.Kind), ActorID: int64(e.Actor.ID), ActorSession: e.Session,
		SubjectType: int16(e.Subject.Type), SubjectID: int64(e.Subject.ID), SubjectNumber: e.Subject.Number,
		Payload: payload,
	})
	if err != nil {
		return wrap(op, err)
	}
	return wrap(op+": notify", s.q.NotifyOrg(ctx, strconv.FormatInt(int64(e.OrgID), 10)))
}

type eventArgs struct {
	projectIDs []int64
	kind       *int16
	actorID    *int64
	session    *string
	// limitedTypes is nil only for a reader of every audience-limited
	// event; concernsUser is the user the others must concern.
	limitedTypes []int16
	concernsUser *int64
}

func argsOf(f query.EventFilter) eventArgs {
	var a eventArgs
	if f.ProjectIDs != nil {
		a.projectIDs = ids(f.ProjectIDs)
	}
	if f.ExcludeActor != nil {
		a.kind, a.actorID = actorCols(f.ExcludeActor)
		a.session = &f.ExcludeSession
	}
	if !f.Limited.All {
		for _, t := range domain.LimitedEventTypes() {
			a.limitedTypes = append(a.limitedTypes, int16(t))
		}
		if f.Limited.User != 0 {
			u := int64(f.Limited.User)
			a.concernsUser = &u
		}
	}
	return a
}

// eventXid returns the xid of position p as text: "0" for the start, the
// largest xid8 for the largest id (which clients send as before= to mean the
// newest page), and otherwise the xid of the event p names. A position must
// name an event of the org with that xid, so a cursor saved against another
// database is domain.ErrNotFound rather than a bound that skips events.
func (s *Store) eventXid(ctx context.Context, orgID domain.ID, p query.EventPosition) (string, error) {
	switch {
	case p.ID == 0:
		return "0", nil
	case p.ID == math.MaxInt64 && p.Xid == 0:
		return "18446744073709551615", nil
	}
	x, err := s.q.EventXid(ctx, sqlcgen.EventXidParams{OrgID: int64(orgID), ID: int64(p.ID)})
	if err != nil {
		return "", wrap("event position", err)
	}
	if p.Xid != 0 && x != strconv.FormatUint(p.Xid, 10) {
		return "", fmt.Errorf("event position: %w", domain.ErrNotFound)
	}
	return x, nil
}

// ListEvents returns one page in feed order, (xid, id), and how many more
// events lie beyond it in the paging direction, capped at RemainingCap. A
// cursor that names no event of the org is domain.ErrNotFound.
func (s *Store) ListEvents(ctx context.Context, orgID domain.ID, f query.EventFilter) ([]domain.Event, int, error) {
	const op = "list events"
	if !f.After.IsZero() && !f.Before.IsZero() {
		return nil, 0, fmt.Errorf("%s: after and before are exclusive", op)
	}
	limit := int32(clampLimit(f.Limit))
	a := argsOf(f)
	var events []domain.Event
	if !f.Before.IsZero() {
		bx, err := s.eventXid(ctx, orgID, f.Before)
		if err != nil {
			return nil, 0, fmt.Errorf("%s: before: %w", op, err)
		}
		rows, err := s.q.ListEventsBefore(ctx, sqlcgen.ListEventsBeforeParams{
			OrgID: int64(orgID), BeforeXid: bx, Before: int64(f.Before.ID), ProjectIds: a.projectIDs,
			ExcludeActorID: a.actorID, ExcludeActorKind: a.kind, ExcludeSession: a.session,
			LimitedTypes: a.limitedTypes, ConcernsUserID: a.concernsUser, MaxRows: limit,
		})
		if err != nil {
			return nil, 0, wrap(op, err)
		}
		events = mapRows(rows, func(r sqlcgen.ListEventsBeforeRow) domain.Event { return eventFromRow(eventRow(r)) })
	} else {
		ax, err := s.eventXid(ctx, orgID, f.After)
		if err != nil {
			return nil, 0, fmt.Errorf("%s: after: %w", op, err)
		}
		rows, err := s.q.ListEventsAfter(ctx, sqlcgen.ListEventsAfterParams{
			OrgID: int64(orgID), AfterXid: ax, After: int64(f.After.ID), ProjectIds: a.projectIDs,
			ExcludeActorID: a.actorID, ExcludeActorKind: a.kind, ExcludeSession: a.session,
			LimitedTypes: a.limitedTypes, ConcernsUserID: a.concernsUser, MaxRows: limit,
		})
		if err != nil {
			return nil, 0, wrap(op, err)
		}
		events = mapRows(rows, func(r sqlcgen.ListEventsAfterRow) domain.Event { return eventFromRow(eventRow(r)) })
	}
	if len(events) < int(limit) {
		return events, 0, nil
	}
	edge := events[len(events)-1]
	if !f.Before.IsZero() {
		edge = events[0]
	}
	ex := strconv.FormatUint(edge.Xid, 10)
	var remaining int64
	var err error
	if !f.Before.IsZero() {
		remaining, err = s.q.CountEventsBefore(ctx, sqlcgen.CountEventsBeforeParams{
			OrgID: int64(orgID), BeforeXid: ex, Before: int64(edge.ID), ProjectIds: a.projectIDs,
			ExcludeActorID: a.actorID, ExcludeActorKind: a.kind, ExcludeSession: a.session,
			LimitedTypes: a.limitedTypes, ConcernsUserID: a.concernsUser, MaxRows: query.RemainingCap,
		})
	} else {
		remaining, err = s.q.CountEventsAfter(ctx, sqlcgen.CountEventsAfterParams{
			OrgID: int64(orgID), AfterXid: ex, After: int64(edge.ID), ProjectIds: a.projectIDs,
			ExcludeActorID: a.actorID, ExcludeActorKind: a.kind, ExcludeSession: a.session,
			LimitedTypes: a.limitedTypes, ConcernsUserID: a.concernsUser, MaxRows: query.RemainingCap,
		})
	}
	if err != nil {
		return nil, 0, wrap(op+": count remaining", err)
	}
	return events, int(remaining), nil
}

// CountEventsAfter counts matching events after f.After in feed order,
// stopping at f.Limit when positive. A cursor that names no event of the org
// is domain.ErrNotFound.
func (s *Store) CountEventsAfter(ctx context.Context, orgID domain.ID, f query.EventFilter) (int, error) {
	limit := int32(f.Limit)
	if limit <= 0 {
		limit = query.RemainingCap
	}
	ax, err := s.eventXid(ctx, orgID, f.After)
	if err != nil {
		return 0, fmt.Errorf("count events: after: %w", err)
	}
	a := argsOf(f)
	n, err := s.q.CountEventsAfter(ctx, sqlcgen.CountEventsAfterParams{
		OrgID: int64(orgID), AfterXid: ax, After: int64(f.After.ID), ProjectIds: a.projectIDs,
		ExcludeActorID: a.actorID, ExcludeActorKind: a.kind, ExcludeSession: a.session,
		LimitedTypes: a.limitedTypes, ConcernsUserID: a.concernsUser, MaxRows: limit,
	})
	if err != nil {
		return 0, wrap("count events", err)
	}
	return int(n), nil
}

// ListEventsForSubject returns the newest events on one subject of the org,
// audience-limited ones left out.
func (s *Store) ListEventsForSubject(ctx context.Context, orgID domain.ID, subj domain.Subject, limit int) ([]domain.Event, error) {
	rows, err := s.q.ListEventsForSubject(ctx, sqlcgen.ListEventsForSubjectParams{
		OrgID: int64(orgID), SubjectType: int16(subj.Type), SubjectID: int64(subj.ID), MaxRows: int32(clampLimit(limit)),
		LimitedTypes: argsOf(query.EventFilter{}).limitedTypes,
	})
	if err != nil {
		return nil, wrap("list events for subject", err)
	}
	return mapRows(rows, func(r sqlcgen.ListEventsForSubjectRow) domain.Event { return eventFromRow(eventRow(r)) }), nil
}

// CardsCreatedInSession returns the cards among cardIDs that actor created
// in session.
func (s *Store) CardsCreatedInSession(ctx context.Context, orgID domain.ID, cardIDs []domain.ID, actor domain.ActorRef, session string) ([]domain.ID, error) {
	if len(cardIDs) == 0 {
		return nil, nil
	}
	rows, err := s.q.CardsCreatedInSession(ctx, sqlcgen.CardsCreatedInSessionParams{
		OrgID: int64(orgID), SubjectType: int16(domain.SubjectCard), CardIds: ids(cardIDs), CreatedType: int16(domain.EventCardCreated),
		ActorKind: int16(actor.Kind), ActorID: int64(actor.ID), Session: session,
	})
	if err != nil {
		return nil, wrap("cards created in session", err)
	}
	return mapRows(rows, func(id int64) domain.ID { return domain.ID(id) }), nil
}

// CardCreatedSession returns the session label the card was created in;
// domain.ErrNotFound when its card.created event is missing.
func (s *Store) CardCreatedSession(ctx context.Context, orgID, cardID domain.ID) (string, error) {
	session, err := s.q.CardCreatedSession(ctx, sqlcgen.CardCreatedSessionParams{
		OrgID: int64(orgID), SubjectType: int16(domain.SubjectCard), SubjectID: int64(cardID), CreatedType: int16(domain.EventCardCreated),
	})
	return session, wrap("card created session", err)
}

// CardParkedFrom returns the status the card left the last time it moved to
// waiting; ErrNotFound when it never did.
func (s *Store) CardParkedFrom(ctx context.Context, orgID, cardID domain.ID) (domain.Status, error) {
	from, err := s.q.CardParkedFrom(ctx, sqlcgen.CardParkedFromParams{
		OrgID: int64(orgID), SubjectType: int16(domain.SubjectCard), SubjectID: int64(cardID),
		ChangedType: int16(domain.EventCardStatusChanged), ToStatus: domain.StatusWaiting.String(),
	})
	if err != nil {
		return 0, wrap("card parked from", err)
	}
	st, err := domain.ParseStatus(from)
	if err != nil {
		return 0, fmt.Errorf("card parked from: %w", err)
	}
	return st, nil
}

// LatestEventPosition returns the position of the newest visible event in
// the org that f's projects and audience reach; zero when there is none.
// The rest of f is ignored.
func (s *Store) LatestEventPosition(ctx context.Context, orgID domain.ID, f query.EventFilter) (query.EventPosition, error) {
	a := argsOf(f)
	rows, err := s.q.LatestEventPosition(ctx, sqlcgen.LatestEventPositionParams{
		OrgID: int64(orgID), ProjectIds: a.projectIDs, LimitedTypes: a.limitedTypes, ConcernsUserID: a.concernsUser,
	})
	if err != nil {
		return query.EventPosition{}, wrap("latest event position", err)
	}
	if len(rows) == 0 {
		return query.EventPosition{}, nil
	}
	return query.EventPosition{Xid: parseXid(rows[0].Xid), ID: domain.ID(rows[0].ID)}, nil
}

// Listen holds one connection on the notify channel and calls fn with the
// org id of every notification until ctx ends. It returns nil on ctx
// cancellation and the connection error otherwise, so the caller can
// reconnect with backoff.
func (s *Store) Listen(ctx context.Context, fn func(orgID domain.ID)) error {
	if s.inTx {
		return errors.New("listen: not inside a transaction")
	}
	pc, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("listen: acquire connection: %w", err)
	}
	// A connection that was listening must not go back to the pool: take it
	// out now and close it when done.
	conn := pc.Hijack()
	defer conn.Close(context.WithoutCancel(ctx))

	if _, err := conn.Exec(ctx, "LISTEN "+notifyChannel); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("listen: wait: %w", err)
		}
		id, err := strconv.ParseInt(n.Payload, 10, 64)
		if err != nil {
			continue
		}
		fn(domain.ID(id))
	}
}
