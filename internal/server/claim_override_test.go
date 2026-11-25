package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

// One rule decides who may act on a card another actor holds, on every
// door: claiming over it, releasing it and moving the card. A live claim
// binds everyone but humans with triage; an expired claim binds nobody.
// Agents never override, even as reviewers. The caller creates the card, so
// the card permission never decides the answer, only the claim.
func TestClaimOverrideIsOneRule(t *testing.T) {
	t.Parallel()
	type door func(e *testEnv, secret string, n int64) int
	doors := map[string]door{
		"claim": func(e *testEnv, secret string, n int64) int {
			return e.do(secret, http.MethodPost, cardPathOf(n, "/claim"), apitypes.ClaimCard{}).Code
		},
		"release": func(e *testEnv, secret string, n int64) int {
			return e.do(secret, http.MethodPost, cardPathOf(n, "/release"), apitypes.ClaimCard{}).Code
		},
		"move to done": func(e *testEnv, secret string, n int64) int {
			c := e.show(secret, n)
			return e.do(secret, http.MethodPost, cardPathOf(n, "/transition"), apitypes.TransitionCard{Version: c.Version, Status: "done"}).Code
		},
	}
	tests := []struct {
		name    string
		holder  string
		caller  string
		expired bool
		allowed bool
	}{
		{"member over a live claim", agent2Secret, memberSecret, false, false},
		{"owner over a live claim", memberSecret, userSecret, false, true},
		{"reviewer agent over a live claim", memberSecret, agentSecret, false, false},
		{"member over an expired claim", agent2Secret, memberSecret, true, true},
		{"reviewer agent over an expired claim", memberSecret, agentSecret, true, true},
	}
	for _, tt := range tests {
		for name, act := range doors {
			t.Run(tt.name+"/"+name, func(t *testing.T) {
				t.Parallel()
				e := newEnv(t)
				e.asReviewer()
				c := e.readyCard(tt.caller, "held card")
				e.claim(tt.holder, c.Number)
				if tt.expired {
					e.advance(31 * time.Minute)
				}
				code := act(e, tt.caller, c.Number)
				if ok := code == http.StatusOK; ok != tt.allowed {
					t.Errorf("%s by %s = %d, want allowed %v", name, tt.name, code, tt.allowed)
				}
			})
		}
	}
}
