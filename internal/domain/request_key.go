package domain

import "time"

// MaxRequestKeyLen bounds a client's idempotency key.
const MaxRequestKeyLen = 64

// RequestOp is the write a request key was first used for. Stored as a smallint.
type RequestOp int16

// RequestOp values.
const (
	RequestCreateCard RequestOp = 1
	RequestComment    RequestOp = 2
	RequestTransition RequestOp = 3
)

// RequestKey records that an actor's write with Key was made, and what it
// made: CardID is the card created, moved or commented on, SubjectID that
// card again, or the comment for RequestComment. Keys are per actor and
// never expire.
type RequestKey struct {
	Actor     ActorRef
	Key       string
	OrgID     ID
	ProjectID ID
	Operation RequestOp
	CardID    ID
	SubjectID ID
	CreatedAt time.Time
}

// ValidRequestKey reports whether s is 1 to 64 characters of letters,
// digits, hyphen and underscore.
func ValidRequestKey(s string) bool {
	if s == "" || len(s) > MaxRequestKeyLen {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}
