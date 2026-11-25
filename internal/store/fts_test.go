package store

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func TestFTSVectorsMatchIndexes(t *testing.T) {
	t.Parallel()
	schema, err := os.ReadFile("migrations/00001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		vec   string
		alias string
		index string
	}{
		{"cards", cardFTSVec, "c", "cards_search_idx"},
		{"notes", noteFTSVec, "n", "notes_search_idx"},
		{"cards in a match", cardInnerVec, "ci", "cards_search_idx"},
		{"notes in a match", noteInnerVec, "ni", "notes_search_idx"},
		{"card pages", pageFTSVec, "p", "card_pages_search_idx"},
		{"note pages", pageFTSVec, "p", "note_pages_search_idx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stmt := regexp.MustCompile(`CREATE INDEX ` + tt.index + `[^;]*;`).FindString(string(schema))
			if stmt == "" {
				t.Fatalf("CREATE INDEX %s not found in the migration", tt.index)
			}
			bare := strings.NewReplacer(tt.alias+".title", "title", tt.alias+".body", "body", tt.alias+".text", "text").Replace(tt.vec)
			if !strings.Contains(stmt, bare) {
				t.Errorf("index %q does not contain %q, so queries using %q cannot use it", stmt, bare, tt.vec)
			}
		})
	}
}

// The store clamps every page to the size clients are told is the maximum.
func TestMaxPageSizeIsTheWireConstant(t *testing.T) {
	t.Parallel()
	if maxPageSize != apitypes.MaxPageSize {
		t.Errorf("store maxPageSize = %d, apitypes.MaxPageSize = %d", maxPageSize, apitypes.MaxPageSize)
	}
}
