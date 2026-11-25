package server

import (
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func TestCascadeDropReasonOfChildren(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		reason     string
		duplicate  bool
		wantReason string
	}{
		{name: "duplicate story makes obsolete children", reason: "duplicate", duplicate: true, wantReason: "obsolete"},
		{name: "wontfix story makes wontfix children", reason: "wontfix", wantReason: "wontfix"},
		{name: "obsolete story makes obsolete children", reason: "obsolete", wantReason: "obsolete"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			s := e.story("Abandoned")
			in := apitypes.TransitionCard{Status: "dropped", DropReason: tt.reason, Cascade: true}
			if tt.duplicate {
				survivor := e.story("Survivor")
				in.Duplicate = &survivor.Number
			}
			task := e.child(s.Number, "task")
			question := e.newCard(agentSecret, apitypes.CreateCard{Kind: "question", Title: "q", Status: "ready", Parent: &s.Number})
			decision := e.newCard(agentSecret, apitypes.CreateCard{Kind: "decision", Title: "d", Parent: &s.Number})

			e.move(userSecret, s.Number, in)

			want := map[int64]string{task.Number: tt.wantReason, question.Number: tt.wantReason, decision.Number: "rejected"}
			for n, reason := range want {
				if got := e.show(userSecret, n); got.Status != "dropped" || got.DropReason != reason {
					t.Errorf("child #%d = %s/%s, want dropped/%s", n, got.Status, got.DropReason, reason)
				}
				var last map[string]any
				for _, ev := range e.eventsOf("card.status_changed") {
					if ev["_subject"] == float64(n) {
						last = ev
					}
				}
				if last == nil {
					t.Errorf("child #%d has no card.status_changed event", n)
					continue
				}
				if last["cascade_of"] != float64(s.Number) || last["drop_reason"] != reason {
					t.Errorf("child #%d event = cascade_of %v, drop_reason %v, want %d, %s", n, last["cascade_of"], last["drop_reason"], s.Number, reason)
				}
			}
		})
	}
}
