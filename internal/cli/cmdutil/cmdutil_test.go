package cmdutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

func TestFactoryTimeFallsBackToWallClock(t *testing.T) {
	t.Parallel()
	fixed := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	if got := (&Factory{Now: func() time.Time { return fixed }}).Time(); !got.Equal(fixed) {
		t.Errorf("Time() with Now set = %v, want %v", got, fixed)
	}
	if got := (&Factory{}).Time(); time.Since(got) > time.Minute {
		t.Errorf("Time() with nil Now = %v, want about now", got)
	}
}

func TestExitCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"generic", errors.New("boom"), 1},
		{"silent", ErrSilent, 1},
		{"usage", Usagef("bad flag"), 2},
		{"wrapped usage", fmt.Errorf("add: %w", Usagef("x")), 2},
		{"not found", &client.APIError{Status: 404}, 3},
		{"wrapped not found", fmt.Errorf("show: %w", &client.APIError{Status: 404}), 3},
		{"version conflict", &client.APIError{Status: 409, CurrentVersion: 3}, 4},
		{"unauthorized", &client.APIError{Status: 401}, 5},
		{"forbidden", &client.APIError{Status: 403}, 5},
		{"validation", &client.APIError{Status: 422}, 2},
		{"server error", &client.APIError{Status: 500}, 1},
		{"connection", &client.ConnError{URL: "http://x", Err: errors.New("refused")}, 1},
		{"forced code wins", &ExitError{Code: 3, Err: ErrSilent}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestFormatErrorNamesTheFix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want []string
	}{
		{"conflict prints hint and version", &client.APIError{Status: 409, Title: "Conflict", Detail: "stale", Hint: "run `lorvik show 4`", CurrentVersion: 8},
			[]string{"lorvik: Conflict: stale", "current_version: 8", "hint: run `lorvik show 4`"}},
		{"401 names login and the env var", &client.APIError{Status: 401, Title: "Unauthorized"}, []string{"hint: sign in with `lorvik login`", "LORVIK_TOKEN or LORVIK_CONFIG"}},
		{"connection names the url", &client.ConnError{URL: "http://h:9", Err: errors.New("refused")}, []string{"cannot reach http://h:9", "check LORVIK_API_URL"}},
		{"field errors listed", &client.APIError{Status: 422, Title: "Bad", Errors: []apitypes.ProblemField{{Field: "title", Message: "required"}}}, []string{"  title: required"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := FormatError(tt.err, nil)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("FormatError = %q, missing %q", got, w)
				}
			}
		})
	}
}

func TestWriteJSONIsIndentedAndUnescaped(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := WriteJSON(&b, map[string]any{"b": []int{1}, "a": "<x>"}); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": \"<x>\",\n  \"b\": [\n    1\n  ]\n}\n"
	if b.String() != want {
		t.Errorf("WriteJSON = %q, want %q", b.String(), want)
	}
}

func TestWriteJSONFieldAddsAFieldToAnObjectOnly(t *testing.T) {
	t.Parallel()
	field := map[string]any{"cards": []string{"<a>"}}
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"object", map[string]any{"a": 1}, "{\n  \"a\": 1,\n  \"holding\": {\n    \"cards\": [\n      \"<a>\"\n    ]\n  }\n}\n"},
		{"empty object", struct{}{}, "{\n  \"holding\": {\n    \"cards\": [\n      \"<a>\"\n    ]\n  }\n}\n"},
		{"object ending in a nested object", map[string]any{"a": map[string]int{"b": 1}}, "{\n  \"a\": {\n    \"b\": 1\n  },\n  \"holding\": {\n    \"cards\": [\n      \"<a>\"\n    ]\n  }\n}\n"},
		{"array", []int{1}, "[\n  1\n]\n"},
		{"array of objects", []map[string]int{{"a": 1}}, "[\n  {\n    \"a\": 1\n  }\n]\n"},
		{"string", "x}", "\"x}\"\n"},
		{"null", nil, "null\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var b bytes.Buffer
			if err := WriteJSONField(&b, tt.v, "holding", field); err != nil {
				t.Fatal(err)
			}
			if b.String() != tt.want {
				t.Errorf("WriteJSONField = %q, want %q", b.String(), tt.want)
			}
			if !json.Valid(b.Bytes()) {
				t.Errorf("output is not valid JSON: %q", b.String())
			}
		})
	}
}

func TestRelTime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		ago  time.Duration
		want string
	}{
		{10 * time.Second, "now"},
		{3 * time.Minute, "3m ago"},
		{59 * time.Minute, "59m ago"},
		{5 * time.Hour, "5h ago"},
		{49 * time.Hour, "2d ago"},
		{40 * 24 * time.Hour, "2026-09-08"},
		{-28 * time.Minute, "in 28m"},
	}
	for _, tt := range tests {
		if got := RelTime(now, now.Add(-tt.ago)); got != tt.want {
			t.Errorf("RelTime(%v ago) = %q, want %q", tt.ago, got, tt.want)
		}
	}
}

func TestRelTimeDatesUseTheMachineZone(t *testing.T) {
	// Not parallel: time.Local is process-wide.
	old := time.Local
	time.Local = time.FixedZone("east", 10*3600)
	defer func() { time.Local = old }()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	// 23:00 UTC on the 8th is already the 9th ten hours east.
	if got := RelTime(now, time.Date(2026, 9, 8, 23, 0, 0, 0, time.UTC)); got != "2026-09-09" {
		t.Errorf("RelTime = %q, want the date in the machine zone", got)
	}
}

func nameMap(m map[string]string) NameFunc {
	return func(a apitypes.Actor) string { return m[a.ID] }
}

func TestCardLine(t *testing.T) {
	t.Parallel()
	nm := nameMap(map[string]string{"7": "claude-code", "8": "ana"})
	tests := []struct {
		name  string
		in    apitypes.CardBrief
		color bool
		names NameFunc
		want  string
	}{
		{"claimed", apitypes.CardBrief{Number: 42, Status: "working", Priority: "high", Title: "Fix login",
			Claim: &apitypes.Claim{Actor: apitypes.Actor{Kind: "agent", ID: "7"}}}, false, nm,
			"#42  working  high  Fix login  @claude-code (agent)"},
		{"claimed by a chat", apitypes.CardBrief{Number: 42, Status: "working", Priority: "high", Title: "Fix login",
			Claim: &apitypes.Claim{Actor: apitypes.Actor{Kind: "agent", ID: "7"}, Session: "calm-lynx-4aa4"}}, false, nm,
			"#42  working  high  Fix login  @claude-code (agent) (calm-lynx-4aa4)"},
		{"claimed by a named chat", apitypes.CardBrief{Number: 42, Status: "working", Priority: "high", Title: "Fix login",
			Claim: &apitypes.Claim{Actor: apitypes.Actor{Kind: "agent", ID: "7"}, Session: "calm-lynx-4aa4", SessionName: "Sign-in fixes"}}, false, nm,
			"#42  working  high  Fix login  @claude-code (agent) (Sign-in fixes, calm-lynx-4aa4)"},
		{"assignee when unclaimed", apitypes.CardBrief{Number: 5, Status: "ready", Priority: "normal", Title: "T",
			Assignee: &apitypes.Actor{Kind: "user", ID: "8"}}, false, nm, "#5  ready  normal  T  @ana"},
		{"claim wins over assignee", apitypes.CardBrief{Number: 5, Status: "working", Priority: "normal", Title: "T",
			Claim: &apitypes.Claim{Actor: apitypes.Actor{ID: "7"}}, Assignee: &apitypes.Actor{ID: "8"}}, false, nm, "#5  working  normal  T  @claude-code"},
		{"unknown actor falls back to the id", apitypes.CardBrief{Number: 5, Status: "working", Priority: "normal", Title: "T",
			Claim: &apitypes.Claim{Actor: apitypes.Actor{ID: "99"}}}, false, nm, "#5  working  normal  T  @99"},
		{"no name function falls back to the id", apitypes.CardBrief{Number: 5, Status: "working", Priority: "normal", Title: "T",
			Claim: &apitypes.Claim{Actor: apitypes.Actor{ID: "99"}}}, false, nil, "#5  working  normal  T  @99"},
		{"bare", apitypes.CardBrief{Number: 7, Status: "ready", Priority: "normal", Title: "T"}, false, nm, "#7  ready  normal  T"},
		{"question", apitypes.CardBrief{Number: 9, Kind: "question", Status: "ready", Priority: "normal", Title: "Which db"}, false, nm, "#9  ready  normal  ? Which db"},
		{"story", apitypes.CardBrief{Number: 20, Kind: "story", Status: "working", Priority: "normal", Title: "Webhook delivery"}, false, nm, "#20  working  normal  S Webhook delivery"},
		{"decision", apitypes.CardBrief{Number: 2, Kind: "decision", Status: "done", Priority: "normal", Title: "Use pgx"}, false, nm, "#2  done  normal  D Use pgx"},
		{"cross-project row names its project", apitypes.CardBrief{Number: 3, Kind: "decision", Project: "global", Status: "done", Priority: "normal", Title: "T"}, false, nm, "#3  done  normal  D T  (global)"},
		{"color only on status", apitypes.CardBrief{Number: 1, Status: "done", Priority: "low", Title: "T"}, true, nm, "#1  \x1b[32mdone\x1b[0m  low  T"},
		{"waiting is colored", apitypes.CardBrief{Number: 1, Status: "waiting", Priority: "low", Title: "T"}, true, nm, "#1  \x1b[31mwaiting\x1b[0m  low  T"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := CardLine(tt.in, tt.color, tt.names); got != tt.want {
				t.Errorf("CardLine = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBriefOfKeepsHolderAndIdentity(t *testing.T) {
	t.Parallel()
	claim := &apitypes.Claim{Actor: apitypes.Actor{Kind: "agent", ID: "7"}}
	assignee := &apitypes.Actor{Kind: "user", ID: "8"}
	c := apitypes.Card{ID: "100", Number: 3, Kind: "task", Status: "working", Title: "T", Priority: "high", Claim: claim, Assignee: assignee, Body: "dropped"}
	b := BriefOf(c)
	if b.ID != "100" || b.Number != 3 || b.Claim != claim || b.Assignee != assignee || b.Priority != "high" {
		t.Errorf("BriefOf = %+v", b)
	}
}

func TestNoteLines(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	proj := "demo"
	nm := nameMap(map[string]string{"1": "ana", "2": "bo"})
	n := apitypes.NoteBrief{Slug: "arch", Title: "Architecture", UpdatedBy: apitypes.Actor{Kind: "user", ID: "2"}, UpdatedAt: now.Add(-3 * time.Minute)}
	if got, want := NoteLine(n, now, nm), "arch  Architecture  @bo  3m ago  (org)"; got != want {
		t.Errorf("NoteLine = %q, want %q", got, want)
	}
	n.Status, n.Project = "archived", &proj
	if got, want := NoteLine(n, now, nm), "arch  Architecture  @bo  3m ago  (demo)  archived"; got != want {
		t.Errorf("archived NoteLine = %q, want %q", got, want)
	}
}

func TestPagingLine(t *testing.T) {
	t.Parallel()
	next := func(c string) string { return "lorvik ls --cursor " + c }
	tests := []struct {
		name string
		page apitypes.Page
		next func(string) string
		want string
	}{
		{"nothing remaining prints nothing", apitypes.Page{Next: "abc"}, next, ""},
		{"remaining with cursor", apitypes.Page{Next: "abc", Remaining: 37}, next, "remaining 37, next: lorvik ls --cursor abc\n"},
		{"cap prints a plus", apitypes.Page{Next: "abc", Remaining: 100}, next, "remaining 100+, next: lorvik ls --cursor abc\n"},
		{"a page with no cursor", apitypes.Page{Remaining: 4}, next, "remaining 4 (narrow the query or raise --limit)\n"},
		{"no continuation command", apitypes.Page{Next: "abc", Remaining: 4}, nil, "remaining 4 (narrow the query or raise --limit)\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var b bytes.Buffer
			p := &Printer{W: &b}
			p.Paging(tt.page, tt.next)
			if b.String() != tt.want {
				t.Errorf("Paging = %q, want %q", b.String(), tt.want)
			}
		})
	}
}

func TestFormatErrorNamesSandbox(t *testing.T) {
	t.Parallel()
	err := &client.ConnError{URL: "http://h:9", Err: errors.New("operation not permitted")}
	env := map[string]string{"CODEX_SANDBOX_NETWORK_DISABLED": "1"}
	got := FormatError(err, func(k string) string { return env[k] })
	if !strings.Contains(got, "Codex sandbox") || !strings.Contains(got, "sandbox_workspace_write") {
		t.Errorf("FormatError = %q, want the Codex sandbox hint", got)
	}
	if plain := FormatError(err, func(string) string { return "" }); strings.Contains(plain, "sandbox") {
		t.Errorf("FormatError without the variable = %q, want no sandbox hint", plain)
	}
}
