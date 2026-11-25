package client

import (
	"net/url"
	"strconv"
	"strings"
)

// seg escapes one path segment.
func seg(s string) string { return url.PathEscape(s) }

func num(n int64) string { return strconv.FormatInt(n, 10) }

// withQuery appends v to path, omitting an empty query.
func withQuery(path string, v url.Values) string {
	if len(v) == 0 {
		return path
	}
	return path + "?" + v.Encode()
}

// setIf sets key when val is non-empty.
func setIf(v url.Values, key, val string) {
	if strings.TrimSpace(val) != "" {
		v.Set(key, val)
	}
}

// setLimit sets limit and cursor when present.
func setLimit(v url.Values, limit int, cursor string) {
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	setIf(v, "cursor", cursor)
}

// PageParams pages a forward-only list. Zero values send nothing, so the server's
// default page applies.
type PageParams struct {
	Limit  int
	Cursor string
}

func pageQuery(path string, p PageParams, v url.Values) string {
	setLimit(v, p.Limit, p.Cursor)
	return withQuery(path, v)
}

// HistoryParams pages a comment or revision list, which runs newest first and
// carries no cursor or count. Before is a comment id or a version, exclusive;
// empty starts at the newest. Limit 0 sends none, so the server's default page
// applies. A page shorter than Limit is the last; a full one may or may not be.
type HistoryParams struct {
	Before string
	Limit  int
}

func historyValues(p HistoryParams) url.Values {
	v := url.Values{}
	setIf(v, "before", p.Before)
	if p.Limit > 0 {
		v.Set("limit", strconv.Itoa(p.Limit))
	}
	return v
}

// setBool sets key to "true" when on.
func setBool(v url.Values, key string, on bool) {
	if on {
		v.Set(key, "true")
	}
}

// setNonEmpty sets each query key whose value is non-empty; pairs are key, value.
func setNonEmpty(v url.Values, keyValue ...string) {
	for i := 0; i+1 < len(keyValue); i += 2 {
		setIf(v, keyValue[i], keyValue[i+1])
	}
}

func timeFilterValues(createdAfter, createdBefore, updatedAfter, updatedBefore string) url.Values {
	v := url.Values{}
	setNonEmpty(v, "created_after", createdAfter, "created_before", createdBefore,
		"updated_after", updatedAfter, "updated_before", updatedBefore)
	return v
}
