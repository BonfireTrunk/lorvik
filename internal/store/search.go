package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 100
	// headlineOpts marks matches with ** and keeps snippets short.
	headlineOpts = "StartSel=**, StopSel=**, MaxFragments=1, MinWords=5, MaxWords=25"
)

// The vectors must equal, apart from the table alias, the expressions of
// cards_search_idx, notes_search_idx, card_pages_search_idx and
// note_pages_search_idx in migrations/00001_init.sql:
//
//	CREATE INDEX cards_search_idx ON cards USING gin (to_tsvector('english', title || ' ' || body));
//	CREATE INDEX notes_search_idx ON notes USING gin (to_tsvector('english', title || ' ' || body));
//	CREATE INDEX card_pages_search_idx ON card_pages USING gin (to_tsvector('english', text));
//	CREATE INDEX note_pages_search_idx ON note_pages USING gin (to_tsvector('english', text));
//
// Postgres uses a GIN index only when the query expression is the indexed one,
// so any drift turns a match into a sequential scan. fts_test.go pins them.
const (
	cardFTSVec = "to_tsvector('english', c.title || ' ' || c.body)"
	noteFTSVec = "to_tsvector('english', n.title || ' ' || n.body)"
	pageFTSVec = "to_tsvector('english', p.text)"
	// cardFTSVec and noteFTSVec on the inner aliases of cardMatch and noteMatch.
	cardInnerVec = "to_tsvector('english', ci.title || ' ' || ci.body)"
	noteInnerVec = "to_tsvector('english', ni.title || ' ' || ni.body)"
	ftsQuery     = "websearch_to_tsquery('english', @query)"
)

// A card or note matches when its own vector matches or its page text does.
// The two are asked as a union of ids, each answered by its own index: an OR
// on the row would rebuild every row's vector, as no index serves both sides.
// The expressions take their query from ftsQuery; searchSQL swaps in the
// query it computes once.
const (
	cardMatch = "c.id IN (SELECT ci.id FROM cards ci WHERE " + cardInnerVec + " @@ " + ftsQuery +
		" UNION ALL SELECT p.card_id FROM card_pages p WHERE " + pageFTSVec + " @@ " + ftsQuery + ")"
	noteMatch = "n.id IN (SELECT ni.id FROM notes ni WHERE " + noteInnerVec + " @@ " + ftsQuery +
		" UNION ALL SELECT p.note_id FROM note_pages p WHERE " + pageFTSVec + " @@ " + ftsQuery + ")"
	// The rank adds the page's rank, 0 when there is no page.
	cardRankValue = "(ts_rank(" + cardFTSVec + ", " + ftsQuery +
		") + coalesce((SELECT ts_rank(" + pageFTSVec + ", " + ftsQuery + ") FROM card_pages p WHERE p.card_id = c.id), 0))::float8"
	noteRankValue = "(ts_rank(" + noteFTSVec + ", " + ftsQuery +
		") + coalesce((SELECT ts_rank(" + pageFTSVec + ", " + ftsQuery + ") FROM note_pages p WHERE p.note_id = n.id), 0))::float8"
	// The snippet is cut from the title, body and page text together.
	cardSnippetText = "c.title || ' ' || c.body || ' ' || coalesce((SELECT p.text FROM card_pages p WHERE p.card_id = c.id), '')"
	noteSnippetText = "n.title || ' ' || n.body || ' ' || coalesce((SELECT p.text FROM note_pages p WHERE p.note_id = n.id), '')"
)

// searchSQL ranks matches across cards and notes of one org,
// optionally limited to one project. A match on the card's or note's own
// vector uses its index; one on page text uses the page table's.
var searchSQL = fmt.Sprintf(`
WITH q AS (SELECT %[4]s AS tsq)
SELECT *, count(*) OVER () AS total FROM (
    SELECT %[2]d::smallint AS type, c.id, c.kind::smallint AS kind, c.number, '' AS slug, c.project_id, c.title, c.status::smallint AS status,
           ts_headline('english', %[5]s, q.tsq, '%[1]s') AS snippet,
           %[6]s AS rank
    FROM cards c, q
    WHERE c.org_id = @org_id AND %[7]s
      AND (@project_ids::bigint[] IS NULL OR c.project_id = ANY(@project_ids::bigint[]))
    UNION ALL
    SELECT %[3]d::smallint, n.id, 0::smallint, 0, n.slug, n.project_id, n.title, NULL::smallint,
           ts_headline('english', %[8]s, q.tsq, '%[1]s'),
           %[9]s
    FROM notes n, q
    WHERE n.org_id = @org_id AND n.status > 0 AND %[10]s
      AND (@project_ids::bigint[] IS NULL OR n.project_id = ANY(@project_ids::bigint[]))
) hits
WHERE @after_rank::float8 IS NULL OR rank < @after_rank::float8
   OR (rank = @after_rank::float8 AND (type, id) > (@after_type::smallint, @after_id::bigint))
ORDER BY rank DESC, type, id
LIMIT @max_rows`, headlineOpts, domain.SubjectCard, domain.SubjectNote, ftsQuery,
	cardSnippetText, onQ(cardRankValue), cardMatch, noteSnippetText, onQ(noteRankValue), noteMatch)

// onQ points an expression built on ftsQuery at the query alias of searchSQL.
func onQ(expr string) string { return strings.ReplaceAll(expr, ftsQuery, "q.tsq") }

type searchRow struct {
	Type      int16   `db:"type"`
	ID        int64   `db:"id"`
	Kind      int16   `db:"kind"`
	Number    int64   `db:"number"`
	Slug      string  `db:"slug"`
	ProjectID *int64  `db:"project_id"`
	Title     string  `db:"title"`
	Status    *int16  `db:"status"`
	Snippet   string  `db:"snippet"`
	Rank      float64 `db:"rank"`
	Total     int     `db:"total"`
}

// Search runs a web-style full-text query over cards and notes,
// best match first, continuing from after when it is not nil. A nil projectID
// searches the whole org. It also returns how many hits lie beyond the page,
// capped at query.RemainingCap.
func (s *Store) Search(ctx context.Context, orgID domain.ID, projectIDs []domain.ID, text string, limit int, after *query.SearchCursor) ([]query.SearchHit, int, error) {
	const op = "search"
	if strings.TrimSpace(text) == "" {
		return []query.SearchHit{}, 0, nil
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	var scope []int64
	if projectIDs != nil {
		scope = ids(projectIDs)
	}
	args := pgx.NamedArgs{
		"query": text, "org_id": int64(orgID), "project_ids": scope, "max_rows": min(limit, maxSearchLimit),
		"after_rank": nil, "after_type": nil, "after_id": nil,
	}
	if after != nil {
		args["after_rank"], args["after_type"], args["after_id"] = after.Rank, int16(after.Type), int64(after.ID)
	}
	rows, err := s.db.Query(ctx, searchSQL, args)
	if err != nil {
		return nil, 0, wrap(op, err)
	}
	hits, err := pgx.CollectRows(rows, pgx.RowToStructByName[searchRow])
	if err != nil {
		return nil, 0, wrap(op, err)
	}
	remaining := 0
	if len(hits) > 0 {
		remaining = min(hits[0].Total-len(hits), query.RemainingCap)
	}
	return mapRows(hits, func(r searchRow) query.SearchHit {
		return query.SearchHit{
			Type: domain.SubjectType(r.Type), ID: domain.ID(r.ID), Kind: domain.CardKind(r.Kind), Number: r.Number, Slug: r.Slug,
			ProjectID: domainIDPtr(r.ProjectID), Title: r.Title, Status: hitStatus(r), Snippet: r.Snippet, Rank: r.Rank,
		}
	}), remaining, nil
}

// hitStatus names the lifecycle status of a card hit in the same words the
// list endpoints print. Notes carry none.
func hitStatus(r searchRow) string {
	if r.Status == nil {
		return ""
	}
	if domain.SubjectType(r.Type) == domain.SubjectCard {
		return domain.Status(*r.Status).String()
	}
	return ""
}

// SearchRank is the relevance of one card or note for a full-text query, the
// value a ranked list orders by.
func (s *Store) SearchRank(ctx context.Context, subject domain.SubjectType, id domain.ID, text string) (float64, error) {
	sql := "SELECT " + cardRankValue + " FROM cards c WHERE c.id = @id"
	if subject == domain.SubjectNote {
		sql = "SELECT " + noteRankValue + " FROM notes n WHERE n.id = @id"
	}
	var rank float64
	if err := s.db.QueryRow(ctx, sql, pgx.NamedArgs{"id": int64(id), "query": text}).Scan(&rank); err != nil {
		return 0, wrap("search rank", err)
	}
	return rank, nil
}
