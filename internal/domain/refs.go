package domain

import (
	"regexp"
	"strconv"
)

// Ref is a parsed reference. Exactly one of Number, ID or Key is meaningful:
//   - Number: a card number ("42"), scoped by Project or the
//     source's own project.
//   - ID: a snowflake id of any referenceable entity; globally unique, so
//     Project is ignored.
//   - Key: a note slug, scoped by Project or the source's own project.
//
// Project is the optional prefix ("web" in [[card:web/42]]); it is empty
// when the text had no prefix.
type Ref struct {
	Type    RefType
	Project string
	Number  int64
	ID      ID
	Key     string
}

var refRE = regexp.MustCompile(`\[\[(card|note):(?:([a-z0-9][a-z0-9-]{0,62})/)?([A-Za-z0-9][A-Za-z0-9_-]{0,63})\]\]`)

// idThreshold separates a card number from a snowflake id: ids
// carry at least one millisecond in their top bits, so they are never below
// 1 << 22; numbers never get near it.
const idThreshold = int64(1) << idTimeShift

// ParseRefs extracts every reference from markdown, deduplicated in order of
// first appearance. It is pure: it does not check that the target exists.
// Accepted forms:
//
//	[[card:42]]  [[card:web/42]]  [[card:7245123456789012345]]
//	[[note:slug]]  [[note:web/slug]]  [[note:7245...]]
//
// A non-numeric key on a card is dropped.
func ParseRefs(body string) []Ref {
	seen := map[[3]string]bool{}
	var out []Ref
	for _, m := range refRE.FindAllStringSubmatch(body, -1) {
		k := [3]string{m[1], m[2], m[3]}
		if seen[k] {
			continue
		}
		seen[k] = true
		typ, err := ParseSubjectType(m[1])
		if err != nil {
			continue
		}
		r := Ref{Type: typ, Project: m[2]}
		if n, err := strconv.ParseInt(m[3], 10, 64); err == nil && n > 0 {
			switch {
			case n >= idThreshold:
				r.ID = ID(n)
				r.Project = ""
			case typ == RefNote:
				r.Key = m[3] // notes have no numbers; a small number is a slug
			default:
				r.Number = n
			}
		} else if typ == RefNote {
			r.Key = m[3]
		} else {
			continue // cards are referenced by number or id only
		}
		out = append(out, r)
	}
	return out
}

// RefLink is a resolved reference stored for backlinks: the source that
// mentions and the target it points at, both by id.
type RefLink struct {
	SourceType SubjectType
	SourceID   ID
	TargetType SubjectType
	TargetID   ID
}
