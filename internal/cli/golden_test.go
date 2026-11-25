package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func ago(d time.Duration) time.Time { return fixedNow.Add(-d) }

var (
	claude = apitypes.Actor{Kind: "agent", ID: "106666050296328901"}
	ana    = apitypes.Actor{Kind: "user", ID: "106666050296328877"}
)

func fixtureActors() apitypes.Actors {
	return apitypes.Actors{
		Users:  []apitypes.User{{ID: ana.ID, Login: "ana", Name: "Ana Souza"}},
		Agents: []apitypes.Agent{{ID: claude.ID, Name: "claude-code"}},
	}
}

func fixtureCards() apitypes.CardList {
	return apitypes.CardList{
		Next: "eyJsIjoiY2FyZHMifQ", Prev: "eyJiIjp0cnVlfQ", Remaining: 4,
		Items: []apitypes.CardBrief{
			{ID: "5001", Number: 42, Kind: "task", Status: "working", Title: "Fix login", Priority: "high", Claim: &apitypes.Claim{Actor: claude, ExpiresAt: fixedNow.Add(20 * time.Minute)}},
			{ID: "5002", Number: 43, Kind: "task", Status: "ready", Title: "Add rate limiting", Priority: "normal"},
			{ID: "5003", Number: 17, Kind: "question", Status: "ready", Title: "Postgres or SQLite for the cache?", Priority: "normal", Assignee: &ana},
		},
	}
}

// myCard is the working fixture card with its claim held by the caller.
func myCard() apitypes.Card {
	c := fixtureCard()
	cl := *c.Claim
	cl.Mine = true
	c.Claim = &cl
	return c
}

func fixtureCard() apitypes.Card {
	return apitypes.Card{
		ID: "5001", Number: 42, Project: "demo", Kind: "task", Status: "working", Title: "Fix login", Priority: "high",
		Body:   "Users are redirected in a loop when the session has expired.",
		Parent: new(int64(12)), CreatedBy: ana, Version: 7,
		Claim:     &apitypes.Claim{Actor: claude, Session: "brisk-otter", ExpiresAt: fixedNow.Add(28 * time.Minute)},
		CreatedAt: ago(72 * time.Hour), UpdatedAt: ago(3 * time.Minute),
		Links: []apitypes.CardLink{
			{Kind: "blocks", Number: 44, Title: "Ship v0.2", Status: "backlog", Direction: "out"},
			{Kind: "blocks", Number: 40, Title: "Session store", Status: "done", Direction: "in"},
		},
		Children:  []apitypes.CardBrief{{ID: "5004", Number: 45, Kind: "task", Status: "ready", Title: "Add regression test", Priority: "normal"}},
		BlockedBy: []apitypes.CardBrief{{ID: "5003", Number: 17, Kind: "question", Status: "ready", Title: "Postgres or SQLite for the cache?", Priority: "normal"}},
		Comment:   &apitypes.Comment{ID: "8001", Card: 42, Actor: claude, CreatedAt: ago(2 * time.Hour), Body: "Cookie parsing done.\nTests for expiry left."},
		Events: []apitypes.Event{
			{ID: "9102", Position: eventPos(9102), Type: "card.claimed", Actor: claude, Subject: apitypes.Subject{Type: "card", ID: "5001", Number: 42}, Payload: json.RawMessage(`{"title":"Fix login"}`), CreatedAt: ago(3 * time.Minute)},
			{ID: "9101", Position: eventPos(9101), Type: "card.status_changed", Actor: ana, Subject: apitypes.Subject{Type: "card", ID: "5001", Number: 42}, Payload: json.RawMessage(`{"title":"Fix login","from":"backlog","to":"ready"}`), CreatedAt: ago(5 * time.Hour)},
		},
	}
}

func fixtureInbox() apitypes.Inbox {
	return apitypes.Inbox{
		Cursor: eventPos(9103), Remaining: 2, OpenQuestions: 1,
		Events: []apitypes.Event{
			{ID: "9102", Position: eventPos(9102), Type: "card.answered", Actor: ana, Subject: apitypes.Subject{Type: "card", ID: "5003", Number: 17}, Payload: json.RawMessage(`{"title":"Postgres or SQLite for the cache?","answer":"postgres","decided":false}`), CreatedAt: ago(4 * time.Minute), Mine: true},
			{ID: "9103", Position: eventPos(9103), Type: "card.status_changed", Actor: ana, Subject: apitypes.Subject{Type: "card", ID: "5002", Number: 43}, Payload: json.RawMessage(`{"title":"Add rate limiting","from":"backlog","to":"ready"}`), CreatedAt: ago(40 * time.Second)},
		},
		Claims:              []apitypes.CardBrief{{ID: "5001", Number: 42, Kind: "task", Status: "working", Title: "Fix login", Priority: "high", Claim: &apitypes.Claim{Actor: claude, Session: "witty-lynx-3f9a", ExpiresAt: fixedNow.Add(30 * time.Minute)}}},
		ClaimsOtherSessions: []apitypes.CardBrief{{ID: "5004", Number: 44, Kind: "task", Status: "working", Title: "Index the events", Priority: "normal", Claim: &apitypes.Claim{Actor: claude, Session: "quiet-comet-c38c", ExpiresAt: fixedNow.Add(20 * time.Minute)}}},
		Locks:               []apitypes.Lock{},
	}
}

func fixtureNote() apitypes.Note {
	return apitypes.Note{
		ID: "6001", Project: new("demo"), Slug: "architecture", Title: "Architecture", Status: "active", Version: 4,
		Body:      "The server is a single binary.\nSee [[card:7]].",
		CreatedBy: ana, UpdatedBy: claude, CreatedAt: ago(30 * 24 * time.Hour), UpdatedAt: ago(2 * time.Hour),
		Backlinks: []apitypes.Backlink{
			{Type: "card", ID: "5001", Number: 42, Title: "Fix login"},
			{Type: "note", ID: "6002", Slug: "runbook", Title: "Runbook"},
		},
	}
}

func fixtureRoutes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/projects/demo/cards":      reply(200, fixtureCards()),
		"GET /api/v1/projects/demo/cards/next": reply(200, fixtureCard()),
		"GET /api/v1/projects/demo/cards/42":   reply(200, fixtureCard()),
		"GET /api/v1/inbox":                    reply(200, fixtureInbox()),
		"GET /api/v1/notes/architecture":       reply(200, fixtureNote()),
		"GET /api/v1/actors":                   reply(200, fixtureActors()),
	}
}

func TestHelpGolden(t *testing.T) {
	t.Parallel()
	tests := []struct {
		file string
		args []string
	}{
		{"help_root.golden", []string{"--help"}},
		{"help_config.golden", []string{"help", "config"}},
		{"help_scripting.golden", []string{"help", "scripting"}},
		{"help_ls.golden", []string{"ls", "--help"}},
		{"help_show.golden", []string{"show", "--help"}},
		{"help_history.golden", []string{"history", "--help"}},
		{"help_add.golden", []string{"add", "--help"}},
		{"help_sync.golden", []string{"sync", "--help"}},
		{"help_drop.golden", []string{"drop", "--help"}},
		{"help_done.golden", []string{"done", "--help"}},
		{"help_start.golden", []string{"start", "--help"}},
		{"help_decide.golden", []string{"decide", "--help"}},
		{"help_decisions.golden", []string{"decisions", "--help"}},
		{"help_decisions_ls.golden", []string{"decisions", "ls", "--help"}},
		{"help_fold.golden", []string{"fold", "--help"}},
		{"help_inbox.golden", []string{"inbox", "--help"}},
		{"help_wait.golden", []string{"wait", "--help"}},
		{"help_lock.golden", []string{"lock", "--help"}},
		{"help_lock_run.golden", []string{"lock", "run", "--help"}},
		{"help_unlock.golden", []string{"unlock", "--help"}},
		{"help_locks.golden", []string{"locks", "--help"}},
		{"help_status.golden", []string{"status", "--help"}},
		{"help_who.golden", []string{"who", "--help"}},
		{"help_agent.golden", []string{"agent", "--help"}},
		{"help_agent_register.golden", []string{"agent", "register", "--help"}},
		{"help_token_create.golden", []string{"token", "create", "--help"}},
		{"help_login.golden", []string{"login", "--help"}},
		{"help_project.golden", []string{"project", "--help"}},
		{"help_project_create.golden", []string{"project", "create", "--help"}},
		{"help_member.golden", []string{"member", "--help"}},
		{"help_org_set.golden", []string{"org", "set", "--help"}},
		{"help_note.golden", []string{"note", "--help"}},
		{"help_note_show.golden", []string{"note", "show", "--help"}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, nil)
			if err := h.run(t, tt.args...); err != nil {
				t.Fatal(err)
			}
			golden(t, tt.file, h.out.String())
		})
	}
}

// A topic is reachable as 'lorvik help <topic>' and as 'lorvik <topic>'.
func TestHelpTopicsPrintFromBothSpellings(t *testing.T) {
	t.Parallel()
	for _, topic := range []string{"config", "scripting"} {
		t.Run(topic, func(t *testing.T) {
			t.Parallel()
			viaHelp, direct := newHarness(t, nil), newHarness(t, nil)
			if err := viaHelp.run(t, "help", topic); err != nil {
				t.Fatal(err)
			}
			if err := direct.run(t, topic); err != nil {
				t.Fatal(err)
			}
			if viaHelp.out.String() == "" || viaHelp.out.String() != direct.out.String() {
				t.Errorf("help %s and %s differ:\n--- help ---\n%s\n--- direct ---\n%s", topic, topic, viaHelp.out, direct.out)
			}
		})
	}
}

func TestRootHelpHeadingsAreBoldOnlyWithColour(t *testing.T) {
	t.Parallel()
	headings := []string{"Get started", "The loop", "Usage", "Find work", "Help topics", "Flags"}
	tests := []struct {
		name  string
		color bool
	}{
		{"colour on", true},
		{"colour off", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, nil)
			h.streams.SetTTY(false, tt.color)
			if err := h.run(t, "--help"); err != nil {
				t.Fatal(err)
			}
			got := h.out.String()
			if !tt.color {
				if strings.Contains(got, "\x1b") {
					t.Errorf("escape bytes in plain help:\n%q", got)
				}
				return
			}
			for _, name := range headings {
				if !strings.Contains(got, "\x1b[1m"+name+"\x1b[0m\n") {
					t.Errorf("heading %q is not bold in:\n%s", name, got)
				}
			}
		})
	}
}

func TestOutputGolden(t *testing.T) {
	t.Parallel()
	srv := serve(t, fixtureRoutes())
	tests := []struct {
		name string
		args []string
	}{
		{"ls", []string{"ls"}},
		{"show", []string{"show", "42"}},
		{"next", []string{"next"}},
		{"inbox", []string{"inbox"}},
		{"note_show", []string{"note", "show", "architecture"}},
	}
	for _, tt := range tests {
		for _, mode := range []string{"human", "json"} {
			t.Run(tt.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				h := newHarness(t, srv)
				args := append([]string{"-p", "demo"}, tt.args...)
				if mode == "json" {
					args = append(args, "--json")
				}
				if err := h.run(t, args...); err != nil {
					t.Fatal(err)
				}
				if h.errOut.String() != "" {
					t.Errorf("stderr = %q, want empty", h.errOut.String())
				}
				golden(t, tt.name+"."+mode+".golden", h.out.String())
			})
		}
	}
}

func TestHumanOutputStaysCompact(t *testing.T) {
	t.Parallel()
	srv := serve(t, fixtureRoutes())
	h := newHarness(t, srv)
	if err := h.run(t, "-p", "demo", "ls"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(h.out.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("ls printed %d lines, want 3 cards + the paging line:\n%s", len(lines), h.out.String())
	}
	if lines[0] != "#42  working  high  Fix login  @claude-code (agent)" {
		t.Errorf("card line = %q", lines[0])
	}
	if want := "remaining 4, next: lorvik ls --project demo --cursor eyJsIjoiY2FyZHMifQ"; lines[3] != want {
		t.Errorf("trailer = %q, want %q", lines[3], want)
	}
	if strings.Contains(h.out.String(), "\x1b[") {
		t.Error("colour escape codes written to a non-TTY")
	}
}

// events covers every event type the server writes, with the payload keys
// the services emit, plus one type the CLI has never heard of.
func eventFixtures() []apitypes.Event {
	card := apitypes.Subject{Type: "card", ID: "5001", Number: 42}
	note := apitypes.Subject{Type: "note", ID: "6001"}
	proj := apitypes.Subject{Type: "project", ID: "4001"}
	agent := apitypes.Subject{Type: "agent", ID: "3001"}
	member := apitypes.Subject{Type: "membership", ID: "2001"}
	tok := apitypes.Subject{Type: "token", ID: "1001"}
	lock := apitypes.Subject{Type: "lock", ID: "1101"}
	web := "web"
	ev := func(typ string, actor apitypes.Actor, subj apitypes.Subject, payload string) apitypes.Event {
		e := apitypes.Event{ID: "1", Position: eventPos(1), Type: typ, Actor: actor, Subject: subj}
		if payload != "" {
			e.Payload = json.RawMessage(payload)
		}
		return e
	}
	bob := `{"kind":"user","id":"106666050296328999"}`
	bot := `{"kind":"agent","id":"106666050296328901"}`
	projEv := ev("membership.granted", ana, member, `{"actor":`+bot+`,"role":"reviewer","project":"web"}`)
	projEv.Project = &web
	endingEv := ev("card.claim_ending", claude, card, `{"title":"Fix login","expires_at":"2026-10-18T12:30:00Z","session":"quiet-heron-1a2b"}`)
	endingEv.CreatedAt = time.Date(2026, 10, 18, 12, 25, 0, 0, time.UTC)
	return []apitypes.Event{
		ev("card.created", ana, card, `{"title":"Fix login","kind":"task","status":"backlog","priority":"high"}`),
		ev("card.created", claude, apitypes.Subject{Type: "card", ID: "5003", Number: 17}, `{"title":"Which db?","kind":"question","status":"ready","priority":"normal"}`),
		ev("card.updated", ana, card, `{"title":"Fix login","fields":["title","priority"]}`),
		ev("card.status_changed", claude, card, `{"title":"Fix login","from":"working","to":"done"}`),
		ev("card.status_changed", ana, card, `{"title":"Fix login","from":"ready","to":"dropped","drop_reason":"wontfix"}`),
		ev("card.status_changed", claude, card, `{"title":"Fix login","from":"ready","to":"waiting","blocked_by":17,"blocker_title":"Which db?"}`),
		ev("card.status_changed", ana, card, `{"title":"Fix login","from":"waiting","to":"ready","unblocked_by":17,"blocker_title":"Which db?"}`),
		ev("card.claimed", claude, card, `{"title":"Fix login","expires_at":"2026-10-18T12:30:00Z"}`),
		ev("card.claimed", ana, card, `{"title":"Fix login","taken_from":`+bot+`}`),
		ev("card.released", claude, card, `{"title":"Fix login","status":"ready"}`),
		ev("card.released", ana, card, `{"title":"Fix login","status":"ready","released_from":`+bot+`}`),
		ev("card.claimed", claude, card, `{"title":"Fix login","taken_from":`+bot+`,"taken_from_session":"quiet-heron-1a2b"}`),
		ev("card.released", ana, card, `{"title":"Fix login","status":"ready","released_from":`+bot+`,"released_from_session":"quiet-heron-1a2b"}`),
		endingEv,
		ev("card.claim_ending", ana, card, `{"title":"Fix login"}`),
		ev("card.claim_expired", claude, card, `{"title":"Fix login","status":"ready"}`),
		ev("card.claim_expired", claude, card, `{"title":"Fix login","status":"ready","session":"quiet-heron-1a2b"}`),
		ev("card.answered", ana, apitypes.Subject{Type: "card", ID: "5003", Number: 17}, `{"title":"Which db?","answer":"postgres","decided":true}`),
		ev("card.answered", claude, apitypes.Subject{Type: "card", ID: "5003", Number: 17}, `{"title":"Which db?","answer":"postgres","relayed":true}`),
		ev("card.answered", ana, apitypes.Subject{Type: "card", ID: "5003", Number: 17}, `{"title":"Which db?","answer":"postgres","confirmed":true}`),
		ev("card.answered", ana, apitypes.Subject{Type: "card", ID: "5003", Number: 17}, `{"title":"Which db?","answer":"sqlite","confirmed":true,"previous":"postgres"}`),
		ev("card.answered", claude, apitypes.Subject{Type: "card", ID: "5003", Number: 17}, `{"title":"Which db?","answer":"postgres","decided":false,"for":{"kind":"user","id":"106666050296328877"},"reason":"decision #7"}`),
		ev("card.kept", ana, apitypes.Subject{Type: "card", ID: "5003", Number: 17}, `{"title":"Which db?","kind":"question"}`),
		ev("card.status_changed", claude, apitypes.Subject{Type: "card", ID: "5006", Number: 9}, `{"title":"Use pgx","from":"ready","to":"done","kind":"decision","note":"board.md 2.3","for":{"kind":"user","id":"106666050296328877"}}`),
		ev("card.status_changed", claude, apitypes.Subject{Type: "card", ID: "5006", Number: 9}, `{"title":"Use pgx","from":"ready","to":"dropped","kind":"decision","drop_reason":"rejected","note":"board.md 2.3","for":{"kind":"user","id":"106666050296328877"}}`),
		ev("card.kept", ana, apitypes.Subject{Type: "card", ID: "5006", Number: 9}, `{"title":"Use pgx","kind":"decision"}`),
		ev("card.created", claude, apitypes.Subject{Type: "card", ID: "5003", Number: 17}, `{"title":"Which db?","kind":"question","status":"ready","priority":"normal","for_person":true}`),
		ev("card.for_person", claude, apitypes.Subject{Type: "card", ID: "5003", Number: 17}, `{"title":"Which db?","set":true}`),
		ev("card.for_person", ana, apitypes.Subject{Type: "card", ID: "5003", Number: 17}, `{"title":"Which db?","set":false}`),
		ev("card.linked", ana, card, `{"title":"Fix login","kind":"blocks","to":44,"to_title":"Ship v0.2"}`),
		ev("card.unlinked", ana, card, `{"title":"Fix login","kind":"relates","to":45,"to_title":"Regression test"}`),
		ev("card.comment", claude, card, `{"title":"Fix login","comment":"8001"}`),
		ev("card.status_changed", ana, card, `{"title":"Fix login","from":"done","to":"dropped","drop_reason":"superseded","superseded_by":9}`),
		ev("card.status_changed", claude, apitypes.Subject{Type: "card", ID: "5005", Number: 12}, `{"title":"Auth rework","from":"ready","to":"working","by_child":42}`),
		ev("card.folded", ana, apitypes.Subject{Type: "card", ID: "5005", Number: 12}, `{"title":"Auth rework","into":"docs/requirements/auth.md"}`),
		ev("card.folded", ana, card, `{"title":"Fix login","into":""}`),
		ev("card.created", ana, apitypes.Subject{Type: "card", ID: "5006", Number: 20}, `{"title":"Webhook delivery","kind":"story","status":"ready","priority":"normal"}`),
		ev("card.created", claude, apitypes.Subject{Type: "card", ID: "5007", Number: 7}, `{"title":"Use pgx","kind":"decision","status":"ready","priority":"normal"}`),
		ev("card.created", ana, apitypes.Subject{Type: "card", ID: "5008", Number: 8}, `{"title":"Use pgx v6","kind":"decision","status":"done","priority":"normal","supersedes":7}`),
		ev("card.status_changed", ana, apitypes.Subject{Type: "card", ID: "5007", Number: 7}, `{"title":"Use pgx","kind":"decision","from":"ready","to":"done"}`),
		ev("card.status_changed", ana, apitypes.Subject{Type: "card", ID: "5007", Number: 7}, `{"title":"Use pgx","from":"ready","to":"dropped","drop_reason":"rejected","note":"not now"}`),
		ev("card.status_changed", ana, apitypes.Subject{Type: "card", ID: "5007", Number: 7}, `{"title":"Use pgx","from":"done","to":"dropped","drop_reason":"revoked"}`),
		ev("card.status_changed", ana, apitypes.Subject{Type: "card", ID: "5009", Number: 22}, `{"title":"Retry","from":"ready","to":"dropped","drop_reason":"wontfix","cascade_of":20}`),
		ev("note.created", ana, note, `{"title":"Architecture","slug":"architecture","status":"active","version":1}`),
		ev("note.updated", claude, note, `{"title":"Architecture","slug":"architecture","status":"active","version":2}`),
		ev("note.updated", ana, note, `{"title":"Architecture","slug":"architecture","status":"archived","version":3}`),
		ev("project.created", ana, proj, `{"title":"Web","slug":"web","visibility":"org"}`),
		ev("project.updated", ana, proj, `{"title":"Web","slug":"web","visibility":"private","status":"active"}`),
		ev("project.archived", ana, proj, `{"title":"Web","slug":"web","visibility":"org","status":"archived"}`),
		ev("agent.registered", ana, agent, `{"title":"claude-code","harness":"claude-code","status":"active","role":"member"}`),
		ev("agent.registered", ana, agent, `{"title":"codex","harness":"codex","status":"pending","role":"member"}`),
		ev("agent.approved", ana, agent, `{"title":"codex"}`),
		ev("agent.revoked", ana, agent, `{"title":"codex"}`),
		ev("agent.restored", ana, agent, `{"title":"codex","status":"active"}`),
		ev("agent.restored", ana, agent, `{"title":"codex","status":"pending"}`),
		ev("agent.removed", ana, agent, `{"title":"codex"}`),
		ev("agent.join_requested", claude, agent, `{"title":"claude-code","project":"web","role":"member"}`),
		ev("agent.joined", claude, agent, `{"title":"claude-code","project":"web","rights":["work"],"auto":true}`),
		ev("agent.joined", ana, agent, `{"title":"claude-code","project":"web","rights":["work","ready"]}`),
		ev("agent.joined", ana, agent, `{"title":"claude-code","project":"web","rights":[]}`),
		ev("agent.joined", ana, agent, `{"title":"claude-code","project":"web","role":"viewer"}`),
		ev("agent.session_rights", ana, agent, `{"title":"claude-code","project":"web","session":"quiet-heron-1a2b","rights":["ready"]}`),
		ev("agent.session_rights", ana, agent, `{"title":"claude-code","project":"web","session":"quiet-heron-1a2b","rights":[]}`),
		ev("agent.session_rights", ana, agent, `{"title":"claude-code","project":"web","session":"quiet-heron-1a2b","rights":["answer","ready"],"ends_at":"0001-01-01T08:00:00Z"}`),
		ev("agent.session_rights", claude, agent, `{"title":"claude-code","project":"web","session":"quiet-heron-1a2b","rights":[],"ended_at":"0001-01-01T08:00:00Z"}`),
		ev("agent.join_denied", ana, agent, `{"title":"claude-code","project":"api"}`),
		ev("agent.updated", ana, agent, `{"title":"claude-code","agent_join":"allow"}`),
		ev("agent.join_setting", ana, agent, `{"agent_join":"allow"}`),
		ev("agent.join_setting", ana, agent, `{"agent_join":"ask","project":"api"}`),
		ev("membership.granted", ana, member, `{"actor":`+bot+`,"role":"member"}`),
		ev("membership.granted", ana, member, `{"actor":`+bot+`,"role":"viewer","rights":["work","ready"]}`),
		ev("membership.changed", ana, member, `{"actor":`+bot+`,"role":"viewer","rights":["work"],"project":"web"}`),
		projEv,
		ev("membership.changed", ana, member, `{"actor":`+bob+`,"role":"admin"}`),
		ev("membership.removed", ana, member, `{"actor":`+bot+`}`),
		ev("membership.requested", claude, member, ""),
		ev("membership.invited", ana, member, `{"actor":`+bob+`,"role":"member"}`),
		ev("lock.acquired", claude, lock, `{"name":"deploy","purpose":"release v0.4","ttl":600}`),
		ev("lock.released", claude, lock, `{"name":"deploy","forced":false}`),
		ev("lock.released", ana, lock, `{"name":"deploy","forced":true,"holder":`+bot+`}`),
		ev("lock.expired", claude, lock, `{"name":"deploy","holder":`+bot+`,"purpose":"release v0.4"}`),
		ev("token.rotated", ana, tok, `{"prefix":"lvk_u_ab12cd","replaced_by":"1002"}`),
		ev("token.reuse_detected", ana, tok, `{"prefix":"lvk_u_ab12cd"}`),
		ev("identity.linked", ana, member, `{"provider":"github","login":"ana-gh"}`),
		ev("identity.unlinked", ana, member, `{"provider":"github","login":"ana-gh"}`),
		ev("identity.signin_failed", ana, member, ""),
		ev("identity.signin_refused", ana, member, ""),
		ev("user.deleted", ana, member, ""),
		ev("card.frobbed", ana, card, `{"title":"Fix login"}`),
		ev("widget.spun", claude, apitypes.Subject{Type: "widget", ID: "77"}, ""),
	}
}

func TestEventSentencesGolden(t *testing.T) {
	t.Parallel()
	names := func(a apitypes.Actor) string {
		return map[string]string{ana.ID: "ana", claude.ID: "claude-code", "106666050296328999": "bo"}[a.ID]
	}
	var b strings.Builder
	for _, e := range eventFixtures() {
		fmt.Fprintf(&b, "%-24s %s\n", e.Type, eventSentence(e, names))
	}
	golden(t, "event_sentences.golden", b.String())
}

// serverEventTypes reads the closed set of event type names from the server's
// domain registry. The CLI cannot import that package, so the test reads its
// source and follows it as it grows.
func serverEventTypes(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "domain", "event.go"))
	if err != nil {
		t.Fatal(err)
	}
	_, names, ok := strings.Cut(string(src), "[]string{")
	if !ok {
		t.Fatal("domain/event.go: no event type names found")
	}
	names, _, _ = strings.Cut(names, "}}")
	types := regexp.MustCompile(`"([a-z]+\.[a-z_]+)"`).FindAllStringSubmatch(names, -1)
	if len(types) == 0 {
		t.Fatal("domain/event.go: no event type names found")
	}
	var out []string
	for _, m := range types {
		out = append(out, m[1])
	}
	return out
}

func TestEveryServerEventTypeHasASentence(t *testing.T) {
	t.Parallel()
	covered := map[string]bool{}
	for _, e := range eventFixtures() {
		covered[e.Type] = true
	}
	for _, typ := range serverEventTypes(t) {
		if !covered[typ] {
			t.Errorf("no fixture for event type %s", typ)
		}
		e := apitypes.Event{Type: typ, Actor: ana, Subject: apitypes.Subject{Type: "card", ID: "1", Number: 1}}
		if got := eventSentence(e, nil); strings.Contains(got, typ) {
			t.Errorf("%s fell through to the generic sentence: %q", typ, got)
		}
	}
}

func TestEventActorsIncludePayloadActors(t *testing.T) {
	t.Parallel()
	evs := []apitypes.Event{
		{Type: "card.claimed", Actor: ana, Payload: json.RawMessage(`{"taken_from":{"kind":"agent","id":"106666050296328901"}}`)},
		{Type: "membership.granted", Actor: ana, Payload: json.RawMessage(`{"actor":{"kind":"user","id":"55"}}`)},
		{Type: "card.created", Actor: claude, Payload: json.RawMessage(`not json`)},
	}
	var ids []string
	for _, a := range eventsActors(evs) {
		ids = append(ids, a.ID)
	}
	want := "106666050296328877,106666050296328901,106666050296328877,55,106666050296328901"
	if strings.Join(ids, ",") != want {
		t.Errorf("actors = %v, want %s", ids, want)
	}
}
