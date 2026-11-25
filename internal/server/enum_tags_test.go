package server

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// labels lists every name an enumeration's String gives, in value order. An
// unknown value prints as "what(n)", so a parenthesis marks a gap.
func labels[T ~int16](str func(T) string) []string {
	var out []string
	for v := -128; v < 128; v++ {
		if s := str(T(v)); !strings.Contains(s, "(") {
			out = append(out, s)
		}
	}
	return out
}

func enumTag(t *testing.T, v any, field string) []string {
	t.Helper()
	f, ok := reflect.TypeOf(v).FieldByName(field)
	if !ok {
		t.Fatalf("%T has no field %s", v, field)
	}
	return strings.Split(f.Tag.Get("enum"), ",")
}

// The OpenAPI enums on the wire types are hand-written; a domain value they
// omit makes validating clients reject a real answer, as happened with lock
// events. Each tag must list exactly the domain's names; "" on either side
// only marks an optional field or a zero value and is not compared.
func TestWireEnumsMatchDomain(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		wire   any
		field  string
		domain []string
	}{
		{"event subject", apitypes.Subject{}, "Type", labels(domain.SubjectType.String)},
		{"card kind", apitypes.Card{}, "Kind", labels(domain.CardKind.String)},
		{"card status", apitypes.Card{}, "Status", labels(domain.Status.String)},
		{"card drop reason", apitypes.Card{}, "DropReason", labels(domain.DropReason.String)},
		{"card priority", apitypes.Card{}, "Priority", labels(domain.Priority.String)},
		{"link kind", apitypes.CardLink{}, "Kind", labels(domain.CardLinkKind.String)},
		{"project visibility", apitypes.Project{}, "Visibility", labels(domain.Visibility.String)},
		{"project concurrency", apitypes.Project{}, "Concurrency", labels(domain.Concurrency.String)},
		{"project scope", apitypes.Project{}, "Scope", labels(domain.ProjectScope.String)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := slices.DeleteFunc(enumTag(t, tt.wire, tt.field), func(s string) bool { return s == "" })
			want := slices.DeleteFunc(slices.Clone(tt.domain), func(s string) bool { return s == "" })
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("%T.%s enum = %v, domain has %v", tt.wire, tt.field, got, want)
			}
		})
	}
}
