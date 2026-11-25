package domain

import (
	"slices"
	"testing"
	"time"
)

func TestEnumRoundTrips(t *testing.T) {
	t.Parallel()
	for _, s := range AllStatuses {
		got, err := ParseStatus(s.String())
		if err != nil || got != s {
			t.Errorf("status %d: round trip gave %d, %v", s, got, err)
		}
	}
	for _, e := range AllEventTypes {
		got, err := ParseEventType(e.String())
		if err != nil || got != e {
			t.Errorf("event type %d: round trip gave %d, %v", e, got, err)
		}
	}
	cases := []struct {
		name string
		ok   func() bool
	}{
		{"actor kind", func() bool {
			k, err := ParseActorKind("agent")
			return err == nil && k == ActorAgent && k.String() == "agent"
		}},
		{"card kind", func() bool { k, err := ParseCardKind("question"); return err == nil && k == KindQuestion }},
		{"priority", func() bool {
			p, err := ParsePriority("urgent")
			return err == nil && p == PriorityUrgent && PriorityUrgent > PriorityLow
		}},
		{"drop reason empty", func() bool { r, err := ParseDropReason(""); return err == nil && r == DropNone && r.String() == "" }},
		{"drop reason", func() bool { r, err := ParseDropReason("wontfix"); return err == nil && r == DropWontFix }},
		{"link kind", func() bool { k, err := ParseCardLinkKind("blocks"); return err == nil && k == LinkBlocks }},
		{"story kind", func() bool {
			k, err := ParseCardKind("story")
			return err == nil && k == KindStory && k.String() == "story"
		}},
		{"decision kind", func() bool {
			k, err := ParseCardKind("decision")
			return err == nil && k == KindDecision && k.String() == "decision"
		}},
		{"drop reasons of decisions", func() bool {
			r, e1 := ParseDropReason("rejected")
			v, e2 := ParseDropReason("revoked")
			u, e3 := ParseDropReason("superseded")
			return e1 == nil && e2 == nil && e3 == nil && r == DropRejected && v == DropRevoked && u == DropSuperseded && r.DecisionOnly() && !DropWontFix.DecisionOnly()
		}},
		{"supersedes and applies links", func() bool {
			a, e1 := ParseCardLinkKind("supersedes")
			b, e2 := ParseCardLinkKind("applies")
			return e1 == nil && e2 == nil && a == LinkSupersedes && b == LinkApplies && int16(a) == 4 && int16(b) == 5
		}},
		{"provider", func() bool { p, err := ParseProvider("password"); return err == nil && p == ProviderPassword }},
		{"harness", func() bool { h, err := ParseHarness("claude-code"); return err == nil && h == HarnessClaudeCode }},
		{"harnesses the CLI detects", func() bool {
			g, err := ParseHarness("goose")
			return err == nil && g == HarnessGoose && int16(g) == 10 && HarnessPi.String() == "pi" && HarnessDsh.String() == "dsh" && HarnessQwen.String() == "qwen"
		}},
		{"agent join", func() bool {
			j, err := ParseAgentJoin("ask")
			return err == nil && j == AgentJoinAsk && int16(j) == 20 && AgentJoin(0) == AgentJoinFollow && int16(AgentJoinAllow) == 10
		}},
		{"visibility", func() bool { v, err := ParseVisibility("private"); return err == nil && v == VisibilityPrivate }},
		{"concurrency", func() bool {
			c, err := ParseConcurrency("serial")
			return err == nil && c == ConcurrencySerial && c.String() == "serial" && ConcurrencySerial > ConcurrencyParallel
		}},
		{"scope", func() bool { s, err := ParseTokenScope("read"); return err == nil && s == ScopeRead }},
		{"subject", func() bool { s, err := ParseSubjectType("membership"); return err == nil && s == SubjectMembership }},
		{"role", func() bool { r, err := ParseRole("reviewer"); return err == nil && r == RoleReviewer }},
	}
	for _, c := range cases {
		if !c.ok() {
			t.Errorf("%s: round trip failed", c.name)
		}
	}
}

func TestEnumParseRejectsUnknown(t *testing.T) {
	t.Parallel()
	if _, err := ParseStatus("blocked"); err == nil {
		t.Error("blocked is no longer a status; ParseStatus must refuse it")
	}
	if _, err := ParseRole("superuser"); err == nil {
		t.Error("unknown role accepted")
	}
	if s := Status(3).String(); s != "status(3)" {
		t.Errorf("unknown value prints %q, want status(3)", s)
	}
}

// Always ask from any say wins, then allow from any; with neither, or with
// no say at all, the owner is asked.
func TestDecideAgentJoin(t *testing.T) {
	t.Parallel()
	f, a, q := AgentJoinFollow, AgentJoinAllow, AgentJoinAsk
	tests := []struct {
		says []AgentJoin
		want AgentJoin
	}{
		{nil, q},
		{[]AgentJoin{f, f, f, f}, q},
		{[]AgentJoin{a, f, f, f}, a},
		{[]AgentJoin{f, f, f, a}, a},
		{[]AgentJoin{a, a, f, q}, q},
		{[]AgentJoin{q, f, a, a}, q},
	}
	for _, tc := range tests {
		if got := DecideAgentJoin(tc.says...); got != tc.want {
			t.Errorf("DecideAgentJoin(%v) = %v, want %v", tc.says, got, tc.want)
		}
	}
}

// Each level may give only its own says: the account switch allows, a
// project admin asks, an agent and a stored rule for a project do either.
func TestAgentJoinIsValidatedPerLevel(t *testing.T) {
	t.Parallel()
	u := User{Login: "ana"}
	a := Agent{Name: "bot"}
	p := Project{Slug: "web", Name: "Web", Lease: 30 * time.Minute, Visibility: VisibilityOrg, Concurrency: ConcurrencyParallel}
	r := AgentJoinRule{}
	for _, tc := range []struct {
		level string
		valid func(AgentJoin) error
		ok    []AgentJoin
	}{
		{"account", func(j AgentJoin) error { u.AgentJoin = j; return u.Validate() }, []AgentJoin{AgentJoinFollow, AgentJoinAllow}},
		{"agent", func(j AgentJoin) error { a.AgentJoin = j; return a.Validate() }, []AgentJoin{AgentJoinFollow, AgentJoinAllow, AgentJoinAsk}},
		{"project", func(j AgentJoin) error { p.AgentJoin = j; return p.Validate() }, []AgentJoin{AgentJoinFollow, AgentJoinAsk}},
		{"rule", func(j AgentJoin) error { r.AgentJoin = j; return r.Validate() }, []AgentJoin{AgentJoinAllow, AgentJoinAsk}},
	} {
		for _, j := range []AgentJoin{AgentJoinFollow, AgentJoinAllow, AgentJoinAsk, 7} {
			err := tc.valid(j)
			if want := slices.Contains(tc.ok, j); (err == nil) != want {
				t.Errorf("%s %v: err = %v, want valid %v", tc.level, j, err, want)
			}
		}
	}
}
