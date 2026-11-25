package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

// named is the headers of a request from chat label whose client sends name.
func named(label, name string) []string {
	return []string{apitypes.HeaderSession, label, apitypes.HeaderSessionName, url.PathEscape(name)}
}

// The name a chat's client sends shows beside its label wherever the chat
// does: on the card it holds, in the live claims, on its lock, on the Access
// page's chat row and in its own /me. A subagent's request names its chat.
func TestAChatsNameShowsBesideItsLabel(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	card := e.readyCard(userSecret, "held by a named chat")
	if rec := e.do(agentSecret, http.MethodPost, "/projects/lorvik/cards/"+itoa(card.Number)+"/claim", apitypes.ClaimCard{}, named("quiet-heron-1a2b-c7", "Sign-in fixes")...); rec.Code != http.StatusOK {
		t.Fatalf("claim = %d: %s", rec.Code, rec.Body)
	}
	if got := e.show(userSecret, card.Number).Claim; got == nil || got.Session != "quiet-heron-1a2b-c7" || got.SessionName != "Sign-in fixes" {
		t.Errorf("claim = %+v, want the subagent's label with its chat's name", got)
	}
	live := call[apitypes.LiveClaimList](e, userSecret, http.MethodGet, "/claims", nil, http.StatusOK).Items
	if len(live) != 1 || live[0].SessionName != "Sign-in fixes" {
		t.Errorf("live claims = %+v, want one named Sign-in fixes", live)
	}
	lock := e.acquire(agentSecret, "ci", "quiet-heron-1a2b", apitypes.AcquireLock{Purpose: "gate"}).created(t)
	if lock.SessionName != "Sign-in fixes" {
		t.Errorf("lock session name = %q, want Sign-in fixes", lock.SessionName)
	}
	ag := call[apitypes.Agent](e, userSecret, http.MethodGet, "/agents/"+e.agent.ID.String(), nil, http.StatusOK)
	if len(ag.Sessions) != 1 || ag.Sessions[0].Label != "quiet-heron-1a2b" || ag.Sessions[0].Name != "Sign-in fixes" {
		t.Errorf("agent sessions = %+v, want the base label named Sign-in fixes", ag.Sessions)
	}
	rec := e.do(agentSecret, http.MethodGet, "/me", nil, apitypes.HeaderSession, "quiet-heron-1a2b")
	if !strings.Contains(rec.Body.String(), `"session_name":"Sign-in fixes"`) {
		t.Errorf("/me = %s, want the chat's name", rec.Body)
	}
	if got := e.show(userSecret, e.readyCard(userSecret, "nobody holds this").Number).Claim; got != nil {
		t.Errorf("unclaimed card has claim %+v", got)
	}
}

// A renamed chat reads by its new name from its next request, without
// waiting for the once-a-minute touch, and a name sent again unchanged does
// not reach the store.
func TestARenamedChatIsStoredAtOnceAndAnUnchangedNameIsNot(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	me := func(name string) {
		t.Helper()
		if rec := e.do(agentSecret, http.MethodGet, "/me", nil, named("quiet-heron-1a2b", name)...); rec.Code != http.StatusOK {
			t.Fatalf("/me = %d: %s", rec.Code, rec.Body)
		}
	}
	stored := func() string {
		t.Helper()
		ag := call[apitypes.Agent](e, userSecret, http.MethodGet, "/agents/"+e.agent.ID.String(), nil, http.StatusOK)
		if len(ag.Sessions) != 1 {
			t.Fatalf("sessions = %+v, want one", ag.Sessions)
		}
		return ag.Sessions[0].Name
	}
	me("first name")
	me("first name")
	me("first name")
	if e.fs.sessionNamings != 1 {
		t.Errorf("store namings = %d, want 1 for three requests with one name", e.fs.sessionNamings)
	}
	me("second name")
	if got := stored(); got != "second name" {
		t.Errorf("name = %q, want second name at once", got)
	}
	// A request that sends no name leaves the stored one alone.
	e.advance(2 * touchInterval)
	if rec := e.do(agentSecret, http.MethodGet, "/me", nil, apitypes.HeaderSession, "quiet-heron-1a2b"); rec.Code != http.StatusOK {
		t.Fatalf("/me = %d", rec.Code)
	}
	if got := stored(); got != "second name" {
		t.Errorf("name after a request without one = %q, want second name kept", got)
	}
}

// A chat whose client finds no name is named through the route; a person's
// request and one that names no chat are refused; the last name sent wins,
// so a client that sends one later replaces it; empty clears.
func TestSettingAChatsNameByRoute(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	put := func(secret, label, name string, want int) apitypes.AgentSession {
		t.Helper()
		var hdr []string
		if label != "" {
			hdr = []string{apitypes.HeaderSession, label}
		}
		rec := e.do(secret, http.MethodPut, "/me/session-name", apitypes.PutSessionName{Name: name}, hdr...)
		if rec.Code != want {
			t.Fatalf("PUT /me/session-name as %s = %d, want %d: %s", label, rec.Code, want, rec.Body)
		}
		var got apitypes.AgentSession
		if want == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
		}
		return got
	}
	if got := put(agentSecret, "quiet-heron-1a2b-c3", "  Release\tnotes\n", http.StatusOK); got.Label != "quiet-heron-1a2b" || got.Name != "Release notes" {
		t.Errorf("named = %+v, want the base label named Release notes", got)
	}
	put(userSecret, "quiet-heron-1a2b", "mine", http.StatusForbidden)
	put(agentSecret, "", "nameless", http.StatusUnprocessableEntity)

	sessionName := func() string {
		t.Helper()
		return call[apitypes.Agent](e, userSecret, http.MethodGet, "/agents/"+e.agent.ID.String(), nil, http.StatusOK).Sessions[0].Name
	}
	if rec := e.do(agentSecret, http.MethodGet, "/me", nil, named("quiet-heron-1a2b", "From the harness")...); rec.Code != http.StatusOK {
		t.Fatalf("/me = %d", rec.Code)
	}
	if got := sessionName(); got != "From the harness" {
		t.Errorf("name = %q, want the one the client sent last", got)
	}
	put(agentSecret, "quiet-heron-1a2b", "", http.StatusOK)
	if got := sessionName(); got != "" {
		t.Errorf("name after clearing = %q, want none", got)
	}
}

// The server remembers the last name a chat sent only to skip writes that
// would change nothing. A name set by route in between, or a write that
// failed, must not make it skip the next one.
func TestANameSentAgainIsStoredWhenTheStoredOneDiffers(t *testing.T) {
	t.Parallel()
	me := func(t *testing.T, e *testEnv, name string) {
		t.Helper()
		hdr := []string{apitypes.HeaderSession, "quiet-heron-1a2b"}
		if name != "" {
			hdr = named("quiet-heron-1a2b", name)
		}
		if rec := e.do(agentSecret, http.MethodGet, "/me", nil, hdr...); rec.Code != http.StatusOK {
			t.Fatalf("/me = %d: %s", rec.Code, rec.Body)
		}
	}
	stored := func(t *testing.T, e *testEnv) string {
		t.Helper()
		ag := call[apitypes.Agent](e, userSecret, http.MethodGet, "/agents/"+e.agent.ID.String(), nil, http.StatusOK)
		if len(ag.Sessions) != 1 {
			t.Fatalf("sessions = %+v, want one", ag.Sessions)
		}
		return ag.Sessions[0].Name
	}
	t.Run("after a name set by route", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		me(t, e, "From the harness")
		if rec := e.do(agentSecret, http.MethodPut, "/me/session-name", apitypes.PutSessionName{Name: "By command"}, apitypes.HeaderSession, "quiet-heron-1a2b"); rec.Code != http.StatusOK {
			t.Fatalf("PUT = %d: %s", rec.Code, rec.Body)
		}
		me(t, e, "From the harness")
		if got := stored(t, e); got != "From the harness" {
			t.Errorf("name = %q, want the one the client sent again, at once", got)
		}
	})
	t.Run("after a failed write", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		me(t, e, "")
		e.fs.failNaming = errors.New("store down")
		me(t, e, "Sign-in fixes")
		if got := stored(t, e); got != "" {
			t.Fatalf("name = %q, want none after the failed write", got)
		}
		me(t, e, "Sign-in fixes")
		if got := stored(t, e); got != "Sign-in fixes" {
			t.Errorf("name = %q, want it stored by the next request", got)
		}
	})
}

// Listing claims held by many chats of one agent reads their names in one
// query, not one per chat.
func TestChatNamesOfAListAreReadInOneQueryPerAgent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for label, name := range map[string]string{"quiet-heron-1a2b": "one", "calm-lynx-3c4d": "two", "brisk-otter-5e6f-c2": "three"} {
		card := e.readyCard(userSecret, "held by "+name)
		if rec := e.do(agentSecret, http.MethodPost, "/projects/lorvik/cards/"+itoa(card.Number)+"/claim", apitypes.ClaimCard{}, named(label, name)...); rec.Code != http.StatusOK {
			t.Fatalf("claim = %d: %s", rec.Code, rec.Body)
		}
	}
	names := func(items []string) map[string]bool {
		out := map[string]bool{}
		for _, n := range items {
			out[n] = true
		}
		return out
	}
	want := map[string]bool{"one": true, "two": true, "three": true}

	before := e.fs.sessionLookups
	live := call[apitypes.LiveClaimList](e, userSecret, http.MethodGet, "/claims", nil, http.StatusOK).Items
	var got []string
	for _, l := range live {
		got = append(got, l.SessionName)
	}
	if n := e.fs.sessionLookups - before; n != 1 || len(names(got)) != 3 {
		t.Errorf("GET /claims: %d name queries for names %v, want 1 query and %v", n, got, want)
	}

	before = e.fs.sessionLookups
	cards := call[apitypes.CardList](e, userSecret, http.MethodGet, "/projects/lorvik/cards?status=working", nil, http.StatusOK).Items
	got = nil
	for _, c := range cards {
		if c.Claim != nil {
			got = append(got, c.Claim.SessionName)
		}
	}
	if n := e.fs.sessionLookups - before; n != 1 || len(names(got)) != 3 {
		t.Errorf("GET cards: %d name queries for names %v, want 1 query and %v", n, got, want)
	}
}

// A name is cleaned, never refused: it is whatever a person typed into
// their harness. A header that does not decode names nothing.
func TestSentChatNamesAreCleaned(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, header, want string
	}{
		{"control characters and runs of space", url.PathEscape("fix\x00 the\r\n  build\x1b[31m"), "fix the build [31m"},
		{"non-ASCII kept", url.PathEscape("Überprüfung ✓"), "Überprüfung ✓"},
		{"cut to the limit", url.PathEscape(strings.Repeat("é", 90)), strings.Repeat("é", domain.MaxSessionName)},
		{"bad percent-encoding", "half%2", ""},
		{"only white space", url.PathEscape(" \t "), ""},
		{"invisible and direction characters dropped", url.PathEscape("a\u202eb\u200bc\ufeff"), "abc"},
		{"nothing visible left", url.PathEscape("\u200b\u2060"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			if rec := e.do(agentSecret, http.MethodGet, "/me", nil, apitypes.HeaderSession, "quiet-heron-1a2b", apitypes.HeaderSessionName, tt.header); rec.Code != http.StatusOK {
				t.Fatalf("/me = %d: %s", rec.Code, rec.Body)
			}
			ag := call[apitypes.Agent](e, userSecret, http.MethodGet, "/agents/"+e.agent.ID.String(), nil, http.StatusOK)
			if len(ag.Sessions) != 1 || ag.Sessions[0].Name != tt.want {
				t.Errorf("sessions = %+v, want one named %q", ag.Sessions, tt.want)
			}
		})
	}
}
