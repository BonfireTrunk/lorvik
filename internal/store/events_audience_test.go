//go:build integration

package store

import (
	"math"
	"slices"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

// The four feed queries apply one audience rule: an event of a limited type
// is returned, and counted, only for a reader of all of them or for the user
// it concerns as its membership subject or its user actor. The rule in SQL
// must agree with domain.Event.Concerns for every event here.
func TestLimitedEventsReachOnlyTheUserTheyConcern(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	user := e.user
	other := domain.ActorRef{Kind: domain.ActorUser, ID: gen.New()}
	third := domain.ActorRef{Kind: domain.ActorUser, ID: gen.New()}
	membership := func(a domain.ActorRef) domain.Subject {
		return domain.Subject{Type: domain.SubjectMembership, ID: a.ID}
	}
	evs := []domain.Event{
		e.event(t, e.agent, &e.project.ID, domain.EventCardCreated, domain.Subject{Type: domain.SubjectCard, ID: gen.New(), Number: 1}),
		e.event(t, user, nil, domain.EventIdentityLinked, membership(user)),
		e.event(t, other, nil, domain.EventMembershipInvited, membership(user)),
		e.event(t, third, nil, domain.EventIdentitySignInFailed, membership(third)),
		e.event(t, user, nil, domain.EventIdentityUnlinked, membership(other)),
		e.event(t, other, nil, domain.EventMembershipGranted, membership(user)),
		e.event(t, third, nil, domain.EventMembershipRequested, membership(third)),
		// A token event concerns the person whose token it is, as its actor;
		// an agent's concerns nobody.
		e.event(t, user, nil, domain.EventTokenRotated, domain.Subject{Type: domain.SubjectToken, ID: gen.New()}),
		e.event(t, other, nil, domain.EventTokenReuseDetected, domain.Subject{Type: domain.SubjectToken, ID: gen.New()}),
		e.event(t, e.agent, nil, domain.EventTokenRotated, domain.Subject{Type: domain.SubjectToken, ID: gen.New()}),
		// A token whose id is the user's id is not their membership.
		e.event(t, other, nil, domain.EventTokenRotated, domain.Subject{Type: domain.SubjectToken, ID: user.ID}),
		// The same id on an agent actor or on another kind of subject is not the user.
		e.event(t, domain.ActorRef{Kind: domain.ActorAgent, ID: user.ID}, nil, domain.EventIdentityLinked, membership(other)),
		e.event(t, other, nil, domain.EventIdentityLinked, domain.Subject{Type: domain.SubjectAgent, ID: user.ID}),
	}
	e.eventsVisible(t)

	tests := []struct {
		name  string
		reach query.LimitedReach
	}{
		{"no reach", query.LimitedReach{}},
		{"every limited event", query.LimitedReach{All: true}},
		{"the user most of them concern", query.LimitedReach{User: user.ID}},
		{"the user one of them concerns", query.LimitedReach{User: third.ID}},
		{"a user none concerns", query.LimitedReach{User: gen.New()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var want []domain.ID
			for _, ev := range evs {
				if tt.reach.Reaches(ev) {
					want = append(want, ev.ID)
				}
			}
			idsOf := func(got []domain.Event) []domain.ID {
				out := make([]domain.ID, len(got))
				for i, ev := range got {
					out[i] = ev.ID
				}
				return out
			}
			after, _, err := e.st.ListEvents(ctx, e.org.ID, query.EventFilter{Limit: 50, Limited: tt.reach})
			if err != nil || !slices.Equal(idsOf(after), want) {
				t.Errorf("paging forward = %v, %v; want %v", idsOf(after), err, want)
			}
			before, _, err := e.st.ListEvents(ctx, e.org.ID, query.EventFilter{Before: query.EventPosition{ID: math.MaxInt64}, Limit: 50, Limited: tt.reach})
			if err != nil || !slices.Equal(idsOf(before), want) {
				t.Errorf("paging backward = %v, %v; want %v", idsOf(before), err, want)
			}
			first, remaining, err := e.st.ListEvents(ctx, e.org.ID, query.EventFilter{Limit: 1, Limited: tt.reach})
			if err != nil || len(first) != 1 || remaining != len(want)-1 {
				t.Errorf("first page = %d events, %d remaining, %v; want 1 and %d", len(first), remaining, err, len(want)-1)
			}
			last, remaining, err := e.st.ListEvents(ctx, e.org.ID, query.EventFilter{Before: query.EventPosition{ID: math.MaxInt64}, Limit: 1, Limited: tt.reach})
			if err != nil || len(last) != 1 || remaining != len(want)-1 {
				t.Errorf("last page = %d events, %d older, %v; want 1 and %d", len(last), remaining, err, len(want)-1)
			}
			n, err := e.st.CountEventsAfter(ctx, e.org.ID, query.EventFilter{Limited: tt.reach})
			if err != nil || n != len(want) {
				t.Errorf("count = %d, %v; want %d", n, err, len(want))
			}
			latest, err := e.st.LatestEventPosition(ctx, e.org.ID, query.EventFilter{Limited: tt.reach})
			if err != nil || latest.ID != want[len(want)-1] {
				t.Errorf("latest position = %v, %v; want event %v, the newest this reader gets", latest.ID, err, want[len(want)-1])
			}
		})
	}

	// A subject's history has no reader, so it holds no limited event.
	history, err := e.st.ListEventsForSubject(ctx, e.org.ID, membership(user), 50)
	if err != nil || len(history) != 1 || history[0].ID != evs[5].ID {
		t.Errorf("history of the user's membership = %d events, %v; want only the membership.granted", len(history), err)
	}
	// The newest position also keeps to the reader's projects.
	scoped, err := e.st.LatestEventPosition(ctx, e.org.ID, query.EventFilter{ProjectIDs: []domain.ID{gen.New()}})
	if err != nil || scoped.ID != evs[5].ID {
		t.Errorf("latest position for a reader of another project = %v, %v; want the newest org-level event anyone gets", scoped.ID, err)
	}
}
