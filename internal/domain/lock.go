package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Lock bounds. A lock is a lease on a shared resource that two agents must
// not touch at once; the holder renews it explicitly and it lapses on its own.
const (
	MaxLockNameLen    = 40
	MaxLockPurposeLen = 200
	MinLockTTL        = 60 * time.Second
	MaxLockTTL        = 3600 * time.Second
	DefaultLockTTL    = 600 * time.Second
)

// Lock is a named, org-scoped lease. A lock named like a card lane also holds
// back the ready cards in that lane.
type Lock struct {
	ID    ID
	OrgID ID
	// Name is unique within the org: lowercase letters, digits, hyphens and slashes.
	Name   string
	Holder ActorRef
	// Session labels the client session that took the lock; empty when the
	// client sent none. Only the same actor and session may renew it.
	Session    string
	Purpose    string
	CardID     *ID
	AcquiredAt time.Time
	ExpiresAt  time.Time
}

// Expired reports whether the lease has lapsed.
func (l Lock) Expired(now time.Time) bool { return !now.Before(l.ExpiresAt) }

// HeldBy reports whether actor holds the lock in session.
func (l Lock) HeldBy(actor ActorRef, session string) bool {
	return l.Holder == actor && l.Session == session
}

// ParseLockName accepts 1 to 40 characters of a-z, 0-9, hyphen and slash.
func ParseLockName(s string) (string, error) {
	if s == "" || len(s) > MaxLockNameLen {
		return "", invalid("name", "1 to 40 characters")
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '/' {
			return "", invalid("name", "lowercase letters, digits, hyphens and slashes only")
		}
	}
	return s, nil
}

// ValidateLockPurpose requires 1 to 200 characters that are not all blank.
func ValidateLockPurpose(s string) error {
	if strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > MaxLockPurposeLen {
		return invalid("purpose", "1 to 200 characters saying what the lock protects")
	}
	return nil
}

// LockTTL turns the requested seconds into a lease length: zero means
// DefaultLockTTL, anything else must lie within the bounds.
func LockTTL(seconds int) (time.Duration, error) {
	if seconds == 0 {
		return DefaultLockTTL, nil
	}
	d := time.Duration(seconds) * time.Second
	if d < MinLockTTL || d > MaxLockTTL {
		return 0, invalid("ttl_seconds", "between 60 and 3600")
	}
	return d, nil
}
