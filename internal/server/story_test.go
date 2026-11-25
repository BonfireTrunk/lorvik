package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func (e *testEnv) story(title string) apitypes.Card {
	e.t.Helper()
	return e.newCard(userSecret, apitypes.CreateCard{Kind: "story", Title: title})
}

func (e *testEnv) child(story int64, title string) apitypes.Card {
	e.t.Helper()
	return e.newCard(agentSecret, apitypes.CreateCard{Title: title, Status: "ready", Parent: &story})
}

// finish claims a ready card as the agent and marks it done.
func (e *testEnv) finish(n int64) {
	e.t.Helper()
	e.claim(agentSecret, n)
	e.move(agentSecret, n, apitypes.TransitionCard{Status: "done"})
}

func TestStoryParentRules(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.story("Billing")
	other := e.story("Other")
	task := e.readyCard(agentSecret, "task")
	if s.Status != "backlog" || s.Kind != "story" {
		t.Fatalf("story = %+v, want kind story in backlog", s)
	}
	for _, in := range map[string]apitypes.CreateCard{
		"a task as parent":    {Title: "x", Parent: &task.Number},
		"a story with parent": {Kind: "story", Title: "x", Parent: &s.Number},
	} {
		e.problem(userSecret, http.MethodPost, "/projects/lorvik/cards", in, http.StatusConflict)
	}
	// A decision may sit under a story.
	d := e.newCard(agentSecret, apitypes.CreateCard{Kind: "decision", Title: "d", Parent: &s.Number})
	if d.Parent == nil || *d.Parent != s.Number {
		t.Errorf("decision = %+v, want it under the story", d)
	}
	// A story cannot be moved under another story.
	e.problem(userSecret, http.MethodPatch, cardPathOf(other.Number, ""), apitypes.UpdateCard{Version: other.Version, Parent: &s.Number}, http.StatusConflict)
}

func TestStoryGoesWorkingWithItsFirstChildAndCarriesProgress(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.story("Billing")
	a := e.child(s.Number, "a")
	b := e.child(s.Number, "b")
	if got := e.show(userSecret, s.Number); got.Status != "backlog" || got.Progress == nil || got.Progress.Done != 0 || got.Progress.Total != 2 {
		t.Errorf("before work = %+v / %+v, want backlog 0/2", got.Status, got.Progress)
	}
	e.claim(agentSecret, a.Number)
	got := e.show(userSecret, s.Number)
	if got.Status != "working" {
		t.Fatalf("story after first claim = %s, want working", got.Status)
	}
	e.move(agentSecret, a.Number, apitypes.TransitionCard{Status: "done"})
	e.move(userSecret, b.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "obsolete"})
	got = e.show(userSecret, s.Number)
	if got.Progress == nil || got.Progress.Done != 2 || got.Progress.Total != 2 {
		t.Errorf("progress = %+v, want dropped children counted as done (2/2)", got.Progress)
	}
	if len(got.Children) != 2 {
		t.Errorf("children = %+v, want both", got.Children)
	}
}

func TestStoryCloseNeedsTerminalChildrenAndAFold(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.story("Billing")
	a := e.child(s.Number, "a")
	b := e.child(s.Number, "b")
	e.finish(a.Number)

	p := e.moveFails(userSecret, s.Number, apitypes.TransitionCard{Status: "done", Fold: &apitypes.Fold{None: true}}, http.StatusConflict)
	if !strings.Contains(p.Hint+p.Detail, "#"+itoa(b.Number)) || strings.Contains(p.Hint+p.Detail, "#"+itoa(a.Number)+",") {
		t.Errorf("problem = %+v, want it to list the open child #%d", p, b.Number)
	}
	e.finish(b.Number)

	for name, fold := range map[string]*apitypes.Fold{
		"no fold":    nil,
		"empty fold": {},
		"both":       {Into: "docs/x.md", None: true},
	} {
		if p := e.moveFails(userSecret, s.Number, apitypes.TransitionCard{Status: "done", Fold: fold}, http.StatusUnprocessableEntity); !hasField(p, "fold") {
			t.Errorf("%s: errors = %+v, want fold", name, p.Errors)
		}
	}
	closed := e.move(userSecret, s.Number, apitypes.TransitionCard{Status: "done", Fold: &apitypes.Fold{Into: "docs/billing.md"}})
	if closed.Status != "done" || closed.Fold == nil || closed.Fold.Into != "docs/billing.md" || closed.Fold.By.ID != e.user.ID.String() {
		t.Fatalf("closed story = %+v", closed)
	}
	for _, n := range []int64{a.Number, b.Number} {
		if c := e.show(userSecret, n); c.Fold == nil || c.Fold.Into != "docs/billing.md" {
			t.Errorf("child #%d fold = %+v, want the story's mark", n, c.Fold)
		}
	}
	if got := e.eventsOf("card.folded"); len(got) != 1 || got[0]["into"] != "docs/billing.md" || got[0]["_subject"] != float64(s.Number) {
		t.Errorf("card.folded events = %v, want one for the story", got)
	}
}

func TestStoryFoldedIntoNothingIsRecordedWithNoDocument(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.story("Chore")
	e.finish(e.child(s.Number, "a").Number)
	closed := e.move(userSecret, s.Number, apitypes.TransitionCard{Status: "done", Fold: &apitypes.Fold{None: true}})
	if closed.Fold == nil || closed.Fold.Into != "" {
		t.Errorf("fold = %+v, want a mark with no document", closed.Fold)
	}
}

func TestClosedStoryIsClosed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.story("Billing")
	a := e.child(s.Number, "a")
	spare := e.readyCard(agentSecret, "spare")
	e.finish(a.Number)
	e.move(userSecret, s.Number, apitypes.TransitionCard{Status: "done", Fold: &apitypes.Fold{None: true}})

	e.problem(agentSecret, http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Title: "late", Parent: &s.Number}, http.StatusConflict)
	e.problem(userSecret, http.MethodPatch, cardPathOf(spare.Number, ""), apitypes.UpdateCard{Version: spare.Version, Parent: &s.Number}, http.StatusConflict)
	e.problem(userSecret, http.MethodPatch, cardPathOf(a.Number, ""), apitypes.UpdateCard{Version: e.show(userSecret, a.Number).Version, Parent: new(int64(0))}, http.StatusConflict)
	e.moveFails(userSecret, a.Number, apitypes.TransitionCard{Status: "ready"}, http.StatusConflict)
	e.moveFails(userSecret, s.Number, apitypes.TransitionCard{Status: "ready"}, http.StatusConflict)
	e.moveFails(userSecret, s.Number, apitypes.TransitionCard{Status: "working"}, http.StatusConflict)
}

func TestDroppingAStoryCascadesOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.story("Abandoned")
	a := e.child(s.Number, "a")
	b := e.child(s.Number, "b")
	d := e.newCard(agentSecret, apitypes.CreateCard{Kind: "decision", Title: "d", Parent: &s.Number})
	e.claim(agentSecret, a.Number)

	e.moveFails(userSecret, s.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "wontfix"}, http.StatusConflict)
	p := e.moveFails(userSecret, a.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "wontfix", Cascade: true}, http.StatusUnprocessableEntity)
	if !hasField(p, "cascade") {
		t.Errorf("cascade on a task: errors = %+v, want cascade", p.Errors)
	}

	got := e.move(userSecret, s.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "wontfix", Cascade: true})
	if got.Status != "dropped" || got.DropReason != "wontfix" {
		t.Fatalf("story = %+v", got)
	}
	ca := e.show(userSecret, a.Number)
	if ca.Status != "dropped" || ca.DropReason != "wontfix" || ca.Claim != nil {
		t.Errorf("claimed child = %+v, want dropped with the claim released", ca)
	}
	if cb := e.show(userSecret, b.Number); cb.Status != "dropped" {
		t.Errorf("child b = %+v", cb)
	}
	if cd := e.show(userSecret, d.Number); cd.Status != "dropped" || cd.DropReason != "rejected" {
		t.Errorf("decision child = %+v, want dropped as rejected", cd)
	}
	var cascaded int
	for _, ev := range e.eventsOf("card.status_changed") {
		if ev["cascade_of"] == float64(s.Number) {
			cascaded++
		}
	}
	if cascaded != 3 {
		t.Errorf("events marked cascade_of #%d = %d, want 3", s.Number, cascaded)
	}
}

func TestFoldRouteStampsADoneCardWithNoParent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	solo := e.readyCard(agentSecret, "solo")
	e.finish(solo.Number)
	s := e.story("S")
	kid := e.child(s.Number, "kid")
	open := e.readyCard(agentSecret, "open")
	fold := func(n int64, in apitypes.FoldCard, want int) {
		in.Version = e.show(userSecret, n).Version
		e.problem(userSecret, http.MethodPost, cardPathOf(n, "/fold"), in, want)
	}
	fold(solo.Number, apitypes.FoldCard{}, http.StatusUnprocessableEntity)
	fold(solo.Number, apitypes.FoldCard{Into: "a.md", None: true}, http.StatusUnprocessableEntity)
	fold(open.Number, apitypes.FoldCard{None: true}, http.StatusConflict)
	e.finish(kid.Number)
	fold(kid.Number, apitypes.FoldCard{None: true}, http.StatusConflict)

	got := call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(solo.Number, "/fold"),
		apitypes.FoldCard{Version: e.show(userSecret, solo.Number).Version, Into: "docs/solo.md"}, http.StatusOK)
	if got.Fold == nil || got.Fold.Into != "docs/solo.md" {
		t.Fatalf("folded = %+v", got.Fold)
	}
	fold(solo.Number, apitypes.FoldCard{None: true}, http.StatusConflict)
	if ev := e.eventsOf("card.folded"); len(ev) != 1 || ev[0]["_subject"] != float64(solo.Number) {
		t.Errorf("card.folded events = %v", ev)
	}
}

func TestUnfoldedListsDoneParentlessCardsNewestClosedFirstAndPages(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	var order []int64
	for _, title := range []string{"one", "two", "three"} {
		c := e.readyCard(agentSecret, title)
		e.advance(1)
		e.finish(c.Number)
		e.advance(1)
		order = append([]int64{c.Number}, order...)
	}
	folded := e.readyCard(agentSecret, "folded")
	e.finish(folded.Number)
	call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(folded.Number, "/fold"),
		apitypes.FoldCard{Version: e.show(userSecret, folded.Number).Version, None: true}, http.StatusOK)
	s := e.story("S")
	kid := e.child(s.Number, "kid")
	e.finish(kid.Number)
	e.readyCard(agentSecret, "still open")
	dropped := e.readyCard(agentSecret, "dropped")
	e.move(userSecret, dropped.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "obsolete"})

	var got []int64
	cursor := ""
	for range 5 {
		page := call[apitypes.CardList](e, userSecret, http.MethodGet, "/projects/lorvik/unfolded?limit=2&cursor="+cursor, nil, http.StatusOK)
		for _, c := range page.Items {
			got = append(got, c.Number)
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if len(got) != 3 || got[0] != order[0] || got[1] != order[1] || got[2] != order[2] {
		t.Errorf("unfolded = %v, want %v (done, no parent, not folded; newest closed first)", got, order)
	}
}

func TestListAllCardsSpansProjectsAndHidesPrivateOnes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "web", Name: "Web"}, http.StatusCreated)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "secret", Name: "Secret", Visibility: "private"}, http.StatusCreated)
	l := e.readyCard(agentSecret, "lorvik card")
	w := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/web/cards", apitypes.CreateCard{Title: "web card"}, http.StatusCreated)
	hidden := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/secret/cards", apitypes.CreateCard{Title: "hidden"}, http.StatusCreated)

	slugs := func(secret, query string) map[string]int64 {
		out := map[string]int64{}
		for _, c := range call[apitypes.CardList](e, secret, http.MethodGet, "/cards"+query, nil, http.StatusOK).Items {
			out[c.Project] = c.Number
		}
		return out
	}
	if got := slugs(userSecret, ""); len(got) != 3 || got["lorvik"] != l.Number || got["web"] != w.Number || got["secret"] != hidden.Number {
		t.Errorf("owner sees %v, want a card from each project with its slug", got)
	}
	if got := slugs(agentSecret, ""); len(got) != 2 || got["secret"] != 0 {
		t.Errorf("agent sees %v, want the private project hidden", got)
	}
	if got := slugs(userSecret, "?project=web"); len(got) != 1 || got["web"] != w.Number {
		t.Errorf("project filter = %v", got)
	}
	p := e.problem(userSecret, http.MethodGet, "/cards?parent=1", nil, http.StatusUnprocessableEntity)
	if !hasField(p, "parent") {
		t.Errorf("errors = %+v, want parent", p.Errors)
	}
	page := call[apitypes.CardList](e, userSecret, http.MethodGet, "/cards?limit=2", nil, http.StatusOK)
	if len(page.Items) != 2 || page.Next == "" {
		t.Fatalf("first page = %+v", page)
	}
	rest := call[apitypes.CardList](e, userSecret, http.MethodGet, "/cards?limit=2&cursor="+page.Next, nil, http.StatusOK)
	if len(rest.Items) != 1 || rest.Items[0].ID == page.Items[0].ID || rest.Items[0].ID == page.Items[1].ID {
		t.Errorf("second page = %+v, want the remaining card", rest.Items)
	}
}

func TestStoryWithOnlyADecisionChildClosesFromReady(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.story("Pick a store")
	e.move(userSecret, s.Number, apitypes.TransitionCard{Status: "ready"})
	d := e.newCard(agentSecret, apitypes.CreateCard{Kind: "decision", Title: "Use pgx", Parent: &s.Number})
	e.moveFails(userSecret, s.Number, apitypes.TransitionCard{Status: "done", Fold: &apitypes.Fold{None: true}}, http.StatusConflict)
	e.move(userSecret, d.Number, apitypes.TransitionCard{Status: "done"})
	if got := e.show(userSecret, s.Number); got.Status != "ready" {
		t.Fatalf("story = %s, want ready: a decision child never starts it", got.Status)
	}
	e.moveFails(userSecret, s.Number, apitypes.TransitionCard{Status: "done"}, http.StatusUnprocessableEntity)
	closed := e.move(userSecret, s.Number, apitypes.TransitionCard{Status: "done", Fold: &apitypes.Fold{Into: "docs/store.md"}})
	if closed.Status != "done" || closed.Fold == nil || closed.Fold.Into != "docs/store.md" {
		t.Errorf("closed = %+v", closed)
	}
	if c := e.show(userSecret, d.Number); c.Fold == nil {
		t.Errorf("decision child = %+v, want the story's fold mark", c.Fold)
	}
}

func TestStoryIsNeverClaimedNorMovedToWorking(t *testing.T) {
	t.Parallel()
	const hint = "a story is not worked: start one of its children"
	e := newEnv(t)
	s := e.story("Billing")
	e.move(userSecret, s.Number, apitypes.TransitionCard{Status: "ready"})
	if p := e.problem(userSecret, http.MethodPost, cardPathOf(s.Number, "/claim"), apitypes.ClaimCard{}, http.StatusConflict); p.Hint != hint {
		t.Errorf("claim hint = %q, want %q", p.Hint, hint)
	}
	if p := e.moveFails(userSecret, s.Number, apitypes.TransitionCard{Status: "working"}, http.StatusConflict); p.Hint != hint {
		t.Errorf("transition hint = %q, want %q", p.Hint, hint)
	}
	if got := e.show(userSecret, s.Number); got.Status != "ready" || got.Claim != nil {
		t.Errorf("story = %+v, want ready and unclaimed", got)
	}
}

func TestSweeperLeavesAWorkingStoryAloneWhenAChildClaimExpires(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.story("Billing")
	a := e.child(s.Number, "a")
	e.claim(agentSecret, a.Number)
	if got := e.show(userSecret, s.Number); got.Status != "working" {
		t.Fatalf("story = %s, want working once the child started", got.Status)
	}
	e.advance(31 * time.Minute)
	if _, err := e.svc.sweepOnce(context.Background(), e.clock()); err != nil {
		t.Fatal(err)
	}
	if got := e.show(userSecret, a.Number); got.Status != "ready" || got.Claim != nil {
		t.Errorf("child = %+v, want ready and unclaimed", got)
	}
	if got := e.show(userSecret, s.Number); got.Status != "working" {
		t.Errorf("story = %s, want it to stay working", got.Status)
	}
}

func TestClosingNoteIsKeptUntilReopened(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.story("Notes")
	a := e.child(s.Number, "a")
	e.claim(agentSecret, a.Number)
	e.move(agentSecret, a.Number, apitypes.TransitionCard{Status: "done", Note: "  shipped in abc123 "})
	if got := e.show(userSecret, a.Number); got.ClosingNote != "shipped in abc123" {
		t.Fatalf("closing_note after done = %q, want the trimmed note", got.ClosingNote)
	}
	e.move(userSecret, a.Number, apitypes.TransitionCard{Status: "ready"})
	if got := e.show(userSecret, a.Number); got.ClosingNote != "" {
		t.Fatalf("closing_note after reopen = %q, want empty", got.ClosingNote)
	}
	e.move(userSecret, a.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "obsolete", Note: "superseded by b"})
	if got := e.show(userSecret, a.Number); got.ClosingNote != "superseded by b" {
		t.Fatalf("closing_note after drop = %q, want the drop note", got.ClosingNote)
	}
}

func TestStoryClosesFromWaitingButNotBackToBacklogOrWorking(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   apitypes.TransitionCard
		want int
	}{
		{"done with a fold", apitypes.TransitionCard{Status: "done", Fold: &apitypes.Fold{None: true}}, http.StatusOK},
		{"dropped", apitypes.TransitionCard{Status: "dropped", DropReason: "obsolete"}, http.StatusOK},
		{"backlog", apitypes.TransitionCard{Status: "backlog"}, http.StatusConflict},
		{"working", apitypes.TransitionCard{Status: "working"}, http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			s := e.story("Waiting story")
			e.move(userSecret, s.Number, apitypes.TransitionCard{Status: "ready"})
			e.move(userSecret, s.Number, apitypes.TransitionCard{Status: "waiting"})
			if tt.want == http.StatusOK {
				got := e.move(userSecret, s.Number, tt.in)
				if got.Status != tt.in.Status {
					t.Errorf("story = %s, want %s", got.Status, tt.in.Status)
				}
				return
			}
			e.moveFails(userSecret, s.Number, tt.in, tt.want)
			if got := e.show(userSecret, s.Number); got.Status != "waiting" {
				t.Errorf("story = %s, want it to stay waiting after the refusal", got.Status)
			}
		})
	}
}

func TestListAllCardsDropsArchivedProjects(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "old", Name: "Old"}, http.StatusCreated)
	live := e.readyCard(agentSecret, "live card")
	old := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/old/cards", apitypes.CreateCard{Title: "old card"}, http.StatusCreated)
	call[apitypes.Project](e, userSecret, http.MethodPatch, "/projects/old", apitypes.UpdateProject{Status: new("archived")}, http.StatusOK)

	for _, path := range []string{"/cards", "/cards?project=all"} {
		got := map[string]int64{}
		for _, c := range call[apitypes.CardList](e, userSecret, http.MethodGet, path, nil, http.StatusOK).Items {
			got[c.Project] = c.Number
		}
		if len(got) != 1 || got["lorvik"] != live.Number {
			t.Errorf("GET %s = %v, want only the live project's card", path, got)
		}
	}
	named := call[apitypes.CardList](e, userSecret, http.MethodGet, "/cards?project=old", nil, http.StatusOK)
	if len(named.Items) != 1 || named.Items[0].Number != old.Number {
		t.Errorf("GET /cards?project=old = %+v, want the archived project still readable by name", named.Items)
	}
}

func TestTransitionRefusalsFollowTheDocumentedOrder(t *testing.T) {
	t.Parallel()
	t.Run("a project the caller cannot see is 404 before a stale version or an illegal edge", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "secret", Name: "Secret", Visibility: "private"}, http.StatusCreated)
		c := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/secret/cards", apitypes.CreateCard{Title: "hidden"}, http.StatusCreated)
		e.problem(agentSecret, http.MethodPost, "/projects/secret/cards/"+strconv.FormatInt(c.Number, 10)+"/transition",
			apitypes.TransitionCard{Version: c.Version + 7, Status: "done"}, http.StatusNotFound)
	})
	t.Run("the move permission is 403 before a stale version", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		d := e.decision(agentSecret, "Use pgx", "ready")
		e.problem(agentSecret, http.MethodPost, cardPathOf(d.Number, "/transition"),
			apitypes.TransitionCard{Version: d.Version + 7, Status: "done"}, http.StatusForbidden)
	})
	t.Run("a stale version is 409 with current_version before the drop reason's 422", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		c := e.readyCard(agentSecret, "task")
		p := e.problem(agentSecret, http.MethodPost, cardPathOf(c.Number, "/transition"),
			apitypes.TransitionCard{Version: c.Version + 7, Status: "backlog", DropReason: "obsolete"}, http.StatusConflict)
		if p.CurrentVersion != c.Version {
			t.Errorf("current_version = %d, want %d", p.CurrentVersion, c.Version)
		}
	})
	t.Run("working on a story is refused with the story hint before the table's illegal edge", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		s := e.story("Billing")
		p := e.moveFails(userSecret, s.Number, apitypes.TransitionCard{Status: "working"}, http.StatusConflict)
		if !strings.Contains(p.Hint, "a story is not worked") {
			t.Errorf("hint = %q, want the story-working hint, not the table's", p.Hint)
		}
	})
	t.Run("reopening in a closed story is 409 before the revoked decision's 422", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		s := e.story("Pick a store")
		d := e.newCard(agentSecret, apitypes.CreateCard{Kind: "decision", Title: "d", Parent: &s.Number})
		e.move(userSecret, d.Number, apitypes.TransitionCard{Status: "done"})
		e.move(userSecret, d.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "revoked"})
		e.move(userSecret, s.Number, apitypes.TransitionCard{Status: "ready"})
		e.move(userSecret, s.Number, apitypes.TransitionCard{Status: "done", Fold: &apitypes.Fold{None: true}})
		e.moveFails(userSecret, d.Number, apitypes.TransitionCard{Status: "ready"}, http.StatusConflict)
	})
	t.Run("open children are 409 before the missing fold's 422", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		s := e.story("Billing")
		e.child(s.Number, "open")
		e.move(userSecret, s.Number, apitypes.TransitionCard{Status: "ready"})
		e.moveFails(userSecret, s.Number, apitypes.TransitionCard{Status: "done"}, http.StatusConflict)
	})
}

func TestReleasedOrSweptChildKeepsItsStoryAndCountsAsNotDone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		undo func(e *testEnv, n int64)
	}{
		{"released by the claimant", func(e *testEnv, n int64) {
			call[apitypes.Card](e, agentSecret, http.MethodPost, cardPathOf(n, "/release"), apitypes.ClaimCard{}, http.StatusOK)
		}},
		{"moved back to ready", func(e *testEnv, n int64) {
			e.move(agentSecret, n, apitypes.TransitionCard{Status: "ready"})
		}},
		{"swept after the lease expires", func(e *testEnv, _ int64) {
			e.advance(31 * time.Minute)
			if _, err := e.svc.sweepOnce(context.Background(), e.clock()); err != nil {
				e.t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			s := e.story("Billing")
			finished := e.child(s.Number, "finished")
			working := e.child(s.Number, "working")
			e.finish(finished.Number)
			e.claim(agentSecret, working.Number)

			tt.undo(e, working.Number)

			got := e.show(userSecret, working.Number)
			if got.Status != "ready" || got.Claim != nil || got.Parent == nil || *got.Parent != s.Number {
				t.Errorf("child = %+v, want ready, unclaimed and still under #%d", got, s.Number)
			}
			st := e.show(userSecret, s.Number)
			if st.Progress == nil || st.Progress.Done != 1 || st.Progress.Total != 2 {
				t.Errorf("progress = %+v, want 1 of 2", st.Progress)
			}
		})
	}
}

func TestLastBlockerSettlingReturnsAWaitingCardToReady(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (e *testEnv, target apitypes.Card, blockers [2]apitypes.Card) {
		e = newEnv(t)
		target = e.readyCard(agentSecret, "target")
		for i := range blockers {
			blockers[i] = e.readyCard(agentSecret, "blocker")
			call[apitypes.Card](e, agentSecret, http.MethodPost, cardPathOf(blockers[i].Number, "/links"),
				apitypes.LinkCards{Kind: "blocks", To: target.Number}, http.StatusOK)
		}
		if got := e.show(userSecret, target.Number); got.Status != "waiting" {
			t.Fatalf("target = %s, want waiting while blocked", got.Status)
		}
		return e, target, blockers
	}
	t.Run("done on the last of two blockers", func(t *testing.T) {
		t.Parallel()
		e, target, b := setup(t)
		e.finish(b[0].Number)
		if got := e.show(userSecret, target.Number); got.Status != "waiting" {
			t.Fatalf("target = %s after one of two blockers, want waiting", got.Status)
		}
		e.finish(b[1].Number)
		if got := e.show(userSecret, target.Number); got.Status != "ready" {
			t.Errorf("target = %s after the last blocker, want ready", got.Status)
		}
		var unblocked []map[string]any
		for _, ev := range e.eventsOf("card.status_changed") {
			if ev["_subject"] == float64(target.Number) && ev["to"] == "ready" {
				unblocked = append(unblocked, ev)
			}
		}
		if len(unblocked) != 1 || unblocked[0]["unblocked_by"] != float64(b[1].Number) {
			t.Errorf("unblock events = %v, want one naming #%d", unblocked, b[1].Number)
		}
	})
	t.Run("dropping the blockers' story with cascade", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		s := e.story("Abandoned")
		target := e.readyCard(agentSecret, "target")
		in := e.child(s.Number, "in story")
		out := e.readyCard(agentSecret, "outside")
		for _, b := range []apitypes.Card{in, out} {
			call[apitypes.Card](e, agentSecret, http.MethodPost, cardPathOf(b.Number, "/links"), apitypes.LinkCards{Kind: "blocks", To: target.Number}, http.StatusOK)
		}
		e.move(userSecret, s.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "obsolete", Cascade: true})
		if got := e.show(userSecret, target.Number); got.Status != "waiting" {
			t.Fatalf("target = %s, want waiting while the outside blocker is open", got.Status)
		}
		e.move(userSecret, out.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "obsolete"})
		if got := e.show(userSecret, target.Number); got.Status != "ready" {
			t.Errorf("target = %s, want ready once the last blocker is dropped", got.Status)
		}
	})
}

func TestDecisionGetsAParentThroughPatchAndShowsUnderItsStatus(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	s := e.story("Pick a store")
	d := e.decision(agentSecret, "Use pgx", "ready")
	if d.Parent != nil {
		t.Fatalf("decision parent = %v, want none before the patch", *d.Parent)
	}
	patched := call[apitypes.Card](e, agentSecret, http.MethodPatch, cardPathOf(d.Number, ""), apitypes.UpdateCard{Version: d.Version, Parent: &s.Number}, http.StatusOK)
	if patched.Parent == nil || *patched.Parent != s.Number {
		t.Fatalf("patched = %+v, want parent #%d", patched, s.Number)
	}
	children := e.show(userSecret, s.Number).Children
	if len(children) != 1 || children[0].Number != d.Number || children[0].Kind != "decision" || children[0].Status != "ready" {
		t.Fatalf("children = %+v, want the decision as ready", children)
	}
	e.move(userSecret, d.Number, apitypes.TransitionCard{Status: "done"})
	if children := e.show(userSecret, s.Number).Children; len(children) != 1 || children[0].Status != "done" {
		t.Errorf("children = %+v, want the decision shown as done", children)
	}
}

func TestDoneCarriesAFoldOnlyForACardWithoutAParent(t *testing.T) {
	t.Parallel()
	into := &apitypes.Fold{Into: "docs/solo.md"}
	t.Run("a parentless card done with a document is folded and one card.folded event is written", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		c := e.readyCard(agentSecret, "solo")
		e.claim(agentSecret, c.Number)
		got := e.move(agentSecret, c.Number, apitypes.TransitionCard{Status: "done", Fold: into})
		if got.Status != "done" || got.Fold == nil || got.Fold.Into != "docs/solo.md" || got.Fold.By.ID != e.agent.ID.String() {
			t.Fatalf("card = %+v, want done and folded into docs/solo.md by the agent", got)
		}
		if ev := e.eventsOf("card.folded"); len(ev) != 1 || ev[0]["_subject"] != float64(c.Number) {
			t.Errorf("card.folded events = %v", ev)
		}
	})
	t.Run("none folds with no document", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		c := e.readyCard(agentSecret, "solo")
		e.claim(agentSecret, c.Number)
		got := e.move(agentSecret, c.Number, apitypes.TransitionCard{Status: "done", Fold: &apitypes.Fold{None: true}})
		if got.Fold == nil || got.Fold.Into != "" {
			t.Errorf("fold = %+v, want a mark with no document", got.Fold)
		}
	})
	t.Run("no fold leaves the card unfolded", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		c := e.readyCard(agentSecret, "solo")
		e.finish(c.Number)
		if got := e.show(userSecret, c.Number); got.Fold != nil {
			t.Errorf("fold = %+v, want none", got.Fold)
		}
		if ev := e.eventsOf("card.folded"); len(ev) != 0 {
			t.Errorf("card.folded events = %v, want none", ev)
		}
	})
	t.Run("neither or both of into and none is a 422 on fold", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		c := e.readyCard(agentSecret, "solo")
		e.claim(agentSecret, c.Number)
		for _, f := range []*apitypes.Fold{{}, {Into: "a.md", None: true}} {
			if p := e.moveFails(agentSecret, c.Number, apitypes.TransitionCard{Status: "done", Fold: f}, http.StatusUnprocessableEntity); !hasField(p, "fold") {
				t.Errorf("fold %+v: problem = %+v, want a fold field error", f, p)
			}
		}
	})
	t.Run("a card with a parent is refused with the story hint", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		s := e.story("S")
		kid := e.child(s.Number, "kid")
		e.claim(agentSecret, kid.Number)
		p := e.moveFails(agentSecret, kid.Number, apitypes.TransitionCard{Status: "done", Fold: into}, http.StatusUnprocessableEntity)
		if !hasField(p, "fold") || !strings.Contains(p.Hint, "fold the story instead") {
			t.Errorf("problem = %+v, want a fold field error with the story hint", p)
		}
		if got := e.show(userSecret, kid.Number); got.Status != "working" {
			t.Errorf("status = %s, want working: the refusal moves nothing", got.Status)
		}
	})
	t.Run("a story still needs its fold", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		s := e.story("S")
		e.move(userSecret, s.Number, apitypes.TransitionCard{Status: "ready"})
		if p := e.moveFails(userSecret, s.Number, apitypes.TransitionCard{Status: "done"}, http.StatusUnprocessableEntity); !hasField(p, "fold") {
			t.Errorf("problem = %+v, want a fold field error", p)
		}
	})
	t.Run("a fold on a move other than done is refused", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		c := e.readyCard(agentSecret, "solo")
		p := e.moveFails(agentSecret, c.Number, apitypes.TransitionCard{Status: "backlog", Fold: into}, http.StatusUnprocessableEntity)
		if !hasField(p, "fold") {
			t.Errorf("problem = %+v, want a fold field error", p)
		}
	})
}
