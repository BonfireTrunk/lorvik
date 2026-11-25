package store

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/BonfireTrunk/lorvik/internal/query"
)

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// filter accumulates WHERE clauses and their named arguments for the
// hand-written list queries.
type filter struct {
	clauses []string
	args    pgx.NamedArgs
}

func newFilter() *filter { return &filter{args: pgx.NamedArgs{}} }

// add appends a clause. Arguments are given as name, value pairs.
func (f *filter) add(clause string, nameValue ...any) {
	f.clauses = append(f.clauses, clause)
	for i := 0; i+1 < len(nameValue); i += 2 {
		f.args[nameValue[i].(string)] = nameValue[i+1]
	}
}

func (f *filter) where() string {
	if len(f.clauses) == 0 {
		return "TRUE"
	}
	return strings.Join(f.clauses, " AND ")
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return defaultPageSize
	}
	return min(limit, maxPageSize)
}

// keyset describes how one list is ordered and how to continue it from a
// cursor. The cursor is a set of named arguments (prefixed with "c_") that
// after and before compare against.
type keyset[T any] struct {
	// order is the forward ORDER BY body; reverse is the same with every
	// direction flipped, for paging backward.
	order, reverse string
	// after and before are the WHERE clauses selecting rows that sort
	// strictly after or before the cursor.
	after, before string
	// key extracts the cursor arguments of a row.
	key func(T) pgx.NamedArgs
}

// addRange adds the bounds of r on column col: After inclusive, Before
// exclusive. name prefixes the argument names.
func (f *filter) addRange(col, name string, r query.TimeRange) {
	if !r.After.IsZero() {
		f.add(col+" >= @"+name+"_after", name+"_after", r.After)
	}
	if !r.Before.IsZero() {
		f.add(col+" < @"+name+"_before", name+"_before", r.Before)
	}
}

// listKeyset runs one page of a keyset-paged query. cursor is nil on the
// first page. The page is always returned in forward order; remaining counts
// the rows beyond the page in the paging direction, capped at RemainingCap.
func listKeyset[T any](ctx context.Context, db pgxQuerier, f *filter, cols, from string, ks keyset[T], page query.Page, cursor pgx.NamedArgs) ([]T, int, error) {
	limit := clampLimit(page.Limit)
	order, cont := ks.order, ks.after
	if page.Backward {
		order, cont = ks.reverse, ks.before
	}

	where := f.where()
	args := pgx.NamedArgs{"page_limit": limit}
	maps.Copy(args, f.args)
	if cursor != nil {
		where += " AND " + cont
		maps.Copy(args, cursor)
	}
	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY %s LIMIT @page_limit", cols, from, where, order)
	rows, err := db.Query(ctx, q, args)
	if err != nil {
		return nil, 0, fmt.Errorf("query: %w", err)
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[T])
	if err != nil {
		return nil, 0, fmt.Errorf("scan: %w", err)
	}
	if page.Backward {
		slices.Reverse(items)
	}
	if len(items) < limit {
		return items, 0, nil
	}

	// A full page: count what lies beyond its edge in the paging direction.
	edge := items[len(items)-1]
	if page.Backward {
		edge = items[0]
	}
	cargs := pgx.NamedArgs{"cap": query.RemainingCap}
	maps.Copy(cargs, f.args)
	maps.Copy(cargs, ks.key(edge))
	cq := fmt.Sprintf("SELECT count(*) FROM (SELECT 1 FROM %s WHERE %s AND %s LIMIT @cap) beyond", from, f.where(), cont)
	var remaining int
	if err := db.QueryRow(ctx, cq, cargs).Scan(&remaining); err != nil {
		return nil, 0, fmt.Errorf("count remaining: %w", err)
	}
	return items, remaining, nil
}

// listRanked runs a relevance-ordered query, forward only: one page, plus how
// many more matches follow it, capped. cursor is nil on the first page; after
// is the clause that selects rows sorting after it.
func listRanked[T any](ctx context.Context, db pgxQuerier, f *filter, cols, from, order, after string, cursor pgx.NamedArgs, limit int) ([]T, int, error) {
	limit = clampLimit(limit)
	args := pgx.NamedArgs{"page_limit": limit, "cap": limit + query.RemainingCap}
	maps.Copy(args, f.args)
	where := f.where()
	if cursor != nil {
		where += " AND " + after
		maps.Copy(args, cursor)
	}
	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY %s LIMIT @page_limit", cols, from, where, order)
	rows, err := db.Query(ctx, q, args)
	if err != nil {
		return nil, 0, fmt.Errorf("query: %w", err)
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[T])
	if err != nil {
		return nil, 0, fmt.Errorf("scan: %w", err)
	}
	if len(items) < limit {
		return items, 0, nil
	}
	var total int
	cq := fmt.Sprintf("SELECT count(*) FROM (SELECT 1 FROM %s WHERE %s LIMIT @cap) bounded", from, where)
	if err := db.QueryRow(ctx, cq, args).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count remaining: %w", err)
	}
	return items, max(total-limit, 0), nil
}

// pgxQuerier is the part of pgx.Tx and pgxpool.Pool the dynamic queries use.
type pgxQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
