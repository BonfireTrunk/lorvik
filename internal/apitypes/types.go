// Package apitypes defines the JSON wire types shared by the server and the
// CLI client. It contains data only: no logic, no imports from server, store
// or domain packages. Enum values are plain strings and mirror the domain
// package; the server maps between the two.
//
// Ids are snowflakes carried as decimal strings so JavaScript clients never
// lose precision. Actors are (kind, id) pairs only; clients resolve names
// through GET /actors and cache them.
//
// Field sets here are a public contract. Adding fields is fine; renaming or
// removing one is a breaking change for every installed CLI.
package apitypes

import (
	"encoding/json"
	"time"
)

// Health is the response of GET /api/v1/healthz.
type Health struct {
	Status  string `json:"status" doc:"ok or degraded"`
	Version string `json:"version"`
	// Database is "ok", "unavailable", or "unconfigured".
	Database string `json:"database"`
}

// Problem is the error body, following RFC 9457.
type Problem struct {
	Type     string         `json:"type,omitempty"`
	Title    string         `json:"title"`
	Status   int            `json:"status"`
	Detail   string         `json:"detail,omitempty"`
	Instance string         `json:"instance,omitempty"`
	Errors   []ProblemField `json:"errors,omitempty"`
	// Hint tells an agent how to recover, e.g. "run `lorvik show 42` and retry with the new version".
	Hint string `json:"hint,omitempty"`
	// CurrentVersion is set on 409 version conflicts.
	CurrentVersion int64 `json:"current_version,omitempty"`
	// WaitingCards and BusyLanes are set on the 404 from next when ready cards
	// exist that wait only because their lane has a working card.
	WaitingCards int      `json:"waiting_cards,omitempty"`
	BusyLanes    []string `json:"busy_lanes,omitempty"`
}

// ProblemField is one field-level validation failure.
type ProblemField struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Headers the server sets or reads on every authenticated request.
const (
	// HeaderUnread carries how many events others produced after the cursor
	// the client sent in HeaderCursor; above UnreadCap it reads "more than".
	HeaderUnread = "X-Lorvik-Unread"
	// HeaderCursor is sent by the client: the event position it has read up to.
	HeaderCursor = "X-Lorvik-Cursor"
	// HeaderToken carries a replacement secret when the token used is close
	// to expiry. The client stores it and stops using the old one.
	HeaderToken = "X-Lorvik-Token"
	// HeaderSession is sent by the client: a short label for the chat or
	// process making the call, recorded on the claims it creates. See
	// ValidSession for the accepted form.
	HeaderSession = "X-Lorvik-Session"
	// HeaderIdempotencyKey is sent by the client on a write it may send
	// again: the same key means the same write, which happens once.
	HeaderIdempotencyKey = "Idempotency-Key"
	// HeaderSessionName is sent by an agent's client when its chat has a
	// name people call it by: percent-encoded UTF-8. The server keeps the
	// last one sent for the chat and shows it beside the label.
	HeaderSessionName = "X-Lorvik-Session-Name"
	// HeaderListener is sent, with any value, by a background listener (lorvik
	// inbox --follow): a process that outlives its chat's attention. Its
	// requests do not renew claims, since a listener running is not the chat
	// working.
	HeaderListener = "X-Lorvik-Listener"
	// HeaderHolding is sent by a client, with any value, to be told what its
	// chat holds, and answered with a Holding as base64 of its JSON: the
	// state after the request did its work. The event stream does not
	// answer it.
	HeaderHolding = "X-Lorvik-Holding"
	// UnreadCap bounds the unread count.
	UnreadCap = 100
	// HoldingCap bounds the cards a Holding names and HoldingTitle the
	// characters of each title. The server names fewer when the header would
	// pass 2 KiB, which a proxy with a 4 KiB header buffer still accepts.
	HoldingCap   = 8
	HoldingTitle = 60
)

// Holding is what the calling chat holds: a person's live claims, an agent's
// in the calling session, the soonest to end first.
type Holding struct {
	Cards []HeldCard `json:"cards"`
	// More counts the held cards past HoldingCap, which are not named.
	More int `json:"more,omitempty"`
}

// HeldCard is one card the chat holds and when its claim ends. Title is cut
// to HoldingTitle characters.
type HeldCard struct {
	Project   string    `json:"project"`
	Number    int64     `json:"number"`
	Title     string    `json:"title"`
	ExpiresAt time.Time `json:"expires_at"`
}

// TokenPrefixLen is the length of the token prefix the server lists for a
// token. The server pins its own copy to this in tests.
const TokenPrefixLen = 12

// Paging limits every list shares. Struct tags cannot name constants, so the
// server pins its limit tags and its own copies to these in tests.
const (
	// RemainingCap is the largest remaining count a list reports; a page
	// that says RemainingCap means at least that many.
	RemainingCap = 100
	// MaxPageSize is the largest page a list returns, whatever limit the
	// request names.
	MaxPageSize = 200
	// DefaultPageSize is the page a list returns when no limit is given.
	DefaultPageSize = 50
	// MaxEmbeddedChildren is how many children a card embeds; the rest are
	// read from the card list with parent.
	MaxEmbeddedChildren = 50
	// MaxActorIDs is how many ids one GET /actors takes.
	MaxActorIDs = 100
)

// Actor identifies who did something. Names are not carried: resolve them
// with GET /actors?ids=... and cache the result.
type Actor struct {
	Kind string `json:"kind" enum:"user,agent"`
	ID   string `json:"id"`
}

// User is a human as other callers may see them.
type User struct {
	ID        string    `json:"id"`
	Login     string    `json:"login"`
	Name      string    `json:"name,omitempty"`
	Email     string    `json:"email,omitempty"`
	AvatarURL string    `json:"avatar_url,omitempty"`
	Locale    string    `json:"locale,omitempty" doc:"BCP 47; absent until set"`
	Timezone  string    `json:"timezone,omitempty" doc:"IANA; absent means display in the client's own zone"`
	Status    string    `json:"status" enum:"active,suspended,deleted"`
	CreatedAt time.Time `json:"created_at"`
	// AgentJoin is the user's own account switch, only in /me and PATCH /me.
	AgentJoin string `json:"agent_join,omitempty" enum:",follow,allow" doc:"allow: your agents join your projects without asking you"`
	// GitHubLogin is the GitHub account linked to the user, only in /me.
	GitHubLogin string `json:"github_login,omitempty" doc:"the GitHub account you sign in with; absent when none is linked"`
}

// UpdateUser is the body of PATCH /me. Nil means unchanged.
type UpdateUser struct {
	Name      *string `json:"name,omitempty" maxLength:"200"`
	Email     *string `json:"email,omitempty" maxLength:"254"`
	Locale    *string `json:"locale,omitempty" doc:"BCP 47, e.g. en-GB; empty clears"`
	Timezone  *string `json:"timezone,omitempty" doc:"IANA, e.g. Asia/Kolkata; empty clears"`
	AgentJoin *string `json:"agent_join,omitempty" enum:",follow,allow" doc:"allow: your agents join your projects without asking you"`
}

// Actors is the response of GET /actors?id=: the users and agents found.
type Actors struct {
	Users  []User  `json:"users"`
	Agents []Agent `json:"agents"`
}

// Me is the response of GET /api/v1/me.
type Me struct {
	Actor Actor `json:"actor"`
	Org   Org   `json:"org"`
	// Role is the caller's org-level role, empty for an agent bound only to
	// projects.
	Role string `json:"role,omitempty" enum:",viewer,member,reviewer,admin,owner"`
	// Projects lists the caller's project-level roles.
	Projects    []ProjectRole `json:"projects,omitempty"`
	User        *User         `json:"user,omitempty"`
	Agent       *Agent        `json:"agent,omitempty"`
	TokenPrefix string        `json:"token_prefix"`
	TokenExpiry time.Time     `json:"token_expires_at"`
	// SessionName is the name the server holds for the calling agent's chat.
	SessionName string `json:"session_name,omitempty"`
}

// DeletionPreview is GET /me/deletion: what DELETE /me would do now.
type DeletionPreview struct {
	Closes    []ClosingOrg `json:"closes" doc:"orgs whose only person you are; they close with your account"`
	BlockedBy []string     `json:"blocked_by" doc:"orgs where you are the only owner beside other people; the delete is refused until one of them is owner"`
}

// ClosingOrg is an org a delete would close, with what goes out of reach.
type ClosingOrg struct {
	Name     string `json:"name"`
	Projects int    `json:"projects"`
	Cards    int    `json:"cards"`
}

// DeleteMe is the body of DELETE /me: the caller's login, typed to confirm
// that the account goes for good.
type DeleteMe struct {
	Confirm string `json:"confirm" maxLength:"400"`
}

// PutSessionName is the body of PUT /me/session-name. An empty name clears it.
type PutSessionName struct {
	Name string `json:"name" maxLength:"400"`
}

// ProjectRole is one project the caller has a role in. An agent's role is
// always viewer; its Rights say what it may do. On GET /me they are the
// effective rights of the request's session, its chat's grant included.
type ProjectRole struct {
	Project string   `json:"project" doc:"slug"`
	Role    string   `json:"role"`
	Rights  []string `json:"rights,omitempty" enum:"work,ready,triage,answer,decide" doc:"agents only"`
}

// Org is the tenant.
type Org struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Status    string       `json:"status"`
	Settings  *OrgSettings `json:"settings,omitempty" doc:"admins and owners only"`
	CreatedAt time.Time    `json:"created_at"`
}

// OrgSettings is the org's settings document.
type OrgSettings struct {
	AgentApproval            string `json:"agent_approval,omitempty" enum:",auto,admin"`
	TokenTTLDefault          string `json:"token_ttl_default,omitempty" doc:"Go duration, default 720h"`
	TokenTTLMax              string `json:"token_ttl_max,omitempty" doc:"Go duration, at most 8760h"`
	ProjectVisibilityDefault string `json:"project_visibility_default,omitempty" enum:",org,private"`
}

// UpdateOrg is the body of PATCH /org (admins and owners).
type UpdateOrg struct {
	Name     *string      `json:"name,omitempty" minLength:"1" maxLength:"200"`
	Settings *OrgSettings `json:"settings,omitempty"`
}

// Membership is an actor's org-level role. An agent's role is viewer and its
// Rights carry the rest.
type Membership struct {
	Actor     Actor     `json:"actor"`
	Role      string    `json:"role" enum:"viewer,member,reviewer,admin,owner"`
	Rights    []string  `json:"rights,omitempty" enum:"work,ready,triage,answer,decide" doc:"agents only"`
	Status    string    `json:"status" enum:"active,pending,removed"`
	GrantedBy string    `json:"granted_by,omitempty" doc:"user id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PutMembership is the body of PUT /members/{kind}/{id},
// PUT /projects/{slug}/members/{kind}/{id} and PUT /agents/{id}/projects/{slug}.
// A person takes a role and no rights; an agent takes rights (default work)
// and no role other than viewer.
type PutMembership struct {
	Role   string   `json:"role,omitempty" enum:",viewer,member,reviewer,admin,owner"`
	Rights []string `json:"rights,omitempty" enum:"work,ready,triage,answer,decide"`
}

// ProjectMember is an actor's role within one project.
type ProjectMember struct {
	Project   string    `json:"project" doc:"slug"`
	Actor     Actor     `json:"actor"`
	Role      string    `json:"role" enum:"viewer,member,reviewer"`
	Rights    []string  `json:"rights,omitempty" enum:"work,ready,triage,answer,decide" doc:"agents only"`
	Status    string    `json:"status" enum:"active,removed"`
	GrantedBy string    `json:"granted_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Project groups cards and notes.
type Project struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Visibility  string    `json:"visibility" enum:"org,private"`
	Concurrency string    `json:"concurrency" enum:"parallel,serial" doc:"parallel: a lead fans child cards out to subagents when the harness can; serial: work them in order"`
	Scope       string    `json:"scope" enum:"project,global" doc:"global for the org's one global project, which holds what applies across projects"`
	Lease       string    `json:"lease" doc:"Go duration a claim lives without renewal, e.g. 30m"`
	Status      string    `json:"status" enum:"active,archived"`
	CreatedBy   Actor     `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Role is the caller's effective role in this project.
	Role string `json:"role,omitempty"`
	// Rights are an agent caller's effective rights in this project for the
	// request's session; people carry none.
	Rights []string `json:"rights,omitempty" enum:"work,ready,triage,answer,decide"`
	// ChatGrant is what the owner gave the calling agent's chat here, on
	// GET /projects/{slug}; absent without a live grant.
	ChatGrant *SessionGrant `json:"chat_grant,omitempty"`
	// AgentJoin is ask when a project admin wants every agent asked in.
	AgentJoin string `json:"agent_join" enum:"follow,ask"`
	// MyAgentJoin is the calling user's own rule for their agents here, on
	// GET /projects/{slug} and PUT /projects/{slug}/agent-join.
	MyAgentJoin string `json:"my_agent_join,omitempty" enum:",follow,allow,ask"`
	// Counts is included on GET /projects/{slug}, omitted on lists.
	Counts *StatusCounts `json:"counts,omitempty"`
}

// StatusCounts summarises a project's cards.
type StatusCounts struct {
	Backlog       int `json:"backlog"`
	Ready         int `json:"ready"`
	Working       int `json:"working"`
	Waiting       int `json:"waiting"`
	Done          int `json:"done"`
	Dropped       int `json:"dropped"`
	OpenQuestions int `json:"open_questions"`
}

// CreateProject is the body of POST /api/v1/projects.
type CreateProject struct {
	Slug        string `json:"slug" minLength:"1" maxLength:"64"`
	Name        string `json:"name" minLength:"1" maxLength:"200"`
	Description string `json:"description,omitempty" maxLength:"4000"`
	Visibility  string `json:"visibility,omitempty" enum:",org,private" doc:"default from org settings"`
	Lease       string `json:"lease,omitempty" doc:"Go duration between 1m and 24h; default 30m"`
}

// UpdateProject is the body of PATCH /api/v1/projects/{slug}. Nil means unchanged.
type UpdateProject struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Visibility  *string `json:"visibility,omitempty" enum:",org,private"`
	Concurrency *string `json:"concurrency,omitempty" enum:",parallel,serial"`
	Lease       *string `json:"lease,omitempty"`
	Status      *string `json:"status,omitempty" enum:",active,archived"`
	AgentJoin   *string `json:"agent_join,omitempty" enum:",follow,ask" doc:"ask: every agent is asked in, whatever its owner's settings"`
}

// PutAgentJoin is the body of PUT /projects/{slug}/agent-join: the calling
// user's own rule for their agents joining this project.
type PutAgentJoin struct {
	AgentJoin string `json:"agent_join" enum:"follow,allow,ask"`
}

// Claim says who holds a card and until when. Session is the label the
// holder's client sent when it took the card; empty when it sent none.
type Claim struct {
	Actor   Actor  `json:"actor"`
	Session string `json:"session,omitempty"`
	// SessionName is what people call the holder's chat, when it has a name.
	SessionName string    `json:"session_name,omitempty"`
	ExpiresAt   time.Time `json:"expires_at"`
	// Mine is set when the claim is the caller's own and live: the same actor
	// and, for an agent, the same session.
	Mine bool `json:"mine,omitempty"`
	// Owned is Mine without the lease: the claim is the caller's own, live
	// or lapsed.
	Owned bool `json:"owned,omitempty"`
	// Lease is the length the holder stated for this claim; absent when it
	// follows the project's lease.
	Lease string `json:"lease,omitempty"`
}

// Question carries the fields a question card adds.
type Question struct {
	Choices    []string   `json:"choices,omitempty"`
	Answer     string     `json:"answer,omitempty"`
	AnsweredBy *Actor     `json:"answered_by,omitempty"`
	AnsweredAt *time.Time `json:"answered_at,omitempty"`
}

// CardLink is an edge from this card to another.
type CardLink struct {
	Kind    string `json:"kind" enum:"blocks,relates,duplicates,supersedes,applies"`
	Project string `json:"project,omitempty" doc:"slug of the other card's project when it is not this card's"`
	Number  int64  `json:"number"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	// Direction is "out" when this card is the source, "in" when it is the target.
	Direction string `json:"direction" enum:"in,out"`
}

// CardDecision is what a decision card adds. Supersedes and SupersededBy are
// card numbers in the decision's project, read from its supersedes links.
type CardDecision struct {
	AcceptedBy   *Actor     `json:"accepted_by,omitempty"`
	AcceptedAt   *time.Time `json:"accepted_at,omitempty"`
	Supersedes   *int64     `json:"supersedes,omitempty"`
	SupersededBy *int64     `json:"superseded_by,omitempty"`
}

// CardFold records that a closed card was folded into the requirements
// documents. Into is empty when nothing durable changed.
type CardFold struct {
	Into string    `json:"into"`
	At   time.Time `json:"at"`
	By   Actor     `json:"by"`
}

// Progress counts a story's children; dropped children count as done.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// Card is a task, question, story or decision.
type Card struct {
	ID         string    `json:"id"`
	Project    string    `json:"project" doc:"project slug"`
	Number     int64     `json:"number"`
	Kind       string    `json:"kind" enum:"task,question,story,decision"`
	Status     string    `json:"status" enum:"backlog,ready,working,waiting,done,dropped"`
	DropReason string    `json:"drop_reason,omitempty" enum:",obsolete,duplicate,wontfix,rejected,revoked,superseded"`
	Title      string    `json:"title"`
	Body       string    `json:"body,omitempty"`
	Parent     *int64    `json:"parent,omitempty" doc:"parent card number"`
	Priority   string    `json:"priority" enum:"low,normal,high,urgent"`
	Assignee   *Actor    `json:"assignee,omitempty"`
	Branch     string    `json:"branch,omitempty" doc:"git branch the work is on"`
	Lane       string    `json:"lane,omitempty" doc:"cards in one lane are not worked concurrently"`
	CreatedBy  Actor     `json:"created_by"`
	Version    int64     `json:"version"`
	Claim      *Claim    `json:"claim,omitempty"`
	Question   *Question `json:"question,omitempty"`
	// Decision is filled for decision cards only.
	Decision  *CardDecision `json:"decision,omitempty"`
	Fold      *CardFold     `json:"fold,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	ClosedAt  *time.Time    `json:"closed_at,omitempty"`
	// ClosingNote is the note given with the move that closed the card, kept
	// until it is reopened; the fold brief quotes it.
	ClosingNote string `json:"closing_note,omitempty"`
	// PageBytes is the size of the card's HTML page; the page itself is read
	// with GET /projects/{slug}/cards/{n}/page.
	PageBytes int  `json:"page_bytes,omitempty"`
	Relayed   bool `json:"relayed,omitempty" doc:"a question whose answer an agent relayed"`
	ForPerson bool `json:"for_person,omitempty" doc:"a question only a person may answer"`
	// Behalf is set while the card stands as an agent settled it for a person.
	Behalf *Behalf `json:"behalf,omitempty"`
	// The following are filled on GET /cards/{n} only.
	Links    []CardLink  `json:"links,omitempty"`
	Children []CardBrief `json:"children,omitempty"`
	// ChildrenRemaining is how many children Children leaves out. The card
	// list with parent holds them all, in board order.
	ChildrenRemaining int         `json:"children_remaining,omitempty"`
	Progress          *Progress   `json:"progress,omitempty" doc:"stories only: children done or dropped over all children"`
	Comment           *Comment    `json:"comment,omitempty" doc:"latest comment"`
	Events            []Event     `json:"events,omitempty" doc:"most recent events, newest first"`
	BlockedBy         []CardBrief `json:"blocked_by,omitempty" doc:"open cards or questions blocking this one"`
}

// CardBrief is the list-view shape: enough to decide, not enough to flood context.
type CardBrief struct {
	ID       string `json:"id"`
	Project  string `json:"project,omitempty" doc:"project slug; set on rows of GET /cards"`
	Number   int64  `json:"number"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Title    string `json:"title"`
	Priority string `json:"priority"`
	Parent   *int64 `json:"parent,omitempty"`
	Claim    *Claim `json:"claim,omitempty"`
	Assignee *Actor `json:"assignee,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Lane     string `json:"lane,omitempty"`
	// Decision is filled for decision cards only.
	Decision   *CardDecision `json:"decision,omitempty"`
	DropReason string        `json:"drop_reason,omitempty"`
	Fold       *CardFold     `json:"fold,omitempty"`
	PageBytes  int           `json:"page_bytes,omitempty" doc:"size of the card's HTML page; absent when it has none"`
	Relayed    bool          `json:"relayed,omitempty" doc:"a question whose answer an agent relayed"`
	ForPerson  bool          `json:"for_person,omitempty" doc:"a question only a person may answer"`
	Behalf     *Behalf       `json:"behalf,omitempty"`
	UpdatedAt  time.Time     `json:"updated_at"`
	ClosedAt   *time.Time    `json:"closed_at,omitempty"`
}

// CreateCard is the body of POST /projects/{slug}/cards.
type CreateCard struct {
	Kind     string   `json:"kind,omitempty" enum:",task,question,story,decision" doc:"default task"`
	Title    string   `json:"title" minLength:"1" maxLength:"200"`
	Body     string   `json:"body,omitempty"`
	Parent   *int64   `json:"parent,omitempty" doc:"number of a story in this project"`
	Priority string   `json:"priority,omitempty" enum:",low,normal,high,urgent"`
	Status   string   `json:"status,omitempty" enum:",backlog,ready,done" doc:"default ready for questions and decisions, backlog for tasks and stories; done only for a decision recorded by a human reviewer"`
	Choices  []string `json:"choices,omitempty" doc:"question cards only"`
	Lane     string   `json:"lane,omitempty" maxLength:"40" doc:"no whitespace or commas"`
	// Blocks lists card numbers this question blocks (question cards only).
	Blocks []int64 `json:"blocks,omitempty"`
	// Assignee, when set, assigns the new card; it must be a member of the project.
	Assignee *Actor `json:"assignee,omitempty"`
	// Supersedes is the number of the accepted decision in this project that a
	// new decision replaces; decisions only.
	Supersedes *int64 `json:"supersedes,omitempty"`
	// Page is a self-contained HTML page shown beside the card, up to 1 MiB.
	Page string `json:"page,omitempty" doc:"self-contained HTML page, at most 1 MiB; the body may then be empty"`
	// ForPerson asks a question only a person may answer; question cards only.
	ForPerson bool `json:"for_person,omitempty" doc:"question cards only: no agent answers it, a relayed answer stays allowed"`
}

// UpdateCard is the body of PATCH /cards/{n}. Nil means unchanged. Version is
// required and must match the current card.
type UpdateCard struct {
	Version  int64   `json:"version"`
	Title    *string `json:"title,omitempty"`
	Body     *string `json:"body,omitempty"`
	Parent   *int64  `json:"parent,omitempty" doc:"0 clears the parent"`
	Priority *string `json:"priority,omitempty"`
	Assignee *Actor  `json:"assignee,omitempty" doc:"an actor with an empty id clears"`
	Branch   *string `json:"branch,omitempty" maxLength:"200" doc:"git branch; empty clears"`
	Lane     *string `json:"lane,omitempty" maxLength:"40" doc:"empty clears"`
	Page     *string `json:"page,omitempty" doc:"self-contained HTML page, at most 1 MiB; empty removes it"`
}

// TransitionCard is the body of POST /cards/{n}/transition.
type TransitionCard struct {
	Version    int64  `json:"version"`
	Status     string `json:"status" enum:"backlog,ready,working,waiting,done,dropped"`
	DropReason string `json:"drop_reason,omitempty" enum:",obsolete,duplicate,wontfix,rejected,revoked,superseded" doc:"rejected and revoked apply to decisions only; superseded is never accepted"`
	// Duplicate is the surviving card's number, required when drop_reason is duplicate.
	Duplicate *int64 `json:"duplicate,omitempty"`
	Note      string `json:"note,omitempty" doc:"short note recorded on the event; required from an agent accepting or rejecting a decision: what the verdict rests on"`
	// Fold is required when a story moves to done. A card without a parent may carry one
	// when it moves to done; a card with a parent is folded by its story.
	Fold *Fold `json:"fold,omitempty"`
	// Cascade drops the open children along with a story being dropped.
	Cascade bool `json:"cascade,omitempty"`
}

// Fold says where a closed card's result went: into the document at Into, or
// nowhere durable (None). Exactly one is set.
type Fold struct {
	Into string `json:"into,omitempty" doc:"path of the requirements document"`
	None bool   `json:"none,omitempty" doc:"nothing durable changed"`
}

// FoldCard is the body of POST /projects/{slug}/cards/{n}/fold.
type FoldCard struct {
	Version int64 `json:"version"`
	Fold
}

// ClaimCard is the body of POST /cards/{n}/claim, /renew and /release.
type ClaimCard struct {
	Version int64 `json:"version,omitempty" doc:"optional; when set must match"`
	// Lease is the length the caller states for this claim: how long it
	// lasts without a renewal. Claim and renew only.
	Lease string `json:"lease,omitempty" doc:"Go duration such as 1h, 1m to 24h; omit for the project's lease, or on renew to keep the claim's own"`
}

// Behalf says an agent settled a card for a person under a right that person
// gave it: an answer to a question, or a verdict on a proposed decision.
// Until the person keeps it (KeptAt), it counts as not looked at.
type Behalf struct {
	For Actor `json:"for"`
	By  Actor `json:"by" doc:"the agent that settled it"`
	// Session is the chat the agent acted from, with its name when it has one.
	Session     string     `json:"session,omitempty"`
	SessionName string     `json:"session_name,omitempty"`
	Reason      string     `json:"reason" doc:"what the agent said it relied on"`
	KeptAt      *time.Time `json:"kept_at,omitempty" doc:"absent: the person has not looked at it"`
}

// MarkForPerson is the body of POST /projects/{slug}/cards/{number}/for-person.
// It marks an open question as one only a person answers; with Clear a person
// takes the mark off again.
type MarkForPerson struct {
	Version int64  `json:"version"`
	Clear   bool   `json:"clear,omitempty" doc:"take the mark off; users only"`
	Note    string `json:"note,omitempty" maxLength:"4000" doc:"why a person must answer; required from an agent, posted as its comment"`
}

// KeepCard is the body of POST /projects/{slug}/cards/{number}/keep.
type KeepCard struct {
	Version int64 `json:"version"`
}

// AnswerCard is the body of POST /cards/{n}/answer.
type AnswerCard struct {
	Version int64  `json:"version"`
	Answer  string `json:"answer" minLength:"1"`
	// Decide, when true, also records an accepted decision card from this answer.
	Decide bool `json:"decide,omitempty"`
	// Relayed marks an agent passing on a human's answer. Agents must set it; users must not.
	Relayed bool `json:"relayed,omitempty" doc:"an agent passing on a human's answer; required for agents, refused for users"`
	// Note says where and when the human gave the answer; it is posted as the agent's comment.
	Note string `json:"note,omitempty" doc:"where and when the human gave the answer; required with relayed, posted as the agent's comment"`
}

// LinkCards is the body of POST /cards/{n}/links.
type LinkCards struct {
	Kind    string `json:"kind" enum:"blocks,relates,duplicates,applies" doc:"applies only from a decision; supersedes is created by recording a superseding decision"`
	To      int64  `json:"to" doc:"target card number"`
	Project string `json:"project,omitempty" doc:"slug of the target's project; default this card's project. A global card links across projects, two non-global projects do not link"`
}

// Comment is a note left on a card for whoever picks it up next.
type Comment struct {
	ID        string    `json:"id"`
	Card      int64     `json:"card" doc:"card number"`
	Actor     Actor     `json:"actor"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// Reply is a comment by someone else on a card the caller wrote on or opened,
// newer than the caller last saw it.
type Reply struct {
	Card    CardBrief `json:"card"`
	Comment Comment   `json:"comment"`
	Unseen  int       `json:"unseen"`
}

// ReplyList is the response of GET /projects/{slug}/replies.
type ReplyList struct {
	Items []Reply `json:"items"`
	Page
}

// CreateComment is the body of POST /cards/{n}/comments.
type CreateComment struct {
	Body string `json:"body" minLength:"1"`
}

// Page is the paging part of every list response. Next and Prev are opaque
// cursors; pass one back as ?cursor= to continue. Remaining counts the rows
// beyond this page in the direction it was fetched, capped at 100.
type Page struct {
	Next      string `json:"next,omitempty"`
	Prev      string `json:"prev,omitempty"`
	Remaining int    `json:"remaining"`
}

// ProjectList is the response of GET /projects.
type ProjectList struct {
	Items []Project `json:"items"`
	Page
}

// ProjectMemberList is the response of GET /projects/{slug}/members.
type ProjectMemberList struct {
	Items []ProjectMember `json:"items"`
	Page
}

// MemberList is the response of GET /members.
type MemberList struct {
	Items []Membership `json:"items"`
	Page
}

// AgentList is the response of GET /agents.
type AgentList struct {
	Items []Agent `json:"items"`
	Page
}

// TokenList is the response of GET /tokens.
type TokenList struct {
	Items []Token `json:"items"`
	Page
}

// LockList is the response of GET /locks.
type LockList struct {
	Items []Lock `json:"items"`
	Page
}

// CardFilter is the filter both card lists take.
type CardFilter struct {
	Status  []string `query:"status" enum:"backlog,ready,working,waiting,done,dropped"`
	Kind    string   `query:"kind" enum:",task,question,story,decision"`
	Parent  *int64   `query:"parent" doc:"story number; on GET /cards it needs a project slug"`
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

// ListCardsParams are the query parameters of GET /projects/{slug}/cards.
type ListCardsParams struct {
	CardFilter
	Sort string `query:"sort" enum:",created,updated,priority,number" doc:"default is board order; ascending unless desc is set; not with q"`
	Desc bool   `query:"desc" doc:"reverse the sort; needs sort"`
}

// CardList is a paged list.
type CardList struct {
	Items []CardBrief `json:"items"`
	Page
}

// LinkEnd names one end of a link by its card number.
type LinkEnd struct {
	Number  int64  `json:"number"`
	Project string `json:"project,omitempty" doc:"slug of the card's project when it is not the project listed"`
}

// ProjectLink is one card link with an end in the listed project.
type ProjectLink struct {
	From LinkEnd `json:"from"`
	To   LinkEnd `json:"to"`
	Kind string  `json:"kind" enum:"blocks,relates,duplicates,supersedes,applies"`
}

// ProjectLinkList is the response of GET /projects/{slug}/links. Items are in
// card id order and the list only goes forward.
type ProjectLinkList struct {
	Items []ProjectLink `json:"items"`
	Page
}

// LiveClaimCard names the card a live claim holds.
type LiveClaimCard struct {
	Project string `json:"project"`
	Number  int64  `json:"number"`
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
}

// LiveClaim is one live claim of the org: who holds which card, in which
// session, since when and until when.
type LiveClaim struct {
	Card    LiveClaimCard `json:"card"`
	Holder  Actor         `json:"holder"`
	Session string        `json:"session,omitempty"`
	// SessionName is as on Claim.
	SessionName string    `json:"session_name,omitempty"`
	ClaimedAt   time.Time `json:"claimed_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	// Mine is set as for Claim.Mine.
	Mine bool `json:"mine,omitempty"`
}

// LiveClaimList is the response of GET /claims. Items are ordered by holder
// kind and id, session, then card id, and the list only goes forward.
type LiveClaimList struct {
	Items []LiveClaim `json:"items"`
	Page
}

// Subscription is a card the calling chat is subscribed to.
type Subscription struct {
	Project      string    `json:"project" doc:"project slug"`
	Number       int64     `json:"number"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	SubscribedAt time.Time `json:"subscribed_at"`
}

// SubscriptionList is the response of GET /me/subscriptions: the calling chat's subscriptions to
// cards that are not done or dropped, oldest first.
type SubscriptionList struct {
	Items []Subscription `json:"items"`
}

// NoteKey identifies a note.
type NoteKey struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
}

// ProjectMention is a card of the listed project that references a note with
// [[note:...]].
type ProjectMention struct {
	Card int64   `json:"card" doc:"number of the referencing card"`
	Note NoteKey `json:"note"`
}

// ProjectMentionList is the response of GET /projects/{slug}/mentions. Items
// are in card id order and the list only goes forward.
type ProjectMentionList struct {
	Items []ProjectMention `json:"items"`
	Page
}

// MapGroup is one box of the collapsed map: a story with the status counts of
// all its children, or a lane with the counts of the cards outside any story
// that carry it.
type MapGroup struct {
	Key    string       `json:"key" doc:"names the group in MapGroupLink: story:<number>, or lane:<name> (lane: alone for cards outside any story with no lane)"`
	Kind   string       `json:"kind" enum:"story,lane"`
	Story  int64        `json:"story,omitempty" doc:"story card number; stories only"`
	Title  string       `json:"title,omitempty" doc:"story title; stories only"`
	Status string       `json:"status,omitempty" enum:"backlog,ready,working,waiting,done,dropped" doc:"story status; stories only"`
	Lane   string       `json:"lane,omitempty" doc:"lane name; empty for the group of cards with no lane"`
	Counts StatusCounts `json:"counts"`
}

// MapGroupList is the response of GET /projects/{slug}/map/groups. Groups are
// ordered stories by number, then lanes by name, and the list only goes
// forward.
type MapGroupList struct {
	Items []MapGroup `json:"items"`
	Page
}

// MapGroupLink counts the links of one kind from a card in group From to a
// card in group To. From and To are MapGroup keys.
type MapGroupLink struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Kind  string `json:"kind" enum:"blocks,relates,duplicates,supersedes,applies"`
	Count int    `json:"count"`
}

// MapGroupLinkList is the response of GET /projects/{slug}/map/group-links.
// Items are ordered by from group, to group and kind, and the list only goes
// forward.
type MapGroupLinkList struct {
	Items []MapGroupLink `json:"items"`
	Page
}

// ListAllCardsParams are the query parameters of GET /cards. The filters are
// those of the project list; the order is newest first by id, so there is no
// sort.
type ListAllCardsParams struct {
	Project string `query:"project" doc:"all (default) or a project slug"`
	CardFilter
}

// Note is a markdown page.
type Note struct {
	ID        string     `json:"id"`
	Project   *string    `json:"project,omitempty" doc:"slug"`
	Slug      string     `json:"slug"`
	Title     string     `json:"title"`
	Body      string     `json:"body,omitempty"`
	PageBytes int        `json:"page_bytes,omitempty" doc:"size of the note's HTML page; read it with GET /notes/{key}/page"`
	Status    string     `json:"status" enum:"active,archived"`
	Version   int64      `json:"version"`
	CreatedBy Actor      `json:"created_by"`
	UpdatedBy Actor      `json:"updated_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Backlinks []Backlink `json:"backlinks,omitempty"`
}

// NoteRevision is a past version of a note.
type NoteRevision struct {
	ID        string    `json:"id"`
	Version   int64     `json:"version"`
	UpdatedBy Actor     `json:"updated_by"`
	Title     string    `json:"title"`
	Body      string    `json:"body,omitempty"`
	Page      string    `json:"page,omitempty" doc:"the HTML page of that version; filled on the single revision only"`
	CreatedAt time.Time `json:"created_at" doc:"when this version was written"`
}

// CardRevision is a past version of a card's title and body.
type CardRevision struct {
	ID        string    `json:"id"`
	Version   int64     `json:"version" doc:"the card version this text belonged to"`
	UpdatedBy Actor     `json:"updated_by" doc:"who replaced this text"`
	Title     string    `json:"title"`
	Body      string    `json:"body,omitempty"`
	Page      string    `json:"page,omitempty" doc:"the HTML page of that version; filled on the single revision only"`
	CreatedAt time.Time `json:"created_at" doc:"when the text was replaced"`
}

// Backlink is something that references this entity with [[type:key]].
type Backlink struct {
	Type    string  `json:"type" enum:"card,note"`
	ID      string  `json:"id"`
	Project *string `json:"project,omitempty" doc:"project slug of the referencing entity"`
	Number  int64   `json:"number,omitempty"`
	Slug    string  `json:"slug,omitempty"`
	Title   string  `json:"title"`
}

// NoteBrief is the list-view shape.
type NoteBrief struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Project   *string   `json:"project,omitempty"`
	Status    string    `json:"status"`
	PageBytes int       `json:"page_bytes,omitempty" doc:"size of the note's HTML page; absent when it has none"`
	UpdatedBy Actor     `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateNote is the body of POST /notes.
type CreateNote struct {
	Project string `json:"project" minLength:"1" doc:"slug; use global for what applies across projects"`
	Slug    string `json:"slug" minLength:"1" maxLength:"64"`
	Title   string `json:"title" minLength:"1" maxLength:"200"`
	Body    string `json:"body,omitempty" doc:"markdown; required unless the note has a page"`
	Page    string `json:"page,omitempty" doc:"self-contained HTML page, at most 1 MiB"`
}

// UpdateNote is the body of PATCH /notes/{key}.
type UpdateNote struct {
	Version int64   `json:"version"`
	Title   *string `json:"title,omitempty"`
	Body    *string `json:"body,omitempty"`
	Status  *string `json:"status,omitempty" enum:",active,archived"`
	Page    *string `json:"page,omitempty" doc:"self-contained HTML page, at most 1 MiB; empty removes it"`
}

// HTMLPage is the HTML page of a card or a note. It is only ever sent as
// JSON, never as a document of its own.
type HTMLPage struct {
	HTML    string `json:"html"`
	Bytes   int    `json:"bytes"`
	Version int64  `json:"version" doc:"version of the card or note the page was read with"`
}

// ListNotesParams are the query parameters of GET /notes.
type ListNotesParams struct {
	Project  string `query:"project" doc:"slug; omit for all"`
	Archived bool   `query:"archived" doc:"include archived notes"`
	Query    string `query:"q"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"20"`
	Cursor   string `query:"cursor"`

	CreatedAfter  string `query:"created_after" doc:"RFC 3339; created at or after"`
	CreatedBefore string `query:"created_before" doc:"RFC 3339; created strictly before"`
	UpdatedAfter  string `query:"updated_after" doc:"RFC 3339; updated at or after"`
	UpdatedBefore string `query:"updated_before" doc:"RFC 3339; updated strictly before"`
	Author        string `query:"author" doc:"actor id or 'me'; the creator"`
}

// NoteList is a paged list.
type NoteList struct {
	Items []NoteBrief `json:"items"`
	Page
}

// SearchHit is one match from GET /search.
type SearchHit struct {
	Type    string  `json:"type" enum:"card,note"`
	ID      string  `json:"id"`
	Kind    string  `json:"kind,omitempty" doc:"cards: task, question, story or decision"`
	Number  int64   `json:"number,omitempty" doc:"cards"`
	Slug    string  `json:"slug,omitempty" doc:"notes"`
	Project *string `json:"project,omitempty"`
	Title   string  `json:"title"`
	Status  string  `json:"status,omitempty" doc:"cards: the card status word"`
	Snippet string  `json:"snippet" doc:"matching excerpt with the query terms marked"`
	Rank    float64 `json:"rank"`
}

// SearchResult is the response of GET /search.
type SearchResult struct {
	Items []SearchHit `json:"items"`
	Page
}

// Event is one append-only record. There is no stored summary: clients
// render a sentence from Type, Subject and Payload. Every payload carries
// "title" for card, note and project subjects.
type Event struct {
	ID string `json:"id"`
	// Position is the event's place in feed order. Positions compare as
	// strings in the order the feed delivers events; ids do not, because an
	// event can become visible after one with a higher id. Every event cursor
	// takes a position.
	Position  string          `json:"position"`
	Type      string          `json:"type"`
	Project   *string         `json:"project,omitempty"`
	Actor     Actor           `json:"actor"`
	Subject   Subject         `json:"subject"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	// Mine is set only in the inbox: the subject is a card the caller holds,
	// is assigned to or created.
	Mine bool `json:"mine,omitempty"`
	// Wake is set only in the inbox: the subject is a card this chat holds
	// or created, or one assigned to the caller in the chat's project that
	// no other chat of the caller holds or created, so the event should wake
	// an idle chat. For an agent's chat it is also set for a question the
	// chat may answer and for a person keeping or replacing what the chat
	// settled for them. For a human every session counts as the human's.
	Wake bool `json:"wake,omitempty"`
}

// Subject is the entity an event is about.
type Subject struct {
	Type   string `json:"type" enum:"card,note,project,agent,membership,token,lock"`
	ID     string `json:"id"`
	Number int64  `json:"number,omitempty"`
}

// EventList is a paged list of events in id order.
type EventList struct {
	Items []Event `json:"items"`
	Page
}

// Inbox is the response of GET /inbox: what others did after the cursor the
// caller sent, plus the caller's claims. The client stores Cursor and sends
// it next time; there is no server-side read position.
type Inbox struct {
	Events []Event `json:"events"`
	// Cursor is the event position to remember: the newest event examined,
	// or the one sent when nothing was found.
	Cursor string `json:"cursor"`
	// Remaining is how many events were not returned because of limit.
	Remaining int `json:"remaining"`
	// Claims lists the live claims of the calling session (every claim of a
	// human), renewed by this call.
	Claims []CardBrief `json:"claims"`
	// ClaimsOtherSessions lists the live claims of the same agent under other
	// sessions; this call did not renew them.
	ClaimsOtherSessions []CardBrief `json:"claims_other_sessions"`
	// OpenQuestions counts the questions waiting for the caller: for a human, unanswered ones it can answer; for an agent, the unanswered ones it asked.
	OpenQuestions int `json:"open_questions"`
	// Locks lists every live lock of the org, whoever holds it.
	Locks []Lock `json:"locks"`
}

// Lock is a named lease on a shared resource. Its name may equal a card lane,
// which then holds back the ready cards in that lane.
type Lock struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Holder  Actor  `json:"holder"`
	Session string `json:"session,omitempty"`
	// SessionName is what people call the holder's chat, when it has a name.
	SessionName string `json:"session_name,omitempty"`
	Purpose     string `json:"purpose"`
	// Card is the number of the card the holder works on, with Project its
	// project slug; both absent when the lock names no card.
	Card       *int64    `json:"card,omitempty"`
	Project    string    `json:"project,omitempty"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// AcquireLock is the body of PUT /locks/{name}.
type AcquireLock struct {
	Purpose    string `json:"purpose" minLength:"1" doc:"what the lock protects, 1 to 200 characters"`
	TTLSeconds int    `json:"ttl_seconds,omitempty" doc:"lease length, 60 to 3600; omitted means 600"`
	// Card is a card number, which is per project, so Project names it.
	Card    *int64 `json:"card,omitempty" doc:"number of the card the work is for; needs project"`
	Project string `json:"project,omitempty" doc:"slug of the project the card is in"`
}

// RenewLock is the body of POST /locks/{name}/renew.
type RenewLock struct {
	TTLSeconds int `json:"ttl_seconds,omitempty" doc:"new lease length from now, 60 to 3600; omitted means 600"`
}

// Agent is a registered non-human actor.
type Agent struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Harness   string    `json:"harness" enum:"other,claude-code,codex,opencode,gemini-cli,copilot-cli,cursor,pi,qwen,dsh,goose"`
	Machine   string    `json:"machine,omitempty"`
	Status    string    `json:"status" enum:"active,pending,revoked"`
	CreatedBy string    `json:"created_by" doc:"user id"`
	AgentJoin string    `json:"agent_join" enum:"follow,allow,ask" doc:"allow: joins all its owner's projects without asking; ask: always asks"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Role, Rights and Projects describe where the agent may act: an org
	// membership with org-wide rights, or rights per project. Sessions are
	// the chats seen using its token, newest first, at most 20. Filled on
	// GET /agents/{id} and on registration.
	Role     string         `json:"role,omitempty"`
	Rights   []string       `json:"rights,omitempty" enum:"work,ready,triage,answer,decide" doc:"org-wide rights"`
	Projects []ProjectRole  `json:"projects,omitempty"`
	Sessions []AgentSession `json:"sessions,omitempty"`
}

// AgentSession is one chat seen using an agent's token, by its base label (a
// subagent's -c<n> suffixes folded into its chat), with the rights its owner
// gave that chat on top of the agent's own.
type AgentSession struct {
	Label string `json:"label"`
	// Name is what people call the chat, when it has a name.
	Name       string         `json:"name,omitempty"`
	LastSeenAt *time.Time     `json:"last_seen_at,omitempty" doc:"absent for a chat that holds a grant but was never seen"`
	Grants     []SessionGrant `json:"grants,omitempty"`
}

// SessionGrant is the rights a chat was given in one project.
type SessionGrant struct {
	Project string   `json:"project" doc:"slug"`
	Rights  []string `json:"rights" enum:"work,ready,triage,answer,decide"`
	// EndsAt is when the grant ends by itself.
	EndsAt *time.Time `json:"ends_at,omitempty" doc:"absent: until taken back"`
	// Instructions are the owner's words to the chat.
	Instructions string `json:"instructions,omitempty"`
}

// AgentSessionGrantBody is the body of
// PUT /agents/{id}/sessions/{label}/projects/{slug}: the rights the chat gets
// there on top of the agent's own.
type AgentSessionGrantBody struct {
	Rights       []string   `json:"rights" enum:"work,ready,triage,answer,decide" minItems:"1"`
	EndsAt       *time.Time `json:"ends_at,omitempty" doc:"when the grant ends by itself; must be in the future. Absent: until taken back"`
	Instructions string     `json:"instructions,omitempty" maxLength:"4000" doc:"the owner's words to the chat, which lorvik status shows it"`
}

// UpdateAgent is the body of PATCH /agents/{id}. Nil means unchanged.
type UpdateAgent struct {
	AgentJoin *string `json:"agent_join,omitempty" enum:",follow,allow,ask"`
}

// JoinResult answers an agent's request to join a project: member when it
// already had rights there, joined when its owner's settings let it in, and
// pending while the owner has to decide on the dashboard. Rights are what it
// holds, or would hold once let in.
type JoinResult struct {
	Project string   `json:"project"`
	Status  string   `json:"status" enum:"member,joined,pending"`
	Rights  []string `json:"rights" enum:"work,ready,triage,answer,decide"`
	Message string   `json:"message"`
}

// AgentRequest is an agent's pending request to join a project, for its
// owner to allow or deny.
type AgentRequest struct {
	Agent       Agent     `json:"agent"`
	Project     string    `json:"project"`
	ProjectName string    `json:"project_name"`
	Rights      []string  `json:"rights" enum:"work,ready,triage,answer,decide" doc:"the rights allowing gives"`
	RequestedAt time.Time `json:"requested_at"`
}

// AgentRequestList is the caller's pending agent requests, oldest first.
type AgentRequestList struct {
	Items []AgentRequest `json:"items"`
}

// CreateAgent is the body of POST /agents.
type CreateAgent struct {
	Name    string `json:"name" minLength:"1" maxLength:"64"`
	Harness string `json:"harness,omitempty" enum:",other,claude-code,codex,opencode,gemini-cli,copilot-cli,cursor,pi,qwen,dsh,goose"`
	Machine string `json:"machine,omitempty" maxLength:"128"`
	// Rights are the rights the agent gets, none beyond what its registrant
	// holds. Default work.
	Rights []string `json:"rights,omitempty" enum:"work,ready,triage,answer,decide"`
	// Projects binds the agent to these projects. Empty with OrgWide false
	// binds it to nothing yet; empty with OrgWide true gives it the rights in
	// every org-visible project.
	Projects []string `json:"projects,omitempty" doc:"project slugs"`
	OrgWide  bool     `json:"org_wide,omitempty"`
	// TokenExpiresIn is an optional Go duration of at most 1h, the default.
	TokenExpiresIn string `json:"token_expires_in,omitempty"`
	// Auto marks the registration the CLI makes by itself for a harness from
	// its owner's login. It takes no projects or org role (the agent asks to
	// join each project), never re-creates an agent its owner revoked, and
	// stays pending when the org has admins approve agents, whoever signed in.
	Auto bool `json:"auto,omitempty"`
}

// AgentCreated returns the new agent and, once only, its secret.
type AgentCreated struct {
	Agent          Agent     `json:"agent"`
	Token          string    `json:"token" doc:"shown once; store it"`
	TokenExpiresAt time.Time `json:"token_expires_at"`
}

// Token is a credential as listed (never includes the secret).
type Token struct {
	ID         string     `json:"id"`
	Actor      Actor      `json:"actor"`
	Name       string     `json:"name,omitempty"`
	Prefix     string     `json:"prefix"`
	Scope      string     `json:"scope" enum:"read,write,signin" doc:"signin: a sign-in with no org chosen yet; it only opens /login/orgs"`
	Status     string     `json:"status" enum:"active,rotated,revoked"`
	ExpiresAt  time.Time  `json:"expires_at"`
	ReplacedBy string     `json:"replaced_by,omitempty" doc:"successor token id after rotation"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// CreateToken is the body of POST /tokens: a token for the calling user, or
// for an agent the caller manages when Agent is set.
type CreateToken struct {
	Name      string `json:"name,omitempty" maxLength:"64"`
	Scope     string `json:"scope,omitempty" enum:",read,write" doc:"default write"`
	ExpiresIn string `json:"expires_in,omitempty" doc:"Go duration; default from org settings"`
	Agent     string `json:"agent,omitempty" doc:"agent id; mints a token for that agent"`
}

// TokenCreated returns the token and, once only, its secret.
type TokenCreated struct {
	Token  Token  `json:"token"`
	Secret string `json:"secret" doc:"shown once; store it"`
}

// Login is the body of POST /login (password identities, when enabled).
type Login struct {
	Login    string `json:"login" minLength:"1"`
	Password string `json:"password" minLength:"1"`
	Org      string `json:"org,omitempty" doc:"org id to sign in to; without it the one org the user is a member of or invited to is used, and any other count answers a sign-in token"`
	Choose   bool   `json:"choose,omitempty" doc:"answer a sign-in token even when one org would follow, to choose or create an org"`
	// TokenName labels the token that is minted.
	TokenName string `json:"token_name,omitempty"`
}

// ScopeSignIn is Token.Scope of a sign-in token: the answer of a sign-in
// that settled on no org. It only opens /login/orgs.
const ScopeSignIn = "signin"

// SignInOrg is an org open to a person who signed in.
type SignInOrg struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Role   string `json:"role" enum:"viewer,member,reviewer,admin,owner"`
	Status string `json:"status" enum:"member,invited,requested" doc:"member and invited can be entered; requested waits for an admin of the org"`
}

// SignInOrgs is the answer of GET /login/orgs.
type SignInOrgs struct {
	Orgs []SignInOrg `json:"orgs"`
	// Login is the person signing in, so the Create-an-org screen can name
	// them and ask for it to confirm deleting the account.
	Login string `json:"login"`
}

// CreateOrg is the body of POST /login/orgs.
type CreateOrg struct {
	Name string `json:"name" minLength:"1" maxLength:"200"`
}

// EnterOrg is the body of POST /login/orgs/{id}/token.
type EnterOrg struct {
	// TokenName labels the token that is minted.
	TokenName string `json:"token_name,omitempty" maxLength:"64"`
}

// MaxSession is the longest session label.
const MaxSession = 40

// ValidSession reports whether s is a usable session label: 1 to MaxSession
// characters, lowercase letters, digits and hyphens.
func ValidSession(s string) bool {
	if s == "" || len(s) > MaxSession {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// GitHubAuth is the answer of GET /auth/github: whether GitHub sign-in is on
// and, when it is, the OAuth app's client id for the CLI's device flow.
type GitHubAuth struct {
	Enabled  bool   `json:"enabled"`
	ClientID string `json:"client_id,omitempty"`
}

// GitHubLinkStart is the optional body of POST /me/github-link.
type GitHubLinkStart struct {
	Next string `json:"next,omitempty" doc:"same-origin path to return to; defaults to /admin"`
}

// GitHubLink is the answer of POST /me/github-link: where to send the browser
// to link a GitHub account to the signed-in user.
type GitHubLink struct {
	URL string `json:"url"`
}

// GitHubLogin is the body of POST /login/github: a GitHub user access token
// obtained by the CLI through GitHub's device flow.
type GitHubLogin struct {
	AccessToken string `json:"access_token" minLength:"1" maxLength:"1024"`
	Org         string `json:"org,omitempty" doc:"org id to sign in to; a user who is no member of it leaves a request to join. Without it the one org the user is a member of or invited to is used, and any other count answers a sign-in token"`
	Choose      bool   `json:"choose,omitempty" doc:"answer a sign-in token even when one org would follow, to choose or create an org"`
	// TokenName labels the token that is minted.
	TokenName string `json:"token_name,omitempty"`
}

// Invite is the body of POST /members/invite: a pending membership with the
// role set, for a GitHub user who has not signed in yet.
type Invite struct {
	Login string `json:"login" minLength:"1" doc:"GitHub login"`
	Role  string `json:"role" enum:"viewer,member,reviewer,admin,owner"`
}
