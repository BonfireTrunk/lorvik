package domain

import (
	"errors"
	"testing"
)

func TestCanFoldNamesTheReason(t *testing.T) {
	t.Parallel()
	parent := ID(9)
	tests := []struct {
		name string
		card Card
		want error
	}{
		{"working", Card{Number: 1, Status: StatusWorking}, ErrFoldNotDone},
		{"dropped", Card{Number: 1, Status: StatusDropped}, ErrFoldNotDone},
		{"child of a story", Card{Number: 1, Status: StatusDone, ParentID: &parent}, ErrFoldInStory},
		{"folded", Card{Number: 1, Status: StatusDone, Fold: &Fold{}}, ErrAlreadyFolded},
		{"done, own, unfolded", Card{Number: 1, Status: StatusDone}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.card.CanFold()
			if tt.want == nil {
				if err != nil {
					t.Fatalf("CanFold = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.want) || !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("CanFold = %v, want %v and ErrInvalidTransition", err, tt.want)
			}
			c := tt.card
			if ferr := c.FoldInto("docs/x.md", ActorRef{}, c.UpdatedAt); !errors.Is(ferr, tt.want) || c.Fold != tt.card.Fold {
				t.Errorf("FoldInto = %v and changed the fold; CanFold refused it", ferr)
			}
		})
	}
}

// Neither a decision nor a story is ever claimed, whoever asks.
func TestWorkableRefusesDecisionsAndStories(t *testing.T) {
	t.Parallel()
	for kind, ok := range map[CardKind]bool{KindTask: true, KindQuestion: true, KindDecision: false, KindStory: false} {
		c := Card{Kind: kind, Status: StatusReady}
		if err := c.Workable(); (err == nil) != ok || (err != nil && !errors.Is(err, ErrInvalidTransition)) {
			t.Errorf("%s: Workable = %v, want workable %v", kind, err, ok)
		}
		if _, err := NewClaim(c, nil, ActorRef{}, "", c.UpdatedAt, 1, 0); (err == nil) != ok {
			t.Errorf("%s: NewClaim = %v, want allowed %v", kind, err, ok)
		}
	}
}
