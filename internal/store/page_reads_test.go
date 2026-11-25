package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Lists select whole card and note rows, so a page must never be a column of
// those tables or appear in a list or search statement; only the page
// queries and the revision snapshots read it.
func TestOnlyPageQueriesReadPageHTML(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		"PutCardPage": true, "GetCardPage": true, "PutNotePage": true, "GetNotePage": true,
		"SnapshotCardRevision": true, "SnapshotNoteRevision": true, "GetCardRevision": true, "GetNoteRevision": true,
	}
	files, err := filepath.Glob("queries/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("query files = %v, %v", files, err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for stmt := range strings.SplitSeq(string(b), "-- name: ") {
			name, _, _ := strings.Cut(stmt, " ")
			if strings.Contains(stmt, "html") && !allowed[name] {
				t.Errorf("%s: query %s reads page html", f, name)
			}
		}
	}
	for name, sql := range map[string]string{"searchSQL": searchSQL, "cardMatch": cardMatch, "noteMatch": noteMatch,
		"cardRankValue": cardRankValue, "noteRankValue": noteRankValue} {
		if strings.Contains(sql, "html") {
			t.Errorf("%s reads page html", name)
		}
	}
}

func TestPageTablesAreNotColumnsOfCardsOrNotes(t *testing.T) {
	t.Parallel()
	schema, err := os.ReadFile("migrations/00001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"cards", "notes"} {
		start := strings.Index(string(schema), "CREATE TABLE "+table+" (")
		if start < 0 {
			t.Fatalf("table %s not found", table)
		}
		body, _, _ := strings.Cut(string(schema)[start:], "\n);")
		if strings.Contains(body, "html") {
			t.Errorf("table %s has an html column; pages belong in %s_pages", table, strings.TrimSuffix(table, "s"))
		}
	}
}
