//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// waitBlocked polls until a backend of this database waits on a lock while
// running the named sqlc query, so a test knows the second transaction is
// queued behind the first before the first commits.
func waitBlocked(ctx context.Context, t *testing.T, e *env, queryName string) {
	t.Helper()
	for {
		var n int
		err := e.st.db.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '-- name: ' || $1 || ' %'`, queryName).Scan(&n)
		if err != nil {
			t.Fatalf("poll pg_stat_activity: %v", err)
		}
		if n > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("no backend blocked in %s before the deadline", queryName)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestConcurrentNoteEditsLoseWithAVersionConflict(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	n := e.note(t, e.project.ID, "race", "v1")
	edit := func(st *Store, body string) error {
		m := n
		m.Body, m.UpdatedAt = body, t0.Add(time.Minute)
		_, err := st.UpdateNote(ctx, m, n.Version, gen.New(), false)
		return err
	}
	loser := make(chan error, 1)
	err := e.st.WithTx(ctx, func(tx *Store) error {
		if err := edit(tx, "first"); err != nil {
			return err
		}
		// Started now, the second edit reads version 1 and queues on the
		// revision row the first edit has not committed yet.
		go func() { loser <- edit(e.st, "second") }()
		waitBlocked(ctx, t, e, "SnapshotNoteRevision")
		return nil
	})
	if err != nil {
		t.Fatalf("first edit: %v", err)
	}
	if err := <-loser; !errors.Is(err, domain.ErrVersionConflict) {
		t.Errorf("second edit = %v, want ErrVersionConflict", err)
	}
	got, err := e.st.GetNote(ctx, n.ID)
	if err != nil || got.Body != "first" || got.Version != n.Version+1 {
		t.Errorf("note = %q v%d, %v; want the first edit at v%d", got.Body, got.Version, err, n.Version+1)
	}
}

func TestLockTokenMakesASecondWriterReadTheFirstDecision(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	tok := e.token(t, e.agent, "lvk_a_"+gen.New().String(), t0.Add(time.Hour))
	second := make(chan domain.Token, 1)
	err := e.st.WithTx(ctx, func(tx *Store) error {
		cur, err := tx.LockToken(ctx, tok.ID)
		if err != nil {
			return err
		}
		go func() {
			_ = e.st.WithTx(ctx, func(tx2 *Store) error {
				got, err := tx2.LockToken(ctx, tok.ID)
				if err != nil {
					t.Errorf("second lock: %v", err)
				}
				second <- got
				return err
			})
		}()
		waitBlocked(ctx, t, e, "LockToken")
		cur.Status = domain.RecordRevoked
		return tx.UpdateToken(ctx, cur)
	})
	if err != nil {
		t.Fatalf("revoke under lock: %v", err)
	}
	if got := <-second; got.Status != domain.RecordRevoked {
		t.Errorf("second writer read status %v, want revoked: it must wait for the first and see its write", got.Status)
	}
}

func TestRevokeTokensForActorCatchesASuccessorMintedConcurrently(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	old := e.token(t, e.agent, "lvk_a_"+gen.New().String(), t0.Add(time.Hour))
	var successor domain.Token
	revoked := make(chan error, 1)
	err := e.st.WithTx(ctx, func(tx *Store) error {
		cur, err := tx.LockToken(ctx, old.ID)
		if err != nil {
			return err
		}
		secret := "lvk_a_" + gen.New().String()
		successor, err = tx.CreateToken(ctx, domain.Token{ID: gen.New(), OrgID: e.org.ID, Actor: e.agent, Name: "t", Prefix: secret[:domain.TokenPrefixLen],
			Hash: domain.HashToken(secret), Scope: domain.ScopeWrite, Status: domain.RecordActive, ExpiresAt: t0.Add(2 * time.Hour), CreatedAt: t0})
		if err != nil {
			return err
		}
		go func() { revoked <- e.st.RevokeTokensForActor(ctx, e.org.ID, e.agent) }()
		waitBlocked(ctx, t, e, "RevokeTokensForActor")
		cur.Status, cur.ReplacedBy = domain.RecordRotated, &successor.ID
		return tx.UpdateToken(ctx, cur)
	})
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := <-revoked; err != nil {
		t.Fatalf("revoke: %v", err)
	}
	for _, id := range []domain.ID{old.ID, successor.ID} {
		if got, err := e.st.GetToken(ctx, id); err != nil || got.Status != domain.RecordRevoked {
			t.Errorf("token %s = %v, %v; want revoked", id, got.Status, err)
		}
	}
}

func TestAgentNamesAreUniqueAmongLiveAgents(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	agent := func(name string, status domain.RecordStatus) error {
		_, err := e.st.CreateAgent(ctx, domain.Agent{ID: gen.New(), OrgID: e.org.ID, Name: name, Harness: domain.HarnessCodex, CreatedBy: e.user.ID,
			Status: status, CreatedAt: t0, UpdatedAt: t0})
		return err
	}
	if err := agent("gone", domain.RecordRevoked); err != nil {
		t.Fatal(err)
	}
	if err := agent("gone", domain.RecordActive); err != nil {
		t.Errorf("reusing a revoked agent's name = %v, want it allowed", err)
	}
	var ve *domain.ValidationError
	if err := agent("gone", domain.RecordPending); !errors.As(err, &ve) || ve.Field != "agents_name_key" {
		t.Errorf("a second live agent of that name = %v, want the agents_name_key refusal", err)
	}
}
