package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/query"
)

const (
	anaSecret      = "lvk_u_ana-secret"
	anaAgentSecret = "lvk_a_ana-agent-secret"
)

// limitedEnv is newEnv plus a member "ana", an org-wide agent she owns, and
// one event of every audience-limited type about her account, one about
// sam's, a rotation of her agent's token, and two org-level events everyone
// may read. The last of those is the mark a stream reader stops at.
type limitedEnv struct {
	*testEnv
	ana, anasAgent domain.ActorRef
}

const streamMark = "membership.changed"

func newLimitedEnv(t *testing.T) limitedEnv {
	t.Helper()
	e := newEnv(t)
	ctx := context.Background()
	ana := e.seedUser("ana", domain.RoleMember)
	e.seedToken(anaSecret, ana, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
	a, _ := e.fs.CreateAgent(ctx, domain.Agent{ID: e.ids.New(), OrgID: e.org.ID, Name: "anas-agent", Harness: domain.HarnessClaudeCode, CreatedBy: ana.ID,
		Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	_, _ = e.fs.PutMembership(ctx, domain.Membership{OrgID: e.org.ID, Actor: a.Ref(), Role: domain.RoleViewer, Rights: domain.RightWork, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0})
	e.renewed = append(e.renewed, e.seedToken(anaAgentSecret, a.Ref(), domain.ScopeWrite, t0.Add(domain.AgentTokenTTL)).ID)

	// A token event is about the token; its actor is whose token it is.
	token := tokenSubject(e.ids.New())
	for _, ev := range []struct {
		typ     domain.EventType
		actor   domain.ActorRef
		subject domain.Subject
	}{
		{domain.EventMembershipInvited, e.user, membershipSubject(ana.ID)},
		{domain.EventMembershipRequested, ana, membershipSubject(ana.ID)},
		{domain.EventIdentityLinked, ana, membershipSubject(ana.ID)},
		{domain.EventIdentityUnlinked, ana, membershipSubject(ana.ID)},
		{domain.EventIdentitySignInFailed, ana, membershipSubject(ana.ID)},
		{domain.EventIdentitySignInRefused, ana, membershipSubject(ana.ID)},
		{domain.EventTokenRotated, ana, token},
		{domain.EventTokenReuseDetected, ana, token},
		{domain.EventTokenRotated, a.Ref(), token},
		{domain.EventIdentityLinked, e.member, membershipSubject(e.member.ID)},
		{domain.EventMembershipGranted, e.user, membershipSubject(ana.ID)},
		{domain.EventMembershipChanged, e.user, membershipSubject(e.member.ID)},
	} {
		err := e.fs.WithTx(ctx, func(tx Store) error {
			return e.svc.emit(ctx, tx, ev.actor, e.org.ID, ev.typ, ev.subject, nil)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return limitedEnv{e, ana, a.Ref()}
}

// seen renders events as "type about <whose account>", limited types only,
// sorted: the membership an event is about, or for a token event its actor.
func (e limitedEnv) seen(evs []apitypes.Event) []string {
	who := map[string]string{e.ana.ID.String(): "ana", e.member.ID.String(): "sam", e.anasAgent.ID.String(): "ana's agent"}
	out := []string{}
	for _, ev := range evs {
		if t, err := domain.ParseEventType(ev.Type); err == nil && t.Limited() {
			about := ev.Subject.ID
			if ev.Subject.Type == "token" {
				about = ev.Actor.ID
			}
			out = append(out, ev.Type+" about "+who[about])
		}
	}
	slices.Sort(out)
	return out
}

// streamed reads the stream as secret from the start of the feed up to the
// mark every reader gets.
func (e limitedEnv) streamed(t *testing.T, srv *httptest.Server, secret string) []apitypes.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+apiPrefix+"/events/stream?after=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream = %d", resp.StatusCode)
	}
	var out []apitypes.Event
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue
		}
		var ev apitypes.Event
		if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &ev); err != nil {
			t.Fatalf("stream event: %v", err)
		}
		out = append(out, ev)
		if ev.Type == streamMark {
			return out
		}
	}
	t.Fatalf("stream ended before the mark: %v", sc.Err())
	return nil
}

// failedSignIns is the failed sign-in events secret's holder gets on the feed.
func failedSignIns(e *testEnv, secret string) []apitypes.Event {
	e.t.Helper()
	return eventsOfType(e, secret, "identity.signin_failed")
}

// eventsOfType is the events of one type secret's holder gets on the feed.
func eventsOfType(e *testEnv, secret, typ string) []apitypes.Event {
	e.t.Helper()
	var out []apitypes.Event
	for _, ev := range call[apitypes.EventList](e, secret, http.MethodGet, "/events?limit=200", nil, http.StatusOK).Items {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

// inboxCursor reads the inbox as secret with the query and returns the
// cursor to store next and the events listed.
func inboxCursor(e *testEnv, secret, query string) (string, []apitypes.Event) {
	e.t.Helper()
	rec := e.do(secret, http.MethodGet, "/inbox?"+query, nil, apitypes.HeaderSession, "calm-lynx-4aa4")
	var in apitypes.Inbox
	if err := json.Unmarshal(rec.Body.Bytes(), &in); err != nil || rec.Code != http.StatusOK {
		e.t.Fatalf("inbox = %d, %v: %s", rec.Code, err, rec.Body)
	}
	return in.Cursor, in.Events
}

// emitAbout writes an org-level event of typ by and about a user.
func emitAbout(e *testEnv, typ domain.EventType, about domain.ActorRef) {
	e.t.Helper()
	ctx := context.Background()
	err := e.fs.WithTx(ctx, func(tx Store) error {
		return e.svc.emit(ctx, tx, about, e.org.ID, typ, membershipSubject(about.ID), nil)
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

// Events about one person's account reach that person and the org's admins
// and nobody else, on every way the server hands out events: the feed, the
// inbox, the unread count and the stream. An agent gets none, whoever owns
// it.
func TestLimitedEventsReachOnlyThePersonConcernedAndAdmins(t *testing.T) {
	t.Parallel()
	anas := []string{
		"identity.linked about ana", "identity.signin_failed about ana", "identity.signin_refused about ana", "identity.unlinked about ana",
		"membership.invited about ana", "membership.requested about ana",
		"token.reuse_detected about ana", "token.rotated about ana",
	}
	tests := []struct {
		name   string
		secret string
		want   []string
	}{
		{"an owner sees them all", userSecret, append([]string{"identity.linked about sam", "token.rotated about ana's agent"}, anas...)},
		{"the person concerned sees their own", anaSecret, anas},
		{"another member sees only their own", memberSecret, []string{"identity.linked about sam"}},
		{"an admin's agent sees none", agentSecret, []string{}},
		{"the agent of the person concerned sees none", anaAgentSecret, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newLimitedEnv(t)
			slices.Sort(tt.want)
			// A session label keeps the reader's own events in the inbox and
			// the count: only what they wrote in this session is left out.
			session := []string{apitypes.HeaderSession, "calm-lynx-4aa4"}

			feed := call[apitypes.EventList](e.testEnv, tt.secret, http.MethodGet, "/events?limit=200", nil, http.StatusOK)
			if got := e.seen(feed.Items); !slices.Equal(got, tt.want) {
				t.Errorf("feed = %v, want %v", got, tt.want)
			}
			older := call[apitypes.EventList](e.testEnv, tt.secret, http.MethodGet, "/events?limit=200&before="+e.latestPosition(), nil, http.StatusOK)
			if got := e.seen(older.Items); !slices.Equal(got, tt.want) {
				t.Errorf("feed paged backwards = %v, want %v", got, tt.want)
			}

			rec := e.do(tt.secret, http.MethodGet, "/inbox?limit=200", nil, session...)
			if rec.Code != http.StatusOK {
				t.Fatalf("inbox = %d: %s", rec.Code, rec.Body)
			}
			var inbox apitypes.Inbox
			if err := json.Unmarshal(rec.Body.Bytes(), &inbox); err != nil {
				t.Fatal(err)
			}
			if got := e.seen(inbox.Events); !slices.Equal(got, tt.want) {
				t.Errorf("inbox = %v, want %v", got, tt.want)
			}

			rec = e.do(tt.secret, http.MethodGet, "/me", nil, append(session, apitypes.HeaderCursor, "0")...)
			if got, want := rec.Header().Get(apitypes.HeaderUnread), strconv.Itoa(len(tt.want)+2); got != want {
				t.Errorf("unread = %s, want %s: the limited events this reader gets and the two anyone gets", got, want)
			}

			srv := httptest.NewServer(e.h)
			defer srv.Close()
			if got := e.seen(e.streamed(t, srv, tt.secret)); !slices.Equal(got, tt.want) {
				t.Errorf("stream = %v, want %v", got, tt.want)
			}
		})
	}
}

// A page that is full of events the reader may not get must not count them
// as remaining either, or the count would tell a member how many there are.
func TestLimitedEventsAreNotCountedAsRemaining(t *testing.T) {
	t.Parallel()
	e := newLimitedEnv(t)
	page := call[apitypes.EventList](e.testEnv, agentSecret, http.MethodGet, "/events?limit=2", nil, http.StatusOK)
	if len(page.Items) != 2 || page.Remaining != 0 {
		t.Errorf("an agent's first page = %d events, %d remaining; want the 2 anyone gets and 0 remaining", len(page.Items), page.Remaining)
	}
}

// A wrong password for a login that exists records one event, which its
// owner and the org's admins get; an unknown login records none; and the
// caller is told the same thing both times.
func TestAWrongPasswordForAKnownLoginRecordsAnEvent(t *testing.T) {
	t.Parallel()
	e := newSignInEnv(t)
	failed := func(secret string) []apitypes.Event { return failedSignIns(e, secret) }

	unknown := e.passwordSignIn(addrA, "nobody", "guess")
	wantSignIn(t, "unknown login", unknown, http.StatusUnauthorized, "")
	if got := failed(userSecret); len(got) != 0 {
		t.Fatalf("an unknown login recorded %+v", got)
	}

	wrong := e.passwordSignIn(addrA, "sam", "guess")
	wantSignIn(t, "wrong password", wrong, http.StatusUnauthorized, "")
	if unknown.Body.String() != wrong.Body.String() {
		t.Errorf("refusals differ:\n%s\n%s", unknown.Body, wrong.Body)
	}
	got := failed(userSecret)
	if len(got) != 1 {
		t.Fatalf("the owner sees %d failed sign-ins, want 1", len(got))
	}
	sam := e.member.ID.String()
	if ev := got[0]; ev.Actor.ID != sam || ev.Subject.Type != "membership" || ev.Subject.ID != sam || ev.Project != nil || string(ev.Payload) != "null" {
		t.Errorf("event = %+v, want sam as actor and subject, no project and a null payload", ev)
	}
	if got := failed(memberSecret); len(got) != 1 {
		t.Errorf("sam sees %d failed sign-ins on their own login, want 1", len(got))
	}
	if got := failed(agentSecret); len(got) != 0 {
		t.Errorf("an agent sees %+v", got)
	}

	// Sam did not do this, so it is not left out of what sam reads as their
	// own doing: the dashboard, which sends no session label, shows it in the
	// inbox and counts it as unread.
	var inbox apitypes.Inbox
	rec := e.do(memberSecret, http.MethodGet, "/inbox?limit=200", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &inbox); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("inbox = %d, %v", rec.Code, err)
	}
	if !slices.ContainsFunc(inbox.Events, func(ev apitypes.Event) bool { return ev.Type == "identity.signin_failed" }) {
		t.Errorf("sam's dashboard inbox = %+v, want the failed sign-in on their login", inbox.Events)
	}
	if got := e.do(memberSecret, http.MethodGet, "/me", nil, apitypes.HeaderCursor, "0").Header().Get(apitypes.HeaderUnread); got != "1" {
		t.Errorf("sam's unread = %s, want 1", got)
	}
	if apitypes.ValidSession(domain.UnpromptedSession) {
		t.Errorf("%q is a label a request can carry", domain.UnpromptedSession)
	}

	wantSignIn(t, "right password", e.passwordSignIn(addrA, "sam", memberPassword), http.StatusCreated, "")
	if got := failed(userSecret); len(got) != 1 {
		t.Errorf("a right password left %d failed sign-ins, want still 1", len(got))
	}
}

// Callers who are not signed in can try a login about 80 times an hour
// within the sign-in limits, so only the first wrong password in an hour
// becomes an event; the rest are in the server log.
func TestFailedSignInsAreRecordedOnceAnHourPerLogin(t *testing.T) {
	t.Parallel()
	e := newSignInEnv(t)
	count := func() int { return len(failedSignIns(e, userSecret)) }
	for range 3 {
		wantSignIn(t, "wrong password", e.passwordSignIn(addrA, "sam", "guess"), http.StatusUnauthorized, "")
	}
	wantSignIn(t, "wrong password from another address", e.passwordSignIn(addrB, "sam", "guess"), http.StatusUnauthorized, "")
	if got := count(); got != 1 {
		t.Errorf("four wrong passwords in an hour recorded %d events, want 1", got)
	}
	wantSignIn(t, "wrong password for another login", e.passwordSignIn(addrA, "ada", "guess"), http.StatusUnauthorized, "")
	if got := count(); got != 2 {
		t.Errorf("another login's wrong password: %d events, want 2", got)
	}
	e.advance(failedEventWindow + time.Minute)
	wantSignIn(t, "wrong password an hour later", e.passwordSignIn(addrA, "sam", "guess"), http.StatusUnauthorized, "")
	if got := count(); got != 3 {
		t.Errorf("a wrong password an hour later: %d events, want 3", got)
	}
}

// One wrong password may be the owner's slip; wrong passwords that reach a
// sign-in limit are guessing. The one that fills a limit records a second
// event, once an hour per login like the first, so the feed tells the two
// apart (#334 B). A refused try checks no password and records nothing.
func TestWrongPasswordsThatReachALimitRecordASecondEvent(t *testing.T) {
	t.Parallel()
	refused := func(e *testEnv, secret string) []apitypes.Event {
		return eventsOfType(e, secret, "identity.signin_refused")
	}
	t.Run("from one address", func(t *testing.T) {
		t.Parallel()
		e := newSignInEnv(t)
		for range pairFailures - 1 {
			wantSignIn(t, "wrong password", e.passwordSignIn(addrA, "sam", "guess"), http.StatusUnauthorized, "")
		}
		if got := refused(e, userSecret); len(got) != 0 {
			t.Fatalf("under the limit: %d refused events, want none", len(got))
		}
		wantSignIn(t, "the wrong password that fills the limit", e.passwordSignIn(addrA, "sam", "guess"), http.StatusUnauthorized, "")
		got := refused(e, userSecret)
		if len(got) != 1 {
			t.Fatalf("at the limit: %d refused events, want 1", len(got))
		}
		sam := e.member.ID.String()
		if ev := got[0]; ev.Actor.ID != sam || ev.Subject.Type != "membership" || ev.Subject.ID != sam || ev.Project != nil || string(ev.Payload) != "null" {
			t.Errorf("event = %+v, want sam as actor and subject, no project and a null payload", ev)
		}
		if n := len(failedSignIns(e, userSecret)); n != 1 {
			t.Errorf("%d failed sign-in events beside it, want the hour's 1", n)
		}
		if n := len(refused(e, memberSecret)); n != 1 {
			t.Errorf("sam sees %d refused events on their own login, want 1", n)
		}
		if got := refused(e, agentSecret); len(got) != 0 {
			t.Errorf("an agent sees %+v", got)
		}

		wantSignIn(t, "a refused try", e.passwordSignIn(addrA, "sam", "guess"), http.StatusTooManyRequests, "900")
		for range pairFailures {
			wantSignIn(t, "wrong password from another address", e.passwordSignIn(addrB, "sam", "guess"), http.StatusUnauthorized, "")
		}
		if n := len(refused(e, userSecret)); n != 1 {
			t.Errorf("a second address at its limit in the same hour: %d refused events, want still 1", n)
		}
		e.advance(failedEventWindow + time.Minute)
		for range pairFailures {
			wantSignIn(t, "wrong password an hour later", e.passwordSignIn(addrA, "sam", "guess"), http.StatusUnauthorized, "")
		}
		if n := len(refused(e, userSecret)); n != 2 {
			t.Errorf("at the limit again an hour later: %d refused events, want 2", n)
		}
	})
	t.Run("from anywhere", func(t *testing.T) {
		t.Parallel()
		e := newSignInEnv(t)
		addr := func(i int) string { return fmt.Sprintf("198.51.100.%d", i+1) }
		for i := range loginFailures - 1 {
			wantSignIn(t, "wrong password", e.passwordSignIn(addr(i), "sam", "guess"), http.StatusUnauthorized, "")
		}
		if got := refused(e, userSecret); len(got) != 0 {
			t.Fatalf("under the limit: %d refused events, want none", len(got))
		}
		wantSignIn(t, "the wrong password that fills the limit", e.passwordSignIn(addr(loginFailures), "sam", "guess"), http.StatusUnauthorized, "")
		if n := len(refused(e, userSecret)); n != 1 {
			t.Errorf("at the limit: %d refused events, want 1", n)
		}
	})
	t.Run("an unknown login", func(t *testing.T) {
		t.Parallel()
		e := newSignInEnv(t)
		for range pairFailures {
			wantSignIn(t, "wrong password", e.passwordSignIn(addrA, "nobody", "guess"), http.StatusUnauthorized, "")
		}
		wantSignIn(t, "a refused try", e.passwordSignIn(addrA, "nobody", "guess"), http.StatusTooManyRequests, "900")
		if n := len(e.fs.events); n != 0 {
			t.Errorf("a login nobody has recorded %d events, want none", n)
		}
	})
}

// The two events of a wrong password are recorded apart: a write that
// fails or panics for one leaves the other written, and gives back only its
// own hour.
func TestAFailedSignInEventThatIsNotWrittenLeavesTheOtherOne(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		fail func() error
	}{
		{"the write fails", func() error { return errors.New("no room") }},
		{"the write panics", func() error { panic("boom") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newSignInEnv(t)
			e.fs.onAppend = func(ev domain.Event) error {
				if ev.Type == domain.EventIdentitySignInFailed {
					return tt.fail()
				}
				return nil
			}
			for range pairFailures {
				wantSignIn(t, "wrong password", e.passwordSignIn(addrA, "sam", "guess"), http.StatusUnauthorized, "")
			}
			if failed, refused := len(failedSignIns(e, userSecret)), len(eventsOfType(e, userSecret, "identity.signin_refused")); failed != 0 || refused != 1 {
				t.Fatalf("%d failed and %d refused events, want 0 and 1", failed, refused)
			}
			e.fs.onAppend = nil
			wantSignIn(t, "wrong password from another address", e.passwordSignIn(addrB, "sam", "guess"), http.StatusUnauthorized, "")
			if failed := len(failedSignIns(e, userSecret)); failed != 1 {
				t.Errorf("once writes work again: %d failed events, want 1", failed)
			}
		})
	}
}

// forgettingCounters notes whether the context of each Forget was still live.
type forgettingCounters struct {
	Counters
	live []bool
}

func (c *forgettingCounters) Forget(ctx context.Context, key string) {
	c.live = append(c.live, ctx.Err() == nil)
	c.Counters.Forget(ctx, key)
}

// A write that used up its time must still give the hour back: with shared
// counters that is a call of its own, and it needs a context that is not
// already over.
func TestAFailedSignInWriteThatTimedOutStillGivesTheHourBack(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := newSignInEnv(t)
		counters := &forgettingCounters{Counters: e.svc.limits.failed}
		e.svc.limits.failed = counters
		e.fs.onAppend = func(domain.Event) error {
			time.Sleep(signInFailedTimeout + time.Second)
			return context.DeadlineExceeded
		}
		wantSignIn(t, "wrong password", e.passwordSignIn(addrA, "sam", "guess"), http.StatusUnauthorized, "")
		if !slices.Equal(counters.live, []bool{true}) {
			t.Errorf("the hour was given back with live contexts %v, want once with a live one", counters.live)
		}
	})
}

// count reports the wrong password that brings a count to its limit, from
// one address or from anywhere, and only that one.
func TestCountReportsTheWrongPasswordThatFillsALimit(t *testing.T) {
	t.Parallel()
	now := t0
	l := newSignInLimits(func() time.Time { return now }, slog.New(slog.DiscardHandler))
	ctx := context.Background()
	one := attempt{pairKey: "pair", shareKey: "share", loginKey: "login"}
	filledAt := func(a func(i int) attempt, n int) []int {
		var at []int
		for i := 1; i <= n; i++ {
			if l.count(ctx, a(i), false) {
				at = append(at, i)
			}
		}
		return at
	}
	same := func(int) attempt { return one }
	if got := filledAt(same, pairFailures+1); !slices.Equal(got, []int{pairFailures}) {
		t.Errorf("one address: filled at %v, want only at %d", got, pairFailures)
	}
	// The address has used its share of the login's count; its own limit
	// still fills, and reports, once the 15 minutes are over.
	now = now.Add(pairWindow + time.Second)
	if got := filledAt(same, pairFailures); !slices.Equal(got, []int{pairFailures}) {
		t.Errorf("the same address 15 minutes later: filled at %v, want at %d", got, pairFailures)
	}
	if l.count(ctx, one, true) {
		t.Error("a right password reported a filled limit")
	}
	// Another login, one wrong password from each of many addresses.
	spread := func(i int) attempt {
		return attempt{pairKey: fmt.Sprint("pair", i), shareKey: fmt.Sprint("share", i), loginKey: "other login"}
	}
	if got := filledAt(spread, loginFailures+1); !slices.Equal(got, []int{loginFailures}) {
		t.Errorf("from anywhere: filled at %v, want only at %d", got, loginFailures)
	}
}

// A cursor handed to a reader names only events that reader may get. One
// that moved for a hidden event would tell them something happened, and
// when, though the event itself is kept from them.
func TestTheInboxCursorDoesNotMoveForAnEventTheReaderMayNotGet(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	cursor := func(secret, after string) string {
		c, _ := inboxCursor(e, secret, "cursor="+after)
		return c
	}
	emitAbout(e, domain.EventMembershipChanged, e.user)
	member, agent, owner := cursor(memberSecret, ""), cursor(agentSecret, ""), cursor(userSecret, "")
	if member == "" || member != agent || member != owner {
		t.Fatalf("cursors after an event anyone gets = %q, %q, %q; want one position", member, agent, owner)
	}
	emitAbout(e, domain.EventIdentityLinked, e.user)
	if got := cursor(memberSecret, member); got != member {
		t.Errorf("a member's cursor moved from %s to %s for an event about someone else's account", member, got)
	}
	if got, evs := inboxCursor(e, memberSecret, "project=lorvik&cursor="+member); got != member || len(evs) != 0 {
		t.Errorf("a member's inbox of one project: cursor %s and %d events, want the cursor still at %s and none", got, len(evs), member)
	}
	if got := cursor(agentSecret, agent); got != agent {
		t.Errorf("an agent's cursor moved from %s to %s for an event it may not get", agent, got)
	}
	if got := cursor(userSecret, owner); got == owner {
		t.Errorf("the owner's cursor stayed at %s, want it past the event", owner)
	}
}

// An inbox cursor only moves forward. A reader whose stored cursor names an
// event they no longer get, an admin since made a member, keeps it; the
// position of the newest event they may still read is older.
func TestAnInboxCursorNeverMovesBack(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ana := e.seedUser("ana", domain.RoleAdmin)
	e.seedToken(anaSecret, ana, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
	cursor := func(after string) string {
		c, _ := inboxCursor(e, anaSecret, "cursor="+after)
		return c
	}
	emitAbout(e, domain.EventMembershipChanged, e.member)
	visible := e.latestPosition()
	emitAbout(e, domain.EventIdentityLinked, e.user)
	past := cursor("")
	if past != e.latestPosition() {
		t.Fatalf("an admin's cursor = %s, want %s, past the event about the owner's account", past, e.latestPosition())
	}
	if _, err := e.fs.PutMembership(context.Background(), domain.Membership{OrgID: e.org.ID, Actor: ana, Role: domain.RoleMember, Status: domain.RecordActive, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if got := cursor(past); got != past {
		t.Errorf("made a member, the cursor went from %s to %s; the newest event they may read is at %s", past, got, visible)
	}
	// A bare event id, as older clients stored, still becomes a position.
	_, id, _ := strings.Cut(visible, "-")
	if got := cursor(strings.TrimLeft(id, "0")); got != visible {
		t.Errorf("a bare event id came back as %s, want the position %s", got, visible)
	}
	emitAbout(e, domain.EventMembershipChanged, e.member)
	if got := cursor(past); got != e.latestPosition() {
		t.Errorf("after a new event anyone gets the cursor = %s, want %s", got, e.latestPosition())
	}
}

// A stream opened without a position starts at the newest event its caller
// may get, as the inbox cursor does.
func TestAStreamStartsAtTheNewestEventItsCallerMayGet(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	emitAbout(e, domain.EventMembershipChanged, e.member)
	visible := e.latestPosition()
	emitAbout(e, domain.EventIdentityLinked, e.user)
	for _, tt := range []struct {
		name  string
		actor domain.ActorRef
		want  string
	}{
		{"a member", e.member, visible},
		{"the owner", e.user, e.latestPosition()},
	} {
		tok := e.seedToken("lvk_u_start-"+tt.actor.ID.String(), tt.actor, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
		p, prob := e.svc.standing(t.Context(), tok, "", e.clock())
		if prob != nil {
			t.Fatal(prob)
		}
		start, err := e.svc.StreamStart(WithPrincipal(t.Context(), p), "", "")
		if err != nil {
			t.Fatal(err)
		}
		if got := eventPosition(start); got != tt.want {
			t.Errorf("%s's stream starts at %s, want %s", tt.name, got, tt.want)
		}
	}
}

// The hour's one event is spent only when an event was written. A write
// that failed, or a login with no org to record it in, must not leave the
// wrong passwords of the next hour unrecorded; and a panic while recording
// must not reach the request.
func TestAFailedSignInThatRecordedNothingDoesNotSpendTheHour(t *testing.T) {
	t.Parallel()
	membership := func(e *testEnv, status domain.RecordStatus) {
		e.t.Helper()
		m := domain.Membership{OrgID: e.org.ID, Actor: e.member, Role: domain.RoleMember, Status: status, CreatedAt: t0, UpdatedAt: t0}
		if _, err := e.fs.PutMembership(context.Background(), m); err != nil {
			e.t.Fatal(err)
		}
	}
	tests := []struct {
		name     string
		hinder   func(e *testEnv)
		unhinder func(e *testEnv)
	}{
		{"the write fails",
			func(e *testEnv) { e.fs.onAppend = func(domain.Event) error { return errors.New("no room") } },
			func(e *testEnv) { e.fs.onAppend = nil }},
		{"the write panics",
			func(e *testEnv) { e.fs.onAppend = func(domain.Event) error { panic("boom") } },
			func(e *testEnv) { e.fs.onAppend = nil }},
		{"the login's owner is in no org",
			func(e *testEnv) { membership(e, domain.RecordPending) },
			func(e *testEnv) { membership(e, domain.RecordActive) }},
		{"nothing is in the way", func(*testEnv) {}, func(*testEnv) {}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newSignInEnv(t)
			tt.hinder(e)
			wantSignIn(t, "wrong password", e.passwordSignIn(addrA, "sam", "guess"), http.StatusUnauthorized, "")
			tt.unhinder(e)
			wantSignIn(t, "wrong password again", e.passwordSignIn(addrA, "sam", "guess"), http.StatusUnauthorized, "")
			if got := len(failedSignIns(e, userSecret)); got != 1 {
				t.Errorf("two wrong passwords recorded %d events, want 1", got)
			}
		})
	}
}

func TestLimitedReachByCaller(t *testing.T) {
	t.Parallel()
	user := domain.ActorRef{Kind: domain.ActorUser, ID: 7}
	agent := domain.ActorRef{Kind: domain.ActorAgent, ID: 9}
	all, own, none := query.LimitedReach{All: true}, query.LimitedReach{User: 7}, query.LimitedReach{}
	tests := []struct {
		name string
		p    Principal
		want query.LimitedReach
	}{
		{"owner", Principal{Actor: user, HasOrgRole: true, OrgRole: domain.RoleOwner}, all},
		{"admin", Principal{Actor: user, HasOrgRole: true, OrgRole: domain.RoleAdmin}, all},
		{"admin with a read-only token", Principal{Actor: user, HasOrgRole: true, OrgRole: domain.RoleAdmin, ReadOnly: true}, all},
		{"reviewer", Principal{Actor: user, HasOrgRole: true, OrgRole: domain.RoleReviewer}, own},
		{"member", Principal{Actor: user, HasOrgRole: true, OrgRole: domain.RoleMember}, own},
		{"a person with no org role", Principal{Actor: user}, own},
		{"an owner's agent", Principal{Actor: agent, HasOrgRole: true, OrgRole: domain.RoleViewer, Owner: 7, OwnerOrgRole: domain.RoleOwner}, none},
		{"an agent holding every right", Principal{Actor: agent, HasOrgRole: true, OrgRole: domain.RoleOwner, OrgRights: ^domain.Rights(0), Owner: 7, OwnerOrgRole: domain.RoleOwner}, none},
	}
	for _, tt := range tests {
		if got := tt.p.limitedReach(); got != tt.want {
			t.Errorf("%s: reach = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

// The event is written aside of the request, so the refusal does not wait
// for it: a login that exists must not answer later than one that does not.
func TestAFailedSignInIsRecordedAsideOfTheRequest(t *testing.T) {
	t.Parallel()
	e := newSignInEnv(t)
	var held []func()
	e.svc.aside = func(f func()) { held = append(held, f) }
	wantSignIn(t, "wrong password", e.passwordSignIn(addrA, "sam", "guess"), http.StatusUnauthorized, "")
	if n := len(e.fs.events); n != 0 || len(held) != 1 {
		t.Fatalf("after the answer: %d events and %d pieces of work set aside, want 0 and 1", n, len(held))
	}
	held[0]()
	if n := len(e.fs.events); n != 1 {
		t.Errorf("after the work ran: %d events, want 1", n)
	}
}

func TestLimitedEventTypesAndWhoTheyConcern(t *testing.T) {
	t.Parallel()
	want := []domain.EventType{
		domain.EventMembershipInvited, domain.EventMembershipRequested,
		domain.EventIdentityLinked, domain.EventIdentityUnlinked, domain.EventIdentitySignInFailed, domain.EventIdentitySignInRefused,
		domain.EventTokenRotated, domain.EventTokenReuseDetected,
	}
	for _, typ := range domain.AllEventTypes {
		if got := typ.Limited(); got != slices.Contains(want, typ) {
			t.Errorf("%s limited = %v", typ, got)
		}
	}
	user, other := domain.ID(7), domain.ID(8)
	tests := []struct {
		name string
		ev   domain.Event
		want bool
	}{
		{"its subject", domain.Event{Actor: domain.ActorRef{Kind: domain.ActorUser, ID: other}, Subject: membershipSubject(user)}, true},
		{"its actor", domain.Event{Actor: domain.ActorRef{Kind: domain.ActorUser, ID: user}, Subject: membershipSubject(other)}, true},
		{"someone else's", domain.Event{Actor: domain.ActorRef{Kind: domain.ActorUser, ID: other}, Subject: membershipSubject(other)}, false},
		{"an agent with the same id is not the user", domain.Event{Actor: domain.ActorRef{Kind: domain.ActorAgent, ID: user}, Subject: membershipSubject(other)}, false},
		{"another kind of subject with the same id", domain.Event{Actor: domain.ActorRef{Kind: domain.ActorUser, ID: other}, Subject: agentSubject(user)}, false},
		{"their own token", domain.Event{Actor: domain.ActorRef{Kind: domain.ActorUser, ID: user}, Subject: tokenSubject(other)}, true},
		{"someone else's token with the same id", domain.Event{Actor: domain.ActorRef{Kind: domain.ActorUser, ID: other}, Subject: tokenSubject(user)}, false},
	}
	for _, tt := range tests {
		if got := tt.ev.Concerns(user); got != tt.want {
			t.Errorf("%s: Concerns = %v, want %v", tt.name, got, tt.want)
		}
	}
	if (domain.Event{Subject: membershipSubject(0)}).Concerns(0) {
		t.Error("no user is concerned by an event with a zero subject id")
	}
}

// A stream wakes for every event of its org, also for one its caller may
// not get. That wake-up must not move the caller's next heartbeat, or the
// gap would tell them something happened.
func TestAHiddenEventDoesNotShiftAStreamsHeartbeat(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		tok := e.seedToken("lvk_u_stream-secret", e.member, domain.ScopeWrite, t0.Add(domain.DefaultTokenTTL))
		p, prob := e.svc.standing(t.Context(), tok, "", e.clock())
		if prob != nil {
			t.Fatal(prob)
		}
		ctx, cancel := context.WithCancel(WithPrincipal(t.Context(), p))
		head, herr := e.svc.StreamStart(ctx, "", "")
		if herr != nil {
			t.Fatal(herr)
		}
		start := time.Now()
		var beats []time.Duration
		done := make(chan error, 1)
		go func() {
			done <- e.svc.StreamEvents(ctx, "", head, func(ev apitypes.Event) error {
				t.Errorf("the member was sent %s", ev.Type)
				return nil
			}, func() error {
				beats = append(beats, time.Since(start))
				return nil
			})
		}()
		time.Sleep(10 * time.Second)
		err := e.fs.WithTx(ctx, func(tx Store) error {
			return e.svc.emit(ctx, tx, e.user, e.org.ID, domain.EventIdentityLinked, membershipSubject(e.user.ID), nil)
		})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(2*streamPoll - 10*time.Second)
		synctest.Wait()
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if len(beats) != 1 || beats[0] != 2*streamPoll {
			t.Errorf("heartbeats at %v, want one at %v as on a stream nothing woke", beats, 2*streamPoll)
		}
	})
}
