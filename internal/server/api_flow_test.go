package server

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

func TestAnswerWithDecideCreatesAcceptedDecisionCardAndUnblocks(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task := e.readyCard(agentSecret, "Choose storage")
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "Postgres or SQLite?", Body: "Need multi-writer.", Blocks: []int64{task.Number}})
	rec := e.do(userSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: q.Version, Answer: "Postgres", Decide: true})
	if rec.Code != http.StatusOK {
		t.Fatalf("answer = %d: %s", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/projects/lorvik/cards/3" {
		t.Errorf("Location = %q, want the new decision card", loc)
	}
	d := e.show(agentSecret, 3)
	want := "## Context\n\nNeed multi-writer.\n\n## Decision\n\nPostgres\n"
	if d.Kind != "decision" || d.Status != "done" || d.Title != q.Title || d.Body != want || d.Decision == nil || d.Decision.AcceptedBy == nil || d.Decision.AcceptedBy.ID != e.user.ID.String() {
		t.Errorf("decision card = %+v, want an accepted decision built from the question", d)
	}
	if len(d.Links) != 1 || d.Links[0].Kind != "relates" || d.Links[0].Number != q.Number || d.Links[0].Direction != "out" {
		t.Errorf("decision links = %+v, want relates to the question", d.Links)
	}
	if got := e.show(agentSecret, task.Number); got.Status != "ready" || len(got.BlockedBy) != 0 {
		t.Errorf("unblocked task = %+v, want ready with no blockers", got)
	}
	answered := e.show(agentSecret, q.Number)
	if answered.Status != "done" || answered.Question.Answer != "Postgres" || answered.Question.AnsweredBy == nil || len(answered.Links) != 2 || answered.Links[1].Kind != "relates" || answered.Links[1].Direction != "in" {
		t.Errorf("answered question = %+v", answered)
	}
}

func TestAnswerKeepsCardWaitingWhileAnotherBlockerIsOpen(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task := e.readyCard(agentSecret, "t")
	q1 := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "q1", Blocks: []int64{task.Number}})
	e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "q2", Blocks: []int64{task.Number}})
	call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(q1.Number, "/answer"), apitypes.AnswerCard{Version: q1.Version, Answer: "yes"}, http.StatusOK)
	if got := e.show(agentSecret, task.Number); got.Status != "waiting" || len(got.BlockedBy) != 1 {
		t.Errorf("task = %+v, want still waiting behind q2", got)
	}
}

func TestAgentsAndMembersCannotAnswerQuestions(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "q"})
	e.problem(agentSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: 1, Answer: "x"}, http.StatusForbidden)
	e.problem(memberSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: 1, Answer: "x"}, http.StatusForbidden)
}

func TestNextSkipsBlockedAndClaimedCardsAndReturns404WhenNothingReady(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p := e.problem(agentSecret, http.MethodGet, "/projects/lorvik/cards/next", nil, http.StatusNotFound)
	if !strings.Contains(p.Hint, "lorvik add") {
		t.Errorf("hint = %q", p.Hint)
	}
	low := e.newCard(agentSecret, apitypes.CreateCard{Title: "low", Status: "ready", Priority: "low"})
	high := e.newCard(agentSecret, apitypes.CreateCard{Title: "high", Status: "ready", Priority: "high"})
	e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "q", Blocks: []int64{high.Number}})
	if got := call[apitypes.Card](e, agentSecret, http.MethodGet, "/projects/lorvik/cards/next", nil, http.StatusOK); got.Number != low.Number {
		t.Errorf("next = #%d, want #%d (high is blocked)", got.Number, low.Number)
	}
	e.claim(agent2Secret, low.Number)
	e.problem(agentSecret, http.MethodGet, "/projects/lorvik/cards/next", nil, http.StatusNotFound)
}

func TestInboxReturnsOthersEventsAfterCursorAndAdvancesIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	first := call[apitypes.Inbox](e, agentSecret, http.MethodGet, "/inbox", nil, http.StatusOK)
	if len(first.Events) != 0 || first.Cursor != "" {
		t.Fatalf("empty inbox = %+v, want no events and no cursor", first)
	}
	e.readyCard(agentSecret, "own card")
	e.readyCard(agent2Secret, "their card")
	e.readyCard(userSecret, "owner's card")
	in := call[apitypes.Inbox](e, agentSecret, http.MethodGet, "/inbox", nil, http.StatusOK)
	if len(in.Events) != 2 || in.Events[0].Actor.ID != e.agent2.ID.String() || in.Events[1].Actor.ID != e.user.ID.String() {
		t.Fatalf("inbox = %+v, want the two events by others, oldest first", in.Events)
	}
	if in.Cursor != e.latestPosition() {
		t.Errorf("cursor = %q, want the latest event %q", in.Cursor, e.latestPosition())
	}
	again := call[apitypes.Inbox](e, agentSecret, http.MethodGet, "/inbox?cursor="+in.Cursor, nil, http.StatusOK)
	if len(again.Events) != 0 || again.Cursor != in.Cursor {
		t.Errorf("inbox after cursor = %+v, want nothing new and the same cursor", again)
	}
	e.readyCard(agent2Secret, "one more")
	e.readyCard(agent2Secret, "and another")
	limited := call[apitypes.Inbox](e, agentSecret, http.MethodGet, "/inbox?cursor="+in.Cursor+"&limit=1", nil, http.StatusOK)
	if len(limited.Events) != 1 || limited.Remaining != 1 || limited.Cursor != limited.Events[0].Position {
		t.Errorf("limited inbox = %+v, want one event, one remaining, cursor at the returned event", limited)
	}
	e.problem(agentSecret, http.MethodGet, "/inbox?cursor=abc", nil, http.StatusUnprocessableEntity)
}

func TestInboxScopedToProjectNeverSkipsOtherProjectsEvents(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "other", Name: "Other"}, http.StatusCreated)
	e.readyCard(userSecret, "in lorvik")
	call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/other/cards", apitypes.CreateCard{Title: "in other", Status: "ready"}, http.StatusCreated)
	scoped := call[apitypes.Inbox](e, agentSecret, http.MethodGet, "/inbox?project=lorvik", nil, http.StatusOK)
	if len(scoped.Events) != 1 || scoped.Cursor != scoped.Events[0].Position {
		t.Fatalf("scoped inbox = %+v, want the lorvik event with the cursor stopping at it", scoped)
	}
	quiet := call[apitypes.Inbox](e, agentSecret, http.MethodGet, "/inbox?project=lorvik&cursor="+scoped.Cursor, nil, http.StatusOK)
	if len(quiet.Events) != 0 || quiet.Cursor != scoped.Cursor {
		t.Errorf("scoped inbox again = %+v, want nothing new and an unchanged cursor", quiet)
	}
	all := call[apitypes.Inbox](e, agentSecret, http.MethodGet, "/inbox?cursor="+scoped.Cursor, nil, http.StatusOK)
	if len(all.Events) != 1 || all.Events[0].Project == nil || *all.Events[0].Project != "other" {
		t.Errorf("org inbox after the scoped cursor = %+v, want the event from the other project", all.Events)
	}
}

func TestInboxListsClaimsAndOpenQuestionCount(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(agentSecret, "mine")
	e.claim(agentSecret, c.Number)
	e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "asked by agent"})
	e.newCard(agent2Secret, apitypes.CreateCard{Kind: "question", Title: "asked by agent2"})
	in := call[apitypes.Inbox](e, agentSecret, http.MethodGet, "/inbox", nil, http.StatusOK)
	if len(in.Claims) != 1 || in.Claims[0].Number != c.Number || in.OpenQuestions != 1 {
		t.Errorf("inbox = claims %+v, open questions %d; want one claim and the agent's own question", in.Claims, in.OpenQuestions)
	}
	if u := call[apitypes.Inbox](e, userSecret, http.MethodGet, "/inbox", nil, http.StatusOK); u.OpenQuestions != 2 {
		t.Errorf("user open questions = %d, want both", u.OpenQuestions)
	}
}

func TestUnreadHeaderCountsOthersEventsAfterTheCursorHeader(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	rec := e.do(agentSecret, http.MethodGet, "/me", nil)
	if rec.Header().Get(apitypes.HeaderUnread) != "" {
		t.Errorf("unread without a cursor header = %q, want none", rec.Header().Get(apitypes.HeaderUnread))
	}
	e.readyCard(agentSecret, "own")
	e.readyCard(agent2Secret, "theirs")
	e.readyCard(userSecret, "owner's")
	rec = e.do(agentSecret, http.MethodGet, "/me", nil, apitypes.HeaderCursor, "0")
	if got := rec.Header().Get(apitypes.HeaderUnread); got != "2" {
		t.Errorf("unread from 0 = %q, want 2 (own event ignored)", got)
	}
	rec = e.do(agentSecret, http.MethodGet, "/me", nil, apitypes.HeaderCursor, e.latestPosition())
	if got := rec.Header().Get(apitypes.HeaderUnread); got != "0" {
		t.Errorf("unread at head = %q, want 0", got)
	}
}

func TestUnreadHeaderIsCapped(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for i := range apitypes.UnreadCap + 5 {
		e.readyCard(agent2Secret, "c"+strconv.Itoa(i))
	}
	rec := e.do(agentSecret, http.MethodGet, "/me", nil, apitypes.HeaderCursor, "0")
	if got := rec.Header().Get(apitypes.HeaderUnread); got != strconv.Itoa(apitypes.UnreadCap+1) {
		t.Errorf("unread = %q, want cap+1 = %d", got, apitypes.UnreadCap+1)
	}
}

func TestTokenTouchIsThrottledToOncePerMinute(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	lastUsed := func() *time.Time {
		e.fs.mu.Lock()
		defer e.fs.mu.Unlock()
		for _, tok := range e.fs.tokens {
			if tok.Actor == e.agent {
				return tok.LastUsedAt
			}
		}
		return nil
	}
	call[apitypes.Me](e, agentSecret, http.MethodGet, "/me", nil, http.StatusOK)
	if got := lastUsed(); got == nil || !got.Equal(t0) {
		t.Fatalf("last used = %v, want t0", got)
	}
	e.advance(30 * time.Second)
	call[apitypes.Me](e, agentSecret, http.MethodGet, "/me", nil, http.StatusOK)
	if got := lastUsed(); !got.Equal(t0) {
		t.Errorf("last used after 30s = %v, want unchanged", got)
	}
	e.advance(31 * time.Second)
	call[apitypes.Me](e, agentSecret, http.MethodGet, "/me", nil, http.StatusOK)
	if got := lastUsed(); !got.Equal(t0.Add(61 * time.Second)) {
		t.Errorf("last used after 61s = %v, want touched", got)
	}
}

func TestNoteSaveRefreshesBacklinksAndKeepsRevisions(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(agentSecret, "target")
	n := call[apitypes.Note](e, agentSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "lorvik", Slug: "arch", Title: "Architecture", Body: "See [[card:1]]."}, http.StatusCreated)
	target := call[apitypes.Note](e, agentSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "lorvik", Slug: "db", Title: "Database", Body: "Mentioned by [[note:arch]]? no, this is the target"}, http.StatusCreated)
	_ = c
	got := call[apitypes.Note](e, agentSecret, http.MethodPatch, "/notes/arch?project=lorvik", apitypes.UpdateNote{Version: n.Version, Body: new("Now see [[note:db]] instead.")}, http.StatusOK)
	if got.Version != 2 {
		t.Fatalf("version = %d", got.Version)
	}
	shownCard := e.show(agentSecret, c.Number)
	_ = shownCard
	db := call[apitypes.Note](e, agentSecret, http.MethodGet, "/notes/"+target.ID, nil, http.StatusOK)
	if len(db.Backlinks) != 1 || db.Backlinks[0].Slug != "arch" || db.Backlinks[0].Type != "note" {
		t.Errorf("backlinks on db = %+v, want arch", db.Backlinks)
	}
	revs := call[[]apitypes.NoteRevision](e, agentSecret, http.MethodGet, "/notes/arch/revisions?project=lorvik", nil, http.StatusOK)
	if len(revs) != 1 || revs[0].Version != 1 || revs[0].Body != "" {
		t.Errorf("revisions = %+v, want version 1 without body", revs)
	}
	rev := call[apitypes.NoteRevision](e, agentSecret, http.MethodGet, "/notes/arch/revisions/1?project=lorvik", nil, http.StatusOK)
	if rev.Body != "See [[card:1]]." {
		t.Errorf("revision 1 body = %q", rev.Body)
	}
	e.problem(agentSecret, http.MethodPost, "/notes", apitypes.CreateNote{Project: "lorvik", Slug: "arch", Title: "dup", Body: "x"}, http.StatusConflict)
	arch := call[apitypes.Note](e, agentSecret, http.MethodPatch, "/notes/arch?project=lorvik", apitypes.UpdateNote{Version: 2, Status: new("archived")}, http.StatusOK)
	if arch.Status != "archived" {
		t.Errorf("status = %q", arch.Status)
	}
	if l := call[apitypes.NoteList](e, agentSecret, http.MethodGet, "/notes?project=lorvik", nil, http.StatusOK); len(l.Items) != 1 || l.Items[0].Slug != "db" {
		t.Errorf("active notes = %+v, want only db", l.Items)
	}
}

func TestCommentsAreListedNewestFirstAndShownOnCard(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(agentSecret, "task")
	call[apitypes.Comment](e, agentSecret, http.MethodPost, cardPathOf(c.Number, "/comments"), apitypes.CreateComment{Body: "first"}, http.StatusCreated)
	call[apitypes.Comment](e, agent2Secret, http.MethodPost, cardPathOf(c.Number, "/comments"), apitypes.CreateComment{Body: "second"}, http.StatusCreated)
	hs := call[[]apitypes.Comment](e, agentSecret, http.MethodGet, cardPathOf(c.Number, "/comments"), nil, http.StatusOK)
	if len(hs) != 2 || hs[0].Body != "second" || hs[0].Actor.ID != e.agent2.ID.String() {
		t.Errorf("comments = %+v, want newest first", hs)
	}
	if got := e.show(agentSecret, c.Number); got.Comment == nil || got.Comment.Body != "second" {
		t.Errorf("card comment = %+v", got.Comment)
	}
}

func TestSweeperReleasesExpiredClaimAndEmitsEventAsClaimant(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(agentSecret, "task")
	e.claim(agentSecret, c.Number)
	e.advance(31 * time.Minute)
	if _, err := e.svc.sweepOnce(context.Background(), e.clock()); err != nil {
		t.Fatal(err)
	}
	got := e.show(userSecret, c.Number)
	if got.Status != "ready" || got.Claim != nil {
		t.Errorf("after sweep = %+v, want ready and unclaimed", got)
	}
	ev := e.lastEvent()
	if ev.Type != domain.EventCardClaimExpired || ev.Actor != e.agent {
		t.Errorf("event = %+v, want claim.expired by the former claimant", ev)
	}
}

func TestListEventsBeforeReturnsOlderWindowOldestFirstAndRejectsBothBounds(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for i := range 5 {
		e.readyCard(agentSecret, "c"+strconv.Itoa(i))
	}
	all := call[apitypes.EventList](e, userSecret, http.MethodGet, "/events", nil, http.StatusOK)
	if len(all.Items) != 5 || all.Next != all.Items[4].Position || all.Prev != all.Items[0].Position {
		t.Fatalf("events = %d next=%q prev=%q", len(all.Items), all.Next, all.Prev)
	}
	older := call[apitypes.EventList](e, userSecret, http.MethodGet, "/events?before="+all.Items[3].ID+"&limit=2", nil, http.StatusOK)
	if len(older.Items) != 2 || older.Items[0].ID != all.Items[1].ID || older.Items[1].ID != all.Items[2].ID || older.Remaining != 1 {
		t.Errorf("before window = %+v remaining=%d, want items 1 and 2 with 1 remaining", older.Items, older.Remaining)
	}
	newer := call[apitypes.EventList](e, userSecret, http.MethodGet, "/events?after="+all.Items[2].ID, nil, http.StatusOK)
	if len(newer.Items) != 2 || newer.Items[0].ID != all.Items[3].ID {
		t.Errorf("after window = %+v", newer.Items)
	}
	e.problem(userSecret, http.MethodGet, "/events?after="+all.Items[0].ID+"&before="+all.Items[4].ID, nil, http.StatusBadRequest)
	// A project-bound agent only sees its projects' events.
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "other", Name: "Other"}, http.StatusCreated)
	call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/other/cards", apitypes.CreateCard{Title: "elsewhere"}, http.StatusCreated)
	scoped := call[apitypes.EventList](e, agent2Secret, http.MethodGet, "/events", nil, http.StatusOK)
	for _, ev := range scoped.Items {
		if ev.Project != nil && *ev.Project != "lorvik" {
			t.Errorf("project-bound agent saw event in %q", *ev.Project)
		}
	}
	if len(scoped.Items) != 5 {
		t.Errorf("scoped events = %d, want the 5 lorvik card events", len(scoped.Items))
	}
}

func TestLoginMintsTokenOnlyWhenEnabled(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	hash, err := domain.HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = e.fs.CreateIdentity(context.Background(), domain.Identity{ID: e.ids.New(), UserID: e.user.ID, Provider: domain.ProviderPassword, ProviderID: "ada", Secret: hash, Login: "ada"})
	e.problem("", http.MethodPost, "/login", apitypes.Login{Login: "ada", Password: "correct horse"}, http.StatusNotFound)
	e.svc.password = true
	e.problem("", http.MethodPost, "/login", apitypes.Login{Login: "ada", Password: "wrong"}, http.StatusUnauthorized)
	got := call[apitypes.TokenCreated](e, "", http.MethodPost, "/login", apitypes.Login{Login: "Ada", Password: "correct horse"}, http.StatusCreated)
	if !strings.HasPrefix(got.Secret, "lvk_u_") || got.Token.Actor.ID != e.user.ID.String() {
		t.Fatalf("login = %+v", got)
	}
	me := call[apitypes.Me](e, got.Secret, http.MethodGet, "/me", nil, http.StatusOK)
	if me.Role != "owner" {
		t.Errorf("me via login token = %+v", me)
	}
	if !slices.Contains([]string{"write"}, got.Token.Scope) {
		t.Errorf("scope = %q", got.Token.Scope)
	}
}

func TestNextOffersOnlyCardsAssignedToTheCallerOrNobody(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	theirs := e.readyCard(userSecret, "assigned to the owner")
	call[apitypes.Card](e, userSecret, http.MethodPatch, cardPathOf(theirs.Number, ""),
		apitypes.UpdateCard{Version: theirs.Version, Assignee: &apitypes.Actor{Kind: "user", ID: e.user.ID.String()}}, http.StatusOK)
	e.problem(agentSecret, http.MethodGet, "/projects/lorvik/cards/next", nil, http.StatusNotFound)
	free := e.readyCard(userSecret, "unassigned")
	if got := call[apitypes.Card](e, agentSecret, http.MethodGet, "/projects/lorvik/cards/next", nil, http.StatusOK); got.Number != free.Number {
		t.Errorf("next for the agent = #%d, want unassigned #%d", got.Number, free.Number)
	}
	if got := call[apitypes.Card](e, userSecret, http.MethodGet, "/projects/lorvik/cards/next", nil, http.StatusOK); got.Number != theirs.Number {
		t.Errorf("next for the assignee = #%d, want their own #%d", got.Number, theirs.Number)
	}
}

func TestNextSkipsLanesWithAWorkingCardAndExplainsWhenThatIsAllThatIsLeft(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	working := e.newCard(userSecret, apitypes.CreateCard{Title: "schema change", Status: "ready", Lane: "schema"})
	waitingA := e.newCard(userSecret, apitypes.CreateCard{Title: "another schema change", Status: "ready", Lane: "schema", Priority: "high"})
	e.newCard(userSecret, apitypes.CreateCard{Title: "web change", Status: "ready", Lane: "web"})
	call[apitypes.Card](e, agentSecret, http.MethodPost, cardPathOf(working.Number, "/claim"), apitypes.ClaimCard{}, http.StatusOK)
	if got := e.show(agentSecret, working.Number); got.Status != "working" || got.Lane != "schema" {
		t.Fatalf("claimed card = %+v, want working in lane schema", got)
	}
	if got := call[apitypes.Card](e, agent2Secret, http.MethodGet, "/projects/lorvik/cards/next", nil, http.StatusOK); got.Lane != "web" {
		t.Errorf("next = #%d lane %q, want the web card (the schema lane is busy, though #%d has higher priority)", got.Number, got.Lane, waitingA.Number)
	}
	web := call[apitypes.Card](e, agent2Secret, http.MethodGet, "/projects/lorvik/cards/next", nil, http.StatusOK)
	call[apitypes.Card](e, agent2Secret, http.MethodPost, cardPathOf(web.Number, "/claim"), apitypes.ClaimCard{}, http.StatusOK)
	p := e.problem(agent2Secret, http.MethodGet, "/projects/lorvik/cards/next", nil, http.StatusNotFound)
	if p.WaitingCards != 1 || len(p.BusyLanes) != 1 || p.BusyLanes[0] != "schema" {
		t.Errorf("404 = %+v, want 1 waiting card on lane schema", p)
	}
	call[apitypes.Card](e, agentSecret, http.MethodPost, cardPathOf(working.Number, "/release"), apitypes.ClaimCard{}, http.StatusOK)
	if got := call[apitypes.Card](e, agent2Secret, http.MethodGet, "/projects/lorvik/cards/next", nil, http.StatusOK); got.Number == web.Number {
		t.Errorf("next = #%d after the lane freed, want a schema card", got.Number)
	}
}

func TestBranchAndLaneAreEditableAndClearable(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.newCard(userSecret, apitypes.CreateCard{Title: "t", Lane: " web "})
	if c.Lane != "web" || c.Branch != "" {
		t.Fatalf("created = %+v, want lane trimmed to web", c)
	}
	c = call[apitypes.Card](e, userSecret, http.MethodPatch, cardPathOf(c.Number, ""),
		apitypes.UpdateCard{Version: c.Version, Branch: new("card-1-x"), Lane: new("")}, http.StatusOK)
	if c.Branch != "card-1-x" || c.Lane != "" {
		t.Errorf("after edit = %+v, want branch set and lane cleared", c)
	}
	p := e.problem(userSecret, http.MethodPatch, cardPathOf(c.Number, ""),
		apitypes.UpdateCard{Version: c.Version, Lane: new("two words")}, http.StatusUnprocessableEntity)
	if len(p.Errors) == 0 || p.Errors[0].Field != "lane" {
		t.Errorf("problem = %+v, want a lane field error", p)
	}
}

func TestProjectConcurrencyStartsParallelAndIsSetByUsersOnly(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	created := call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "other", Name: "Other"}, http.StatusCreated)
	if created.Concurrency != "parallel" {
		t.Fatalf("new project concurrency = %q, want parallel", created.Concurrency)
	}
	set := call[apitypes.Project](e, userSecret, http.MethodPatch, "/projects/other", apitypes.UpdateProject{Concurrency: new("serial")}, http.StatusOK)
	if set.Concurrency != "serial" {
		t.Errorf("after patch = %q, want serial", set.Concurrency)
	}
	e.problem(agentSecret, http.MethodPatch, "/projects/other", apitypes.UpdateProject{Concurrency: new("parallel")}, http.StatusForbidden)
	e.problem(userSecret, http.MethodPatch, "/projects/other", apitypes.UpdateProject{Concurrency: new("sideways")}, http.StatusUnprocessableEntity)
}

// More open questions than the largest list page, so a count taken from one
// page of cards comes up short.
func TestAgentOpenQuestionsCountEveryQuestionItAsked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	const asked = apitypes.MaxPageSize + 1
	for range asked {
		e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "which?"})
	}
	e.newCard(agent2Secret, apitypes.CreateCard{Kind: "question", Title: "someone else asks"})
	if got := call[apitypes.Inbox](e, agentSecret, http.MethodGet, "/inbox", nil, http.StatusOK).OpenQuestions; got != asked {
		t.Errorf("open questions = %d, want %d: only the agent's own, all of them", got, asked)
	}
}
