package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
)

// CardFilterQuery is the filter set both card lists take, bound once from the
// query string and turned into a store filter by Service.cardFilters. It is
// the server's own copy of the fields apitypes.ListCardsParams and
// ListAllCardsParams describe for clients; TestCardListParamsMatchTheRoutes
// keeps the two in step. huma cannot bind pointer query parameters, so parent
// is a plain number where 0 means unset.
type CardFilterQuery struct {
	Status  []string `query:"status" enum:"backlog,ready,working,waiting,done,dropped"`
	Kind    string   `query:"kind" enum:",task,question,story,decision"`
	Parent  int64    `query:"parent" minimum:"0" doc:"on /cards, needs a project slug"`
	NoStory bool     `query:"no_story" doc:"only cards in no story: no parent, and not a story themselves; not with parent"`
	Lane    string   `query:"lane" doc:"only cards with this lane; not with no_lane"`
	NoLane  bool     `query:"no_lane" doc:"only cards with no lane"`
	Mine    bool     `query:"mine" doc:"only cards claimed by or assigned to the caller"`
	Open    bool     `query:"open" doc:"exclude done and dropped"`
	Relayed bool     `query:"relayed" doc:"only questions whose answer an agent relayed"`
	Query   string   `query:"q" doc:"full-text search on title and body"`
	Limit   int      `query:"limit" minimum:"1" maximum:"200" default:"10"`
	Cursor  string   `query:"cursor" doc:"next or prev from a previous page"`

	CreatedAfter  string   `query:"created_after" doc:"RFC 3339; created at or after"`
	CreatedBefore string   `query:"created_before" doc:"RFC 3339; created strictly before"`
	UpdatedAfter  string   `query:"updated_after" doc:"RFC 3339; updated at or after"`
	UpdatedBefore string   `query:"updated_before" doc:"RFC 3339; updated strictly before"`
	ClosedAfter   string   `query:"closed_after" doc:"RFC 3339; closed (done or dropped) at or after"`
	ClosedBefore  string   `query:"closed_before" doc:"RFC 3339; closed strictly before"`
	Assignee      string   `query:"assignee" doc:"actor id, 'me', or 'none'; matches assigned or claimed"`
	Priority      []string `query:"priority" enum:"low,normal,high,urgent"`
	AnsweredFor   string   `query:"answered_for" doc:"me or a user id: only cards an agent settled for that person and they have not kept"`
}

type listCardsIn struct {
	SlugPath
	CardFilterQuery
	Sort string `query:"sort" enum:",created,updated,priority,number" doc:"default is board order; ascending unless desc is set; not with q"`
	Desc bool   `query:"desc" doc:"reverse the sort; needs sort"`
}

type listAllCardsIn struct {
	Project string `query:"project" doc:"all (default) or a project slug"`
	CardFilterQuery
}

// cardBody is a card path plus a JSON body.
type cardBody[T any] struct {
	CardPath
	Body T
}

// RequestKeyHeader is the Idempotency-Key of a write that may be sent again.
// Exported because huma does not read the header tags of an unexported
// embedded struct.
type RequestKeyHeader struct {
	Key string `header:"Idempotency-Key" doc:"1 to 64 of A-Z a-z 0-9 - _; the same key makes the write happen once"`
}

// keyedCardBody is a card body that takes an Idempotency-Key.
type keyedCardBody[T any] struct {
	CardPath
	RequestKeyHeader
	Body T
}

// optionalClaimBody lets claim, renew and release accept an empty request.
type optionalClaimBody struct {
	CardPath
	Body apitypes.ClaimCard `required:"false"`
}

type answerOut struct {
	Location string `header:"Location"`
	Body     apitypes.Card
}

// mapEdgesIn is the input of the project link and mention lists.
type mapEdgesIn struct {
	SlugPath
	Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Cursor string `query:"cursor" doc:"next from a previous page"`
}

func (r *routes) registerCards() {
	cards := "/projects/{slug}/cards"
	one := cards + "/{number}"

	huma.Register(r.api, operation("list-cards", http.MethodGet, cards, "cards", "List cards, one page in board order", http.StatusOK),
		handle(r, func(ctx context.Context, in *listCardsIn) (apitypes.CardList, error) {
			return r.svc.ListCards(ctx, in.Slug, in.CardFilterQuery, in.Sort, in.Desc)
		}))
	huma.Register(r.api, operation("list-all-cards", http.MethodGet, "/cards", "cards", "List cards across projects, newest first", http.StatusOK),
		handle(r, func(ctx context.Context, in *listAllCardsIn) (apitypes.CardList, error) {
			return r.svc.ListAllCards(ctx, in.Project, in.CardFilterQuery)
		}))
	huma.Register(r.api, operation("list-unfolded", http.MethodGet, "/projects/{slug}/unfolded", "cards", "Done cards with no parent and no fold mark, newest closed first", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct {
			SlugPath
			Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"20"`
			Cursor string `query:"cursor" doc:"next or prev from a previous page"`
		}) (apitypes.CardList, error) {
			return r.svc.ListUnfolded(ctx, in.Slug, in.Limit, in.Cursor)
		}))
	huma.Register(r.api, operation("list-project-links", http.MethodGet, "/projects/{slug}/links", "cards", "Card links with an end in the project, both directions, in card id order", http.StatusOK),
		handle(r, func(ctx context.Context, in *mapEdgesIn) (apitypes.ProjectLinkList, error) {
			return r.svc.ListProjectLinks(ctx, in.Slug, in.Limit, in.Cursor)
		}))
	huma.Register(r.api, operation("list-project-mentions", http.MethodGet, "/projects/{slug}/mentions", "cards", "Notes the project's cards reference, in card id order", http.StatusOK),
		handle(r, func(ctx context.Context, in *mapEdgesIn) (apitypes.ProjectMentionList, error) {
			return r.svc.ListProjectMentions(ctx, in.Slug, in.Limit, in.Cursor)
		}))
	huma.Register(r.api, operation("list-map-groups", http.MethodGet, "/projects/{slug}/map/groups", "cards", "Story and lane boxes of the collapsed map, with status counts", http.StatusOK),
		handle(r, func(ctx context.Context, in *mapEdgesIn) (apitypes.MapGroupList, error) {
			return r.svc.ListMapGroups(ctx, in.Slug, in.Limit, in.Cursor)
		}))
	huma.Register(r.api, operation("list-map-group-links", http.MethodGet, "/projects/{slug}/map/group-links", "cards", "Link counts between the map groups, by kind", http.StatusOK),
		handle(r, func(ctx context.Context, in *mapEdgesIn) (apitypes.MapGroupLinkList, error) {
			return r.svc.ListMapGroupLinks(ctx, in.Slug, in.Limit, in.Cursor)
		}))
	huma.Register(r.api, carryingPage(operation("create-card", http.MethodPost, cards, "cards", "Create a task, question, story or decision", http.StatusCreated)),
		handle(r, func(ctx context.Context, in *struct {
			SlugPath
			RequestKeyHeader
			Body apitypes.CreateCard
		}) (apitypes.Card, error) {
			return r.svc.CreateCard(ctx, in.Slug, in.Body, in.Key)
		}))
	huma.Register(r.api, operation("next-card", http.MethodGet, cards+"/next", "cards", "Best ready card, or 404", http.StatusOK),
		handle(r, func(ctx context.Context, in *SlugPath) (apitypes.Card, error) { return r.svc.NextCard(ctx, in.Slug) }))
	huma.Register(r.api, operation("get-card", http.MethodGet, one, "cards", "Get a card with links, children, progress and events", http.StatusOK),
		handle(r, func(ctx context.Context, in *CardPath) (apitypes.Card, error) {
			return r.svc.GetCard(ctx, in.Slug, in.Number)
		}))
	huma.Register(r.api, carryingPage(operation("update-card", http.MethodPatch, one, "cards", "Edit a card (version required)", http.StatusOK)),
		handle(r, func(ctx context.Context, in *cardBody[apitypes.UpdateCard]) (apitypes.Card, error) {
			return r.svc.UpdateCard(ctx, in.Slug, in.Number, in.Body)
		}))
	huma.Register(r.api, operation("get-card-page", http.MethodGet, one+"/page", "cards", "The card's HTML page as JSON, or 404 when it has none", http.StatusOK),
		handle(r, func(ctx context.Context, in *CardPath) (apitypes.HTMLPage, error) {
			return r.svc.GetCardPage(ctx, in.Slug, in.Number)
		}))
	huma.Register(r.api, operation("list-card-revisions", http.MethodGet, one+"/revisions", "cards", "Past titles and bodies of a card, newest first, without bodies", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct {
			CardPath
			RevisionPage
		}) ([]apitypes.CardRevision, error) {
			return r.svc.ListCardRevisions(ctx, in.Slug, in.Number, in.page())
		}))
	huma.Register(r.api, operation("get-card-revision", http.MethodGet, one+"/revisions/{version}", "cards", "One past version of a card with its body", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct {
			CardPath
			Version int64 `path:"version" minimum:"1"`
		}) (apitypes.CardRevision, error) {
			return r.svc.GetCardRevision(ctx, in.Slug, in.Number, in.Version)
		}))
	huma.Register(r.api, operation("transition-card", http.MethodPost, one+"/transition", "cards", "Change card status", http.StatusOK),
		handle(r, func(ctx context.Context, in *keyedCardBody[apitypes.TransitionCard]) (apitypes.Card, error) {
			return r.svc.Transition(ctx, in.Slug, in.Number, in.Body, in.Key)
		}))
	huma.Register(r.api, operation("fold-card", http.MethodPost, one+"/fold", "cards", "Fold a done card that has no parent into a document, or into nothing", http.StatusOK),
		handle(r, func(ctx context.Context, in *cardBody[apitypes.FoldCard]) (apitypes.Card, error) {
			return r.svc.FoldCard(ctx, in.Slug, in.Number, in.Body)
		}))
	huma.Register(r.api, operation("claim-card", http.MethodPost, one+"/claim", "cards", "Claim a card", http.StatusOK),
		handle(r, func(ctx context.Context, in *optionalClaimBody) (apitypes.Card, error) {
			return r.svc.Claim(ctx, in.Slug, in.Number, in.Body.Version, in.Body.Lease)
		}))
	huma.Register(r.api, operation("renew-card", http.MethodPost, one+"/renew", "cards", "Renew a claim", http.StatusOK),
		handle(r, func(ctx context.Context, in *optionalClaimBody) (apitypes.Card, error) {
			return r.svc.Renew(ctx, in.Slug, in.Number, in.Body.Version, in.Body.Lease)
		}))
	huma.Register(r.api, operation("release-card", http.MethodPost, one+"/release", "cards", "Release a claim", http.StatusOK),
		handle(r, func(ctx context.Context, in *optionalClaimBody) (apitypes.Card, error) {
			if in.Body.Lease != "" {
				return apitypes.Card{}, invalidField("lease", "a release takes none")
			}
			return r.svc.Release(ctx, in.Slug, in.Number, in.Body.Version)
		}))
	huma.Register(r.api, operation("answer-card", http.MethodPost, one+"/answer", "cards", "Answer a question, or relay a human's answer (agents)", http.StatusOK),
		func(ctx context.Context, in *cardBody[apitypes.AnswerCard]) (*answerOut, error) {
			c, loc, err := r.svc.Answer(ctx, in.Slug, in.Number, in.Body)
			if err != nil {
				return nil, r.fail(ctx, err)
			}
			return &answerOut{Location: loc, Body: c}, nil
		})
	huma.Register(r.api, operation("keep-card", http.MethodPost, one+"/keep", "cards", "Keep what an agent settled for you on the card", http.StatusOK),
		handle(r, func(ctx context.Context, in *cardBody[apitypes.KeepCard]) (apitypes.Card, error) {
			return r.svc.Keep(ctx, in.Slug, in.Number, in.Body.Version)
		}))
	huma.Register(r.api, operation("mark-card-for-person", http.MethodPost, one+"/for-person", "cards", "Mark a question as one only a person answers, or clear the mark", http.StatusOK),
		handle(r, func(ctx context.Context, in *cardBody[apitypes.MarkForPerson]) (apitypes.Card, error) {
			return r.svc.ForPerson(ctx, in.Slug, in.Number, in.Body)
		}))
	huma.Register(r.api, operation("link-cards", http.MethodPost, one+"/links", "cards", "Link this card to another", http.StatusOK),
		handle(r, func(ctx context.Context, in *cardBody[apitypes.LinkCards]) (apitypes.Card, error) {
			return r.svc.LinkCards(ctx, in.Slug, in.Number, in.Body)
		}))

	type unlinkIn struct {
		CardPath
		Kind    string `path:"kind" enum:"blocks,relates,duplicates,applies"`
		To      int64  `path:"to" minimum:"1"`
		Project string `query:"project" doc:"slug of the target's project when it is not the card's"`
	}
	huma.Register(r.api, operation("unlink-cards", http.MethodDelete, one+"/links/{kind}/{to}", "cards", "Remove a link", http.StatusNoContent),
		handleEmpty(r, func(ctx context.Context, in *unlinkIn) error {
			return r.svc.UnlinkCards(ctx, in.Slug, in.Number, in.Kind, in.To, in.Project)
		}))

	huma.Register(r.api, operation("list-comments", http.MethodGet, one+"/comments", "cards", "Comments on a card, newest first", http.StatusOK),
		handle(r, func(ctx context.Context, in *struct {
			CardPath
			Before string `query:"before" doc:"comment id; only older comments"`
			Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50" doc:"at most this many"`
		}) ([]apitypes.Comment, error) {
			return r.svc.ListComments(ctx, in.Slug, in.Number, in.Before, in.Limit)
		}))
	huma.Register(r.api, operation("create-comment", http.MethodPost, one+"/comments", "cards", "Leave a comment", http.StatusCreated),
		handle(r, func(ctx context.Context, in *keyedCardBody[apitypes.CreateComment]) (apitypes.Comment, error) {
			return r.svc.CreateComment(ctx, in.Slug, in.Number, in.Body.Body, in.Key)
		}))
	huma.Register(r.api, operation("mark-card-seen", http.MethodPost, one+"/seen", "cards", "Record that you opened a card", http.StatusNoContent),
		handleEmpty(r, func(ctx context.Context, in *CardPath) error {
			return r.svc.MarkCardSeen(ctx, in.Slug, in.Number)
		}))

	huma.Register(r.api, operation("subscribe-card", http.MethodPut, one+"/subscription", "cards", "Subscribe the calling agent chat to a card: events on it wake the chat", http.StatusNoContent),
		handleEmpty(r, func(ctx context.Context, in *CardPath) error {
			return r.svc.SubscribeCard(ctx, in.Slug, in.Number)
		}))
	huma.Register(r.api, operation("unsubscribe-card", http.MethodDelete, one+"/subscription", "cards", "Unsubscribe the calling agent chat from a card, also when it was not subscribed", http.StatusNoContent),
		handleEmpty(r, func(ctx context.Context, in *CardPath) error {
			return r.svc.UnsubscribeCard(ctx, in.Slug, in.Number)
		}))

	type repliesIn struct {
		SlugPath
		Cursor string `query:"cursor"`
		Limit  int32  `query:"limit" default:"20" minimum:"1" maximum:"100"`
	}
	huma.Register(r.api, operation("list-replies", http.MethodGet, "/projects/{slug}/replies", "cards", "Comments by others on cards you wrote on, since you last saw them", http.StatusOK),
		handle(r, func(ctx context.Context, in *repliesIn) (apitypes.ReplyList, error) {
			return r.svc.ListReplies(ctx, in.Slug, in.Cursor, in.Limit)
		}))
	huma.Register(r.api, operation("mark-replies-seen", http.MethodPost, "/projects/{slug}/replies/seen", "cards", "Mark every card with replies to you seen, as opening each would", http.StatusNoContent),
		handleEmpty(r, func(ctx context.Context, in *SlugPath) error {
			return r.svc.MarkRepliesSeen(ctx, in.Slug)
		}))
}
