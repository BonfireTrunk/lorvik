package domain

import "time"

// CardSubscription is one chat subscribed to one card: an event on the card wakes the
// chat. Session is a base label (BaseSession), so a chat's subagents share
// its subscriptions.
type CardSubscription struct {
	OrgID     ID
	CardID    ID
	AgentID   ID
	Session   string
	CreatedAt time.Time
}

// MaxChatSubscriptions is how many open cards one chat may be subscribed to at once.
const MaxChatSubscriptions = 50
