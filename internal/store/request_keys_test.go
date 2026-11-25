//go:build integration

package store

import (
	"errors"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/domain"
)

func (e *env) requestKey(actor domain.ActorRef, key string) domain.RequestKey {
	return domain.RequestKey{Actor: actor, Key: key, OrgID: e.org.ID, ProjectID: e.project.ID, Operation: domain.RequestComment,
		CardID: gen.New(), SubjectID: gen.New(), CreatedAt: t0}
}

func TestClaimRequestKeyReturnsTheFirstRowToALaterClaim(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	first := e.requestKey(e.user, "key-1")
	got, claimed, err := e.st.ClaimRequestKey(ctx, first)
	if err != nil || !claimed || got != first {
		t.Fatalf("first claim = %+v, %v, %v; want the row, claimed", got, claimed, err)
	}
	again := e.requestKey(e.user, "key-1")
	again.Operation = domain.RequestTransition
	got, claimed, err = e.st.ClaimRequestKey(ctx, again)
	if err != nil || claimed {
		t.Fatalf("second claim = %v, %v; want not claimed", claimed, err)
	}
	if !got.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("created_at = %v, want %v", got.CreatedAt, first.CreatedAt)
	}
	got.CreatedAt = first.CreatedAt
	if got != first {
		t.Errorf("second claim returned %+v, want the first row %+v", got, first)
	}
}

func TestClaimRequestKeyIsPerActor(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	for _, actor := range []domain.ActorRef{e.user, e.agent} {
		if _, claimed, err := e.st.ClaimRequestKey(ctx, e.requestKey(actor, "shared")); err != nil || !claimed {
			t.Errorf("claim by %+v = %v, %v; want claimed", actor, claimed, err)
		}
	}
}

func TestClaimRequestKeyInARolledBackTransactionLeavesTheKeyFree(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	errUndo := errors.New("undo")
	err := e.st.WithTx(ctx, func(tx *Store) error {
		if _, claimed, err := tx.ClaimRequestKey(ctx, e.requestKey(e.user, "key-rb")); err != nil || !claimed {
			t.Errorf("claim in the transaction = %v, %v; want claimed", claimed, err)
		}
		return errUndo
	})
	if !errors.Is(err, errUndo) {
		t.Fatalf("WithTx = %v, want the callback's error", err)
	}
	if _, claimed, err := e.st.ClaimRequestKey(ctx, e.requestKey(e.user, "key-rb")); err != nil || !claimed {
		t.Errorf("claim after the rollback = %v, %v; want claimed", claimed, err)
	}
}
