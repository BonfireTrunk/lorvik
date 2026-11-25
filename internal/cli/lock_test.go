package cli

import (
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func fixtureLock() apitypes.Lock {
	return apitypes.Lock{
		ID: "9001", Name: "schema", Holder: claude, Session: "keen-otter-a0f7", Purpose: "regenerating sqlc",
		Card: new(int64(42)), Project: "demo", AcquiredAt: fixedNow, ExpiresAt: fixedNow.Add(10*time.Minute + 30*time.Second),
	}
}

func lockProblem(status int, detail string) http.HandlerFunc {
	return problem(status, apitypes.Problem{Title: http.StatusText(status), Detail: detail, Hint: "wait, or pick other work"})
}

func TestLockAcquireSendsTTLAndPurposeAndPrintsTheLine(t *testing.T) {
	t.Parallel()
	var rec recorder
	h := newHarness(t, serve(t, rec.wrapAll(map[string]http.HandlerFunc{
		"PUT /api/v1/locks/schema": reply(201, fixtureLock()),
	})))
	if err := h.run(t, "lock", "schema", "-m", "regenerating sqlc", "--for", "15m"); err != nil {
		t.Fatal(err)
	}
	if got, want := h.out.String(), "lock schema held until 12:10:30 (15m) for \"regenerating sqlc\"\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got, want := rec.last(t, "PUT").Body, `{"purpose":"regenerating sqlc","ttl_seconds":900}`; strings.TrimSpace(got) != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestLockDefaultsToTenMinutes(t *testing.T) {
	t.Parallel()
	var rec recorder
	h := newHarness(t, serve(t, rec.wrapAll(map[string]http.HandlerFunc{
		"PUT /api/v1/locks/schema": reply(201, fixtureLock()),
	})))
	if err := h.run(t, "lock", "schema", "-m", "x"); err != nil {
		t.Fatal(err)
	}
	if got := rec.last(t, "PUT").Body; !strings.Contains(got, `"ttl_seconds":600`) {
		t.Errorf("body = %s, want ttl_seconds 600", got)
	}
}

func TestLockCardSendsCardAndProject(t *testing.T) {
	t.Parallel()
	var rec recorder
	h := newHarness(t, serve(t, rec.wrapAll(map[string]http.HandlerFunc{
		"PUT /api/v1/locks/schema": reply(201, fixtureLock()),
	})))
	if err := h.run(t, "-p", "demo", "lock", "schema", "-m", "x", "--card", "42"); err != nil {
		t.Fatal(err)
	}
	got := rec.last(t, "PUT").Body
	if !strings.Contains(got, `"card":42`) || !strings.Contains(got, `"project":"demo"`) {
		t.Errorf("body = %s, want card 42 and project demo", got)
	}
}

func TestLockUsageErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{"no purpose", []string{"lock", "schema"}},
		{"non-positive ttl", []string{"lock", "schema", "-m", "x", "--for", "0s"}},
		{"card without project", []string{"lock", "schema", "-m", "x", "--card", "3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, serve(t, nil))
			err := h.run(t, tt.args...)
			if cmdutil.ExitCode(err) != cmdutil.ExitUsage {
				t.Errorf("exit = %d (%v), want 2", cmdutil.ExitCode(err), err)
			}
		})
	}
}

func TestLockHeldExitsFourWithServerMessage(t *testing.T) {
	t.Parallel()
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"PUT /api/v1/locks/schema": lockProblem(409, "lock held: schema is held by agent 7"),
	}))
	err := h.run(t, "lock", "schema", "-m", "x")
	if cmdutil.ExitCode(err) != cmdutil.ExitConflict {
		t.Fatalf("exit = %d (%v), want 4", cmdutil.ExitCode(err), err)
	}
	msg := cmdutil.FormatError(err, nil)
	if !strings.Contains(msg, "lock held: schema is held by agent 7") || !strings.Contains(msg, "hint: wait, or pick other work") {
		t.Errorf("message = %q", msg)
	}
}

func TestLockWaitAcquiresAfterAConflict(t *testing.T) {
	t.Parallel()
	var puts atomic.Int32
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"PUT /api/v1/locks/schema": func(w http.ResponseWriter, r *http.Request) {
			if puts.Add(1) == 1 {
				lockProblem(409, `lock held: schema is held by agent 7 (session "keen") for "gen", 40s left`)(w, r)
				return
			}
			reply(201, fixtureLock())(w, r)
		},
	}))
	h.f.LockPoll = time.Millisecond
	if err := h.run(t, "lock", "schema", "-m", "x", "--wait"); err != nil {
		t.Fatal(err)
	}
	if puts.Load() != 2 {
		t.Errorf("PUT count = %d, want 2", puts.Load())
	}
	if got, want := h.errOut.String(), "waiting for schema: held by agent 7 (session \"keen\") for \"gen\", 40s left\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestLockWaitDoesNotRetryOtherErrors(t *testing.T) {
	t.Parallel()
	var puts atomic.Int32
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"PUT /api/v1/locks/schema": func(w http.ResponseWriter, r *http.Request) {
			puts.Add(1)
			lockProblem(403, "no")(w, r)
		},
	}))
	h.f.LockPoll = time.Millisecond
	err := h.run(t, "lock", "schema", "-m", "x", "--wait")
	if cmdutil.ExitCode(err) != cmdutil.ExitUnauthorized || puts.Load() != 1 {
		t.Errorf("exit = %d, PUTs = %d, want 5 and 1", cmdutil.ExitCode(err), puts.Load())
	}
}

func TestUnlockForceSendsForce(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"plain", []string{"unlock", "schema"}, "DELETE /api/v1/locks/schema"},
		{"force", []string{"unlock", "schema", "--force"}, "DELETE /api/v1/locks/schema?force=true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var rec recorder
			h := newHarness(t, serve(t, rec.wrapAll(map[string]http.HandlerFunc{
				"DELETE /api/v1/locks/schema": reply(204, nil),
			})))
			if err := h.run(t, tt.args...); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(rec.calls(), []string{tt.want}) {
				t.Errorf("calls = %v, want [%s]", rec.calls(), tt.want)
			}
			if h.out.String() != "released schema\n" {
				t.Errorf("stdout = %q", h.out.String())
			}
		})
	}
}

func TestUnlockJSON(t *testing.T) {
	t.Parallel()
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{"DELETE /api/v1/locks/schema": reply(204, nil)}))
	if err := h.run(t, "--json", "unlock", "schema"); err != nil {
		t.Fatal(err)
	}
	if got, want := h.out.String(), "{\n  \"name\": \"schema\",\n  \"released\": true\n}\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestLocksListsOneLinePerLock(t *testing.T) {
	t.Parallel()
	port := fixtureLock()
	port.Name, port.Holder, port.Session, port.Card, port.ExpiresAt = "port/8080", ana, "", nil, fixedNow.Add(time.Minute)
	gate := fixtureLock()
	gate.Name, gate.SessionName, gate.Card = "ci", "Gate run", nil
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/locks":  reply(200, apitypes.LockList{Items: []apitypes.Lock{fixtureLock(), port, gate}}),
		"GET /api/v1/actors": reply(200, fixtureActors()),
	}))
	if err := h.run(t, "locks"); err != nil {
		t.Fatal(err)
	}
	want := "schema  claude-code (agent)  keen-otter-a0f7  11m left  \"regenerating sqlc\"  #42\n" +
		"port/8080  Ana Souza  -  1m left  \"regenerating sqlc\"\n" +
		"ci  claude-code (agent)  Gate run (keen-otter-a0f7)  11m left  \"regenerating sqlc\"\n"
	if h.out.String() != want {
		t.Errorf("stdout =\n%q\nwant\n%q", h.out.String(), want)
	}
}

func TestLocksEmpty(t *testing.T) {
	t.Parallel()
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{"GET /api/v1/locks": reply(200, apitypes.LockList{})}))
	if err := h.run(t, "locks"); err != nil {
		t.Fatal(err)
	}
	if h.out.String() != "no locks\n" {
		t.Errorf("stdout = %q", h.out.String())
	}
}

func TestInboxPrintsLiveLocks(t *testing.T) {
	t.Parallel()
	h := newHarness(t, serve(t, map[string]http.HandlerFunc{
		"GET /api/v1/inbox":  reply(200, apitypes.Inbox{Events: []apitypes.Event{}, Cursor: "5", Locks: []apitypes.Lock{fixtureLock()}}),
		"GET /api/v1/actors": reply(200, fixtureActors()),
	}))
	if err := h.run(t, "inbox", "--peek"); err != nil {
		t.Fatal(err)
	}
	want := "no new events\nlocks:\n  schema  claude-code (agent)  keen-otter-a0f7  11m left  \"regenerating sqlc\"  #42\n"
	if h.out.String() != want {
		t.Errorf("stdout = %q, want %q", h.out.String(), want)
	}
}

func lockRunRoutes(rec *recorder, renew http.HandlerFunc) map[string]http.HandlerFunc {
	return rec.wrapAll(map[string]http.HandlerFunc{
		"PUT /api/v1/locks/schema":        reply(201, fixtureLock()),
		"POST /api/v1/locks/schema/renew": renew,
		"DELETE /api/v1/locks/schema":     reply(204, nil),
	})
}

func TestLockRunAcquiresRunsAndReleases(t *testing.T) {
	t.Parallel()
	var rec recorder
	h := newHarness(t, serve(t, lockRunRoutes(&rec, reply(200, fixtureLock()))))
	if err := h.run(t, "lock", "run", "schema", "-m", "x", "--", "true"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"PUT /api/v1/locks/schema", "DELETE /api/v1/locks/schema"}; !slices.Equal(rec.calls(), want) {
		t.Errorf("calls = %v, want %v", rec.calls(), want)
	}
	if h.out.String() != "" || h.errOut.String() != "" {
		t.Errorf("output = %q / %q, want none", h.out.String(), h.errOut.String())
	}
}

func TestLockRunExitsWithTheCommandsCodeAndReleases(t *testing.T) {
	t.Parallel()
	var rec recorder
	h := newHarness(t, serve(t, lockRunRoutes(&rec, reply(200, fixtureLock()))))
	err := h.run(t, "lock", "run", "schema", "-m", "x", "--", "sh", "-c", "exit 3")
	if cmdutil.ExitCode(err) != 3 {
		t.Fatalf("exit = %d (%v), want 3", cmdutil.ExitCode(err), err)
	}
	if got := rec.calls(); got[len(got)-1] != "DELETE /api/v1/locks/schema" {
		t.Errorf("calls = %v, want a final DELETE", got)
	}
}

func TestLockRunKillsTheCommandWhenARenewalFails(t *testing.T) {
	t.Parallel()
	var rec recorder
	h := newHarness(t, serve(t, lockRunRoutes(&rec, lockProblem(404, "lock not found"))))
	h.f.LockRenew = 20 * time.Millisecond
	start := time.Now()
	err := h.run(t, "lock", "run", "schema", "-m", "x", "--", "sleep", "30")
	if cmdutil.ExitCode(err) != cmdutil.ExitConflict {
		t.Fatalf("exit = %d (%v), want 4", cmdutil.ExitCode(err), err)
	}
	if !strings.HasPrefix(err.Error(), "lock schema lost: ") || !strings.HasSuffix(err.Error(), "; command killed") {
		t.Errorf("error = %q", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %s: the command was not killed", time.Since(start))
	}
	for _, c := range rec.calls() {
		if strings.HasPrefix(c, "DELETE") {
			t.Errorf("released a lock it had lost: %v", rec.calls())
		}
	}
}

func TestLockRunNeedsDashDash(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{"no dash", []string{"lock", "run", "schema", "-m", "x", "true"}},
		{"nothing after dash", []string{"lock", "run", "schema", "-m", "x", "--"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var rec recorder
			h := newHarness(t, serve(t, lockRunRoutes(&rec, reply(200, fixtureLock()))))
			err := h.run(t, tt.args...)
			if cmdutil.ExitCode(err) != cmdutil.ExitUsage {
				t.Errorf("exit = %d (%v), want 2", cmdutil.ExitCode(err), err)
			}
			if len(rec.calls()) != 0 {
				t.Errorf("calls = %v, want none", rec.calls())
			}
		})
	}
}
