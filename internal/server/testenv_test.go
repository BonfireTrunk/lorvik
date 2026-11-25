package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// testEnv is the real API over a fake store. The service clock is frozen at
// t0 and advanced by tests; ids come from a generator on the real clock so
// the counter never has to wait for a frozen millisecond.
type testEnv struct {
	t      *testing.T
	h      http.Handler
	svc    *Service
	fs     *fakeStore
	ids    *domain.IDGen
	org    domain.Org
	proj   domain.Project
	global domain.Project
	user   domain.ActorRef // owner
	member domain.ActorRef // a user with the member role
	agent  domain.ActorRef // org-wide agent, work and ready rights
	agent2 domain.ActorRef // agent bound to the project only, work and ready rights

	mu  sync.Mutex
	now time.Time
	// renewed are the seeded agent tokens, which advance keeps alive the
	// way the CLI renews an agent's token from its owner's login.
	renewed []domain.ID
}

const (
	userSecret   = "lvk_u_owner-secret"
	memberSecret = "lvk_u_member-secret"
	agentSecret  = "lvk_a_agent-secret"
	agent2Secret = "lvk_a_agent2-secret"
)

func (e *testEnv) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *testEnv) advance(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = e.now.Add(d)
	for _, id := range e.renewed {
		e.fs.renewToken(id, e.now, domain.AgentTokenTTL)
	}
}

// newEnv seeds one org, an owner, a member, an org-wide agent, a
// project-bound agent and a project "lorvik". The users' tokens last 30
// days, the agents' an hour, renewed whenever the clock advances.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	fs := newFakeStore()
	// Ids read a clock that moves a millisecond per read. Inside a synctest
	// bubble time.Now starts before IDEpoch and never reaches it, so a counter
	// that wraps would wait for a next millisecond that never comes.
	var tick atomic.Int64
	ids, err := domain.NewIDGen(1, func() time.Time { return t0.Add(time.Duration(tick.Add(1)) * time.Millisecond) })
	if err != nil {
		t.Fatal(err)
	}
	e := &testEnv{t: t, fs: fs, ids: ids, now: t0}
	e.org, _ = fs.CreateOrg(ctx, domain.Org{ID: ids.New(), Name: "Acme", Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	e.user = e.seedUser("ada", domain.RoleOwner)
	e.member = e.seedUser("sam", domain.RoleMember)
	e.proj, _ = fs.CreateProject(ctx, domain.Project{ID: ids.New(), OrgID: e.org.ID, Slug: "lorvik", Name: "Lorvik", Visibility: domain.VisibilityOrg, Concurrency: domain.ConcurrencyParallel, Scope: domain.ProjectScopeProject,
		Lease: 30 * time.Minute, CreatedBy: e.user, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	e.global, _ = fs.CreateProject(ctx, domain.Project{ID: ids.New(), OrgID: e.org.ID, Slug: domain.GlobalSlug, Name: "Global", Visibility: domain.VisibilityOrg, Concurrency: domain.ConcurrencyParallel,
		Scope: domain.ProjectScopeGlobal, Lease: 30 * time.Minute, CreatedBy: e.user, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	e.agent = e.seedAgent("claude-code", domain.HarnessClaudeCode)
	_, _ = fs.PutMembership(ctx, domain.Membership{OrgID: e.org.ID, Actor: e.agent, Role: domain.RoleViewer, Rights: domain.RightWork | domain.RightReady, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	e.agent2 = e.seedAgent("codex", domain.HarnessCodex)
	_, _ = fs.PutProjectMember(ctx, domain.ProjectMember{ProjectID: e.proj.ID, Actor: e.agent2, Role: domain.RoleViewer, Rights: domain.RightWork | domain.RightReady, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	for secret, actor := range map[string]domain.ActorRef{userSecret: e.user, memberSecret: e.member, agentSecret: e.agent, agent2Secret: e.agent2} {
		if actor.Kind == domain.ActorAgent {
			e.renewed = append(e.renewed, e.seedToken(secret, actor, domain.ScopeWrite, t0.Add(domain.AgentTokenTTL)).ID)
			continue
		}
		e.seedToken(secret, actor, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
	}
	e.svc = NewService(fs, ids, e.clock, slog.New(slog.DiscardHandler))
	e.svc.aside = func(f func()) { f() }
	e.h = newAPI(Deps{Logger: slog.New(slog.DiscardHandler), Store: fs, DBPing: fs.Ping, Now: e.clock}, e.svc)
	return e
}

func (e *testEnv) seedUser(login string, role domain.Role) domain.ActorRef {
	ctx := context.Background()
	u, _ := e.fs.CreateUser(ctx, domain.User{ID: e.ids.New(), Login: login, Name: login, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	if role != 0 {
		_, _ = e.fs.PutMembership(ctx, domain.Membership{OrgID: e.org.ID, Actor: u.Ref(), Role: role, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	}
	return u.Ref()
}

func (e *testEnv) seedAgent(name string, h domain.Harness) domain.ActorRef {
	a, _ := e.fs.CreateAgent(context.Background(), domain.Agent{ID: e.ids.New(), OrgID: e.org.ID, Name: name, Harness: h, CreatedBy: e.user.ID,
		Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	return a.Ref()
}

func (e *testEnv) seedToken(secret string, actor domain.ActorRef, scope domain.TokenScope, expires time.Time) domain.Token {
	e.t.Helper()
	tok, err := e.fs.CreateToken(context.Background(), domain.Token{ID: e.ids.New(), OrgID: e.org.ID, Actor: actor, Name: "test", Prefix: secret[:domain.TokenPrefixLen],
		Hash: domain.HashToken(secret), Scope: scope, Status: domain.RecordActive, ExpiresAt: expires, CreatedAt: t0})
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

func (e *testEnv) do(secret, method, path string, body any, headers ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, "/api/v1"+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// call performs a request, requires the status, and decodes the body into T.
func call[T any](e *testEnv, secret, method, path string, body any, want int) T {
	e.t.Helper()
	rec := e.do(secret, method, path, body)
	if rec.Code != want {
		e.t.Fatalf("%s %s = %d, want %d: %s", method, path, rec.Code, want, rec.Body)
	}
	var v T
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			e.t.Fatalf("decode %s %s: %v: %s", method, path, err, rec.Body)
		}
	}
	return v
}

func (e *testEnv) problem(secret, method, path string, body any, want int) apitypes.Problem {
	e.t.Helper()
	rec := e.do(secret, method, path, body)
	if rec.Code != want {
		e.t.Fatalf("%s %s = %d, want %d: %s", method, path, rec.Code, want, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		e.t.Errorf("%s %s content type = %q, want problem+json", method, path, ct)
	}
	var p apitypes.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		e.t.Fatalf("decode problem: %v: %s", err, rec.Body)
	}
	return p
}

func (e *testEnv) newCard(secret string, in apitypes.CreateCard) apitypes.Card {
	e.t.Helper()
	return call[apitypes.Card](e, secret, http.MethodPost, "/projects/lorvik/cards", in, http.StatusCreated)
}

// readyCard creates a ready task.
func (e *testEnv) readyCard(secret, title string) apitypes.Card {
	e.t.Helper()
	return e.newCard(secret, apitypes.CreateCard{Title: title, Status: "ready"})
}

func (e *testEnv) claim(secret string, n int64) apitypes.Card {
	e.t.Helper()
	return call[apitypes.Card](e, secret, http.MethodPost, cardPathOf(n, "/claim"), apitypes.ClaimCard{}, http.StatusOK)
}

func (e *testEnv) show(secret string, n int64) apitypes.Card {
	e.t.Helper()
	return call[apitypes.Card](e, secret, http.MethodGet, cardPathOf(n, ""), nil, http.StatusOK)
}

func (e *testEnv) eventTypes() []string {
	e.fs.mu.Lock()
	defer e.fs.mu.Unlock()
	var out []string
	for _, ev := range e.fs.events {
		out = append(out, ev.Type.String())
	}
	return out
}

func (e *testEnv) lastEvent() domain.Event {
	e.fs.mu.Lock()
	defer e.fs.mu.Unlock()
	return e.fs.events[len(e.fs.events)-1]
}

// latestPosition is the wire position of the newest event, or "" when there
// is none.
func (e *testEnv) latestPosition() string {
	e.fs.mu.Lock()
	defer e.fs.mu.Unlock()
	if len(e.fs.events) == 0 {
		return ""
	}
	return eventPosition(positionOf(e.fs.events[len(e.fs.events)-1]))
}

func cardPathOf(n int64, suffix string) string {
	return "/projects/lorvik/cards/" + strconv.FormatInt(n, 10) + suffix
}

//go:fix inline

//go:fix inline

func hasField(p apitypes.Problem, field string) bool {
	for _, f := range p.Errors {
		if f.Field == field {
			return true
		}
	}
	return false
}

// A test env inside a synctest bubble can take more ids than the counter
// holds in one millisecond without waiting for good.
func TestTheTestEnvsIDsKeepComingInsideASynctestBubble(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		prev := e.ids.New()
		for range 1 << 17 {
			next := e.ids.New()
			if next <= prev {
				t.Fatalf("id %d after %d, want increasing", next, prev)
			}
			prev = next
		}
	})
}
