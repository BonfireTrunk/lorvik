package domain

import (
	"slices"
	"strings"
	"testing"
)

func TestParseRefs(t *testing.T) {
	t.Parallel()
	const anID = "7245123456789012345"
	tests := []struct {
		name string
		body string
		want []Ref
	}{
		{"bare card", "[[card:42]]", []Ref{{Type: RefCard, Number: 42}}},
		{"prefixed card", "[[card:web/42]]", []Ref{{Type: RefCard, Project: "web", Number: 42}}},
		{"card by id", "[[card:" + anID + "]]", []Ref{{Type: RefCard, ID: 7245123456789012345}}},
		{"card by id ignores prefix", "[[card:web/" + anID + "]]", []Ref{{Type: RefCard, ID: 7245123456789012345}}},
		{"decision references are gone", "[[decision:7]] [[decision:web/7]]", nil},
		{"bare note", "[[note:deploy-process]]", []Ref{{Type: RefNote, Key: "deploy-process"}}},
		{"note by id", "[[note:" + anID + "]]", []Ref{{Type: RefNote, ID: 7245123456789012345}}},
		{"project note", "[[note:web/deploy]]", []Ref{{Type: RefNote, Project: "web", Key: "deploy"}}},
		{"global note by prefix", "[[note:global/deploy]]", []Ref{{Type: RefNote, Project: GlobalSlug, Key: "deploy"}}},
		{"numeric slug below the id threshold is a note key", "[[note:2026]]", []Ref{{Type: RefNote, Key: "2026"}}},
		{"order kept", "[[note:x]] [[card:web/1]] [[card:7]]", []Ref{
			{Type: RefNote, Key: "x"},
			{Type: RefCard, Project: "web", Number: 1},
			{Type: RefCard, Number: 7},
		}},
		{"dedup same form", "[[card:42]] [[card:42]]", []Ref{{Type: RefCard, Number: 42}}},
		{"same number in different projects is not a duplicate", "[[card:web/42]] [[card:api/42]] [[card:42]]", []Ref{
			{Type: RefCard, Project: "web", Number: 42},
			{Type: RefCard, Project: "api", Number: 42},
			{Type: RefCard, Number: 42},
		}},
		{"non-numeric card", "[[card:abc]] [[card:web/abc]]", nil},
		{"zero card", "[[card:0]] [[card:web/0]]", nil},
		{"unknown type", "[[bogus:1]]", nil},
		{"uppercase project", "[[card:Web/42]]", nil},
		{"project starting with dash", "[[card:-web/42]]", nil},
		{"empty key", "[[card:web/]] [[note:]]", nil},
		{"two slashes", "[[note:a/b/c]]", nil},
		{"key with bad char", "[[note:bad key]] [[note:bad.key]]", nil},
		{"key too long", "[[note:" + strings.Repeat("a", 65) + "]]", nil},
		{"surrounding text", "See [[card:web/42]], and (see [[note:global/guide]]).", []Ref{
			{Type: RefCard, Project: "web", Number: 42},
			{Type: RefNote, Project: GlobalSlug, Key: "guide"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ParseRefs(tt.body)
			if !slices.Equal(got, tt.want) {
				t.Errorf("ParseRefs(%q) = %+v, want %+v", tt.body, got, tt.want)
			}
		})
	}
}
