package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func newTestClient(t *testing.T, h http.HandlerFunc, token string) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, token)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestProblemBodyParsing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
		want   APIError
		errHas string
	}{
		{
			name:   "version conflict keeps hint and current version",
			status: 409,
			body:   `{"title":"Conflict","status":409,"detail":"card changed","hint":"run lorvik show 42","current_version":7}`,
			want:   APIError{Status: 409, Title: "Conflict", Detail: "card changed", Hint: "run lorvik show 42", CurrentVersion: 7},
			errHas: "api 409: Conflict: card changed",
		},
		{
			name:   "validation errors are kept",
			status: 422,
			body:   `{"title":"Unprocessable","status":422,"errors":[{"field":"title","message":"required"}]}`,
			want:   APIError{Status: 422, Title: "Unprocessable", Errors: []apitypes.ProblemField{{Field: "title", Message: "required"}}},
			errHas: "api 422: Unprocessable",
		},
		{
			name:   "non problem body falls back to raw text",
			status: 502,
			body:   "bad gateway\n",
			want:   APIError{Status: 502, Body: "bad gateway\n"},
			errHas: "api 502: bad gateway",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}, "")
			_, err := c.GetCard(t.Context(), "demo", 42)
			ae, ok := errors.AsType[*APIError](err)
			if !ok {
				t.Fatalf("err = %v, want *APIError", err)
			}
			if ae.Status != tt.want.Status || ae.Title != tt.want.Title || ae.Detail != tt.want.Detail ||
				ae.Hint != tt.want.Hint || ae.CurrentVersion != tt.want.CurrentVersion || ae.Body != tt.want.Body ||
				len(ae.Errors) != len(tt.want.Errors) {
				t.Errorf("APIError = %+v, want %+v", *ae, tt.want)
			}
			if len(ae.Errors) == 1 && ae.Errors[0] != tt.want.Errors[0] {
				t.Errorf("Errors = %+v", ae.Errors)
			}
			if !strings.Contains(ae.Error(), tt.errHas) {
				t.Errorf("Error() = %q, want it to contain %q", ae.Error(), tt.errHas)
			}
		})
	}
}

func TestUnreadHeaderParsing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  int
		unread  string
		want    int
		wantErr bool
	}{
		{"success", 200, "3", 3, false},
		{"cap plus one reads as more than the cap", 200, "101", apitypes.UnreadCap + 1, false},
		{"error responses still carry the count", 404, "5", 5, true},
		{"absent header resets to zero", 200, "", 0, false},
		{"garbage is ignored", 200, "x", 0, false},
		{"negative is clamped", 200, "-4", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				if tt.unread != "" {
					w.Header().Set(apitypes.HeaderUnread, tt.unread)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{}`))
			}, "")
			c.last = LastUpdates{Unread: 99}
			_, err := c.Me(t.Context())
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got := c.LastUpdates().Unread; got != tt.want {
				t.Errorf("Unread = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCursorHeaderSentOnlyWhenSet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		cursor string
		want   string
	}{
		{"stored cursor is sent", "106666050296328877", "106666050296328877"},
		{"zero means from the beginning and is sent", "0", "0"},
		{"empty is omitted", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Values(apitypes.HeaderCursor)
				_, _ = w.Write([]byte(`{}`))
			}, "")
			c.Cursor = tt.cursor
			if _, err := c.Me(t.Context()); err != nil {
				t.Fatal(err)
			}
			if tt.want == "" {
				if len(got) != 0 {
					t.Errorf("header = %v, want none", got)
				}
				return
			}
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("header = %v, want %q", got, tt.want)
			}
		})
	}
}

func TestRotatedTokenIsSurfacedAndAdopted(t *testing.T) {
	t.Parallel()
	var auth []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		if len(auth) == 1 {
			w.Header().Set(apitypes.HeaderToken, "lvk_u_new")
		}
		_, _ = w.Write([]byte(`{}`))
	}, "lvk_u_old")
	var seen []string
	c.OnToken = func(s string) { seen = append(seen, s) }
	for range 2 {
		if _, err := c.Me(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 1 || seen[0] != "lvk_u_new" {
		t.Errorf("OnToken calls = %v, want one with the new secret", seen)
	}
	if auth[0] != "Bearer lvk_u_old" || auth[1] != "Bearer lvk_u_new" {
		t.Errorf("Authorization = %v, want the old secret then the new one", auth)
	}
	if c.Token() != "lvk_u_new" {
		t.Errorf("Token() = %q", c.Token())
	}
}

func TestRotatedTokenOnErrorResponseIsStillSurfaced(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(apitypes.HeaderToken, "lvk_u_new")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"title":"Conflict","status":409}`))
	}, "lvk_u_old")
	var seen string
	c.OnToken = func(s string) { seen = s }
	if _, err := c.Me(t.Context()); err == nil {
		t.Fatal("want an error")
	}
	if seen != "lvk_u_new" {
		t.Errorf("seen = %q, a rotated secret must not be lost on a failed call", seen)
	}
}

func TestBearerHeader(t *testing.T) {
	t.Parallel()
	tests := []struct {
		token, want string
	}{
		{"lvk_secret", "Bearer lvk_secret"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run("token="+tt.token, func(t *testing.T) {
			t.Parallel()
			var got string
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("Authorization")
				_, _ = w.Write([]byte(`{}`))
			}, tt.token)
			if _, err := c.Me(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Authorization = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestListParamsEncoding(t *testing.T) {
	t.Parallel()
	parent := int64(3)
	tests := []struct {
		name string
		call func(c *Client) error
		want string
	}{
		{"cards", func(c *Client) error {
			_, err := c.ListCards(t.Context(), "demo", apitypes.ListCardsParams{
				Status: []string{"ready", "waiting"}, Mine: true, Open: true, Parent: &parent, Limit: 5, Cursor: "abc_-", Query: "a b", Kind: "task",
			})
			return err
		}, "/api/v1/projects/demo/cards?cursor=abc_-&kind=task&limit=5&mine=true&open=true&parent=3&q=a+b&status=ready%2Cwaiting"},
		{"cards with every filter", func(c *Client) error {
			_, err := c.ListCards(t.Context(), "a/b", apitypes.ListCardsParams{
				Status: []string{"ready", "waiting"}, Kind: "task", Parent: new(int64(0)), Mine: true, Open: true, Query: "a & b", Limit: 25, Cursor: "a/b+",
				CreatedAfter: "2026-10-01", CreatedBefore: "2026-10-02", UpdatedAfter: "2026-10-03", UpdatedBefore: "2026-10-04",
				ClosedAfter: "2026-10-05", ClosedBefore: "2026-10-06", Assignee: "me", Priority: []string{"high", "urgent"}, Sort: "updated", Desc: true,
			})
			return err
		}, "/api/v1/projects/a%2Fb/cards?assignee=me&closed_after=2026-10-05&closed_before=2026-10-06&created_after=2026-10-01&created_before=2026-10-02&cursor=a%2Fb%2B&desc=true&kind=task&limit=25&mine=true&open=true&parent=0&priority=high%2Curgent&q=a+%26+b&sort=updated&status=ready%2Cwaiting&updated_after=2026-10-03&updated_before=2026-10-04"},
		{"all cards with every filter", func(c *Client) error {
			_, err := c.ListAllCards(t.Context(), apitypes.ListAllCardsParams{
				Project: "demo", Status: []string{"ready", "waiting"}, Kind: "task", Parent: new(int64(0)), Mine: true, Open: true, Query: "a & b", Limit: 25, Cursor: "a/b+",
				CreatedAfter: "2026-10-01", CreatedBefore: "2026-10-02", UpdatedAfter: "2026-10-03", UpdatedBefore: "2026-10-04",
				ClosedAfter: "2026-10-05", ClosedBefore: "2026-10-06", Assignee: "me", Priority: []string{"high", "urgent"},
			})
			return err
		}, "/api/v1/cards?assignee=me&closed_after=2026-10-05&closed_before=2026-10-06&created_after=2026-10-01&created_before=2026-10-02&cursor=a%2Fb%2B&kind=task&limit=25&mine=true&open=true&parent=0&priority=high%2Curgent&project=demo&q=a+%26+b&status=ready%2Cwaiting&updated_after=2026-10-03&updated_before=2026-10-04"},
		{"all cards omit absent and whitespace filters", func(c *Client) error {
			_, err := c.ListAllCards(t.Context(), apitypes.ListAllCardsParams{Project: " ", Query: "\t", Assignee: " ", CreatedAfter: " "})
			return err
		}, "/api/v1/cards"},
		{"cards omit whitespace filters", func(c *Client) error {
			_, err := c.ListCards(t.Context(), "demo", apitypes.ListCardsParams{Query: " ", Assignee: "\t", Sort: " ", Cursor: " "})
			return err
		}, "/api/v1/projects/demo/cards"},
		{"notes with every filter", func(c *Client) error {
			_, err := c.ListNotes(t.Context(), apitypes.ListNotesParams{
				Project: "demo", Archived: true, Query: "a & b", Limit: 25, Cursor: "a/b+", Author: "55",
				CreatedAfter: "2026-10-01", CreatedBefore: "2026-10-02", UpdatedAfter: "2026-10-03", UpdatedBefore: "2026-10-04",
			})
			return err
		}, "/api/v1/notes?archived=true&author=55&created_after=2026-10-01&created_before=2026-10-02&cursor=a%2Fb%2B&limit=25&project=demo&q=a+%26+b&updated_after=2026-10-03&updated_before=2026-10-04"},
		{"cards without params", func(c *Client) error {
			_, err := c.ListCards(t.Context(), "demo", apitypes.ListCardsParams{})
			return err
		}, "/api/v1/projects/demo/cards"},
		{"notes archived", func(c *Client) error {
			_, err := c.ListNotes(t.Context(), apitypes.ListNotesParams{Project: "demo", Archived: true, Query: "x", Limit: 20, Cursor: "n"})
			return err
		}, "/api/v1/notes?archived=true&cursor=n&limit=20&project=demo&q=x"},
		{"events before", func(c *Client) error {
			_, err := c.Events(t.Context(), EventsParams{Before: "9223372036854775807", Limit: 1})
			return err
		}, "/api/v1/events?before=9223372036854775807&limit=1"},
		{"inbox", func(c *Client) error {
			_, err := c.Inbox(t.Context(), InboxParams{Project: "demo", Cursor: "0", Limit: 20})
			return err
		}, "/api/v1/inbox?cursor=0&limit=20&project=demo"},
		{"search", func(c *Client) error {
			_, err := c.Search(t.Context(), "session expiry", "", "", 10)
			return err
		}, "/api/v1/search?limit=10&q=session+expiry"},
		{"search next page", func(c *Client) error {
			_, err := c.Search(t.Context(), "pgx", "demo", "tok", 10)
			return err
		}, "/api/v1/search?cursor=tok&limit=10&project=demo&q=pgx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got string
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.RequestURI()
				_, _ = w.Write([]byte(`{"items":[]}`))
			}, "")
			if err := tt.call(c); err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("request = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestActorsBatchesIdsAndMergesChunks(t *testing.T) {
	t.Parallel()
	var queries []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query().Get("id"))
		_ = json.NewEncoder(w).Encode(apitypes.Actors{
			Users:  []apitypes.User{{ID: "1", Login: "ana"}},
			Agents: []apitypes.Agent{{ID: "2", Name: "bot"}},
		})
	}, "")
	got, err := c.Actors(t.Context(), []string{"1", "2", "3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0] != "1,2,3" {
		t.Errorf("queries = %q, want one comma separated id parameter", queries)
	}
	if len(got.Users) != 1 || len(got.Agents) != 1 {
		t.Errorf("actors = %+v", got)
	}

	queries = nil
	ids := make([]string, 0, actorsChunk+1)
	for i := range actorsChunk + 1 {
		ids = append(ids, strconv.Itoa(1000+i))
	}
	got, err = c.Actors(t.Context(), ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 || len(got.Users) != 2 {
		t.Errorf("queries = %d, users = %d, want 2 chunks merged", len(queries), len(got.Users))
	}

	queries = nil
	if _, err := c.Actors(t.Context(), nil); err != nil || len(queries) != 0 {
		t.Errorf("empty id list made %d requests (err %v), want none", len(queries), err)
	}
}

func TestRoutesAndMethods(t *testing.T) {
	t.Parallel()
	var method, path string
	var body string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.RequestURI()
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`null`))
	}, "t")
	ctx := t.Context()
	user := apitypes.Actor{Kind: "user", ID: "77"}
	tests := []struct {
		name       string
		call       func() error
		wantMethod string
		wantPath   string
		wantBody   string
	}{
		{"unlink cards", func() error { return c.UnlinkCards(ctx, "demo", 4, "blocks", 9, "") }, "DELETE", "/api/v1/projects/demo/cards/4/links/blocks/9", ""},
		{"unlink cards across projects", func() error { return c.UnlinkCards(ctx, "global", 4, "applies", 9, "web") }, "DELETE", "/api/v1/projects/global/cards/4/links/applies/9?project=web", ""},
		{"list all cards", func() error {
			_, err := c.ListAllCards(ctx, apitypes.ListAllCardsParams{Project: "all", Kind: "decision", Status: []string{"done", "ready"}, Limit: 10})
			return err
		}, "GET", "/api/v1/cards?kind=decision&limit=10&project=all&status=done%2Cready", ""},
		{"list unfolded", func() error { _, err := c.ListUnfolded(ctx, "demo", 5, "tok"); return err }, "GET", "/api/v1/projects/demo/unfolded?cursor=tok&limit=5", ""},
		{"fold card", func() error {
			_, err := c.FoldCard(ctx, "demo", 31, apitypes.FoldCard{Version: 3, None: true})
			return err
		}, "POST", "/api/v1/projects/demo/cards/31/fold", `{"version":3,"none":true}`},
		{"revoke token", func() error { return c.RevokeToken(ctx, "abc") }, "DELETE", "/api/v1/tokens/abc", ""},
		{"revoke agent escapes id", func() error { return c.RevokeAgent(ctx, "a/b") }, "DELETE", "/api/v1/agents/a%2Fb", ""},
		{"approve agent", func() error { _, err := c.ApproveAgent(ctx, "5"); return err }, "POST", "/api/v1/agents/5/approve", ""},
		{"bind agent", func() error {
			_, err := c.BindAgent(ctx, "5", "web", apitypes.PutMembership{Role: "member"})
			return err
		}, "PUT", "/api/v1/agents/5/projects/web", `{"role":"member"}`},
		{"unbind agent", func() error { return c.UnbindAgent(ctx, "5", "web") }, "DELETE", "/api/v1/agents/5/projects/web", ""},
		{"list agents all", func() error { _, err := c.ListAgents(ctx, true, PageParams{Limit: 5, Cursor: "tok"}); return err }, "GET", "/api/v1/agents?all=true&cursor=tok&limit=5", ""},
		{"put member", func() error {
			_, err := c.PutMember(ctx, user, apitypes.PutMembership{Role: "admin"})
			return err
		}, "PUT", "/api/v1/members/user/77", `{"role":"admin"}`},
		{"remove member", func() error { return c.RemoveMember(ctx, user) }, "DELETE", "/api/v1/members/user/77", ""},
		{"put project member", func() error {
			_, err := c.PutProjectMember(ctx, "web", user, apitypes.PutMembership{Role: "viewer"})
			return err
		}, "PUT", "/api/v1/projects/web/members/user/77", `{"role":"viewer"}`},
		{"remove project member", func() error { return c.RemoveProjectMember(ctx, "web", user) }, "DELETE", "/api/v1/projects/web/members/user/77", ""},
		{"login", func() error {
			_, err := c.Login(ctx, apitypes.Login{Login: "ana", Password: "pw"})
			return err
		}, "POST", "/api/v1/login", `{"login":"ana","password":"pw"}`},
		{"note by slug", func() error { _, err := c.GetNote(ctx, "run book", "web"); return err }, "GET", "/api/v1/notes/run%20book?project=web", ""},
		{"note by id has no project", func() error { _, err := c.GetNote(ctx, "106666050296328877", ""); return err }, "GET", "/api/v1/notes/106666050296328877", ""},
		{"archive note", func() error {
			s := "archived"
			_, err := c.UpdateNote(ctx, "arch", "web", apitypes.UpdateNote{Version: 3, Status: &s})
			return err
		}, "PATCH", "/api/v1/notes/arch?project=web", `{"version":3,"status":"archived"}`},
		{"note revisions", func() error { _, err := c.ListNoteRevisions(ctx, "arch", "", HistoryParams{}); return err }, "GET", "/api/v1/notes/arch/revisions", ""},
		{"note revision", func() error { _, err := c.GetNoteRevision(ctx, "arch", "web", 2); return err }, "GET", "/api/v1/notes/arch/revisions/2?project=web", ""},
		{"create comment", func() error { _, err := c.CreateComment(ctx, "demo", 4, "done"); return err }, "POST", "/api/v1/projects/demo/cards/4/comments", `{"body":"done"}`},
		{"list comments", func() error { _, err := c.ListComments(ctx, "demo", 4, HistoryParams{}); return err }, "GET", "/api/v1/projects/demo/cards/4/comments", ""},
		{"page comments", func() error {
			_, err := c.ListComments(ctx, "demo", 4, HistoryParams{Before: "106666050296328877", Limit: 11})
			return err
		}, "GET", "/api/v1/projects/demo/cards/4/comments?before=106666050296328877&limit=11", ""},
		{"page card revisions", func() error {
			_, err := c.ListCardRevisions(ctx, "demo", 4, HistoryParams{Before: "7", Limit: 11})
			return err
		}, "GET", "/api/v1/projects/demo/cards/4/revisions?before=7&limit=11", ""},
		{"page note revisions keep the project", func() error {
			_, err := c.ListNoteRevisions(ctx, "arch", "web", HistoryParams{Before: "7", Limit: 11})
			return err
		}, "GET", "/api/v1/notes/arch/revisions?before=7&limit=11&project=web", ""},
		{"acquire lock escapes a slash", func() error {
			_, err := c.AcquireLock(ctx, "db/schema", apitypes.AcquireLock{Purpose: "migrate", TTLSeconds: 120})
			return err
		}, "PUT", "/api/v1/locks/db%2Fschema", `{"purpose":"migrate","ttl_seconds":120}`},
		{"renew lock", func() error { _, err := c.RenewLock(ctx, "gen", apitypes.RenewLock{TTLSeconds: 300}); return err }, "POST", "/api/v1/locks/gen/renew", `{"ttl_seconds":300}`},
		{"release lock", func() error { return c.ReleaseLock(ctx, "gen", false) }, "DELETE", "/api/v1/locks/gen", ""},
		{"force release lock", func() error { return c.ReleaseLock(ctx, "gen", true) }, "DELETE", "/api/v1/locks/gen?force=true", ""},
		{"list locks", func() error { _, err := c.ListLocks(ctx, PageParams{}); return err }, "GET", "/api/v1/locks", ""},
		{"update project", func() error {
			s := "archived"
			_, err := c.UpdateProject(ctx, "web", apitypes.UpdateProject{Status: &s})
			return err
		}, "PATCH", "/api/v1/projects/web", `{"status":"archived"}`},
	}
	for _, tt := range tests {
		method, path, body = "", "", ""
		if err := tt.call(); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if method != tt.wantMethod || path != tt.wantPath {
			t.Errorf("%s: %s %s, want %s %s", tt.name, method, path, tt.wantMethod, tt.wantPath)
		}
		if body != tt.wantBody {
			t.Errorf("%s: body %q, want %q", tt.name, body, tt.wantBody)
		}
	}
}

func TestStreamEventsParsesSSE(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") != "5" || r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("request = %s accept %q", r.URL.RequestURI(), r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": keepalive\n\n")
		_, _ = io.WriteString(w, "id: 6\nevent: message\ndata: {\"id\":\"6\",\"type\":\"card.created\",\"actor\":{\"kind\":\"user\",\"id\":\"1\"}}\n\n")
		_, _ = io.WriteString(w, "id: 7\ndata: {\"id\":\"7\",\"type\":\"card.claimed\"}\n\n")
	}, "")
	var got []string
	err := c.StreamEvents(t.Context(), "", "5", func(e apitypes.Event) error {
		got = append(got, e.ID+" "+e.Type)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "6 card.created,7 card.claimed" {
		t.Errorf("events = %v", got)
	}
}

func TestStreamEventsStopsWhenCallbackFails(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "data: {\"id\":\"1\"}\n\ndata: {\"id\":\"2\"}\n\n")
	}, "")
	boom := errors.New("stop")
	calls := 0
	err := c.StreamEvents(t.Context(), "", "", func(apitypes.Event) error { calls++; return boom })
	if !errors.Is(err, boom) || calls != 1 {
		t.Errorf("err = %v after %d calls, want the callback error after one", err, calls)
	}
}

func TestAnswerCardCapturesLocation(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/api/v1/projects/demo/cards/12")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":4,"status":"done"}`))
	}, "")
	res, err := c.AnswerCard(t.Context(), "demo", 4, apitypes.AnswerCard{Version: 1, Answer: "x", Decide: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.DecisionLocation != "/api/v1/projects/demo/cards/12" || res.Card.Number != 4 {
		t.Errorf("result = %+v", res)
	}
}

func TestConnectionFailureNamesURL(t *testing.T) {
	t.Parallel()
	url := closedURL(t)
	c, err := New(url, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Health(t.Context())
	ce, ok := errors.AsType[*ConnError](err)
	if !ok {
		t.Fatalf("err = %v, want *ConnError", err)
	}
	if ce.URL != url || !strings.Contains(err.Error(), url) {
		t.Errorf("ConnError = %v", ce)
	}
}

// Not parallel: it closes the idle connections of the transport every other
// user of the process shares.
func TestAClientsConnectionsAreItsOwn(t *testing.T) {
	var opened atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			opened.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := c.Health(t.Context()); err != nil {
			t.Fatal(err)
		}
		http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	}
	if got := opened.Load(); got != 1 {
		t.Errorf("connections opened = %d, want 1: closing the shared transport's idle connections must not close the client's", got)
	}
}

func TestNewRejectsBadURL(t *testing.T) {
	t.Parallel()
	for _, u := range []string{"", "localhost:8080", "/just/a/path"} {
		if _, err := New(u, ""); err == nil {
			t.Errorf("New(%q) succeeded, want error", u)
		}
	}
}

// closedURL returns the URL of a port that was just released, so connecting
// is refused at once instead of hanging.
func closedURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	return u
}

func TestEventHeadReturnsThePositionNotTheID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		items string
		want  string
	}{
		{"newest event", `[{"id":"5","position":"00000000000000000200-0000000000000000005"}]`, "00000000000000000200-0000000000000000005"},
		{"empty log", `[]`, "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"items":`+tt.items+`}`)
			}, "")
			got, err := c.EventHead(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("EventHead = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRequestBodiesAreNotHTMLEscaped(t *testing.T) {
	t.Parallel()
	var got string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}, "tok")
	if _, err := c.CreateCard(t.Context(), "lorvik", apitypes.CreateCard{Title: "t", Page: "<p>a & b</p>"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"page":"<p>a & b</p>"`) {
		t.Errorf("request body = %s, want the page unescaped so a 1 MiB page stays near 1 MiB", got)
	}
}

func TestRedirectToAnotherOriginIsRefusedAndKeepsTheToken(t *testing.T) {
	t.Parallel()
	var leaked atomic.Value
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(other.Close)
	var followed atomic.Bool
	// The first hop stays on the server and is followed; the second leaves it.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiPrefix+"/me" {
			http.Redirect(w, r, apiPrefix+"/moved", http.StatusFound)
			return
		}
		followed.Store(r.Header.Get("Authorization") == "Bearer secret")
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	}, "secret")

	_, err := c.Me(t.Context())
	if err == nil || !strings.Contains(err.Error(), "refused a redirect") {
		t.Errorf("Me = %v, want the redirect refused", err)
	}
	if _, down := errors.AsType[*ConnError](err); down {
		t.Errorf("Me = %v: a refused redirect counts as an unreachable server, so a write would be queued", err)
	}
	if !followed.Load() {
		t.Error("the redirect within the server was not followed with the token")
	}
	if v := leaked.Load(); v != nil {
		t.Errorf("the other server got a request with Authorization %q", v)
	}
}

func TestAStreamRedirectedToAnotherOriginIsRefusedAndKeepsTheToken(t *testing.T) {
	t.Parallel()
	var leaked atomic.Value
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	t.Cleanup(other.Close)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	}, "secret")

	err := c.StreamEvents(t.Context(), "", "", func(apitypes.Event) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "refused a redirect") {
		t.Errorf("StreamEvents = %v, want the redirect refused", err)
	}
	if v := leaked.Load(); v != nil {
		t.Errorf("the other server got a request with Authorization %q", v)
	}
}

// A reply that ends before its body is complete may follow a write the server
// did apply. It must read as a lost reply, so the write goes out again under
// its key, and never as a decode error the caller answers with a new write.
func TestAReplyCutOffMidBodyIsAConnectionError(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		conn, buf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			panic(err)
		}
		defer conn.Close()
		_, _ = buf.WriteString("HTTP/1.1 201 Created\r\nContent-Type: application/json\r\nContent-Length: 400\r\n\r\n{\"id\":\"9\",\"numb")
		_ = buf.Flush()
	}, "secret")

	_, err := c.CreateComment(t.Context(), "demo", 12, "x")
	if _, down := errors.AsType[*ConnError](err); !down {
		t.Errorf("CreateComment = %v, want a ConnError", err)
	}
}

func TestSameOrigin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a, b string
		want bool
	}{
		{"https://lorvik.example", "https://LORVIK.example:443/api", true},
		{"http://localhost", "http://localhost:80", true},
		{"http://localhost:8080", "http://localhost:9999", false},
		{"https://lorvik.example", "http://lorvik.example", false},
		{"https://lorvik.example", "https://evil.lorvik.example", false},
	}
	for _, tt := range tests {
		a, _ := url.Parse(tt.a)
		b, _ := url.Parse(tt.b)
		if got := SameOrigin(a, b); got != tt.want {
			t.Errorf("SameOrigin(%s, %s) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// The client asks for the list only when told to, keeps the last answer, and
// a response that says nothing (the event stream, an older server) or
// nothing readable does not erase it.
func TestHoldingIsAskedForAndTheLastAnswerKept(t *testing.T) {
	t.Parallel()
	held := apitypes.Holding{Cards: []apitypes.HeldCard{{Project: "demo", Number: 42, Title: "Fix login"}}}
	raw, err := json.Marshal(held)
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	answer := ""
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.Header.Get(apitypes.HeaderHolding))
		if answer != "" {
			w.Header().Set(apitypes.HeaderHolding, answer)
		}
		_, _ = w.Write([]byte(`{}`))
	}, "")
	me := func() *apitypes.Holding {
		t.Helper()
		if _, err := c.Me(t.Context()); err != nil {
			t.Fatal(err)
		}
		return c.LastUpdates().Holding
	}
	if got := me(); got != nil {
		t.Errorf("Holding = %+v before any answer, want nil", got)
	}
	c.Holding = true
	answer = base64.StdEncoding.EncodeToString(raw)
	if got := me(); got == nil || len(got.Cards) != 1 || got.Cards[0].Number != 42 {
		t.Fatalf("Holding = %+v, want the card the server named", got)
	}
	for _, answer = range []string{"", "not base64!", base64.StdEncoding.EncodeToString([]byte("not json"))} {
		if got := me(); got == nil || len(got.Cards) != 1 {
			t.Errorf("after the answer %q Holding = %+v, want the last one kept", answer, got)
		}
	}
	answer = base64.StdEncoding.EncodeToString([]byte(`{"cards":[]}`))
	if got := me(); got == nil || len(got.Cards) != 0 {
		t.Errorf("Holding = %+v after an empty list, want it replaced by the empty one", got)
	}
	if want := []string{"", "1", "1", "1", "1", "1"}; strings.Join(asked, ",") != strings.Join(want, ",") {
		t.Errorf("%s sent = %q, want %q", apitypes.HeaderHolding, asked, want)
	}
}

func TestWithRequestKeySendsIdempotencyKeyOnlyWhenSet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ctx  func(t *testing.T) context.Context
		want string
	}{
		{"key in the context", func(t *testing.T) context.Context { return WithRequestKey(t.Context(), "k-1") }, "k-1"},
		{"no key", func(t *testing.T) context.Context { return t.Context() }, ""},
		{"empty key", func(t *testing.T) context.Context { return WithRequestKey(t.Context(), "") }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got atomic.Value
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				got.Store(r.Header.Get(apitypes.HeaderIdempotencyKey))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}, "tok")
			if _, err := c.CreateComment(tt.ctx(t), "demo", 1, "hi"); err != nil {
				t.Fatal(err)
			}
			if got.Load() != tt.want {
				t.Errorf("Idempotency-Key = %q, want %q", got.Load(), tt.want)
			}
		})
	}
}
