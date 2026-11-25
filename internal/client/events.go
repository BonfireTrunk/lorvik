package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

// EventsParams are the query parameters of GET /events. After and Before are
// event positions (a bare event id is also accepted) and cannot be combined.
type EventsParams struct {
	Project string
	After   string
	Before  string
	Limit   int
}

// Events calls GET /events: events after a position, or the newest ones before a
// position, oldest first.
func (c *Client) Events(ctx context.Context, p EventsParams) (apitypes.EventList, error) {
	v := url.Values{}
	setIf(v, "project", p.Project)
	setIf(v, "after", p.After)
	setIf(v, "before", p.Before)
	setLimit(v, p.Limit, "")
	return call[apitypes.EventList](ctx, c, http.MethodGet, withQuery("/events", v), nil)
}

// maxEventID is larger than any event id; with before= it selects the newest
// events. The server still reads a bare id there.
const maxEventID = "9223372036854775807"

// EventHead returns the newest event's position, or "0" on an empty log. A chat with
// no usable cursor starts from here: replaying the whole log would report
// every old event at once.
func (c *Client) EventHead(ctx context.Context) (string, error) {
	list, err := c.Events(ctx, EventsParams{Before: maxEventID, Limit: 1})
	if err != nil {
		return "", err
	}
	if n := len(list.Items); n > 0 {
		return list.Items[n-1].Position, nil
	}
	return "0", nil
}

// InboxParams are the query parameters of GET /inbox. Cursor is the event position
// to read after; empty reads from the beginning. ChatProject is the project
// the calling chat works in: it narrows which events are marked wake and
// filters nothing out.
type InboxParams struct {
	Project     string
	ChatProject string
	Cursor      string
	Limit       int
}

// Inbox calls GET /inbox. The server keeps no read position: the caller
// stores the returned Cursor and sends it next time.
func (c *Client) Inbox(ctx context.Context, p InboxParams) (apitypes.Inbox, error) {
	v := url.Values{}
	setIf(v, "project", p.Project)
	setIf(v, "chat_project", p.ChatProject)
	setLimit(v, p.Limit, p.Cursor)
	return call[apitypes.Inbox](ctx, c, http.MethodGet, withQuery("/inbox", v), nil)
}

// StreamEvents calls GET /events/stream and invokes fn for each event until
// ctx ends, the stream closes or fn returns an error. An empty after starts
// at the current head. A clean end of stream or a cancelled ctx returns nil.
func (c *Client) StreamEvents(ctx context.Context, project, after string, fn func(apitypes.Event) error) error {
	v := url.Values{}
	setIf(v, "project", project)
	setIf(v, "after", after)
	req, err := c.newRequest(ctx, http.MethodGet, withQuery("/events/stream", v), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	// The shared client has a total timeout that would cut a long stream.
	resp, err := (&http.Client{Transport: c.http.Transport, CheckRedirect: stayOnOrigin}).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return c.connErr(err)
	}
	defer resp.Body.Close()
	c.capture(resp.Header)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		return parseAPIError(resp.StatusCode, raw)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		raw := strings.Join(data, "\n")
		data = data[:0]
		var e apitypes.Event
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return fmt.Errorf("decode event: %w", err)
		}
		return fn(e)
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("read event stream: %w", err)
	}
	return nil
}
