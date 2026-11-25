package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// rawList reads any list page, keeping rows as sent so that two walks compare
// byte for byte.
type rawList struct {
	Items     []json.RawMessage `json:"items"`
	Next      string            `json:"next"`
	Remaining int               `json:"remaining"`
}

func joinRows(rows []json.RawMessage) string {
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, string(r))
	}
	return strings.Join(parts, "\n")
}

// walkList follows next from the first page until the list stops offering it.
func walkList(e *testEnv, secret, path string, limit int) []json.RawMessage {
	e.t.Helper()
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	var out []json.RawMessage
	url := fmt.Sprintf("%s%slimit=%d", path, sep, limit)
	for range 1000 {
		pg := call[rawList](e, secret, http.MethodGet, url, nil, http.StatusOK)
		out = append(out, pg.Items...)
		if pg.Next == "" {
			return out
		}
		url = fmt.Sprintf("%s%slimit=%d&cursor=%s", path, sep, limit, pg.Next)
	}
	e.t.Fatal("next never stopped")
	return nil
}

// Each list holds more rows than the default page, all created at one instant
// where that is a sort key, so a keyset without its tiebreak loses rows.
func TestEveryListRoutePagesWithoutLossOrRepeat(t *testing.T) {
	t.Parallel()
	const seeded = 60
	lists := []struct {
		name, path string
		seed       func(e *testEnv, i int, now time.Time)
	}{
		{"projects", "/projects", func(e *testEnv, i int, _ time.Time) {
			call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: fmt.Sprintf("p-%02d", i), Name: "P"}, http.StatusCreated)
		}},
		{"project members", "/projects/lorvik/members", func(e *testEnv, _ int, now time.Time) {
			if _, err := e.fs.PutProjectMember(t.Context(), domain.ProjectMember{
				ProjectID: e.proj.ID, Actor: domain.ActorRef{Kind: domain.ActorUser, ID: e.ids.New()}, Role: domain.RoleMember, Status: domain.RecordActive, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{"members", "/members", func(e *testEnv, _ int, now time.Time) {
			if _, err := e.fs.PutMembership(t.Context(), domain.Membership{
				OrgID: e.org.ID, Actor: domain.ActorRef{Kind: domain.ActorUser, ID: e.ids.New()}, Role: domain.RoleMember, Status: domain.RecordActive, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{"agents", "/agents", func(e *testEnv, i int, now time.Time) {
			if _, err := e.fs.CreateAgent(t.Context(), domain.Agent{
				ID: e.ids.New(), OrgID: e.org.ID, Name: fmt.Sprintf("bot-%d", i), Harness: domain.HarnessOther, CreatedBy: e.user.ID, Status: domain.RecordActive, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{"tokens", "/tokens", func(e *testEnv, i int, now time.Time) {
			if _, err := e.fs.CreateToken(t.Context(), domain.Token{
				ID: e.ids.New(), OrgID: e.org.ID, Actor: e.user, Name: fmt.Sprintf("t-%d", i), Prefix: "lvk", Hash: fmt.Appendf(nil, "hash-%d", i), Status: domain.RecordActive, ExpiresAt: now.Add(time.Hour), CreatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{"live claims", "/claims", func(e *testEnv, i int, _ time.Time) {
			c := e.readyCard(userSecret, fmt.Sprintf("claimed %d", i))
			e.claimAs(agentSecret, "lorvik", c.Number, fmt.Sprintf("s-%02d", i%7))
		}},
		{"locks", "/locks", func(e *testEnv, i int, now time.Time) {
			if err := e.fs.InsertLock(t.Context(), domain.Lock{
				ID: e.ids.New(), OrgID: e.org.ID, Name: fmt.Sprintf("lock-%02d", i), Holder: e.user, AcquiredAt: now, ExpiresAt: now.Add(time.Hour),
			}); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, l := range lists {
		t.Run(l.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			now := e.svc.now()
			for i := range seeded {
				l.seed(e, i, now)
			}
			full := call[rawList](e, userSecret, http.MethodGet, l.path+"?limit=200", nil, http.StatusOK)
			if len(full.Items) < seeded || full.Next != "" || full.Remaining != 0 {
				t.Fatalf("limit=200: %d rows, next %q, remaining %d; want at least %d and nothing more", len(full.Items), full.Next, full.Remaining, seeded)
			}
			first := call[rawList](e, userSecret, http.MethodGet, l.path, nil, http.StatusOK)
			if len(first.Items) != 50 || first.Next == "" || first.Remaining != len(full.Items)-50 {
				t.Errorf("no limit: %d rows, next %q, remaining %d; want 50 and %d more", len(first.Items), first.Next, first.Remaining, len(full.Items)-50)
			}
			if got := walkList(e, userSecret, l.path, 7); joinRows(got) != joinRows(full.Items) {
				t.Errorf("walking 7 at a time gave %d rows, not the %d of one read in the same order", len(got), len(full.Items))
			}
			for _, limit := range []string{"0", "201"} {
				e.problem(userSecret, http.MethodGet, l.path+"?limit="+limit, nil, http.StatusUnprocessableEntity)
			}
			e.problem(userSecret, http.MethodGet, l.path+"?cursor=garbage", nil, http.StatusBadRequest)
			foreign := cursor{List: cardList}.encode()
			e.problem(userSecret, http.MethodGet, l.path+"?cursor="+foreign, nil, http.StatusBadRequest)
		})
	}
}

// Permission checks read every project, so one past the first page of the
// route is as reachable as any.
func TestAProjectPastTheFirstPageStaysReachable(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for i := range 60 {
		call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: fmt.Sprintf("p-%02d", i), Name: "P"}, http.StatusCreated)
	}
	const last = "zz-last"
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: last, Name: "Last"}, http.StatusCreated)
	card := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/"+last+"/cards", apitypes.CreateCard{Title: "needle", Status: "ready"}, http.StatusCreated)

	for _, p := range call[apitypes.ProjectList](e, agentSecret, http.MethodGet, "/projects", nil, http.StatusOK).Items {
		if p.Slug == last {
			t.Fatalf("%s is on the first page, so this test proves nothing", last)
		}
	}
	call[apitypes.Project](e, agentSecret, http.MethodGet, "/projects/"+last, nil, http.StatusOK)
	call[apitypes.Card](e, agentSecret, http.MethodGet, fmt.Sprintf("/projects/%s/cards/%d", last, card.Number), nil, http.StatusOK)
	if hits := call[apitypes.SearchResult](e, agentSecret, http.MethodGet, "/search?q=needle", nil, http.StatusOK); len(hits.Items) != 1 {
		t.Errorf("search found %d hits, want the card in the project past the first page", len(hits.Items))
	}
}

// Private projects sort after every visible one here, so a count taken before
// the visibility filter would promise pages that hold nothing.
func TestProjectPagesCountOnlyWhatTheCallerMaySee(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for i := range 3 {
		call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: fmt.Sprintf("zz-secret-%d", i), Name: "S", Visibility: "private"}, http.StatusCreated)
	}
	asOwner := call[apitypes.ProjectList](e, userSecret, http.MethodGet, "/projects?limit=200", nil, http.StatusOK).Items
	visible := call[apitypes.ProjectList](e, agentSecret, http.MethodGet, "/projects?limit=200", nil, http.StatusOK).Items
	if len(asOwner) != len(visible)+3 {
		t.Fatalf("owner sees %d projects, agent %d; want the agent short by the 3 private ones", len(asOwner), len(visible))
	}
	first := call[apitypes.ProjectList](e, agentSecret, http.MethodGet, "/projects?limit=1", nil, http.StatusOK)
	if first.Remaining != len(visible)-1 {
		t.Errorf("remaining = %d, want the %d visible projects after the first", first.Remaining, len(visible)-1)
	}
	for _, p := range visible {
		if strings.HasPrefix(p.Slug, "zz-secret") {
			t.Errorf("agent sees private project %s", p.Slug)
		}
	}
}

// A slug sorting before "global" still comes after it, so a cursor comparing
// slugs alone would serve the global project a second time.
func TestProjectPagesKeepTheGlobalProjectFirstAndOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "aaa", Name: "A"}, http.StatusCreated)
	var slugs []string
	for _, raw := range walkList(e, userSecret, "/projects", 1) {
		var p apitypes.Project
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		slugs = append(slugs, p.Slug)
	}
	if want := []string{"global", "aaa", "lorvik"}; !slices.Equal(slugs, want) {
		t.Errorf("walk = %v, want %v", slugs, want)
	}
}

func TestLockPagesSkipAndDoNotCountExpiredLocks(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	now := e.svc.now()
	for name, ttl := range map[string]time.Duration{"a": time.Hour, "b": -time.Minute, "c": time.Hour, "d": 0} {
		if err := e.fs.InsertLock(t.Context(), domain.Lock{ID: e.ids.New(), OrgID: e.org.ID, Name: name, Holder: e.user, AcquiredAt: now.Add(-time.Hour), ExpiresAt: now.Add(ttl)}); err != nil {
			t.Fatal(err)
		}
	}
	first := call[apitypes.LockList](e, userSecret, http.MethodGet, "/locks?limit=1", nil, http.StatusOK)
	if len(first.Items) != 1 || first.Items[0].Name != "a" || first.Remaining != 1 || first.Next == "" {
		t.Fatalf("first page = %+v, want a and one live lock after it", first)
	}
	second := call[apitypes.LockList](e, userSecret, http.MethodGet, "/locks?limit=1&cursor="+first.Next, nil, http.StatusOK)
	if len(second.Items) != 1 || second.Items[0].Name != "c" || second.Remaining != 0 || second.Next != "" {
		t.Errorf("second page = %+v, want c and the list over", second)
	}
}

func TestActorsTakeAtMostTheChunkClientsSend(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ids := make([]string, 0, apitypes.MaxActorIDs+1)
	for range apitypes.MaxActorIDs {
		ids = append(ids, e.ids.New().String())
	}
	call[apitypes.Actors](e, userSecret, http.MethodGet, "/actors?id="+strings.Join(ids, ","), nil, http.StatusOK)
	ids = append(ids, e.ids.New().String())
	e.problem(userSecret, http.MethodGet, "/actors?id="+strings.Join(ids, ","), nil, http.StatusUnprocessableEntity)
}

func TestACardEmbedsAtMostFiftyChildrenAndCountsThemAll(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		children  int
		remaining int
	}{
		{"exactly the embedded maximum", apitypes.MaxEmbeddedChildren, 0},
		{"one past it", apitypes.MaxEmbeddedChildren + 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			story := e.newCard(userSecret, apitypes.CreateCard{Title: "story", Kind: "story"})
			closed := e.newCard(userSecret, apitypes.CreateCard{Title: "closed", Parent: &story.Number, Status: "ready"})
			e.finish(closed.Number)
			var urgent apitypes.Card
			for i := range tt.children - 1 {
				in := apitypes.CreateCard{Title: fmt.Sprintf("child %d", i), Parent: &story.Number}
				// Created last and sorted first: the 50 are not the 50 oldest.
				if i == tt.children-2 {
					in.Priority = "urgent"
				}
				urgent = e.newCard(userSecret, in)
			}

			path := cardPathOf(story.Number, "")
			got := call[apitypes.Card](e, userSecret, http.MethodGet, path, nil, http.StatusOK)
			if len(got.Children) != apitypes.MaxEmbeddedChildren || got.ChildrenRemaining != tt.remaining {
				t.Errorf("children = %d, remaining %d; want %d and %d", len(got.Children), got.ChildrenRemaining, apitypes.MaxEmbeddedChildren, tt.remaining)
			}
			if got.Children[0].Number != urgent.Number {
				t.Errorf("first child = #%d, want the urgent #%d", got.Children[0].Number, urgent.Number)
			}
			if got.Progress == nil || got.Progress.Total != tt.children || got.Progress.Done != 1 {
				t.Errorf("progress = %+v, want %d children with 1 done", got.Progress, tt.children)
			}
			raw := call[map[string]any](e, userSecret, http.MethodGet, path, nil, http.StatusOK)
			if _, present := raw["children_remaining"]; present != (tt.remaining > 0) {
				t.Errorf("children_remaining present = %v with %d left out", present, tt.remaining)
			}
			all := call[apitypes.CardList](e, userSecret, http.MethodGet, fmt.Sprintf("/projects/lorvik/cards?parent=%d&limit=200", story.Number), nil, http.StatusOK)
			if len(all.Items) != tt.children {
				t.Errorf("?parent=%d holds %d cards, want all %d", story.Number, len(all.Items), tt.children)
			}
		})
	}
}
