package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// move transitions a card at its current version and requires success.
func (e *testEnv) move(secret string, n int64, in apitypes.TransitionCard) apitypes.Card {
	e.t.Helper()
	in.Version = e.show(secret, n).Version
	return call[apitypes.Card](e, secret, http.MethodPost, cardPathOf(n, "/transition"), in, http.StatusOK)
}

// moveFails transitions a card at its current version and requires the status.
func (e *testEnv) moveFails(secret string, n int64, in apitypes.TransitionCard, want int) apitypes.Problem {
	e.t.Helper()
	in.Version = e.show(secret, n).Version
	return e.problem(secret, http.MethodPost, cardPathOf(n, "/transition"), in, want)
}

func (e *testEnv) decision(secret, title string, status string) apitypes.Card {
	e.t.Helper()
	return e.newCard(secret, apitypes.CreateCard{Kind: "decision", Title: title, Status: status, Body: "## Context\n\nc\n\n## Decision\n\nd\n"})
}

// asReviewer gives the org-wide agent every right, the most an owner can lend.
func (e *testEnv) asReviewer() {
	e.t.Helper()
	if _, err := e.fs.PutMembership(context.Background(), domain.Membership{OrgID: e.org.ID, Actor: e.agent, Role: domain.RoleViewer, Rights: domain.AllRights,
		Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		e.t.Fatal(err)
	}
}

// eventsOf returns the payloads of the events of one type, oldest first.
func (e *testEnv) eventsOf(typ string) []map[string]any {
	e.t.Helper()
	e.fs.mu.Lock()
	defer e.fs.mu.Unlock()
	var out []map[string]any
	for _, ev := range e.fs.events {
		if ev.Type.String() != typ {
			continue
		}
		m := map[string]any{"_subject": float64(ev.Subject.Number)}
		if err := json.Unmarshal(ev.Payload, &m); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func TestDecisionIsCreatedReadyByAnAgentAndDoneByAHumanReviewer(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p := e.decision(agentSecret, "Use pgx", "")
	if p.Kind != "decision" || p.Status != "ready" || p.Decision == nil || p.Decision.AcceptedBy != nil {
		t.Fatalf("agent decision = %+v, want a proposed (ready) decision with no acceptor", p)
	}
	if got := e.eventsOf("card.created"); got[0]["kind"] != "decision" {
		t.Errorf("card.created payload = %v, want kind decision", got[0])
	}
	d := e.decision(userSecret, "Use Go", "done")
	if d.Status != "done" || d.ClosedAt == nil || d.Decision == nil || d.Decision.AcceptedBy == nil || d.Decision.AcceptedBy.ID != e.user.ID.String() || d.Decision.AcceptedAt == nil {
		t.Errorf("recorded decision = %+v, want done with the reviewer as acceptor", d)
	}
	for name, secret := range map[string]string{"agent": agentSecret, "human member": memberSecret} {
		e.problem(secret, http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Kind: "decision", Title: name, Status: "done"}, http.StatusForbidden)
	}
	for _, st := range []string{"backlog"} {
		pr := e.problem(userSecret, http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Kind: "decision", Title: "x", Status: st}, http.StatusUnprocessableEntity)
		if !hasField(pr, "status") {
			t.Errorf("errors = %+v, want status", pr.Errors)
		}
	}
	// Only a decision may be created done.
	pr := e.problem(userSecret, http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Title: "t", Status: "done"}, http.StatusUnprocessableEntity)
	if !hasField(pr, "status") {
		t.Errorf("errors = %+v, want status", pr.Errors)
	}
}

func TestAgentWithTriageCanEditAProposalAndNoneAcceptsWithoutTheDecideRight(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.giveRights(domain.AllRights &^ domain.RightDecide)
	prop := e.decision(memberSecret, "Use pgx", "")
	e.moveFails(agentSecret, prop.Number, apitypes.TransitionCard{Status: "done"}, http.StatusForbidden)
	e.moveFails(agentSecret, prop.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "rejected"}, http.StatusForbidden)
	got := call[apitypes.Card](e, agentSecret, http.MethodPatch, cardPathOf(prop.Number, ""), apitypes.UpdateCard{Version: prop.Version, Body: new("reworded")}, http.StatusOK)
	if got.Body != "reworded" {
		t.Errorf("edited proposal = %+v", got)
	}
	pr := e.problem(agent2Secret, http.MethodPatch, cardPathOf(prop.Number, ""), apitypes.UpdateCard{Version: got.Version, Body: new("mine")}, http.StatusForbidden)
	if !strings.Contains(pr.Detail, "triage") {
		t.Errorf("detail = %q, want it to name triage", pr.Detail)
	}
	prop = got
	own := call[apitypes.Card](e, memberSecret, http.MethodPatch, cardPathOf(prop.Number, ""), apitypes.UpdateCard{Version: prop.Version, Title: new("Use pgx v5")}, http.StatusOK)
	if own.Title != "Use pgx v5" {
		t.Errorf("own edit = %+v", own)
	}
	// A member cannot accept either, being a human without decision-accept.
	e.moveFails(memberSecret, prop.Number, apitypes.TransitionCard{Status: "done"}, http.StatusForbidden)
	acc := e.move(userSecret, prop.Number, apitypes.TransitionCard{Status: "done"})
	if acc.Status != "done" || acc.Decision.AcceptedBy == nil || acc.Decision.AcceptedBy.ID != e.user.ID.String() || acc.Decision.AcceptedAt == nil {
		t.Errorf("accepted = %+v", acc)
	}
}

func TestDecisionRejectRevokeAndReproposeFollowTheTable(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	prop := e.decision(agentSecret, "Use pgx", "")
	n := prop.Number
	for name, in := range map[string]apitypes.TransitionCard{
		"obsolete reason on a proposal": {Status: "dropped", DropReason: "obsolete"},
		"revoked reason on a proposal":  {Status: "dropped", DropReason: "revoked"},
		"superseded from a client":      {Status: "dropped", DropReason: "superseded"},
		"no reason":                     {Status: "dropped"},
		"waiting":                       {Status: "waiting"},
		"backlog":                       {Status: "backlog"},
	} {
		pr := e.moveFails(userSecret, n, in, http.StatusUnprocessableEntity)
		if !hasField(pr, "status") && !hasField(pr, "drop_reason") {
			t.Errorf("%s: errors = %+v, want status or drop_reason", name, pr.Errors)
		}
	}
	rej := e.move(userSecret, n, apitypes.TransitionCard{Status: "dropped", DropReason: "rejected"})
	if rej.Status != "dropped" || rej.DropReason != "rejected" {
		t.Fatalf("rejected = %+v", rej)
	}
	// Re-proposing: its proposer, or a reviewer; another member cannot.
	e.moveFails(memberSecret, n, apitypes.TransitionCard{Status: "ready"}, http.StatusForbidden)
	if re := e.move(agentSecret, n, apitypes.TransitionCard{Status: "ready"}); re.Status != "ready" || re.DropReason != "" || re.ClosedAt != nil {
		t.Fatalf("re-proposed = %+v", re)
	}
	e.move(userSecret, n, apitypes.TransitionCard{Status: "done"})
	// Accepted: only a revocation is left, and a revoked decision stays dropped.
	e.moveFails(userSecret, n, apitypes.TransitionCard{Status: "ready"}, http.StatusUnprocessableEntity)
	e.moveFails(userSecret, n, apitypes.TransitionCard{Status: "dropped", DropReason: "rejected"}, http.StatusUnprocessableEntity)
	rev := e.move(userSecret, n, apitypes.TransitionCard{Status: "dropped", DropReason: "revoked"})
	if rev.DropReason != "revoked" || rev.Decision == nil || rev.Decision.AcceptedBy == nil {
		t.Errorf("revoked = %+v, want the acceptor kept", rev)
	}
	pr := e.moveFails(userSecret, n, apitypes.TransitionCard{Status: "ready"}, http.StatusUnprocessableEntity)
	if !hasField(pr, "status") {
		t.Errorf("reopening a revoked decision: %+v, want status", pr.Errors)
	}
	// The other kinds cannot be dropped with the decision-only reasons.
	task := e.readyCard(agentSecret, "t")
	pr = e.moveFails(agentSecret, task.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "rejected"}, http.StatusUnprocessableEntity)
	if !hasField(pr, "drop_reason") {
		t.Errorf("task dropped as rejected: %+v, want drop_reason", pr.Errors)
	}
}

func TestDecisionCannotBeClaimedOrStarted(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	d := e.decision(agentSecret, "Use pgx", "")
	p := e.problem(agentSecret, http.MethodPost, cardPathOf(d.Number, "/claim"), apitypes.ClaimCard{}, http.StatusConflict)
	if !strings.Contains(p.Hint, "accepts or rejects") {
		t.Errorf("claim hint = %q", p.Hint)
	}
	e.moveFails(userSecret, d.Number, apitypes.TransitionCard{Status: "working"}, http.StatusConflict)
	e.problem(agentSecret, http.MethodGet, "/projects/lorvik/cards/next", nil, http.StatusNotFound)
}

func TestDecisionTextIsFrozenOnceSettled(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	d := e.decision(userSecret, "Use Go", "done")
	p := e.problem(userSecret, http.MethodPatch, cardPathOf(d.Number, ""), apitypes.UpdateCard{Version: d.Version, Title: new("Use Rust")}, http.StatusConflict)
	if p.Hint != "accepted text is frozen; record a superseding decision" {
		t.Errorf("hint = %q", p.Hint)
	}
	e.problem(userSecret, http.MethodPatch, cardPathOf(d.Number, ""), apitypes.UpdateCard{Version: d.Version, Body: new("x")}, http.StatusConflict)
	got := call[apitypes.Card](e, userSecret, http.MethodPatch, cardPathOf(d.Number, ""),
		apitypes.UpdateCard{Version: d.Version, Priority: new("high"), Lane: new("db")}, http.StatusOK)
	if got.Priority != "high" || got.Lane != "db" {
		t.Errorf("priority and lane edits on a settled decision = %+v, want allowed", got)
	}
	rev := e.move(userSecret, d.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "revoked"})
	e.problem(userSecret, http.MethodPatch, cardPathOf(d.Number, ""), apitypes.UpdateCard{Version: rev.Version, Title: new("again")}, http.StatusConflict)
}

func TestSupersedingDecisionRetiresTheOldOneWhenAccepted(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	old := e.decision(userSecret, "SQLite", "done")
	next := e.newCard(agentSecret, apitypes.CreateCard{Kind: "decision", Title: "Postgres", Supersedes: new(old.Number)})
	if next.Decision == nil || next.Decision.Supersedes == nil || *next.Decision.Supersedes != old.Number {
		t.Fatalf("proposal = %+v, want supersedes #%d", next.Decision, old.Number)
	}
	if got := e.show(agentSecret, old.Number); got.Status != "done" || got.Decision.SupersededBy != nil {
		t.Fatalf("old while the successor is only proposed = %+v, want done and not superseded yet", got)
	}
	e.move(userSecret, next.Number, apitypes.TransitionCard{Status: "done"})
	got := e.show(agentSecret, old.Number)
	if got.Status != "dropped" || got.DropReason != "superseded" || got.Decision.SupersededBy == nil || *got.Decision.SupersededBy != next.Number {
		t.Errorf("old = %+v, want dropped(superseded) by #%d", got, next.Number)
	}
	evs := e.eventsOf("card.status_changed")
	last := evs[len(evs)-1]
	if last["kind"] != "decision" {
		t.Errorf("status_changed kind = %v, want decision", last["kind"])
	}
	if last["superseded_by"] != float64(next.Number) || last["_subject"] != float64(old.Number) || last["drop_reason"] != "superseded" {
		t.Errorf("status_changed event = %v, want superseded_by on the old card", last)
	}
	// The chain continues, and a recorded successor retires its predecessor at once.
	third := e.newCard(userSecret, apitypes.CreateCard{Kind: "decision", Title: "CockroachDB", Status: "done", Supersedes: new(next.Number)})
	if e.show(agentSecret, next.Number).DropReason != "superseded" || third.Status != "done" {
		t.Errorf("recording a successor as done must retire #%d", next.Number)
	}
	// A superseded decision has no second successor, and only decisions are superseded.
	p := e.problem(userSecret, http.MethodPost, "/projects/lorvik/cards", apitypes.CreateCard{Kind: "decision", Title: "again", Supersedes: new(old.Number)}, http.StatusUnprocessableEntity)
	if !hasField(p, "supersedes") {
		t.Errorf("errors = %+v, want supersedes", p.Errors)
	}
	task := e.readyCard(agentSecret, "t")
	for name, in := range map[string]apitypes.CreateCard{
		"a task":         {Kind: "decision", Title: "x", Supersedes: new(task.Number)},
		"a missing card": {Kind: "decision", Title: "x", Supersedes: new(int64(999))},
		"from a task":    {Title: "x", Supersedes: new(old.Number)},
		"a proposal":     {Kind: "decision", Title: "x", Supersedes: new(e.decision(agentSecret, "p", "").Number)},
	} {
		if p := e.problem(userSecret, http.MethodPost, "/projects/lorvik/cards", in, http.StatusUnprocessableEntity); !hasField(p, "supersedes") {
			t.Errorf("supersedes %s: errors = %+v, want supersedes", name, p.Errors)
		}
	}
}

func TestSupersededByOutlivesARevokedSuccessor(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a := e.decision(userSecret, "SQLite", "done")
	b := e.newCard(agentSecret, apitypes.CreateCard{Kind: "decision", Title: "Postgres", Supersedes: new(a.Number)})
	if got := e.show(agentSecret, a.Number); got.Decision.SupersededBy != nil {
		t.Fatalf("while B is proposed, superseded_by = %v, want none", *got.Decision.SupersededBy)
	}
	e.move(userSecret, b.Number, apitypes.TransitionCard{Status: "done"})
	e.move(userSecret, b.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "revoked"})
	got := e.show(agentSecret, a.Number)
	if got.DropReason != "superseded" || got.Decision.SupersededBy == nil || *got.Decision.SupersededBy != b.Number {
		t.Errorf("A after B was revoked = %+v, want dropped(superseded) still by #%d", got, b.Number)
	}
}

func TestSupersedeLeavesAnOldDecisionThatIsNoLongerDone(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	old := e.decision(userSecret, "SQLite", "done")
	a := e.newCard(agentSecret, apitypes.CreateCard{Kind: "decision", Title: "A", Supersedes: new(old.Number)})
	b := e.newCard(agentSecret, apitypes.CreateCard{Kind: "decision", Title: "B", Supersedes: new(old.Number)})
	e.move(userSecret, old.Number, apitypes.TransitionCard{Status: "dropped", DropReason: "revoked"})
	e.move(userSecret, a.Number, apitypes.TransitionCard{Status: "done"})
	e.move(userSecret, b.Number, apitypes.TransitionCard{Status: "done"})
	got := e.show(agentSecret, old.Number)
	if got.DropReason != "revoked" {
		t.Errorf("old = %+v, want it left as revoked", got)
	}
	for _, ev := range e.eventsOf("card.status_changed") {
		if ev["superseded_by"] != nil {
			t.Errorf("event %v: a decision that was no longer done must not be superseded", ev)
		}
	}
}

func TestAppliesLinksFromADecisionAcrossProjectsThroughGlobal(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	call[apitypes.Project](e, userSecret, http.MethodPost, "/projects", apitypes.CreateProject{Slug: "web", Name: "Web"}, http.StatusCreated)
	gd := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/global/cards", apitypes.CreateCard{Kind: "decision", Title: "Go everywhere", Status: "done"}, http.StatusCreated)
	task := e.readyCard(agentSecret, "lorvik task")
	webTask := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/web/cards", apitypes.CreateCard{Title: "web task"}, http.StatusCreated)
	link := func(secret, slug string, n int64, in apitypes.LinkCards, want int) {
		t.Helper()
		if rec := e.do(secret, http.MethodPost, "/projects/"+slug+"/cards/"+itoa(n)+"/links", in); rec.Code != want {
			t.Fatalf("link %v from %s #%d = %d, want %d: %s", in, slug, n, rec.Code, want, rec.Body)
		}
	}
	link(userSecret, "global", gd.Number, apitypes.LinkCards{Kind: "applies", To: task.Number, Project: "lorvik"}, http.StatusOK)
	link(userSecret, "global", gd.Number, apitypes.LinkCards{Kind: "applies", To: webTask.Number, Project: "web"}, http.StatusOK)
	shown := e.show(agentSecret, task.Number)
	if len(shown.Links) != 1 || shown.Links[0].Kind != "applies" || shown.Links[0].Direction != "in" || shown.Links[0].Project != "global" || shown.Links[0].Number != gd.Number {
		t.Errorf("links on the task = %+v, want an incoming applies from global #%d", shown.Links, gd.Number)
	}
	// Applies is for decisions; two ordinary projects do not link; relates works both ways via global.
	link(agentSecret, "lorvik", task.Number, apitypes.LinkCards{Kind: "applies", To: gd.Number, Project: "global"}, http.StatusUnprocessableEntity)
	link(userSecret, "lorvik", task.Number, apitypes.LinkCards{Kind: "relates", To: webTask.Number, Project: "web"}, http.StatusUnprocessableEntity)
	link(agentSecret, "lorvik", task.Number, apitypes.LinkCards{Kind: "relates", To: gd.Number, Project: "global"}, http.StatusOK)
	link(userSecret, "global", gd.Number, apitypes.LinkCards{Kind: "duplicates", To: task.Number, Project: "lorvik"}, http.StatusUnprocessableEntity)
	link(userSecret, "global", gd.Number, apitypes.LinkCards{Kind: "supersedes", To: task.Number, Project: "lorvik"}, http.StatusUnprocessableEntity)
	link(userSecret, "lorvik", task.Number, apitypes.LinkCards{Kind: "relates", To: 1, Project: "nowhere"}, http.StatusUnprocessableEntity)
	// A blocking link across projects parks the target in its own project.
	gt := call[apitypes.Card](e, userSecret, http.MethodPost, "/projects/global/cards", apitypes.CreateCard{Title: "global task", Status: "ready"}, http.StatusCreated)
	link(userSecret, "lorvik", task.Number, apitypes.LinkCards{Kind: "blocks", To: gt.Number, Project: "global"}, http.StatusOK)
	if got := call[apitypes.Card](e, userSecret, http.MethodGet, "/projects/global/cards/"+itoa(gt.Number), nil, http.StatusOK); got.Status != "waiting" {
		t.Errorf("global task = %+v, want waiting behind the lorvik card", got)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
