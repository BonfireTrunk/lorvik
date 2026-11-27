//go:build integration

package store

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
)

const defaultTestDatabaseURL = "postgres://lorvik:lorvik@localhost:5432/lorvik_test?sslmode=disable"

// testStore is opened once in TestMain and shared by all tests. It is nil when
// the database is unreachable, in which case every test skips.
var (
	testStore  *Store
	skipReason string
	gen        *domain.IDGen
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	url := os.Getenv("LORVIK_TEST_DATABASE_URL")
	if url == "" {
		url = defaultTestDatabaseURL
	}
	url = withPoolSize(url, 3*runtime.GOMAXPROCS(0))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := Open(ctx, url)
	if err != nil {
		skipReason = fmt.Sprintf("test database unavailable: %v", err)
		return m.Run()
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "migrate test database: %v\n", err)
		return 1
	}
	gen, err = domain.NewIDGen(-1, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "id generator: %v\n", err)
		return 1
	}
	testStore = st
	return m.Run()
}

// withPoolSize sets the shared pool's size unless the URL already does. A race
// test holds three connections at once (its transaction, a second writer
// blocked behind it and the poll that sees the block), and up to GOMAXPROCS
// tests run in parallel. pgx's default of max(4, CPUs) connections left the
// poll waiting for a free one on a 2-CPU machine until the test's deadline.
func withPoolSize(url string, n int) string {
	if strings.Contains(url, "pool_max_conns") {
		return url
	}
	param := fmt.Sprintf("pool_max_conns=%d", n)
	switch {
	case !strings.Contains(url, "://"):
		return url + " " + param
	case strings.Contains(url, "?"):
		return url + "&" + param
	default:
		return url + "?" + param
	}
}

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// env is an isolated tenant: its own org, project and actors. Tests never
// share rows, so they run in parallel without truncating tables.
type env struct {
	st      *Store
	org     domain.Org
	project domain.Project
	global  domain.Project
	user    domain.ActorRef
	agent   domain.ActorRef
}

func newEnv(t *testing.T) *env {
	t.Helper()
	t.Parallel()
	if testStore == nil {
		t.Skip(skipReason)
	}
	ctx := t.Context()
	org, err := testStore.CreateOrg(ctx, domain.Org{ID: gen.New(), Name: "Test", Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	u, err := testStore.CreateUser(ctx, domain.User{ID: gen.New(), Login: "tester", Name: "Tester", Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	a, err := testStore.CreateAgent(ctx, domain.Agent{ID: gen.New(), OrgID: org.ID, Name: "bot", Harness: domain.HarnessOther, CreatedBy: u.ID, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	e := &env{st: testStore, org: org, user: u.Ref(), agent: a.Ref()}
	e.project = e.newProject(t, "main")
	e.global = e.newProject(t, domain.GlobalSlug)
	return e
}

func (e *env) newProject(t *testing.T, slug string) domain.Project {
	t.Helper()
	scope := domain.ProjectScopeProject
	if slug == domain.GlobalSlug {
		scope = domain.ProjectScopeGlobal
	}
	p, err := e.st.CreateProject(t.Context(), domain.Project{ID: gen.New(), OrgID: e.org.ID, Slug: slug, Name: slug, Visibility: domain.VisibilityOrg, Concurrency: domain.ConcurrencyParallel, Scope: scope,
		Lease: 30 * time.Minute, CreatedBy: e.user, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		t.Fatalf("create project %s: %v", slug, err)
	}
	return p
}

// card creates a ready, normal-priority task in the env's project; mut
// adjusts it first.
func (e *env) card(t *testing.T, mut func(*domain.Card)) domain.Card {
	t.Helper()
	c := domain.Card{ID: gen.New(), OrgID: e.org.ID, ProjectID: e.project.ID, Kind: domain.KindTask, Status: domain.StatusReady,
		Priority: domain.PriorityNormal, Title: "task", CreatedBy: e.agent, CreatedAt: t0, UpdatedAt: t0}
	if mut != nil {
		mut(&c)
	}
	out, err := e.st.CreateCard(t.Context(), c)
	if err != nil {
		t.Fatalf("create card: %v", err)
	}
	return out
}

func (e *env) claim(t *testing.T, c domain.Card, actor domain.ActorRef, expires time.Time) {
	t.Helper()
	cl := domain.Claim{CardID: c.ID, OrgID: e.org.ID, ProjectID: c.ProjectID, Actor: actor, ExpiresAt: expires, CreatedAt: t0}
	if err := e.st.PutClaim(t.Context(), cl); err != nil {
		t.Fatalf("put claim: %v", err)
	}
}

func (e *env) event(t *testing.T, actor domain.ActorRef, projectID *domain.ID, typ domain.EventType, subj domain.Subject) domain.Event {
	t.Helper()
	ev, err := domain.NewEvent(gen.New(), e.org.ID, projectID, typ, actor, subj, map[string]any{"title": "t"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.AppendEvent(t.Context(), ev); err != nil {
		t.Fatalf("append event: %v", err)
	}
	return ev
}

// eventsVisible waits until the feed shows every event of the org. The feed
// stops below the server's snapshot xmin, so a transaction open anywhere on the
// server, a live lorvikd's included, can briefly hold back a committed event.
func (e *env) eventsVisible(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	for {
		var held int
		err := e.st.db.QueryRow(ctx, `SELECT count(*) FROM events WHERE org_id = $1 AND xid >= pg_snapshot_xmin(pg_current_snapshot())`, int64(e.org.ID)).Scan(&held)
		if err != nil {
			t.Fatalf("poll event visibility: %v", err)
		}
		if held == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func numbers(cards []domain.Card) []int64 {
	out := make([]int64, 0, len(cards))
	for _, c := range cards {
		out = append(out, c.Number)
	}
	return out
}

func equalInts(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTimestampsScanInUTCWhateverTheProcessZone(t *testing.T) {
	e := newEnv(t)
	u, err := e.st.GetUser(t.Context(), e.user.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if !u.CreatedAt.Equal(t0) {
		t.Fatalf("created_at = %v, want the instant %v", u.CreatedAt, t0)
	}
	if u.CreatedAt.Location() != time.UTC {
		t.Errorf("created_at location = %v, want UTC", u.CreatedAt.Location())
	}
}
