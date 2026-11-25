package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

type linkEdgeRow struct {
	FromID      int64 `db:"from_id"`
	ToID        int64 `db:"to_id"`
	FromNumber  int64 `db:"from_number"`
	ToNumber    int64 `db:"to_number"`
	FromProject int64 `db:"from_project"`
	ToProject   int64 `db:"to_project"`
	Kind        int16 `db:"kind"`
}

const (
	linkEdgeCols = "l.from_card_id AS from_id, l.to_card_id AS to_id, fc.number AS from_number, tc.number AS to_number, " +
		"fc.project_id AS from_project, tc.project_id AS to_project, l.kind"
	linkEdgeFrom = "card_links l JOIN cards fc ON fc.id = l.from_card_id JOIN cards tc ON tc.id = l.to_card_id"
)

var linkEdgeKeyset = keyset[linkEdgeRow]{
	order: "l.from_card_id, l.to_card_id, l.kind",
	after: "(l.from_card_id, l.to_card_id, l.kind) > (@c_from, @c_to, @c_kind)",
	key: func(r linkEdgeRow) pgx.NamedArgs {
		return pgx.NamedArgs{"c_from": r.FromID, "c_to": r.ToID, "c_kind": r.Kind}
	},
}

// ListProjectLinks returns one page of the links with an end in the project,
// whichever end it is, with both cards in visible when that is not nil.
func (s *Store) ListProjectLinks(ctx context.Context, projectID domain.ID, visible []domain.ID, limit int, after *query.LinkCursor) ([]query.LinkEdge, int, error) {
	const op = "list project links"
	q := newFilter()
	q.add("(fc.project_id = @project_id OR tc.project_id = @project_id)", "project_id", int64(projectID))
	if visible != nil {
		q.add("fc.project_id = ANY(@visible::bigint[]) AND tc.project_id = ANY(@visible::bigint[])", "visible", ids(visible))
	}
	var cursor pgx.NamedArgs
	if after != nil {
		cursor = pgx.NamedArgs{"c_from": int64(after.From), "c_to": int64(after.To), "c_kind": int16(after.Kind)}
	}
	rows, remaining, err := listKeyset(ctx, s.db, q, linkEdgeCols, linkEdgeFrom, linkEdgeKeyset, query.Page{Limit: limit}, cursor)
	if err != nil {
		return nil, 0, wrap(op, err)
	}
	return mapRows(rows, func(r linkEdgeRow) query.LinkEdge {
		return query.LinkEdge{
			FromID: domain.ID(r.FromID), ToID: domain.ID(r.ToID), FromNumber: r.FromNumber, ToNumber: r.ToNumber,
			FromProject: domain.ID(r.FromProject), ToProject: domain.ID(r.ToProject), Kind: domain.CardLinkKind(r.Kind),
		}
	}), remaining, nil
}

type mentionEdgeRow struct {
	CardID     int64  `db:"card_id"`
	CardNumber int64  `db:"card_number"`
	NoteID     int64  `db:"note_id"`
	NoteSlug   string `db:"note_slug"`
}

const (
	mentionEdgeCols = "r.source_id AS card_id, c.number AS card_number, r.target_id AS note_id, n.slug AS note_slug"
	mentionEdgeFrom = "refs r JOIN cards c ON c.id = r.source_id JOIN notes n ON n.id = r.target_id"
)

var mentionEdgeKeyset = keyset[mentionEdgeRow]{
	order: "r.source_id, r.target_id",
	after: "(r.source_id, r.target_id) > (@c_card, @c_note)",
	key:   func(r mentionEdgeRow) pgx.NamedArgs { return pgx.NamedArgs{"c_card": r.CardID, "c_note": r.NoteID} },
}

// ListProjectMentions returns one page of the references from the project's
// cards to notes, with the note in visible when that is not nil.
func (s *Store) ListProjectMentions(ctx context.Context, projectID domain.ID, visible []domain.ID, limit int, after *query.MentionCursor) ([]query.MentionEdge, int, error) {
	const op = "list project mentions"
	q := newFilter()
	q.add("c.project_id = @project_id AND r.source_type = @card_type AND r.target_type = @note_type",
		"project_id", int64(projectID), "card_type", int16(domain.SubjectCard), "note_type", int16(domain.SubjectNote))
	if visible != nil {
		q.add("n.project_id = ANY(@visible::bigint[])", "visible", ids(visible))
	}
	var cursor pgx.NamedArgs
	if after != nil {
		cursor = pgx.NamedArgs{"c_card": int64(after.Card), "c_note": int64(after.Note)}
	}
	rows, remaining, err := listKeyset(ctx, s.db, q, mentionEdgeCols, mentionEdgeFrom, mentionEdgeKeyset, query.Page{Limit: limit}, cursor)
	if err != nil {
		return nil, 0, wrap(op, err)
	}
	return mapRows(rows, func(r mentionEdgeRow) query.MentionEdge {
		return query.MentionEdge{CardID: domain.ID(r.CardID), CardNumber: r.CardNumber, NoteID: domain.ID(r.NoteID), NoteSlug: r.NoteSlug}
	}), remaining, nil
}
