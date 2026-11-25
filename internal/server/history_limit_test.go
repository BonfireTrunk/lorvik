package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

// Each history list holds one row more than the default page, so a list that
// returned everything, or that capped below what was asked, fails on length.
func TestHistoryListsDefaultToFiftyAndRefuseAnUnboundedRead(t *testing.T) {
	t.Parallel()
	const rows = 51
	lists := []struct {
		name string
		seed func(e *testEnv) string
		ids  func(e *testEnv, path string) []string
	}{
		{
			name: "comments",
			seed: func(e *testEnv) string {
				c := e.readyCard(agentSecret, "task")
				for i := range rows {
					call[apitypes.Comment](e, agentSecret, http.MethodPost, cardPathOf(c.Number, "/comments"), apitypes.CreateComment{Body: fmt.Sprint(i)}, http.StatusCreated)
				}
				return cardPathOf(c.Number, "/comments")
			},
			ids: func(e *testEnv, path string) []string {
				var out []string
				for _, h := range call[[]apitypes.Comment](e, agentSecret, http.MethodGet, path, nil, http.StatusOK) {
					out = append(out, h.ID)
				}
				return out
			},
		},
		{
			name: "card revisions",
			seed: func(e *testEnv) string {
				c := e.readyCard(agentSecret, "task")
				for i := range rows {
					c = call[apitypes.Card](e, agentSecret, http.MethodPatch, cardPathOf(c.Number, ""), apitypes.UpdateCard{Version: c.Version, Body: new(fmt.Sprint(i))}, http.StatusOK)
				}
				return cardPathOf(c.Number, "/revisions")
			},
			ids: func(e *testEnv, path string) []string {
				var out []string
				for _, r := range call[[]apitypes.CardRevision](e, agentSecret, http.MethodGet, path, nil, http.StatusOK) {
					out = append(out, fmt.Sprint(r.Version))
				}
				return out
			},
		},
		{
			name: "note revisions",
			seed: func(e *testEnv) string {
				n := call[apitypes.Note](e, agentSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "lorvik", Slug: "paged", Title: "Paged", Body: "start"}, http.StatusCreated)
				for i := range rows {
					n = call[apitypes.Note](e, agentSecret, http.MethodPatch, "/notes/paged?project=lorvik", apitypes.UpdateNote{Version: n.Version, Body: new(fmt.Sprint(i))}, http.StatusOK)
				}
				return "/notes/paged/revisions?project=lorvik"
			},
			ids: func(e *testEnv, path string) []string {
				var out []string
				for _, r := range call[[]apitypes.NoteRevision](e, agentSecret, http.MethodGet, path, nil, http.StatusOK) {
					out = append(out, fmt.Sprint(r.Version))
				}
				return out
			},
		},
	}
	for _, l := range lists {
		t.Run(l.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			path := l.seed(e)
			sep := "?"
			if strings.Contains(path, "?") {
				sep = "&"
			}
			page := l.ids(e, path)
			if len(page) != 50 {
				t.Fatalf("no limit returned %d rows, want the default 50", len(page))
			}
			if got := l.ids(e, path+sep+"limit=200"); len(got) != rows {
				t.Errorf("limit=200 returned %d rows, want all %d", len(got), rows)
			} else if got[0] != page[0] || got[49] != page[49] {
				t.Errorf("default page %v is not the newest of %v", page, got)
			}
			if older := l.ids(e, path+sep+"before="+page[49]); len(older) != 1 {
				t.Errorf("before the default page's last row: %d rows, want the 1 left", len(older))
			}
			for _, limit := range []string{"0", "201"} {
				e.problem(agentSecret, http.MethodGet, path+sep+"limit="+limit, nil, http.StatusUnprocessableEntity)
			}
		})
	}
}
