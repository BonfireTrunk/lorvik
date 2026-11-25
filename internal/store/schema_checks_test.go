//go:build integration

package store

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// The schema refuses an actor kind other than user or agent, and an optional
// actor pair that is half set, whatever the application sends.
func TestActorColumnsAreChecked(t *testing.T) {
	e := newEnv(t)
	c := e.card(t, nil)
	for _, stmt := range []string{
		"UPDATE cards SET created_by_kind = 3 WHERE id = $1",
		"UPDATE cards SET assignee_kind = 1, assignee_id = NULL WHERE id = $1",
		"UPDATE cards SET assignee_kind = NULL, assignee_id = 7 WHERE id = $1",
		"UPDATE cards SET answered_by_kind = 2, answered_by_id = NULL WHERE id = $1",
		"UPDATE cards SET folded_by_kind = NULL, folded_by_id = 7 WHERE id = $1",
		"UPDATE cards SET assignee_kind = 0, assignee_id = 7 WHERE id = $1",
	} {
		_, err := e.st.db.Exec(t.Context(), stmt, int64(c.ID))
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "23514" {
			t.Errorf("%s: err = %v, want a check violation", stmt, err)
		}
	}
	if _, err := e.st.db.Exec(t.Context(), "UPDATE cards SET assignee_kind = 2, assignee_id = 7 WHERE id = $1", int64(c.ID)); err != nil {
		t.Errorf("a whole pair: %v", err)
	}
}
