package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

func cardPath(slug string, n int64, rest string) string {
	return "/projects/" + seg(slug) + "/cards/" + num(n) + rest
}

// ListCards calls GET /projects/{slug}/cards.
func (c *Client) ListCards(ctx context.Context, slug string, p apitypes.ListCardsParams) (apitypes.CardList, error) {
	v := cardFilterValues(p.CardFilter)
	setIf(v, "sort", p.Sort)
	setBool(v, "desc", p.Desc)
	var out apitypes.CardList
	err := c.do(ctx, http.MethodGet, withQuery("/projects/"+seg(slug)+"/cards", v), nil, &out)
	return out, err
}

// ListAllCards calls GET /cards: cards across projects, newest first. Each
// row carries its project slug.
func (c *Client) ListAllCards(ctx context.Context, p apitypes.ListAllCardsParams) (apitypes.CardList, error) {
	v := cardFilterValues(p.CardFilter)
	setIf(v, "project", p.Project)
	var out apitypes.CardList
	err := c.do(ctx, http.MethodGet, withQuery("/cards", v), nil, &out)
	return out, err
}

// ListUnfolded calls GET /projects/{slug}/unfolded: done cards with no parent
// and no fold mark, newest closed first.
func (c *Client) ListUnfolded(ctx context.Context, slug string, limit int, cursor string) (apitypes.CardList, error) {
	v := url.Values{}
	setLimit(v, limit, cursor)
	var out apitypes.CardList
	err := c.do(ctx, http.MethodGet, withQuery("/projects/"+seg(slug)+"/unfolded", v), nil, &out)
	return out, err
}

// FoldCard calls POST .../fold.
func (c *Client) FoldCard(ctx context.Context, slug string, n int64, in apitypes.FoldCard) (apitypes.Card, error) {
	var out apitypes.Card
	err := c.do(ctx, http.MethodPost, cardPath(slug, n, "/fold"), in, &out)
	return out, err
}

// NextCard calls GET /projects/{slug}/cards/next. A 404 APIError means no
// card is ready.
func (c *Client) NextCard(ctx context.Context, slug string) (apitypes.Card, error) {
	var out apitypes.Card
	err := c.do(ctx, http.MethodGet, "/projects/"+seg(slug)+"/cards/next", nil, &out)
	return out, err
}

// GetCard calls GET /projects/{slug}/cards/{number}.
func (c *Client) GetCard(ctx context.Context, slug string, n int64) (apitypes.Card, error) {
	var out apitypes.Card
	err := c.do(ctx, http.MethodGet, cardPath(slug, n, ""), nil, &out)
	return out, err
}

// GetCardPage calls GET /projects/{slug}/cards/{number}/page. A card without a
// page is a 404.
func (c *Client) GetCardPage(ctx context.Context, slug string, n int64) (apitypes.HTMLPage, error) {
	return call[apitypes.HTMLPage](ctx, c, http.MethodGet, cardPath(slug, n, "/page"), nil)
}

// CreateCard calls POST /projects/{slug}/cards.
func (c *Client) CreateCard(ctx context.Context, slug string, in apitypes.CreateCard) (apitypes.Card, error) {
	var out apitypes.Card
	err := c.do(ctx, http.MethodPost, "/projects/"+seg(slug)+"/cards", in, &out)
	return out, err
}

// UpdateCard calls PATCH /projects/{slug}/cards/{number}.
func (c *Client) UpdateCard(ctx context.Context, slug string, n int64, in apitypes.UpdateCard) (apitypes.Card, error) {
	var out apitypes.Card
	err := c.do(ctx, http.MethodPatch, cardPath(slug, n, ""), in, &out)
	return out, err
}

// TransitionCard calls POST .../transition.
func (c *Client) TransitionCard(ctx context.Context, slug string, n int64, in apitypes.TransitionCard) (apitypes.Card, error) {
	var out apitypes.Card
	err := c.do(ctx, http.MethodPost, cardPath(slug, n, "/transition"), in, &out)
	return out, err
}

func (c *Client) claimOp(ctx context.Context, slug string, n int64, op string, in apitypes.ClaimCard) (apitypes.Card, error) {
	var out apitypes.Card
	err := c.do(ctx, http.MethodPost, cardPath(slug, n, "/"+op), in, &out)
	return out, err
}

// ClaimCard calls POST .../claim.
func (c *Client) ClaimCard(ctx context.Context, slug string, n int64, in apitypes.ClaimCard) (apitypes.Card, error) {
	return c.claimOp(ctx, slug, n, "claim", in)
}

// RenewCard calls POST .../renew.
func (c *Client) RenewCard(ctx context.Context, slug string, n int64, in apitypes.ClaimCard) (apitypes.Card, error) {
	return c.claimOp(ctx, slug, n, "renew", in)
}

// ReleaseCard calls POST .../release.
func (c *Client) ReleaseCard(ctx context.Context, slug string, n int64, in apitypes.ClaimCard) (apitypes.Card, error) {
	return c.claimOp(ctx, slug, n, "release", in)
}

// AnsweredCard is the result of answering a question card.
type AnsweredCard struct {
	Card apitypes.Card
	// DecisionLocation is the Location header, set when Decide was true.
	DecisionLocation string
}

// AnswerCard calls POST .../answer.
func (c *Client) AnswerCard(ctx context.Context, slug string, n int64, in apitypes.AnswerCard) (AnsweredCard, error) {
	var out AnsweredCard
	h, err := c.doHeader(ctx, http.MethodPost, cardPath(slug, n, "/answer"), in, &out.Card)
	if h != nil {
		out.DecisionLocation = h.Get("Location")
	}
	return out, err
}

// MarkForPerson calls POST .../for-person.
func (c *Client) MarkForPerson(ctx context.Context, slug string, n int64, in apitypes.MarkForPerson) (apitypes.Card, error) {
	var out apitypes.Card
	err := c.do(ctx, http.MethodPost, cardPath(slug, n, "/for-person"), in, &out)
	return out, err
}

// LinkCards calls POST .../links.
func (c *Client) LinkCards(ctx context.Context, slug string, n int64, in apitypes.LinkCards) (apitypes.Card, error) {
	var out apitypes.Card
	err := c.do(ctx, http.MethodPost, cardPath(slug, n, "/links"), in, &out)
	return out, err
}

// UnlinkCards calls DELETE .../links/{kind}/{to}?project=. project names the
// target's project and may be empty when it is the card's own.
func (c *Client) UnlinkCards(ctx context.Context, slug string, n int64, kind string, to int64, project string) error {
	v := url.Values{}
	setIf(v, "project", project)
	return c.do(ctx, http.MethodDelete, withQuery(cardPath(slug, n, "/links/"+seg(kind)+"/"+strconv.FormatInt(to, 10)), v), nil, nil)
}

// ListCardRevisions calls GET .../revisions, newest first, without bodies.
func (c *Client) ListCardRevisions(ctx context.Context, slug string, n int64, p HistoryParams) ([]apitypes.CardRevision, error) {
	return call[[]apitypes.CardRevision](ctx, c, http.MethodGet, withQuery(cardPath(slug, n, "/revisions"), historyValues(p)), nil)
}

// GetCardRevision calls GET .../revisions/{version}.
func (c *Client) GetCardRevision(ctx context.Context, slug string, n, version int64) (apitypes.CardRevision, error) {
	return call[apitypes.CardRevision](ctx, c, http.MethodGet, cardPath(slug, n, "/revisions/"+strconv.FormatInt(version, 10)), nil)
}

// ListComments calls GET .../comments, newest first.
func (c *Client) ListComments(ctx context.Context, slug string, n int64, p HistoryParams) ([]apitypes.Comment, error) {
	return call[[]apitypes.Comment](ctx, c, http.MethodGet, withQuery(cardPath(slug, n, "/comments"), historyValues(p)), nil)
}

// CreateComment calls POST .../comments.
func (c *Client) CreateComment(ctx context.Context, slug string, n int64, body string) (apitypes.Comment, error) {
	return call[apitypes.Comment](ctx, c, http.MethodPost, cardPath(slug, n, "/comments"), apitypes.CreateComment{Body: body})
}

func cardFilterValues(p apitypes.CardFilter) url.Values {
	v := timeFilterValues(p.CreatedAfter, p.CreatedBefore, p.UpdatedAfter, p.UpdatedBefore)
	setIf(v, "status", strings.Join(p.Status, ","))
	setIf(v, "kind", p.Kind)
	setIf(v, "q", p.Query)
	if p.Parent != nil {
		v.Set("parent", num(*p.Parent))
	}
	setBool(v, "no_story", p.NoStory)
	setIf(v, "lane", p.Lane)
	setBool(v, "no_lane", p.NoLane)
	setBool(v, "mine", p.Mine)
	setBool(v, "open", p.Open)
	setBool(v, "relayed", p.Relayed)
	setNonEmpty(v, "closed_after", p.ClosedAfter, "closed_before", p.ClosedBefore, "assignee", p.Assignee, "answered_for", p.AnsweredFor)
	if len(p.Priority) > 0 {
		v.Set("priority", strings.Join(p.Priority, ","))
	}
	setLimit(v, p.Limit, p.Cursor)
	return v
}

// SubscribeCard calls PUT /projects/{slug}/cards/{number}/subscription: events on the
// card wake the calling agent chat.
func (c *Client) SubscribeCard(ctx context.Context, slug string, n int64) error {
	return c.do(ctx, http.MethodPut, cardPath(slug, n, "/subscription"), nil, nil)
}

// UnsubscribeCard calls DELETE /projects/{slug}/cards/{number}/subscription.
func (c *Client) UnsubscribeCard(ctx context.Context, slug string, n int64) error {
	return c.do(ctx, http.MethodDelete, cardPath(slug, n, "/subscription"), nil, nil)
}

// ListSubscriptions calls GET /me/subscriptions: the cards the calling chat is subscribed to.
func (c *Client) ListSubscriptions(ctx context.Context) (apitypes.SubscriptionList, error) {
	return call[apitypes.SubscriptionList](ctx, c, http.MethodGet, "/me/subscriptions", nil)
}
