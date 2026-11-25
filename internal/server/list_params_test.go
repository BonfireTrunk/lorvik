package server

import (
	"maps"
	"reflect"
	"strconv"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// paramTags are the tags of one query parameter a client depends on.
type paramTags struct{ enum, min, max, def string }

// queryParams flattens a struct's query parameters, embedded structs
// included.
func queryParams(t reflect.Type) map[string]paramTags {
	out := map[string]paramTags{}
	for f := range t.Fields() {
		if f.Anonymous {
			maps.Copy(out, queryParams(f.Type))
			continue
		}
		name := f.Tag.Get("query")
		if name == "" {
			continue
		}
		out[name] = paramTags{f.Tag.Get("enum"), f.Tag.Get("minimum"), f.Tag.Get("maximum"), f.Tag.Get("default")}
	}
	return out
}

// The client builds its query strings from apitypes, and the server binds
// them with its own route inputs. The two must name the same parameters
// with the same enums and limits, or a client sends what the server drops.
func TestCardListParamsMatchTheRoutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		client, wire reflect.Type
	}{
		{"project list", reflect.TypeFor[apitypes.ListCardsParams](), reflect.TypeFor[listCardsIn]()},
		{"all cards", reflect.TypeFor[apitypes.ListAllCardsParams](), reflect.TypeFor[listAllCardsIn]()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client, route := queryParams(tt.client), queryParams(tt.wire)
			for name, tags := range client {
				if got, ok := route[name]; !ok {
					t.Errorf("client sends %s, the route does not bind it", name)
				} else if name != "parent" && got != tags {
					t.Errorf("%s: client tags %+v, route tags %+v", name, tags, got)
				}
			}
			for name := range route {
				if _, ok := client[name]; !ok {
					t.Errorf("route binds %s, the client cannot send it", name)
				}
			}
		})
	}
}

// Every list's limit tag stops at apitypes.MaxPageSize, and the server's own
// remaining cap and token prefix length are the ones clients are told about.
func TestPagingLimitsMatchTheWireConstants(t *testing.T) {
	t.Parallel()
	if query.RemainingCap != apitypes.RemainingCap {
		t.Errorf("query.RemainingCap = %d, apitypes.RemainingCap = %d", query.RemainingCap, apitypes.RemainingCap)
	}
	if domain.TokenPrefixLen != apitypes.TokenPrefixLen {
		t.Errorf("domain.TokenPrefixLen = %d, apitypes.TokenPrefixLen = %d", domain.TokenPrefixLen, apitypes.TokenPrefixLen)
	}
	for _, typ := range []reflect.Type{
		reflect.TypeFor[listCardsIn](), reflect.TypeFor[listAllCardsIn](),
		reflect.TypeFor[apitypes.ListCardsParams](), reflect.TypeFor[apitypes.ListAllCardsParams](),
		reflect.TypeFor[ListPage](),
	} {
		if got := queryParams(typ)["limit"].max; got != strconv.Itoa(apitypes.MaxPageSize) {
			t.Errorf("%s limit maximum = %q, want %d", typ, got, apitypes.MaxPageSize)
		}
	}
	if got := queryParams(reflect.TypeFor[ListPage]())["limit"].def; got != strconv.Itoa(apitypes.DefaultPageSize) {
		t.Errorf("ListPage limit default = %q, want %d", got, apitypes.DefaultPageSize)
	}
}
