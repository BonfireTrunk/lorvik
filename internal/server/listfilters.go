package server

import (
	"time"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// parseInstant reads an RFC 3339 query value; empty is the zero time.
func parseInstant(field, s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, invalidField(field, "want an RFC 3339 time such as 2026-10-20T00:00:00Z")
	}
	return t.UTC(), nil
}

// parseRange reads the <name>_after and <name>_before pair of a list.
func parseRange(name, after, before string) (query.TimeRange, error) {
	var r query.TimeRange
	var err error
	if r.After, err = parseInstant(name+"_after", after); err != nil {
		return query.TimeRange{}, err
	}
	if r.Before, err = parseInstant(name+"_before", before); err != nil {
		return query.TimeRange{}, err
	}
	if !r.After.IsZero() && !r.Before.IsZero() && !r.After.Before(r.Before) {
		return query.TimeRange{}, invalidField(name+"_before", name+"_after must be earlier than "+name+"_before")
	}
	return r, nil
}

// parseActorID reads an actor id or "me" (the caller).
func parseActorID(p Principal, field, s string) (domain.ID, error) {
	if s == "me" {
		return p.Actor.ID, nil
	}
	id, err := domain.ParseID(s)
	if err != nil {
		return 0, invalidField(field, "want an actor id or me")
	}
	return id, nil
}

// cardSorts maps the wire sort names to the store's.
var cardSorts = map[string]query.CardSort{
	"": query.SortBoard, "created": query.SortCreated, "updated": query.SortUpdated, "priority": query.SortPriority, "number": query.SortNumber,
}
