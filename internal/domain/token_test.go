package domain

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// Bootstrap stores the password identity and sign-in looks it up; both key
// it the same way, so these logins all reach one identity.
func TestPasswordKeyIgnoresCaseAndSpace(t *testing.T) {
	t.Parallel()
	for _, login := range []string{"ada", "Ada", " ADA ", "\tada\n"} {
		if got := PasswordKey(login); got != "ada" {
			t.Errorf("PasswordKey(%q) = %q, want ada", login, got)
		}
	}
}

func TestNewTokenMatchesItsSecret(t *testing.T) {
	t.Parallel()
	tok, secret := NewToken(7, 1, bot, "ci", ScopeRead, time.Hour, t0)
	switch {
	case tok.Status != RecordActive:
		t.Errorf("status = %v, want active", tok.Status)
	case !tok.ExpiresAt.Equal(t0.Add(time.Hour)) || !tok.CreatedAt.Equal(t0):
		t.Errorf("created %v expires %v, want %v and an hour later", tok.CreatedAt, tok.ExpiresAt, t0)
	case !strings.HasPrefix(secret, tok.Prefix) || !strings.HasPrefix(secret, TokenPrefix+"a_"):
		t.Errorf("secret %q does not start with prefix %q for an agent", secret, tok.Prefix)
	case !bytes.Equal(tok.Hash, HashToken(secret)):
		t.Error("hash is not the secret's hash")
	case tok.ID != 7 || tok.OrgID != 1 || tok.Actor != bot || tok.Name != "ci" || tok.Scope != ScopeRead:
		t.Errorf("token fields = %+v", tok)
	}
}
