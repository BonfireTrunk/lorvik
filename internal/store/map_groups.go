package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

type mapGroupRow struct {
	Kind          int16  `db:"gk"`
	Number        int64  `db:"gnum"`
	Lane          string `db:"gname"`
	Title         string `db:"title"`
	StoryStatus   int16  `db:"story_status"`
	Backlog       int64  `db:"n_backlog"`
	Ready         int64  `db:"n_ready"`
	Working       int64  `db:"n_working"`
	Waiting       int64  `db:"n_waiting"`
	Done          int64  `db:"n_done"`
	Dropped       int64  `db:"n_dropped"`
	OpenQuestions int64  `db:"n_open_questions"`
}

// mapCountCols counts the cards c of a group per status, and its open
// questions. count(c.id) is 0 for a story with no children, where the left
// join yields one all-null row.
var mapCountCols = fmt.Sprintf(
	"count(c.id) FILTER (WHERE c.status = %d) AS n_backlog, "+
		"count(c.id) FILTER (WHERE c.status = %d) AS n_ready, "+
		"count(c.id) FILTER (WHERE c.status = %d) AS n_working, "+
		"count(c.id) FILTER (WHERE c.status = %d) AS n_waiting, "+
		"count(c.id) FILTER (WHERE c.status = %d) AS n_done, "+
		"count(c.id) FILTER (WHERE c.status = %d) AS n_dropped, "+
		"count(c.id) FILTER (WHERE c.kind = %d AND c.status >= 0) AS n_open_questions",
	domain.StatusBacklog, domain.StatusReady, domain.StatusWorking, domain.StatusWaiting,
	domain.StatusDone, domain.StatusDropped, domain.KindQuestion)

// Lane names are compared in byte order so that the database and the cursor
// agree whatever the database collation is.
var mapGroupsFrom = fmt.Sprintf(`(
	SELECT %[1]d::smallint AS gk, s.number AS gnum, ''::text COLLATE "C" AS gname, s.title AS title, s.status AS story_status, %[3]s
	FROM cards s LEFT JOIN cards c ON c.parent_id = s.id
	WHERE s.project_id = @project_id AND s.kind = %[4]d
	GROUP BY s.id
	UNION ALL
	SELECT %[2]d::smallint, 0::bigint, coalesce(c.lane, '') COLLATE "C", ''::text, 0::smallint, %[3]s
	FROM cards c
	WHERE c.project_id = @project_id AND c.parent_id IS NULL AND c.kind <> %[4]d
	GROUP BY coalesce(c.lane, '')
) g`, query.GroupStory, query.GroupLane, mapCountCols, domain.KindStory)

var mapGroupKeyset = keyset[mapGroupRow]{
	order: "g.gk, g.gnum, g.gname",
	after: "(g.gk, g.gnum, g.gname) > (@c_kind, @c_number, @c_lane)",
	key: func(r mapGroupRow) pgx.NamedArgs {
		return pgx.NamedArgs{"c_kind": r.Kind, "c_number": r.Number, "c_lane": r.Lane}
	},
}

// ListMapGroups returns one page of the project's map groups: one per story,
// with the status counts of all its children, and one per lane, with the
// counts of the cards outside any story, where a card with no lane is in the
// lane named "". Ordered by kind, story number and lane.
func (s *Store) ListMapGroups(ctx context.Context, projectID domain.ID, limit int, after *query.MapGroupRef) ([]query.MapGroup, int, error) {
	q := newFilter()
	q.args["project_id"] = int64(projectID)
	var cursor pgx.NamedArgs
	if after != nil {
		cursor = pgx.NamedArgs{"c_kind": int16(after.Kind), "c_number": after.Number, "c_lane": after.Lane}
	}
	cols := "g.gk, g.gnum, g.gname, g.title, g.story_status, g.n_backlog, g.n_ready, g.n_working, g.n_waiting, g.n_done, g.n_dropped, g.n_open_questions"
	rows, remaining, err := listKeyset(ctx, s.db, q, cols, mapGroupsFrom, mapGroupKeyset, query.Page{Limit: limit}, cursor)
	if err != nil {
		return nil, 0, wrap("list map groups", err)
	}
	return mapRows(rows, func(r mapGroupRow) query.MapGroup {
		return query.MapGroup{
			Ref:         query.MapGroupRef{Kind: query.GroupKind(r.Kind), Number: r.Number, Lane: r.Lane},
			Title:       r.Title,
			StoryStatus: domain.Status(r.StoryStatus),
			Counts: query.CardCounts{
				ByStatus: map[domain.Status]int{
					domain.StatusBacklog: int(r.Backlog), domain.StatusReady: int(r.Ready), domain.StatusWorking: int(r.Working),
					domain.StatusWaiting: int(r.Waiting), domain.StatusDone: int(r.Done), domain.StatusDropped: int(r.Dropped),
				},
				OpenQuestions: int(r.OpenQuestions),
			},
		}
	}), remaining, nil
}

type mapGroupLinkRow struct {
	FromKind   int16  `db:"from_gk"`
	FromNumber int64  `db:"from_gnum"`
	FromLane   string `db:"from_gname"`
	ToKind     int16  `db:"to_gk"`
	ToNumber   int64  `db:"to_gnum"`
	ToLane     string `db:"to_gname"`
	Kind       int16  `db:"kind"`
	N          int64  `db:"n"`
}

// mapGroupOf is the SQL of the group of card c, whose parent is p: its story
// when it has a parent or is a story, else its lane.
func mapGroupOf(c, p, suffix string) string {
	return fmt.Sprintf(
		`CASE WHEN %[1]s.parent_id IS NOT NULL OR %[1]s.kind = %[3]d THEN %[4]d ELSE %[5]d END::smallint AS %[6]s_gk, `+
			`CASE WHEN %[1]s.parent_id IS NOT NULL THEN %[2]s.number WHEN %[1]s.kind = %[3]d THEN %[1]s.number ELSE 0 END AS %[6]s_gnum, `+
			`(CASE WHEN %[1]s.parent_id IS NULL AND %[1]s.kind <> %[3]d THEN coalesce(%[1]s.lane, '') ELSE '' END) COLLATE "C" AS %[6]s_gname`,
		c, p, domain.KindStory, query.GroupStory, query.GroupLane, suffix)
}

// Links with an end in another project are not counted: a group is a part of
// one project.
var mapGroupLinksFrom = `(
	SELECT from_gk, from_gnum, from_gname, to_gk, to_gnum, to_gname, kind, count(*)::bigint AS n
	FROM (
		SELECT ` + mapGroupOf("fc", "fp", "from") + `, ` + mapGroupOf("tc", "tp", "to") + `, l.kind
		FROM card_links l
		JOIN cards fc ON fc.id = l.from_card_id
		JOIN cards tc ON tc.id = l.to_card_id
		LEFT JOIN cards fp ON fp.id = fc.parent_id
		LEFT JOIN cards tp ON tp.id = tc.parent_id
		WHERE fc.project_id = @project_id AND tc.project_id = @project_id
	) e
	WHERE (from_gk, from_gnum, from_gname) <> (to_gk, to_gnum, to_gname)
	GROUP BY from_gk, from_gnum, from_gname, to_gk, to_gnum, to_gname, kind
) g`

var mapGroupLinkKeyset = keyset[mapGroupLinkRow]{
	order: "g.from_gk, g.from_gnum, g.from_gname, g.to_gk, g.to_gnum, g.to_gname, g.kind",
	after: "(g.from_gk, g.from_gnum, g.from_gname, g.to_gk, g.to_gnum, g.to_gname, g.kind) > (@c_fk, @c_fn, @c_fl, @c_tk, @c_tn, @c_tl, @c_kind)",
	key: func(r mapGroupLinkRow) pgx.NamedArgs {
		return pgx.NamedArgs{"c_fk": r.FromKind, "c_fn": r.FromNumber, "c_fl": r.FromLane,
			"c_tk": r.ToKind, "c_tn": r.ToNumber, "c_tl": r.ToLane, "c_kind": r.Kind}
	},
}

// ListMapGroupLinks returns one page of the link counts between the project's
// map groups: per ordered pair of different groups and link kind, how many
// links run from a card of the first to a card of the second. Ordered by from
// group, to group and kind.
func (s *Store) ListMapGroupLinks(ctx context.Context, projectID domain.ID, limit int, after *query.MapGroupLinkCursor) ([]query.MapGroupLink, int, error) {
	q := newFilter()
	q.args["project_id"] = int64(projectID)
	var cursor pgx.NamedArgs
	if after != nil {
		cursor = pgx.NamedArgs{"c_fk": int16(after.From.Kind), "c_fn": after.From.Number, "c_fl": after.From.Lane,
			"c_tk": int16(after.To.Kind), "c_tn": after.To.Number, "c_tl": after.To.Lane, "c_kind": int16(after.Kind)}
	}
	cols := "g.from_gk, g.from_gnum, g.from_gname, g.to_gk, g.to_gnum, g.to_gname, g.kind, g.n"
	rows, remaining, err := listKeyset(ctx, s.db, q, cols, mapGroupLinksFrom, mapGroupLinkKeyset, query.Page{Limit: limit}, cursor)
	if err != nil {
		return nil, 0, wrap("list map group links", err)
	}
	return mapRows(rows, func(r mapGroupLinkRow) query.MapGroupLink {
		return query.MapGroupLink{
			From:  query.MapGroupRef{Kind: query.GroupKind(r.FromKind), Number: r.FromNumber, Lane: r.FromLane},
			To:    query.MapGroupRef{Kind: query.GroupKind(r.ToKind), Number: r.ToNumber, Lane: r.ToLane},
			Kind:  domain.CardLinkKind(r.Kind),
			Count: int(r.N),
		}
	}), remaining, nil
}
