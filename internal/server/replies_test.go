package server

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/domain"
)

const repliesPath = "/projects/lorvik/replies"

func replies(e *testEnv, secret string) []apitypes.Reply {
	e.t.Helper()
	return call[apitypes.ReplyList](e, secret, http.MethodGet, repliesPath, nil, http.StatusOK).Items
}

func comment(e *testEnv, secret string, n int64, body string) apitypes.Comment {
	e.t.Helper()
	return call[apitypes.Comment](e, secret, http.MethodPost, cardPathOf(n, "/comments"), apitypes.CreateComment{Body: body}, http.StatusCreated)
}

func TestRepliesFollowWhoWroteLast(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	q := e.newCard(userSecret, apitypes.CreateCard{Title: "which database?", Kind: "question", Choices: []string{"a", "b"}})
	if got := replies(e, userSecret); len(got) != 0 {
		t.Fatalf("replies before anyone answers = %+v, want none", got)
	}

	answer := comment(e, agentSecret, q.Number, "use postgres")
	got := replies(e, userSecret)
	if len(got) != 1 || got[0].Card.Number != q.Number || got[0].Unseen != 1 || got[0].Comment.ID != answer.ID || got[0].Comment.Body != "use postgres" {
		t.Fatalf("user replies = %+v, want the agent's comment on #%d, unseen 1", got, q.Number)
	}
	if got := replies(e, agentSecret); len(got) != 0 {
		t.Errorf("agent replies = %+v, want none: the only comment is its own and it never wrote or opened the card", got)
	}

	comment(e, userSecret, q.Number, "thanks")
	if got := replies(e, userSecret); len(got) != 0 {
		t.Errorf("user replies after writing back = %+v, want none", got)
	}
	got = replies(e, agentSecret)
	if len(got) != 1 || got[0].Comment.Body != "thanks" || got[0].Unseen != 1 {
		t.Fatalf("agent replies = %+v, want the user's comment, unseen 1", got)
	}

	call[struct{}](e, agentSecret, http.MethodPost, cardPathOf(q.Number, "/seen"), nil, http.StatusNoContent)
	if got := replies(e, agentSecret); len(got) != 0 {
		t.Errorf("agent replies after seen = %+v, want none", got)
	}
}

func TestRepliesCountUnseenAndShowNewest(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.newCard(userSecret, apitypes.CreateCard{Title: "thread"})
	comment(e, agentSecret, c.Number, "first")
	comment(e, agent2Secret, c.Number, "second")
	last := comment(e, agentSecret, c.Number, "third")
	got := replies(e, userSecret)
	if len(got) != 1 || got[0].Unseen != 3 || got[0].Comment.ID != last.ID {
		t.Fatalf("replies = %+v, want one entry for the newest comment with unseen 3", got)
	}
}

func TestRepliesIgnoreCardsTheCallerNeverTouched(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := e.newCard(agentSecret, apitypes.CreateCard{Title: "agent's own"})
	comment(e, agent2Secret, c.Number, "hello")
	if got := replies(e, userSecret); len(got) != 0 {
		t.Errorf("user replies = %+v, want none for a card the user never wrote on or opened", got)
	}
	call[struct{}](e, userSecret, http.MethodPost, cardPathOf(c.Number, "/seen"), nil, http.StatusNoContent)
	comment(e, agent2Secret, c.Number, "again")
	if got := replies(e, userSecret); len(got) != 1 || got[0].Unseen != 1 || got[0].Comment.Body != "again" {
		t.Errorf("user replies after opening = %+v, want only the comment made after opening", got)
	}
}

func TestRepliesRespectProjectAccess(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	stranger := e.seedUser("stranger", 0)
	e.seedToken("lvk_u_stranger-sec", stranger, domain.ScopeWrite, t0.Add(time.Hour))
	e.problem("lvk_u_stranger-sec", http.MethodGet, repliesPath, nil, http.StatusForbidden)
	e.problem("lvk_u_stranger-sec", http.MethodPost, cardPathOf(1, "/seen"), nil, http.StatusForbidden)
}

func TestRepliesLimit(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, title := range []string{"one", "two", "three"} {
		c := e.newCard(userSecret, apitypes.CreateCard{Title: title})
		comment(e, agentSecret, c.Number, "re: "+title)
	}
	got := call[apitypes.ReplyList](e, userSecret, http.MethodGet, repliesPath+"?limit=2", nil, http.StatusOK).Items
	if len(got) != 2 || got[0].Card.Title != "three" || got[1].Card.Title != "two" {
		t.Errorf("limit=2 replies = %+v, want the two newest cards, newest first", got)
	}
	e.problem(userSecret, http.MethodGet, repliesPath+"?limit=101", nil, http.StatusUnprocessableEntity)
}

func TestRepliesPageWithCursor(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, title := range []string{"one", "two", "three"} {
		c := e.newCard(userSecret, apitypes.CreateCard{Title: title})
		comment(e, agentSecret, c.Number, "re: "+title)
	}
	first := call[apitypes.ReplyList](e, userSecret, http.MethodGet, repliesPath+"?limit=2", nil, http.StatusOK)
	if first.Remaining != 1 || first.Next == "" || first.Prev != "" {
		t.Fatalf("first page = remaining %d next %q prev %q; want 1 left, a next and no prev", first.Remaining, first.Next, first.Prev)
	}
	rest := call[apitypes.ReplyList](e, userSecret, http.MethodGet, repliesPath+"?limit=2&cursor="+first.Next, nil, http.StatusOK)
	if len(rest.Items) != 1 || rest.Items[0].Card.Title != "one" || rest.Remaining != 0 {
		t.Errorf("second page = %+v, want the oldest card and nothing left", rest)
	}
	e.problem(userSecret, http.MethodGet, repliesPath+"?cursor=not-a-cursor", nil, http.StatusBadRequest)
	search := call[apitypes.SearchResult](e, userSecret, http.MethodGet, "/search?q=one", nil, http.StatusOK)
	e.problem(userSecret, http.MethodGet, repliesPath+"?cursor="+search.Next, nil, http.StatusBadRequest)
}

// Mark all read clears every reply in the project, past one page of them, and
// a later reply shows again.
func TestMarkRepliesSeenClearsEveryReply(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	var last apitypes.Card
	for i := range markRepliesPage + 2 {
		last = e.newCard(userSecret, apitypes.CreateCard{Title: fmt.Sprintf("card %d", i)})
		comment(e, agentSecret, last.Number, "re")
	}
	if rec := e.do(userSecret, http.MethodPost, repliesPath+"/seen", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("mark replies seen = %d: %s", rec.Code, rec.Body)
	}
	if got := call[apitypes.ReplyList](e, userSecret, http.MethodGet, repliesPath, nil, http.StatusOK); len(got.Items) != 0 || got.Remaining != 0 {
		t.Errorf("replies after mark all read = %d items, %d remaining; want none", len(got.Items), got.Remaining)
	}
	comment(e, agentSecret, last.Number, "again")
	if got := replies(e, userSecret); len(got) != 1 || got[0].Card.Number != last.Number {
		t.Errorf("replies after a new comment = %+v, want the one card", got)
	}
}
