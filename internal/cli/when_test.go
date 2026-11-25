package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func TestParseWhen(t *testing.T) {
	t.Parallel()
	zone := time.FixedZone("test", 5*3600+1800)
	now := time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC)
	tests := []struct {
		name    string
		in      string
		want    time.Time
		wantErr bool
	}{
		{"days", "7d", now.Add(-7 * 24 * time.Hour), false},
		{"hours", "24h", now.Add(-24 * time.Hour), false},
		{"minutes", "30m", now.Add(-30 * time.Minute), false},
		{"zero span is now", "0d", now, false},
		{"day is the start of that day in the zone", "2026-10-20", time.Date(2026, 10, 20, 0, 0, 0, 0, zone), false},
		{"day across a month end", "2026-02-28", time.Date(2026, 2, 28, 0, 0, 0, 0, zone), false},
		{"rfc3339 keeps its offset", "2026-10-20T08:15:00+02:00", time.Date(2026, 10, 20, 6, 15, 0, 0, time.UTC), false},
		{"surrounding space", " 7d ", now.Add(-7 * 24 * time.Hour), false},
		{"empty", "", time.Time{}, true},
		{"unit alone", "d", time.Time{}, true},
		{"negative span", "-7d", time.Time{}, true},
		{"fractional span", "1.5h", time.Time{}, true},
		{"unknown unit", "2w", time.Time{}, true},
		{"no unit", "7", time.Time{}, true},
		{"impossible date", "2026-02-30", time.Time{}, true},
		{"slash date", "2026/10/20", time.Time{}, true},
		{"words", "yesterday", time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseWhen(tt.in, now, zone)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseWhen(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if !tt.wantErr && !got.Equal(tt.want) {
				t.Errorf("parseWhen(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestLsSendsFiltersAndFixesRelativeTimes(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	h := newHarness(t, serve(t, rec.wrapAll(fixtureRoutes())))
	err := h.run(t, "-p", "demo", "ls", "--created-after", "7d", "--updated-before", "2026-10-17T12:00:00Z",
		"--closed-after", "24h", "--assignee", "none", "--priority", "high,urgent", "--sort", "updated", "--desc")
	if err != nil {
		t.Fatal(err)
	}
	// fixedNow is 2026-10-18T12:00:00Z.
	want := "/api/v1/projects/demo/cards?assignee=none&closed_after=2026-10-17T12%3A00%3A00Z&created_after=2026-10-11T12%3A00%3A00Z" +
		"&desc=true&limit=10&priority=high%2Curgent&sort=updated&updated_before=2026-10-17T12%3A00%3A00Z"
	got := rec.last(t, "GET /api/v1/projects/demo/cards").URI
	if got != want {
		t.Errorf("request = %q, want %q", got, want)
	}
	lines := strings.Split(strings.TrimRight(h.out.String(), "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "--created-after 2026-10-11T12:00:00") || strings.Contains(last, "7d") {
		t.Errorf("paging line = %q, want the relative span fixed to an absolute time", last)
	}
}

func TestNotesLsSendFilters(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	routes := fixtureRoutes()
	routes["GET /api/v1/notes"] = reply(200, map[string]any{"items": []any{}, "remaining": 0})
	h := newHarness(t, serve(t, rec.wrapAll(routes)))
	if err := h.run(t, "note", "ls", "--author", "42", "--updated-after", "2026-10-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	want := "/api/v1/notes?author=42&limit=10&updated_after=2026-10-01T00%3A00%3A00Z"
	if got := rec.last(t, "GET /api/v1/notes").URI; got != want {
		t.Errorf("notes request = %q, want %q", got, want)
	}
}

func TestLsFilterUsageErrorsExitTwo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{"bad date", []string{"-p", "x", "ls", "--created-after", "yesterday"}},
		{"bad closed date", []string{"-p", "x", "ls", "--closed-before", "2026-13-01"}},
		{"bad notes date", []string{"note", "ls", "--created-before", "1w"}},
		{"bad sort", []string{"-p", "x", "ls", "--sort", "title"}},
		{"desc without sort", []string{"-p", "x", "ls", "--desc"}},
		{"sort with query", []string{"-p", "x", "ls", "--sort", "created", "-q", "login"}},
		{"bad priority", []string{"-p", "x", "ls", "--priority", "high,huge"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := newHarness(t, serve(t, fixtureRoutes())).run(t, tt.args...)
			if got := cmdutil.ExitCode(err); got != 2 {
				t.Errorf("exit = %d (%v), want 2", got, err)
			}
		})
	}
}
