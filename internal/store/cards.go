package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
	"github.com/BonfireTrunk/lorvik/internal/store/sqlcgen"
)

// CreateCard allocates the next number from the project and inserts the card
// at version 1. The counter update and the insert share one transaction.
func (s *Store) CreateCard(ctx context.Context, c domain.Card) (domain.Card, error) {
	const op = "create card"
	var out domain.Card
	err := s.WithTx(ctx, func(tx *Store) error {
		number, err := tx.q.NextCardNumber(ctx, int64(c.ProjectID))
		if err != nil {
			return wrap(op+": allocate number", err)
		}
		cols := cardToCols(c)
		ak, aid := actorCols(c.Assignee)
		row, err := tx.q.InsertCard(ctx, sqlcgen.InsertCardParams{
			ID: int64(c.ID), OrgID: int64(c.OrgID), ProjectID: int64(c.ProjectID), Number: number,
			ParentID: idPtr(c.ParentID), Kind: int16(c.Kind), Status: int16(c.Status), DropReason: cols.dropReason,
			Priority: int16(c.Priority), CreatedByKind: int16(c.CreatedBy.Kind), CreatedByID: int64(c.CreatedBy.ID),
			AssigneeKind: ak, AssigneeID: aid, AnswerRelayed: cols.relayed, ForPerson: cols.forPerson, AnsweredByKind: cols.answeredByKind, AnsweredByID: cols.answeredByID,
			AnsweredAt: cols.answeredAt, ClosedAt: c.ClosedAt, CreatedAt: c.CreatedAt,
			Title: c.Title, Body: c.Body, PageBytes: int32(c.PageBytes), QuestionChoices: cols.choices, Answer: cols.answer,
			Branch: cols.branch, Lane: cols.lane,
		})
		if err != nil {
			return wrap(op, err)
		}
		out = cardFromRow(row)
		return nil
	})
	return out, err
}

// GetCard returns one card.
func (s *Store) GetCard(ctx context.Context, id domain.ID) (domain.Card, error) {
	row, err := s.q.GetCard(ctx, int64(id))
	if err != nil {
		return domain.Card{}, wrap("get card", err)
	}
	return cardFromRow(row), nil
}

// GetCards returns the cards among ids that exist.
func (s *Store) GetCards(ctx context.Context, in []domain.ID) ([]domain.Card, error) {
	return getMany(ctx, in, "get cards", s.q.GetCards, cardFromRow)
}

// GetCardByNumber resolves a card by its project-local number.
func (s *Store) GetCardByNumber(ctx context.Context, projectID domain.ID, number int64) (domain.Card, error) {
	row, err := s.q.GetCardByNumber(ctx, sqlcgen.GetCardByNumberParams{ProjectID: int64(projectID), Number: number})
	if err != nil {
		return domain.Card{}, wrap("get card by number", err)
	}
	return cardFromRow(row), nil
}

// cardKeyset is the board order: status, priority desc, number. The cursor
// arguments are c_status, c_priority and c_number.
var cardKeyset = keyset[sqlcgen.Card]{
	order:   "c.status, c.priority DESC, c.number",
	reverse: "c.status DESC, c.priority, c.number DESC",
	after: "(c.status > @c_status OR (c.status = @c_status AND (c.priority < @c_priority" +
		" OR (c.priority = @c_priority AND c.number > @c_number))))",
	before: "(c.status < @c_status OR (c.status = @c_status AND (c.priority > @c_priority" +
		" OR (c.priority = @c_priority AND c.number < @c_number))))",
	key: func(r sqlcgen.Card) pgx.NamedArgs {
		return pgx.NamedArgs{"c_status": r.Status, "c_priority": r.Priority, "c_number": r.Number}
	},
}

// sortedCardKeyset orders by one column then number, both in the same
// direction. val extracts the column's cursor argument from a row.
func sortedCardKeyset(col string, desc bool, val func(sqlcgen.Card) any) keyset[sqlcgen.Card] {
	fwd, rev, gt, lt := "", " DESC", ">", "<"
	if desc {
		fwd, rev, gt, lt = " DESC", "", "<", ">"
	}
	return keyset[sqlcgen.Card]{
		order:   col + fwd + ", c.number" + fwd,
		reverse: col + rev + ", c.number" + rev,
		after:   "(" + col + " " + gt + " @c_val OR (" + col + " = @c_val AND c.number " + gt + " @c_number))",
		before:  "(" + col + " " + lt + " @c_val OR (" + col + " = @c_val AND c.number " + lt + " @c_number))",
		key: func(r sqlcgen.Card) pgx.NamedArgs {
			return pgx.NamedArgs{"c_val": val(r), "c_number": r.Number}
		},
	}
}

// cardKeysetFor returns the keyset and the cursor arguments of a card list
// order. The cursor is nil on the first page.
func cardKeysetFor(sort query.CardSort, desc bool, cur *query.CardCursor) (keyset[sqlcgen.Card], pgx.NamedArgs) {
	var ks keyset[sqlcgen.Card]
	var args pgx.NamedArgs
	switch sort {
	case query.SortCreated:
		ks = sortedCardKeyset("c.created_at", desc, func(r sqlcgen.Card) any { return r.CreatedAt })
		if cur != nil {
			args = pgx.NamedArgs{"c_val": cur.CreatedAt}
		}
	case query.SortUpdated:
		ks = sortedCardKeyset("c.updated_at", desc, func(r sqlcgen.Card) any { return r.UpdatedAt })
		if cur != nil {
			args = pgx.NamedArgs{"c_val": cur.UpdatedAt}
		}
	case query.SortPriority:
		ks = sortedCardKeyset("c.priority", desc, func(r sqlcgen.Card) any { return r.Priority })
		if cur != nil {
			args = pgx.NamedArgs{"c_val": int16(cur.Priority)}
		}
	case query.SortNumber:
		ks = sortedCardKeyset("c.number", desc, func(r sqlcgen.Card) any { return r.Number })
		if cur != nil {
			args = pgx.NamedArgs{"c_val": cur.Number}
		}
	default:
		ks = cardKeyset
		if cur != nil {
			args = pgx.NamedArgs{"c_status": int16(cur.Status), "c_priority": int16(cur.Priority), "c_number": cur.Number}
		}
		return ks, args
	}
	if cur != nil {
		args["c_number"] = cur.Number
	}
	return ks, args
}

// cardFilter turns the filter's constraints into WHERE clauses. The scope
// (one project or several) is added by the caller.
func cardFilter(q *filter, f query.CardFilter) {
	if len(f.Statuses) > 0 {
		q.add("c.status = ANY(@statuses::smallint[])", "statuses", int16s(f.Statuses))
	}
	if f.Kind != 0 {
		q.add("c.kind = @kind", "kind", int16(f.Kind))
	}
	if f.ParentID != nil {
		q.add("c.parent_id = @parent_id", "parent_id", int64(*f.ParentID))
	}
	if f.NoStory {
		q.add("c.parent_id IS NULL AND c.kind <> @story_kind", "story_kind", int16(domain.KindStory))
	}
	switch {
	case f.Lane != "":
		q.add("c.lane = @lane", "lane", f.Lane)
	case f.NoLane:
		q.add("c.lane IS NULL")
	}
	if f.Actor != nil {
		q.add("((c.assignee_kind = @actor_kind AND c.assignee_id = @actor_id) OR EXISTS ("+
			"SELECT 1 FROM claims cl WHERE cl.card_id = c.id AND cl.actor_kind = @actor_kind AND cl.actor_id = @actor_id))",
			"actor_kind", int16(f.Actor.Kind), "actor_id", int64(f.Actor.ID))
	}
	if f.Open {
		q.add("c.status >= 0")
	}
	if f.AwaitsKeepBy != nil {
		q.add("c.behalf_user_id = @behalf_user AND c.behalf_kept_at IS NULL", "behalf_user", int64(*f.AwaitsKeepBy))
	}
	if f.Relayed {
		q.add("c.kind = @relayed_kind AND c.status = @relayed_status AND c.answer_relayed",
			"relayed_kind", int16(domain.KindQuestion), "relayed_status", int16(domain.StatusDone))
	}
	if len(f.Priorities) > 0 {
		q.add("c.priority = ANY(@priorities::smallint[])", "priorities", int16s(f.Priorities))
	}
	const claimed = "EXISTS (SELECT 1 FROM claims ac WHERE ac.card_id = c.id%s)"
	switch {
	case f.Assignee != nil:
		q.add("(c.assignee_id = @assignee_id OR "+fmt.Sprintf(claimed, " AND ac.actor_id = @assignee_id")+")",
			"assignee_id", int64(*f.Assignee))
	case f.Unassigned:
		q.add("(c.assignee_id IS NULL AND NOT " + fmt.Sprintf(claimed, "") + ")")
	}
	q.addRange("c.created_at", "created", f.Created)
	q.addRange("c.updated_at", "updated", f.Updated)
	q.addRange("c.closed_at", "closed", f.Closed)
}

const (
	cardRank      = cardRankValue + " DESC, c.number, c.id"
	cardRankAfter = "(" + cardRankValue + " < @c_rank OR (" + cardRankValue + " = @c_rank AND (c.number, c.id) > (@c_number, @c_id)))"
)

// ListCards returns one page of a project's cards in board order, or by
// relevance when the filter carries a query, plus how many rows follow.
func (s *Store) ListCards(ctx context.Context, projectID domain.ID, f query.CardFilter) ([]domain.Card, int, error) {
	const op = "list cards"
	q := newFilter()
	q.add("c.project_id = @project_id", "project_id", int64(projectID))
	ks, cursor := cardKeysetFor(f.Sort, f.Desc, f.Cursor)
	return s.listCardRows(ctx, op, q, f, ks, cursor)
}

// listCardRows applies the card filter to q and returns one page: by
// relevance when the filter carries a query, else in the keyset's order.
func (s *Store) listCardRows(ctx context.Context, op string, q *filter, f query.CardFilter, ks keyset[sqlcgen.Card], cursor pgx.NamedArgs) ([]domain.Card, int, error) {
	cardFilter(q, f)
	if query := strings.TrimSpace(f.Query); query != "" {
		q.add(cardMatch, "query", query)
		var after pgx.NamedArgs
		if f.Cursor != nil {
			after = pgx.NamedArgs{"c_rank": f.Cursor.Rank, "c_number": f.Cursor.Number, "c_id": int64(f.Cursor.ID)}
		}
		rows, remaining, err := listRanked[sqlcgen.Card](ctx, s.db, q, "c.*", "cards c", cardRank, cardRankAfter, after, f.Page.Limit)
		if err != nil {
			return nil, 0, wrap(op, err)
		}
		return mapRows(rows, cardFromRow), remaining, nil
	}
	rows, remaining, err := listKeyset(ctx, s.db, q, "c.*", "cards c", ks, f.Page, cursor)
	if err != nil {
		return nil, 0, wrap(op, err)
	}
	return mapRows(rows, cardFromRow), remaining, nil
}

// cardIDKeyset orders the org-wide list newest first. Card numbers repeat
// across projects, so the id is the key.
var cardIDKeyset = keyset[sqlcgen.Card]{
	order:   "c.id DESC",
	reverse: "c.id",
	after:   "c.id < @c_id",
	before:  "c.id > @c_id",
	key:     func(r sqlcgen.Card) pgx.NamedArgs { return pgx.NamedArgs{"c_id": r.ID} },
}

// ListCardsIn returns one page of the cards of several projects, newest
// first by id (by relevance when the filter carries a query). The sort fields
// of the filter are ignored.
func (s *Store) ListCardsIn(ctx context.Context, projectIDs []domain.ID, f query.CardFilter) ([]domain.Card, int, error) {
	const op = "list cards across projects"
	if len(projectIDs) == 0 {
		return []domain.Card{}, 0, nil
	}
	q := newFilter()
	q.add("c.project_id = ANY(@project_ids::bigint[])", "project_ids", ids(projectIDs))
	var cursor pgx.NamedArgs
	if f.Cursor != nil {
		cursor = pgx.NamedArgs{"c_id": int64(f.Cursor.ID)}
	}
	return s.listCardRows(ctx, op, q, f, cardIDKeyset, cursor)
}

// unfoldedKeyset orders done cards by when they closed, newest first.
var unfoldedKeyset = keyset[sqlcgen.Card]{
	order:   "c.closed_at DESC, c.id DESC",
	reverse: "c.closed_at, c.id",
	after:   "(c.closed_at < @c_closed OR (c.closed_at = @c_closed AND c.id < @c_id))",
	before:  "(c.closed_at > @c_closed OR (c.closed_at = @c_closed AND c.id > @c_id))",
	key: func(r sqlcgen.Card) pgx.NamedArgs {
		return pgx.NamedArgs{"c_closed": r.ClosedAt, "c_id": r.ID}
	},
}

// ListUnfolded returns one page of the project's done cards that have no
// parent and no fold mark, newest closed first.
func (s *Store) ListUnfolded(ctx context.Context, projectID domain.ID, page query.Page, cur *query.UnfoldedCursor) ([]domain.Card, int, error) {
	const op = "list unfolded cards"
	q := newFilter()
	q.add("c.project_id = @project_id AND c.status = @done AND c.parent_id IS NULL AND c.folded_at IS NULL",
		"project_id", int64(projectID), "done", int16(domain.StatusDone))
	var cursor pgx.NamedArgs
	if cur != nil {
		cursor = pgx.NamedArgs{"c_closed": cur.ClosedAt, "c_id": int64(cur.ID)}
	}
	rows, remaining, err := listKeyset(ctx, s.db, q, "c.*", "cards c", unfoldedKeyset, page, cursor)
	if err != nil {
		return nil, 0, wrap(op, err)
	}
	return mapRows(rows, cardFromRow), remaining, nil
}

func int16s[T ~int16](in []T) []int16 {
	out := make([]int16, len(in))
	for i, v := range in {
		out[i] = int16(v)
	}
	return out
}

// NextReady returns the best ready, unclaimed, unblocked task that is
// unassigned or assigned to actor and whose lane has neither a working card
// nor a lock live at now.
func (s *Store) NextReady(ctx context.Context, projectID domain.ID, actor domain.ActorRef, now time.Time) (domain.Card, error) {
	ak, aid := actorCols(&actor)
	row, err := s.q.NextReadyCard(ctx, sqlcgen.NextReadyCardParams{
		ActorKind: ak, ActorID: aid, ProjectID: int64(projectID), ReadyStatus: int16(domain.StatusReady), TaskKind: int16(domain.KindTask),
		BlocksKind: int16(domain.LinkBlocks), DroppedStatus: int16(domain.StatusDropped),
		WorkingStatus: int16(domain.StatusWorking), Now: now,
	})
	if err != nil {
		return domain.Card{}, wrap("next ready card", err)
	}
	return cardFromRow(row), nil
}

// CountOnBusyLanes counts the cards NextReady would offer actor but for a
// working card or a live lock in their lane, per lane in name order.
func (s *Store) CountOnBusyLanes(ctx context.Context, projectID domain.ID, actor domain.ActorRef, now time.Time) ([]query.LaneCount, error) {
	ak, aid := actorCols(&actor)
	rows, err := s.q.CountCardsOnBusyLanes(ctx, sqlcgen.CountCardsOnBusyLanesParams{
		ActorKind: ak, ActorID: aid, ProjectID: int64(projectID), ReadyStatus: int16(domain.StatusReady), TaskKind: int16(domain.KindTask),
		BlocksKind: int16(domain.LinkBlocks), DroppedStatus: int16(domain.StatusDropped),
		WorkingStatus: int16(domain.StatusWorking), Now: now,
	})
	if err != nil {
		return nil, wrap("count cards on busy lanes", err)
	}
	out := make([]query.LaneCount, len(rows))
	for i, r := range rows {
		out[i] = query.LaneCount{Lane: r.Lane, Cards: int(r.Cards)}
	}
	return out, nil
}

// UpdateCard writes every mutable field where the version matches.
func (s *Store) UpdateCard(ctx context.Context, c domain.Card, expectedVersion int64) (domain.Card, error) {
	const op = "update card"
	cols := cardToCols(c)
	ak, aid := actorCols(c.Assignee)
	row, err := s.q.UpdateCard(ctx, sqlcgen.UpdateCardParams{
		ID: int64(c.ID), ExpectedVersion: expectedVersion,
		Status: int16(c.Status), DropReason: cols.dropReason, Priority: int16(c.Priority), ParentID: idPtr(c.ParentID),
		AssigneeKind: ak, AssigneeID: aid, AnswerRelayed: cols.relayed, ForPerson: cols.forPerson, AnsweredByKind: cols.answeredByKind, AnsweredByID: cols.answeredByID,
		AnsweredAt: cols.answeredAt, ClosedAt: c.ClosedAt, UpdatedAt: c.UpdatedAt,
		BehalfUserID: cols.behalfUserID, BehalfAgentID: cols.behalfAgentID, BehalfKeptAt: cols.behalfKeptAt, BehalfReason: cols.behalfReason, BehalfSession: cols.behalfSession,
		Title: c.Title, Body: c.Body, PageBytes: int32(c.PageBytes), QuestionChoices: cols.choices, Answer: cols.answer,
		Branch: cols.branch, Lane: cols.lane,
		FoldedInto: cols.foldedInto, FoldedAt: cols.foldedAt, FoldedByKind: cols.foldedByKind, FoldedByID: cols.foldedByID,
		ClosingNote: optText(c.ClosingNote),
	})
	if err != nil {
		if isNoRows(err) {
			return domain.Card{}, conflictOrNotFound(ctx, op, func(ctx context.Context) (bool, error) {
				return s.q.CardExists(ctx, int64(c.ID))
			})
		}
		return domain.Card{}, wrap(op, err)
	}
	return cardFromRow(row), nil
}

// PutCardPage stores the card's HTML page and its visible text for search,
// replacing any earlier page. The caller sets Card.PageBytes in the same
// transaction.
func (s *Store) PutCardPage(ctx context.Context, cardID domain.ID, html, text string) error {
	return wrap("put card page", s.q.PutCardPage(ctx, sqlcgen.PutCardPageParams{CardID: int64(cardID), Html: html, Text: text}))
}

// DeleteCardPage removes the card's page; no error when it has none.
func (s *Store) DeleteCardPage(ctx context.Context, cardID domain.ID) error {
	return wrap("delete card page", s.q.DeleteCardPage(ctx, int64(cardID)))
}

// GetCardPage returns the card's HTML page, or ErrNotFound when it has none.
func (s *Store) GetCardPage(ctx context.Context, cardID domain.ID) (string, error) {
	html, err := s.q.GetCardPage(ctx, int64(cardID))
	if err != nil {
		return "", wrap("get card page", err)
	}
	return html, nil
}

// SnapshotCardRevision keeps the card's current title, body and page as a
// revision at its current version, attributed to the actor replacing them.
// The page is kept only when pageChanged: otherwise the revision stores none
// and reads resolve it from a newer revision or the current page.
// Call it in the transaction that then updates the card.
func (s *Store) SnapshotCardRevision(ctx context.Context, cardID domain.ID, expectedVersion int64, revisionID domain.ID, by domain.ActorRef, at time.Time, pageChanged bool) error {
	const op = "snapshot card revision"
	n, err := s.q.SnapshotCardRevision(ctx, sqlcgen.SnapshotCardRevisionParams{
		RevisionID: int64(revisionID), UpdatedByKind: int16(by.Kind), UpdatedByID: int64(by.ID), CreatedAt: at,
		ID: int64(cardID), ExpectedVersion: expectedVersion, PageChanged: pageChanged,
	})
	if err != nil {
		return wrap(op, err)
	}
	if n == 0 {
		return conflictOrNotFound(ctx, op, func(ctx context.Context) (bool, error) {
			return s.q.CardExists(ctx, int64(cardID))
		})
	}
	return nil
}

// ListCardRevisions returns one page of a card's past versions, newest
// first, without bodies.
func (s *Store) ListCardRevisions(ctx context.Context, cardID domain.ID, pg query.HistoryPage) ([]domain.CardRevision, error) {
	rows, err := s.q.ListCardRevisions(ctx, sqlcgen.ListCardRevisionsParams{CardID: int64(cardID), Before: pg.Before, MaxRows: historyLimit(pg)})
	if err != nil {
		return nil, wrap("list card revisions", err)
	}
	return mapRows(rows, func(r sqlcgen.ListCardRevisionsRow) domain.CardRevision {
		return domain.CardRevision{ID: domain.ID(r.ID), CardID: domain.ID(r.CardID), Version: r.Version,
			UpdatedBy: actor(r.UpdatedByKind, r.UpdatedByID), CreatedAt: r.CreatedAt, Title: r.Title}
	}), nil
}

// historyLimit is the LIMIT of a history page; no history read is unbounded.
func historyLimit(pg query.HistoryPage) int32 {
	return int32(clampLimit(pg.Limit))
}

// GetCardRevision returns one past version.
func (s *Store) GetCardRevision(ctx context.Context, cardID domain.ID, version int64) (domain.CardRevision, error) {
	row, err := s.q.GetCardRevision(ctx, sqlcgen.GetCardRevisionParams{CardID: int64(cardID), Version: version})
	if err != nil {
		return domain.CardRevision{}, wrap("get card revision", err)
	}
	return cardRevisionFromRow(row), nil
}

// ListOpenCardsAssignedTo returns the open cards of the org assigned to the
// actor, oldest first.
func (s *Store) ListOpenCardsAssignedTo(ctx context.Context, orgID domain.ID, a domain.ActorRef) ([]domain.Card, error) {
	kind, id := int16(a.Kind), int64(a.ID)
	rows, err := s.q.ListOpenCardsAssignedTo(ctx, sqlcgen.ListOpenCardsAssignedToParams{OrgID: int64(orgID), AssigneeKind: &kind, AssigneeID: &id})
	if err != nil {
		return nil, wrap("list assigned cards", err)
	}
	return mapRows(rows, cardFromRow), nil
}

// ListChildren returns direct children by priority then number.
func (s *Store) ListChildren(ctx context.Context, parentID domain.ID) ([]domain.Card, error) {
	pid := int64(parentID)
	rows, err := s.q.ListChildren(ctx, &pid)
	if err != nil {
		return nil, wrap("list children", err)
	}
	return mapRows(rows, cardFromRow), nil
}

// ListFirstChildren returns at most limit direct children, in ListChildren's
// order.
func (s *Store) ListFirstChildren(ctx context.Context, parentID domain.ID, limit int) ([]domain.Card, error) {
	pid := int64(parentID)
	rows, err := s.q.ListFirstChildren(ctx, sqlcgen.ListFirstChildrenParams{ParentID: &pid, MaxRows: int32(limit)})
	if err != nil {
		return nil, wrap("list first children", err)
	}
	return mapRows(rows, cardFromRow), nil
}

// CountChildrenByStatus counts a card's direct children per status.
func (s *Store) CountChildrenByStatus(ctx context.Context, parentID domain.ID) (map[domain.Status]int, error) {
	pid := int64(parentID)
	rows, err := s.q.CountChildrenByStatus(ctx, &pid)
	if err != nil {
		return nil, wrap("count children", err)
	}
	out := make(map[domain.Status]int, len(rows))
	for _, r := range rows {
		out[domain.Status(r.Status)] = int(r.N)
	}
	return out, nil
}

// GetClaim returns the card's claim row, or ErrNotFound.
func (s *Store) GetClaim(ctx context.Context, cardID domain.ID) (domain.Claim, error) {
	row, err := s.q.GetClaim(ctx, int64(cardID))
	if err != nil {
		return domain.Claim{}, wrap("get claim", err)
	}
	return claimFromRow(row), nil
}

// GetClaims returns the claim rows among cardIDs that exist.
func (s *Store) GetClaims(ctx context.Context, cardIDs []domain.ID) ([]domain.Claim, error) {
	return getMany(ctx, cardIDs, "get claims", s.q.GetClaims, claimFromRow)
}

// PutClaim inserts or replaces the card's claim.
func (s *Store) PutClaim(ctx context.Context, c domain.Claim) error {
	return wrap("put claim", s.q.PutClaim(ctx, sqlcgen.PutClaimParams{
		CardID: int64(c.CardID), OrgID: int64(c.OrgID), ProjectID: int64(c.ProjectID),
		ActorKind: int16(c.Actor.Kind), ActorID: int64(c.Actor.ID), Session: c.Session, ExpiresAt: c.ExpiresAt, CreatedAt: c.CreatedAt,
		LeaseSeconds: statedLease(c.Lease),
	}))
}

// DeleteClaim removes the card's claim; no error when none exists.
func (s *Store) DeleteClaim(ctx context.Context, cardID domain.ID) error {
	return wrap("delete claim", s.q.DeleteClaim(ctx, int64(cardID)))
}

// ListClaimsByActor returns the claim rows the actor holds in the org,
// expired ones included until the sweeper deletes them.
func (s *Store) ListClaimsByActor(ctx context.Context, orgID domain.ID, a domain.ActorRef) ([]domain.Claim, error) {
	rows, err := s.q.ListClaimsByActor(ctx, sqlcgen.ListClaimsByActorParams{
		OrgID: int64(orgID), ActorKind: int16(a.Kind), ActorID: int64(a.ID),
	})
	if err != nil {
		return nil, wrap("list claims by actor", err)
	}
	return mapRows(rows, claimFromRow), nil
}

// RenewClaims extends the caller's live claims that are inside the second
// half of their lease and returns the renewed rows. An agent's claims are
// renewed only from the session that took them.
func (s *Store) RenewClaims(ctx context.Context, orgID domain.ID, a domain.ActorRef, session string, now time.Time) ([]domain.Claim, error) {
	rows, err := s.q.RenewClaims(ctx, sqlcgen.RenewClaimsParams{
		Now: now, OrgID: int64(orgID), ActorKind: int16(a.Kind), ActorID: int64(a.ID),
		AnySession: !a.SessionScoped(), Session: session,
	})
	if err != nil {
		return nil, wrap("renew claims", err)
	}
	return mapRows(rows, claimFromRow), nil
}

// ListEndingClaims returns up to limit live claims whose last call is due by
// now and whose current expiry has had none, the soonest to end first.
func (s *Store) ListEndingClaims(ctx context.Context, now time.Time, limit int) ([]domain.Claim, error) {
	rows, err := s.q.ListEndingClaims(ctx, sqlcgen.ListEndingClaimsParams{Now: now, NoticeSeconds: domain.ClaimLastCall.Seconds(), MaxRows: int32(clampLimit(limit))})
	if err != nil {
		return nil, wrap("list ending claims", err)
	}
	return mapRows(rows, claimFromRow), nil
}

// MarkClaimEnding records that the last call for the claim ending at
// expiresAt is written, and reports whether it did.
func (s *Store) MarkClaimEnding(ctx context.Context, cardID domain.ID, expiresAt time.Time) (bool, error) {
	n, err := s.q.MarkClaimEnding(ctx, sqlcgen.MarkClaimEndingParams{CardID: int64(cardID), ExpiresAt: expiresAt})
	return n > 0, wrap("mark claim ending", err)
}

// ListExpiredClaims returns up to limit claims whose lease passed by
// before, the longest expired first.
func (s *Store) ListExpiredClaims(ctx context.Context, before time.Time, limit int) ([]domain.Claim, error) {
	rows, err := s.q.ListExpiredClaims(ctx, sqlcgen.ListExpiredClaimsParams{Before: before, MaxRows: int32(clampLimit(limit))})
	if err != nil {
		return nil, wrap("list expired claims", err)
	}
	return mapRows(rows, claimFromRow), nil
}

// LockExpiredClaim locks the card's claim to the end of the transaction if
// its lease passed by before, or returns domain.ErrNotFound.
func (s *Store) LockExpiredClaim(ctx context.Context, cardID domain.ID, before time.Time) (domain.Claim, error) {
	row, err := s.q.LockExpiredClaim(ctx, sqlcgen.LockExpiredClaimParams{CardID: int64(cardID), Before: before})
	if err != nil {
		return domain.Claim{}, wrap("lock expired claim", err)
	}
	return claimFromRow(row), nil
}

// AddCardLink inserts a link; an existing identical link is left alone.
func (s *Store) AddCardLink(ctx context.Context, l domain.CardLink) error {
	_, err := s.q.AddCardLink(ctx, sqlcgen.AddCardLinkParams{
		FromCardID: int64(l.FromCardID), ToCardID: int64(l.ToCardID), Kind: int16(l.Kind),
		CreatedByKind: int16(l.CreatedBy.Kind), CreatedByID: int64(l.CreatedBy.ID), CreatedAt: l.CreatedAt,
	})
	return wrap("add card link", err)
}

// RemoveCardLink deletes a link, ErrNotFound when it does not exist.
func (s *Store) RemoveCardLink(ctx context.Context, fromID, toID domain.ID, kind domain.CardLinkKind) error {
	const op = "remove card link"
	n, err := s.q.RemoveCardLink(ctx, sqlcgen.RemoveCardLinkParams{FromCardID: int64(fromID), ToCardID: int64(toID), Kind: int16(kind)})
	if err != nil {
		return wrap(op, err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListCardLinks returns links where the card is source or target.
func (s *Store) ListCardLinks(ctx context.Context, cardID domain.ID) ([]domain.CardLink, error) {
	rows, err := s.q.ListCardLinks(ctx, int64(cardID))
	if err != nil {
		return nil, wrap("list card links", err)
	}
	return mapRows(rows, cardLinkFromRow), nil
}

// ListBlockers returns open cards that block cardID.
func (s *Store) ListBlockers(ctx context.Context, cardID domain.ID) ([]domain.Card, error) {
	rows, err := s.q.ListBlockers(ctx, sqlcgen.ListBlockersParams{CardID: int64(cardID), BlocksKind: int16(domain.LinkBlocks)})
	if err != nil {
		return nil, wrap("list blockers", err)
	}
	return mapRows(rows, cardFromRow), nil
}

// AddComment appends a comment to the card.
func (s *Store) AddComment(ctx context.Context, h domain.Comment) error {
	return wrap("add comment", s.q.AddComment(ctx, sqlcgen.AddCommentParams{
		ID: int64(h.ID), CardID: int64(h.CardID), ActorKind: int16(h.Actor.Kind), ActorID: int64(h.Actor.ID), Body: h.Body,
	}))
}

// GetComment returns the comment, or ErrNotFound.
func (s *Store) GetComment(ctx context.Context, id domain.ID) (domain.Comment, error) {
	row, err := s.q.GetComment(ctx, int64(id))
	if err != nil {
		return domain.Comment{}, wrap("get comment", err)
	}
	return commentFromRow(row), nil
}

// ClaimRequestKey records k unless the actor already used k.Key. A claim that
// conflicts with another open transaction waits for it: if that commits, this
// one inserts nothing and the row it made is returned; if it rolls back, this
// one inserts.
func (s *Store) ClaimRequestKey(ctx context.Context, k domain.RequestKey) (domain.RequestKey, bool, error) {
	n, err := s.q.ClaimRequestKey(ctx, sqlcgen.ClaimRequestKeyParams{
		ActorKind: int16(k.Actor.Kind), ActorID: int64(k.Actor.ID), Key: k.Key, OrgID: int64(k.OrgID), ProjectID: int64(k.ProjectID),
		Operation: int16(k.Operation), CardID: int64(k.CardID), SubjectID: int64(k.SubjectID), CreatedAt: k.CreatedAt,
	})
	if err != nil {
		return domain.RequestKey{}, false, wrap("claim request key", err)
	}
	if n == 1 {
		return k, true, nil
	}
	row, err := s.q.GetRequestKey(ctx, sqlcgen.GetRequestKeyParams{ActorKind: int16(k.Actor.Kind), ActorID: int64(k.Actor.ID), Key: k.Key})
	if err != nil {
		return domain.RequestKey{}, false, wrap("get request key", err)
	}
	return domain.RequestKey{
		Actor: actor(row.ActorKind, row.ActorID), Key: row.Key, OrgID: domain.ID(row.OrgID), ProjectID: domain.ID(row.ProjectID),
		Operation: domain.RequestOp(row.Operation), CardID: domain.ID(row.CardID), SubjectID: domain.ID(row.SubjectID), CreatedAt: row.CreatedAt,
	}, false, nil
}

// LatestComment returns the newest comment on the card, or ErrNotFound.
func (s *Store) LatestComment(ctx context.Context, cardID domain.ID) (domain.Comment, error) {
	row, err := s.q.LatestComment(ctx, int64(cardID))
	if err != nil {
		return domain.Comment{}, wrap("latest comment", err)
	}
	return commentFromRow(row), nil
}

// ListComments returns one page of the card's comments, newest first.
func (s *Store) ListComments(ctx context.Context, cardID domain.ID, pg query.HistoryPage) ([]domain.Comment, error) {
	rows, err := s.q.ListComments(ctx, sqlcgen.ListCommentsParams{CardID: int64(cardID), Before: pg.Before, MaxRows: historyLimit(pg)})
	if err != nil {
		return nil, wrap("list comments", err)
	}
	return mapRows(rows, commentFromRow), nil
}

// MarkCardSeen records that the actor saw the card at r.SeenID. The upsert
// keeps the later of the stored and new ids so a stale mark cannot hide a
// reply the actor has not yet read.
func (s *Store) MarkCardSeen(ctx context.Context, r domain.CardRead) error {
	return wrap("mark card seen", s.q.MarkCardSeen(ctx, sqlcgen.MarkCardSeenParams{
		CardID: int64(r.CardID), ProjectID: int64(r.ProjectID),
		ActorKind: int16(r.Actor.Kind), ActorID: int64(r.Actor.ID), SeenID: int64(r.SeenID),
	}))
}

// ListReplies returns, per card, the newest comment by someone else that the
// actor has not seen, newest first.
func (s *Store) ListReplies(ctx context.Context, actor domain.ActorRef, projectID, before domain.ID, limit int32) ([]domain.Reply, int, error) {
	rows, err := s.q.ListReplies(ctx, sqlcgen.ListRepliesParams{
		RowLimit: limit, ActorKind: int16(actor.Kind), ActorID: int64(actor.ID), ProjectID: int64(projectID), Before: int64(before),
	})
	if err != nil {
		return nil, 0, wrap("list replies", err)
	}
	remaining := 0
	if len(rows) > 0 {
		remaining = min(int(rows[0].Total)-len(rows), query.RemainingCap)
	}
	return mapRows(rows, replyFromRow), remaining, nil
}
