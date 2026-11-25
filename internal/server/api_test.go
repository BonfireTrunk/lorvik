package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

func TestUnauthenticatedRequestGets401WithHint(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p := e.problem("", http.MethodGet, "/projects", nil, http.StatusUnauthorized)
	if p.Hint == "" || !strings.Contains(p.Hint, "LORVIK_TOKEN") {
		t.Errorf("hint = %q, want it to name LORVIK_TOKEN", p.Hint)
	}
}

func TestRevokedAndExpiredTokensAreRejected(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.seedToken("lvk_u_revoked-secret", e.user, domain.ScopeWrite, t0.Add(time.Hour))
	e.fs.mu.Lock()
	for _, tok := range e.fs.tokens {
		if tok.Prefix == "lvk_u_revoke" {
			tok.Status = domain.RecordRevoked
		}
	}
	e.fs.mu.Unlock()
	if p := e.problem("lvk_u_revoked-secret", http.MethodGet, "/projects", nil, http.StatusUnauthorized); !strings.Contains(p.Detail, "revoked") {
		t.Errorf("detail = %q, want it to say revoked", p.Detail)
	}
	e.seedToken("lvk_u_expired-secret", e.user, domain.ScopeWrite, t0.Add(-time.Minute))
	if p := e.problem("lvk_u_expired-secret", http.MethodGet, "/projects", nil, http.StatusUnauthorized); !strings.Contains(p.Detail, "expired") {
		t.Errorf("detail = %q, want it to say expired", p.Detail)
	}
}

func TestHealthzAndOpenAPIArePublic(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	h := call[apitypes.Health](e, "", http.MethodGet, "/healthz", nil, http.StatusOK)
	if h.Status != "ok" || h.Database != "ok" {
		t.Errorf("health = %+v, want ok/ok", h)
	}
	rec := e.do("", http.MethodGet, "/openapi.json", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"openapi":"3.1`) {
		t.Errorf("openapi = %d %.60s", rec.Code, rec.Body.String())
	}
}

func TestNilStoreAnswers503ForAPIRoutes(t *testing.T) {
	t.Parallel()
	h := newAPI(Deps{Logger: slog.New(slog.DiscardHandler)}, nil)
	e := &testEnv{t: t, h: h}
	p := e.problem("lvk_u_x", http.MethodGet, "/projects", nil, http.StatusServiceUnavailable)
	if !strings.Contains(p.Hint, "LORVIK_DATABASE_URL") {
		t.Errorf("hint = %q, want it to name LORVIK_DATABASE_URL", p.Hint)
	}
	if rec := e.do("", http.MethodGet, "/healthz", nil); rec.Code != http.StatusOK {
		t.Errorf("healthz without a store = %d, want 200", rec.Code)
	}
}

func TestReadOnlyTokenCannotWrite(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.seedToken("lvk_u_readonly-sec", e.user, domain.ScopeRead, t0.Add(time.Hour))
	if _, ok := call[apitypes.Project](e, "lvk_u_readonly-sec", http.MethodGet, "/projects/lorvik", nil, http.StatusOK), true; !ok {
		t.Fatal("read failed")
	}
	p := e.problem("lvk_u_readonly-sec", http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Title: "x"}, http.StatusForbidden)
	if !strings.Contains(p.Detail, "read-only") {
		t.Errorf("detail = %q, want read-only", p.Detail)
	}
}

// A read-only token learns it is read-only before anything is looked up, so
// the answer does not depend on whether the target exists or is manageable.
func TestReadOnlyIsRefusedBeforeTheLookup(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.seedToken("lvk_u_readonly-sec", e.user, domain.ScopeRead, t0.Add(time.Hour))
	missing := e.ids.New().String()
	for _, path := range []string{"/agents/" + missing, "/tokens/" + missing} {
		p := e.problem("lvk_u_readonly-sec", http.MethodDelete, path, nil, http.StatusForbidden)
		if !strings.Contains(p.Detail, "read-only") {
			t.Errorf("DELETE %s detail = %q, want read-only", path, p.Detail)
		}
	}
}

func TestMeReportsRolesPerActor(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	me := call[apitypes.Me](e, userSecret, http.MethodGet, "/me", nil, http.StatusOK)
	if me.Role != "owner" || me.User == nil || me.User.Login != "ada" || me.Org.ID != e.org.ID.String() {
		t.Errorf("owner me = %+v", me)
	}
	me2 := call[apitypes.Me](e, agent2Secret, http.MethodGet, "/me", nil, http.StatusOK)
	if me2.Role != "" || len(me2.Projects) != 1 || me2.Projects[0].Project != "lorvik" || me2.Projects[0].Role != "viewer" || !slices.Equal(me2.Projects[0].Rights, []string{"work", "ready"}) || me2.Agent == nil {
		t.Errorf("project-bound agent me = %+v, want no org role and one project with the work and ready rights", me2)
	}
}

func TestProjectBoundAgentSeesOnlyItsProjects(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "other", Name: "Other"}, http.StatusCreated)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "secret", Name: "Secret", Visibility: "private"}, http.StatusCreated)
	slugs := func(secret string) []string {
		var out []string
		for _, p := range call[apitypes.ProjectList](e, secret, http.MethodGet, "/projects", nil, http.StatusOK).Items {
			out = append(out, p.Slug)
		}
		return out
	}
	if got := slugs(agent2Secret); !slices.Equal(got, []string{"global", "lorvik"}) {
		t.Errorf("project-bound agent sees %v, want [global lorvik]", got)
	}
	if got := slugs(agentSecret); !slices.Equal(got, []string{"global", "lorvik", "other"}) {
		t.Errorf("org-wide agent sees %v, want [global lorvik other] (private project hidden)", got)
	}
	if got := slugs(userSecret); !slices.Equal(got, []string{"global", "lorvik", "other", "secret"}) {
		t.Errorf("owner sees %v, want everything", got)
	}
	e.problem(agent2Secret, http.MethodGet, "/projects/other", nil, http.StatusNotFound)
	e.problem(agentSecret, http.MethodGet, "/projects/secret", nil, http.StatusNotFound)
}

func TestUserWithoutMembershipIsForbidden(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	stranger := e.seedUser("stranger", 0)
	e.seedToken("lvk_u_stranger-sec", stranger, domain.ScopeWrite, t0.Add(time.Hour))
	e.problem("lvk_u_stranger-sec", http.MethodGet, "/projects", nil, http.StatusForbidden)
}

func TestUserRegistersAgentAndItsSecretWorks(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	in := apitypes.CreateAgent{Name: "gemini", Harness: "gemini-cli", Projects: []string{"lorvik"}, Rights: []string{"work"}}
	created := call[apitypes.AgentCreated](e, memberSecret, http.MethodPost, "/agents", in, http.StatusCreated)
	if created.Token == "" || created.Agent.Status != "active" || len(created.Agent.Projects) != 1 || !slices.Equal(created.Agent.Projects[0].Rights, []string{"work"}) || created.Agent.Projects[0].Role != "viewer" {
		t.Fatalf("created = %+v, want an active agent with a token and the work right in lorvik", created)
	}
	me := call[apitypes.Me](e, created.Token, http.MethodGet, "/me", nil, http.StatusOK)
	if me.Agent == nil || me.Agent.Name != "gemini" || me.Agent.CreatedBy != e.member.ID.String() {
		t.Errorf("me = %+v, want the new agent created by the member", me)
	}
	if !slices.Contains(e.eventTypes(), "agent.registered") {
		t.Errorf("events = %v, want agent.registered", e.eventTypes())
	}
	// A member cannot hand out a right their role lacks, in a project or org-wide.
	p := e.problem(memberSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "boss", Projects: []string{"lorvik"}, Rights: []string{"work", "ready"}}, http.StatusUnprocessableEntity)
	if !hasField(p, "rights") {
		t.Errorf("errors = %+v, want rights", p.Errors)
	}
	p = e.problem(memberSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "boss", OrgWide: true, Rights: []string{"triage"}}, http.StatusUnprocessableEntity)
	if !hasField(p, "rights") {
		t.Errorf("errors = %+v, want rights", p.Errors)
	}
	// The owner can.
	wide := call[apitypes.AgentCreated](e, userSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "wide", OrgWide: true, Rights: []string{"work", "ready", "triage", "answer"}}, http.StatusCreated)
	if wide.Agent.Role != "viewer" || !slices.Equal(wide.Agent.Rights, []string{"work", "ready", "triage", "answer"}) {
		t.Errorf("org-wide agent = role %q rights %v, want viewer with all four rights", wide.Agent.Role, wide.Agent.Rights)
	}
}

func TestAgentApprovalWhenTheOrgRequiresIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	call[apitypes.Org](e, userSecret, http.MethodPatch, "/org", apitypes.UpdateOrg{Settings: &apitypes.OrgSettings{AgentApproval: "admin"}}, http.StatusOK)
	created := call[apitypes.AgentCreated](e, memberSecret, http.MethodPost, "/agents", apitypes.CreateAgent{Name: "pending-one", Projects: []string{"lorvik"}}, http.StatusCreated)
	if created.Agent.Status != "pending" {
		t.Fatalf("status = %q, want pending", created.Agent.Status)
	}
	if p := e.problem(created.Token, http.MethodGet, "/projects", nil, http.StatusForbidden); !strings.Contains(p.Detail, "pending") {
		t.Errorf("pending agent detail = %q", p.Detail)
	}
	e.problem(memberSecret, http.MethodPost, "/agents/"+created.Agent.ID+"/approve", nil, http.StatusForbidden)
	approved := call[apitypes.Agent](e, userSecret, http.MethodPost, "/agents/"+created.Agent.ID+"/approve", nil, http.StatusOK)
	if approved.Status != "active" {
		t.Errorf("approved status = %q", approved.Status)
	}
	call[apitypes.ProjectList](e, created.Token, http.MethodGet, "/projects", nil, http.StatusOK)
}

func TestCreateTaskDefaultsToBacklogAndEmitsCardCreated(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.newCard(agentSecret, apitypes.CreateCard{Title: "Write the README", Body: "Short one."})
	if c.Number != 1 || c.Status != "backlog" || c.Kind != "task" || c.Priority != "normal" || c.Version != 1 || c.Project != "lorvik" {
		t.Errorf("card = %+v", c)
	}
	if c.CreatedBy.Kind != "agent" || c.CreatedBy.ID != e.agent.ID.String() {
		t.Errorf("created_by = %+v, want the agent by id only", c.CreatedBy)
	}
	ev := e.lastEvent()
	if ev.Type != domain.EventCardCreated || ev.Subject.Number != 1 || ev.ProjectID == nil || *ev.ProjectID != e.proj.ID || !strings.Contains(string(ev.Payload), `"title":"Write the README"`) {
		t.Errorf("event = %+v %s", ev, ev.Payload)
	}
}

func TestCreateCardValidationReturns422WithFieldErrors(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p := e.problem(agentSecret, http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Title: "x", Parent: new(int64(99)), Choices: []string{"a"}}, http.StatusUnprocessableEntity)
	if !hasField(p, "parent") || !hasField(p, "choices") {
		t.Errorf("errors = %+v, want parent and choices", p.Errors)
	}
}

func TestViewerCannotWriteAndMemberCannotTriage(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	viewer := e.seedUser("ann", domain.RoleViewer)
	e.seedToken("lvk_u_viewer-secret", viewer, domain.ScopeWrite, t0.Add(time.Hour))
	e.problem("lvk_u_viewer-secret", http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Title: "x"}, http.StatusForbidden)
	call[apitypes.CardList](e, "lvk_u_viewer-secret", http.MethodGet, "/projects/lorvik/cards", nil, http.StatusOK)

	c := e.readyCard(agentSecret, "agent's card")
	// Another member may not triage or drop a card they do not own.
	e.problem(memberSecret, http.MethodPatch, cardPathOf(c.Number, ""), apitypes.UpdateCard{Version: c.Version, Priority: new("high")}, http.StatusForbidden)
	e.problem(memberSecret, http.MethodPost, cardPathOf(c.Number, "/transition"), apitypes.TransitionCard{Version: c.Version, Status: "dropped", DropReason: "obsolete"}, http.StatusForbidden)
	// The creator may; so may the owner (triage).
	call[apitypes.Card](e, agentSecret, http.MethodPatch, cardPathOf(c.Number, ""), apitypes.UpdateCard{Version: c.Version, Title: new("renamed")}, http.StatusOK)
	c = e.show(userSecret, c.Number)
	got := call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(c.Number, "/transition"),
		apitypes.TransitionCard{Version: c.Version, Status: "dropped", DropReason: "obsolete"}, http.StatusOK)
	if got.Status != "dropped" {
		t.Errorf("owner transition = %q, want dropped", got.Status)
	}
}

func TestQuestionWithBlocksParksClaimedTargetInWaiting(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task := e.readyCard(agentSecret, "Pick a database")
	e.claim(agentSecret, task.Number)
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "Postgres or SQLite?", Choices: []string{"postgres", "sqlite"}, Blocks: []int64{task.Number}})
	if q.Status != "ready" || q.Question == nil || len(q.Question.Choices) != 2 {
		t.Errorf("question = %+v, want ready with two choices", q)
	}
	got := e.show(agentSecret, task.Number)
	if got.Status != "waiting" || got.Claim != nil || len(got.BlockedBy) != 1 || got.BlockedBy[0].Number != q.Number {
		t.Errorf("blocked task = %+v, want waiting, unclaimed, blocked by the question", got)
	}
	e.problem(agent2Secret, http.MethodPost, cardPathOf(task.Number, "/claim"), apitypes.ClaimCard{}, http.StatusConflict)
}

func TestQuestionParksABacklogCardItBlocksAndTheAnswerReadiesIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	task := e.newCard(agentSecret, apitypes.CreateCard{Title: "Fix the stream"})
	q := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "A or B?", Blocks: []int64{task.Number}})
	if got := e.show(agentSecret, task.Number); got.Status != "waiting" {
		t.Fatalf("blocked backlog task = %q, want waiting", got.Status)
	}
	call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: q.Version, Answer: "A"}, http.StatusOK)
	if got := e.show(agentSecret, task.Number); got.Status != "ready" {
		t.Errorf("task after the answer = %q, want ready", got.Status)
	}
}

// Only an answer readies a card a question parked from backlog: when its last
// blocker goes without one the card goes back to backlog (#283), while a card
// parked from ready returns to ready.
func TestABlockerGoneWithoutAnAnswerSendsAParkedBacklogCardBack(t *testing.T) {
	t.Parallel()
	unlink := func(e *testEnv, q, target int64) {
		if rec := e.do(userSecret, http.MethodDelete, cardPathOf(q, fmt.Sprintf("/links/blocks/%d", target)), nil); rec.Code != http.StatusNoContent {
			e.t.Fatalf("unlink = %d: %s", rec.Code, rec.Body)
		}
	}
	drop := func(e *testEnv, q, _ int64) {
		e.move(userSecret, q, apitypes.TransitionCard{Status: "dropped", DropReason: "obsolete"})
	}
	answer := func(e *testEnv, q, _ int64) {
		call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(q, "/answer"), apitypes.AnswerCard{Version: e.show(userSecret, q).Version, Answer: "A"}, http.StatusOK)
	}
	tests := []struct {
		name  string
		ready bool
		clear func(e *testEnv, q, target int64)
		want  string
	}{
		{"backlog card, question unlinked", false, unlink, "backlog"},
		{"backlog card, question dropped", false, drop, "backlog"},
		{"backlog card, question answered", false, answer, "ready"},
		{"ready card, question unlinked", true, unlink, "ready"},
		{"ready card, question dropped", true, drop, "ready"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			target := e.newCard(userSecret, apitypes.CreateCard{Title: "Fix the stream"})
			if tt.ready {
				target = e.readyCard(userSecret, "Fix the stream")
			}
			q := e.newCard(userSecret, apitypes.CreateCard{Kind: "question", Title: "A or B?", Blocks: []int64{target.Number}})
			if got := e.show(userSecret, target.Number); got.Status != "waiting" {
				t.Fatalf("blocked card = %q, want waiting", got.Status)
			}
			tt.clear(e, q.Number, target.Number)
			if got := e.show(userSecret, target.Number); got.Status != tt.want {
				t.Errorf("card after its blocker went = %q, want %s", got.Status, tt.want)
			}
		})
	}
}

// The way the last blocker goes decides: an answer to one question does not
// ready a backlog card whose other question is then unlinked.
func TestAnEarlierAnswerDoesNotReadyACardWhoseLastQuestionIsUnlinked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	target := e.newCard(userSecret, apitypes.CreateCard{Title: "Fix the stream"})
	q1 := e.newCard(userSecret, apitypes.CreateCard{Kind: "question", Title: "A or B?", Blocks: []int64{target.Number}})
	q2 := e.newCard(userSecret, apitypes.CreateCard{Kind: "question", Title: "C or D?", Blocks: []int64{target.Number}})
	call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(q1.Number, "/answer"), apitypes.AnswerCard{Version: q1.Version, Answer: "A"}, http.StatusOK)
	if got := e.show(userSecret, target.Number); got.Status != "waiting" {
		t.Fatalf("card after one of two answers = %q, want waiting", got.Status)
	}
	if rec := e.do(userSecret, http.MethodDelete, cardPathOf(q2.Number, fmt.Sprintf("/links/blocks/%d", target.Number)), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink = %d: %s", rec.Code, rec.Body)
	}
	if got := e.show(userSecret, target.Number); got.Status != "backlog" {
		t.Errorf("card after the other question was unlinked = %q, want backlog", got.Status)
	}
}

// A blocker that completes is not one gone without an answer: a card parked
// from backlog whose question was answered is readied when the task that
// also blocks it is done, and sent back when that task is dropped.
func TestAParkedBacklogCardFollowsHowItsLastTaskBlockerEnds(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		to, reason, want string
	}{
		{"done", "", "ready"},
		{"dropped", "obsolete", "backlog"},
	} {
		t.Run(tt.to, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			target := e.newCard(userSecret, apitypes.CreateCard{Title: "Fix the stream"})
			q := e.newCard(userSecret, apitypes.CreateCard{Kind: "question", Title: "A or B?", Blocks: []int64{target.Number}})
			task := e.readyCard(userSecret, "Build the parser")
			call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(task.Number, "/links"), apitypes.LinkCards{Kind: "blocks", To: target.Number}, http.StatusOK)
			call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(q.Number, "/answer"), apitypes.AnswerCard{Version: q.Version, Answer: "A"}, http.StatusOK)
			if tt.to == "done" {
				e.move(userSecret, task.Number, apitypes.TransitionCard{Status: "working"})
			}
			e.move(userSecret, task.Number, apitypes.TransitionCard{Status: tt.to, DropReason: tt.reason})
			if got := e.show(userSecret, target.Number); got.Status != tt.want {
				t.Errorf("card after its task blocker was %s = %q, want %s", tt.to, got.Status, tt.want)
			}
		})
	}
}

func TestTaskBlockingABacklogCardLeavesItInBacklog(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	blocker := e.readyCard(agentSecret, "Build it")
	target := e.newCard(agentSecret, apitypes.CreateCard{Title: "Verify it"})
	call[apitypes.Card](e, userSecret, http.MethodPost, cardPathOf(blocker.Number, "/links"), apitypes.LinkCards{Kind: "blocks", To: target.Number}, http.StatusOK)
	if got := e.show(agentSecret, target.Number); got.Status != "backlog" {
		t.Errorf("target = %q, want backlog: only a question's answer readies a backlog card", got.Status)
	}
}

func TestClaimByAnotherAgentConflictsAndNamesClaimant(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(agentSecret, "Shared task")
	got := e.claim(agentSecret, c.Number)
	if got.Status != "working" || got.Claim == nil || got.Claim.Actor.ID != e.agent.ID.String() || !got.Claim.ExpiresAt.Equal(t0.Add(30*time.Minute)) {
		t.Fatalf("claimed = %+v", got)
	}
	p := e.problem(agent2Secret, http.MethodPost, cardPathOf(c.Number, "/claim"), apitypes.ClaimCard{}, http.StatusConflict)
	if !strings.Contains(p.Detail, e.agent.ID.String()) || !strings.Contains(p.Hint, "lorvik next") {
		t.Errorf("problem = %+v, want claimant id in detail and a next hint", p)
	}
	// After the lease lapses anyone may take it.
	e.advance(31 * time.Minute)
	if got := e.claim(agent2Secret, c.Number); got.Claim == nil || got.Claim.Actor.ID != e.agent2.ID.String() {
		t.Errorf("claim after expiry = %+v, want agent2 as claimant", got.Claim)
	}
}

func TestRequestsRenewOnlyClaimsPastHalfTheirLease(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(agentSecret, "Long task")
	e.claim(agentSecret, c.Number)
	e.advance(10 * time.Minute)
	call[apitypes.Me](e, agentSecret, http.MethodGet, "/me", nil, http.StatusOK)
	if got := e.show(agentSecret, c.Number); !got.Claim.ExpiresAt.Equal(t0.Add(30 * time.Minute)) {
		t.Errorf("expires after 10m = %v, want unchanged (first half of the lease)", got.Claim.ExpiresAt)
	}
	e.advance(10 * time.Minute)
	call[apitypes.Me](e, agentSecret, http.MethodGet, "/me", nil, http.StatusOK)
	if got := e.show(agentSecret, c.Number); !got.Claim.ExpiresAt.Equal(t0.Add(50 * time.Minute)) {
		t.Errorf("expires after 20m = %v, want renewed to now+30m", got.Claim.ExpiresAt)
	}
}

func TestPatchWithStaleVersionConflictsWithCurrentVersion(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.readyCard(agentSecret, "v1")
	call[apitypes.Card](e, agentSecret, http.MethodPatch, cardPathOf(c.Number, ""), apitypes.UpdateCard{Version: 1, Title: new("v2")}, http.StatusOK)
	p := e.problem(agentSecret, http.MethodPatch, cardPathOf(c.Number, ""), apitypes.UpdateCard{Version: 1, Title: new("stale")}, http.StatusConflict)
	if p.CurrentVersion != 2 || !strings.Contains(p.Hint, "version 2") {
		t.Errorf("problem = %+v, want current_version 2 and a retry hint", p)
	}
}

func TestDropAsDuplicateNeedsSurvivorAndLinksIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	keep := e.readyCard(agentSecret, "keep")
	dup := e.readyCard(agentSecret, "dup")
	p := e.problem(agentSecret, http.MethodPost, cardPathOf(dup.Number, "/transition"), apitypes.TransitionCard{Version: 1, Status: "dropped", DropReason: "duplicate"}, http.StatusUnprocessableEntity)
	if !hasField(p, "duplicate") {
		t.Errorf("errors = %+v, want duplicate", p.Errors)
	}
	got := call[apitypes.Card](e, agentSecret, http.MethodPost, cardPathOf(dup.Number, "/transition"),
		apitypes.TransitionCard{Version: 1, Status: "dropped", DropReason: "duplicate", Duplicate: new(keep.Number)}, http.StatusOK)
	if got.Status != "dropped" || got.DropReason != "duplicate" {
		t.Errorf("dropped = %+v", got)
	}
	shown := e.show(agentSecret, keep.Number)
	if len(shown.Links) != 1 || shown.Links[0].Kind != "duplicates" || shown.Links[0].Direction != "in" || shown.Links[0].Number != dup.Number {
		t.Errorf("links on survivor = %+v, want an incoming duplicates link", shown.Links)
	}
}

func TestListCardsPagesWithOpaqueCursors(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for i := range 5 {
		e.readyCard(agentSecret, "card "+string(rune('a'+i)))
	}
	numbers := func(l apitypes.CardList) []int64 {
		var out []int64
		for _, c := range l.Items {
			out = append(out, c.Number)
		}
		return out
	}
	p1 := call[apitypes.CardList](e, agentSecret, http.MethodGet, "/projects/lorvik/cards?limit=2", nil, http.StatusOK)
	if !slices.Equal(numbers(p1), []int64{1, 2}) || p1.Remaining != 3 || p1.Next == "" || p1.Prev == "" {
		t.Fatalf("page 1 = %v remaining=%d next=%q prev=%q", numbers(p1), p1.Remaining, p1.Next, p1.Prev)
	}
	p2 := call[apitypes.CardList](e, agentSecret, http.MethodGet, "/projects/lorvik/cards?limit=2&cursor="+p2cursor(p1.Next), nil, http.StatusOK)
	if !slices.Equal(numbers(p2), []int64{3, 4}) || p2.Remaining != 1 {
		t.Fatalf("page 2 = %v remaining=%d", numbers(p2), p2.Remaining)
	}
	p3 := call[apitypes.CardList](e, agentSecret, http.MethodGet, "/projects/lorvik/cards?limit=2&cursor="+p2cursor(p2.Next), nil, http.StatusOK)
	if !slices.Equal(numbers(p3), []int64{5}) || p3.Remaining != 0 {
		t.Fatalf("page 3 = %v remaining=%d", numbers(p3), p3.Remaining)
	}
	back := call[apitypes.CardList](e, agentSecret, http.MethodGet, "/projects/lorvik/cards?limit=2&cursor="+p2cursor(p3.Prev), nil, http.StatusOK)
	if !slices.Equal(numbers(back), []int64{3, 4}) || back.Remaining != 2 {
		t.Fatalf("back from page 3 = %v remaining=%d, want [3 4] with 2 before", numbers(back), back.Remaining)
	}
	// A cursor from another list, or garbage, is a 400.
	notes := call[apitypes.NoteList](e, agentSecret, http.MethodGet, "/notes", nil, http.StatusOK)
	_ = notes
	e.problem(agentSecret, http.MethodGet, "/projects/lorvik/cards?cursor=garbage", nil, http.StatusBadRequest)
	decCursor := cursor{List: allCardList, ID: 1}.encode()
	e.problem(agentSecret, http.MethodGet, "/projects/lorvik/cards?cursor="+decCursor, nil, http.StatusBadRequest)
}

// p2cursor keeps a cursor safe inside a query string.
func p2cursor(s string) string { return strings.ReplaceAll(s, "=", "%3D") }

func TestTokenRotatesNearExpiryAndOldOneDiesAfterGrace(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	// Created 27 days ago with a 30-day life: inside the last rotation window.
	tok := e.seedToken("lvk_u_expiring-secr", e.user, domain.ScopeWrite, t0.Add(3*24*time.Hour))
	e.fs.mu.Lock()
	e.fs.tokens[tok.ID].CreatedAt = t0.Add(-27 * 24 * time.Hour)
	e.fs.mu.Unlock()
	rec := e.do("lvk_u_expiring-secr", http.MethodGet, "/me", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	successor := rec.Header().Get(apitypes.HeaderToken)
	if !strings.HasPrefix(successor, "lvk_u_") {
		t.Fatalf("%s = %q, want a successor secret", apitypes.HeaderToken, successor)
	}
	if !slices.Contains(e.eventTypes(), "token.rotated") {
		t.Errorf("events = %v, want token.rotated", e.eventTypes())
	}
	// The successor works and is not rotated again; the old one still works in its grace period.
	if rec := e.do(successor, http.MethodGet, "/me", nil); rec.Code != http.StatusOK || rec.Header().Get(apitypes.HeaderToken) != "" {
		t.Errorf("successor call = %d, rotated again = %q", rec.Code, rec.Header().Get(apitypes.HeaderToken))
	}
	if rec := e.do("lvk_u_expiring-secr", http.MethodGet, "/me", nil); rec.Code != http.StatusOK || rec.Header().Get(apitypes.HeaderToken) != "" {
		t.Errorf("old token in grace = %d, want 200 without a second rotation", rec.Code)
	}
	// After the grace period the old secret is dead; presenting it is recorded
	// as a reuse. The successor, held by whoever picked up the header, lives on.
	e.advance(domain.TokenRotationGrace + time.Minute)
	if p := e.problem("lvk_u_expiring-secr", http.MethodGet, "/me", nil, http.StatusUnauthorized); p.Detail != "token rotated" {
		t.Errorf("reuse detail = %q", p.Detail)
	}
	call[apitypes.Me](e, successor, http.MethodGet, "/me", nil, http.StatusOK)
	if !slices.Contains(e.eventTypes(), "token.reuse_detected") {
		t.Errorf("events = %v, want token.reuse_detected", e.eventTypes())
	}
}

func TestRemovingAMemberRevokesTheirTokens(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	path := "/members/user/" + e.member.ID.String()
	e.problem(memberSecret, http.MethodDelete, path, nil, http.StatusForbidden)
	if rec := e.do(userSecret, http.MethodDelete, path, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("remove = %d: %s", rec.Code, rec.Body)
	}
	e.problem(memberSecret, http.MethodGet, "/me", nil, http.StatusUnauthorized)
	members := call[apitypes.MemberList](e, userSecret, http.MethodGet, "/members", nil, http.StatusOK).Items
	for _, m := range members {
		if m.Actor.ID == e.member.ID.String() {
			t.Errorf("removed member still listed: %+v", m)
		}
	}
	if !slices.Contains(e.eventTypes(), "membership.removed") {
		t.Errorf("events = %v, want membership.removed", e.eventTypes())
	}
}

func TestActorsResolvesIdsWithoutLeakingOtherOrgs(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	outsider := e.seedUser("outsider", 0)
	got := call[apitypes.Actors](e, agent2Secret, http.MethodGet,
		"/actors?id="+e.user.ID.String()+","+e.agent.ID.String()+","+outsider.ID.String(), nil, http.StatusOK)
	if len(got.Users) != 1 || got.Users[0].Login != "ada" || len(got.Agents) != 1 || got.Agents[0].Name != "claude-code" {
		t.Errorf("actors = %+v, want the owner and the org agent, not the outsider", got)
	}
}
