package domain

import (
	"crypto/rand"
	"crypto/sha3"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ActorRef identifies who did something: a user or an agent, by id. Names are
// never stored beside it; clients resolve them.
type ActorRef struct {
	Kind ActorKind
	ID   ID
}

// IsZero reports whether the reference is unset.
func (a ActorRef) IsZero() bool { return a.ID == 0 }

// SessionScoped reports whether the actor's claims belong to the session that
// took them. An agent token is shared by every chat that runs under it; a
// human owns their claims in any session.
func (a ActorRef) SessionScoped() bool { return a.Kind == ActorAgent }

// OrgSettings are the per-org toggles, stored as one JSON document and read
// whole. Zero values mean "use the default" (see Defaults).
type OrgSettings struct {
	// AgentApproval is "auto" (default) or "admin": whether an agent registered
	// by a member starts active or pending.
	AgentApproval string `json:"agent_approval,omitempty"`
	// TokenTTLDefault and TokenTTLMax are Go durations ("720h").
	TokenTTLDefault string `json:"token_ttl_default,omitempty"`
	TokenTTLMax     string `json:"token_ttl_max,omitempty"`
	// ProjectVisibilityDefault is "org" (default) or "private".
	ProjectVisibilityDefault string `json:"project_visibility_default,omitempty"`
}

// Default values for OrgSettings.
const (
	DefaultTokenTTL     = 30 * 24 * time.Hour
	MaxTokenTTL         = 365 * 24 * time.Hour
	MinTokenTTL         = time.Hour
	AgentApprovalAuto   = "auto"
	AgentApprovalAdmin  = "admin"
	TokenRotationWindow = 7 * 24 * time.Hour // a token used this close to expiry is replaced
	TokenRotationGrace  = 24 * time.Hour     // how long the replaced token still works
	TokenNoExpiryYear   = 9999               // expires_at for tokens created with --no-expiry
	// AgentTokenTTL is the longest an agent token lives. The CLI renews it
	// from the owner's login, so a leaked one is dead within the hour.
	AgentTokenTTL = time.Hour
	// SignInTokenTTL is how long a person has to choose or create an org
	// after signing in.
	SignInTokenTTL = 10 * time.Minute
)

// TokenTTLDefaultOrDefault returns the org's default token lifetime.
func (s OrgSettings) TokenTTLDefaultOrDefault() time.Duration {
	if d, err := time.ParseDuration(s.TokenTTLDefault); err == nil && d > 0 {
		return d
	}
	return DefaultTokenTTL
}

// TokenTTLMaxOrDefault returns the longest token lifetime the org allows.
func (s OrgSettings) TokenTTLMaxOrDefault() time.Duration {
	if d, err := time.ParseDuration(s.TokenTTLMax); err == nil && d > 0 {
		return d
	}
	return MaxTokenTTL
}

// AgentsNeedApproval reports whether member-registered agents start pending.
func (s OrgSettings) AgentsNeedApproval() bool { return s.AgentApproval == AgentApprovalAdmin }

// ProjectVisibilityOrDefault returns the visibility new projects get.
func (s OrgSettings) ProjectVisibilityOrDefault() Visibility {
	if v, err := ParseVisibility(s.ProjectVisibilityDefault); err == nil {
		return v
	}
	return VisibilityOrg
}

// Validate checks the settings document.
func (s OrgSettings) Validate() error {
	switch s.AgentApproval {
	case "", AgentApprovalAuto, AgentApprovalAdmin:
	default:
		return invalid("agent_approval", "must be auto or admin")
	}
	for field, v := range map[string]string{"token_ttl_default": s.TokenTTLDefault, "token_ttl_max": s.TokenTTLMax} {
		if v == "" {
			continue
		}
		d, err := time.ParseDuration(v)
		if err != nil || d < MinTokenTTL || d > MaxTokenTTL {
			return invalid(field, "must be a duration between 1h and 8760h")
		}
	}
	if s.ProjectVisibilityDefault != "" {
		if _, err := ParseVisibility(s.ProjectVisibilityDefault); err != nil {
			return err
		}
	}
	return nil
}

// Org is the tenant. Status uses RecordActive, RecordPending (created, not
// yet verified), RecordSuspended and RecordDeleted.
type Org struct {
	ID        ID
	Name      string
	Status    RecordStatus
	Settings  OrgSettings
	CreatedAt time.Time
	UpdatedAt time.Time
}

// User is a human. Users exist outside any org and join orgs through
// memberships; they log in through identities. Status uses RecordActive,
// RecordSuspended and RecordDeleted.
type User struct {
	ID        ID
	Login     string // display handle
	Name      string
	Email     string
	AvatarURL string
	Locale    string // BCP 47, for formatting; "" until the user sets one
	Timezone  string // IANA, for displaying times; "" means the client's own zone
	Status    RecordStatus
	// AgentJoin is the account switch: allow lets the user's agents join
	// the user's projects without asking, follow leaves it to the other says.
	AgentJoin AgentJoin
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Ref returns the actor reference for a user.
func (u User) Ref() ActorRef { return ActorRef{Kind: ActorUser, ID: u.ID} }

// Validate checks the fields a user may edit.
func (u User) Validate() error {
	if strings.TrimSpace(u.Login) == "" {
		return invalid("login", "required")
	}
	if len(u.Login) > 64 {
		return invalid("login", "at most 64 characters")
	}
	if u.Timezone != "" {
		if _, err := time.LoadLocation(u.Timezone); err != nil {
			return invalid("timezone", "must be an IANA zone such as Asia/Kolkata")
		}
	}
	if len(u.Locale) > 16 {
		return invalid("locale", "must be a BCP 47 tag such as en or de-DE")
	}
	return validAgentJoin(u.AgentJoin, accountAgentJoins)
}

// Identity is one way a user can log in. ProviderID is the provider's
// permanent id (GitHub's numeric user id, a lowercased email for passwords,
// a credential id for passkeys). Secret holds what the provider needs
// verified locally: an argon2id hash for passwords, a public key for
// passkeys; nil for OAuth providers.
type Identity struct {
	ID          ID
	UserID      ID
	Provider    Provider
	ProviderID  string
	Secret      []byte
	Login       string // provider-side handle at link time
	LastLoginAt *time.Time
	CreatedAt   time.Time
}

// Membership is an actor's role in an org. Both users and agents are
// principals here: an agent with a membership row is org-wide. Status uses
// RecordActive, RecordPending (invited, or an agent awaiting approval) and
// RecordRemoved.
type Membership struct {
	OrgID     ID
	Actor     ActorRef
	Role      Role
	Status    RecordStatus
	Rights    Rights // an agent's rights; 0 for people
	GrantedBy *ID    // user id; nil for the bootstrap owner and for a request made by signing in with GitHub
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ProjectMember is an actor's role in one project. Same shape as Membership;
// admin and owner are not allowed here (those are org roles).
type ProjectMember struct {
	ProjectID ID
	Actor     ActorRef
	Role      Role
	Status    RecordStatus
	Rights    Rights // an agent's rights; 0 for people
	GrantedBy *ID    // user id
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate checks a project membership.
func (m ProjectMember) Validate() error {
	if !m.Role.Valid() || m.Role > RoleReviewer {
		return invalid("role", "project roles go up to reviewer; admin and owner are org roles")
	}
	if m.Actor.Kind == ActorAgent && m.Role != RoleViewer {
		return invalid("role", "an agent's role is viewer; its rights carry the rest")
	}
	if !m.Rights.Valid() {
		return invalid("rights", "unknown rights")
	}
	if m.Actor.Kind == ActorUser && m.Rights != 0 {
		return invalid("rights", "only agents carry rights")
	}
	return nil
}

// AgentJoinRule is a user's own say over their agents joining one project;
// no rule is follow.
type AgentJoinRule struct {
	ProjectID ID
	UserID    ID
	AgentJoin AgentJoin // allow or ask
	UpdatedAt time.Time
}

// Validate checks a stored rule: follow is the absence of one.
func (r AgentJoinRule) Validate() error {
	return validAgentJoin(r.AgentJoin, []AgentJoin{AgentJoinAllow, AgentJoinAsk})
}

// Validate checks an org membership.
func (m Membership) Validate() error {
	if !m.Role.Valid() {
		return invalid("role", "unknown role")
	}
	if m.Actor.Kind == ActorAgent && m.Role != RoleViewer {
		return invalid("role", "an agent's role is viewer; its rights carry the rest")
	}
	if !m.Rights.Valid() {
		return invalid("rights", "unknown rights")
	}
	if m.Actor.Kind == ActorUser && m.Rights != 0 {
		return invalid("rights", "only agents carry rights")
	}
	return nil
}

// Agent is a non-human actor registered in one org. Where it may act is
// decided by its memberships and project memberships, not by this row.
// Status uses RecordActive, RecordPending (awaiting approval), RecordRevoked
// (stopped, can be restored) and RecordForgotten (removed for good).
type Agent struct {
	ID        ID
	OrgID     ID
	Name      string
	Harness   Harness
	Machine   string
	CreatedBy ID // the owner's user id; the agent acts only within the owner's current rights
	Status    RecordStatus
	AgentJoin AgentJoin // follow, allow in all the owner's projects, or always ask
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Ref returns the actor reference for an agent.
func (a Agent) Ref() ActorRef { return ActorRef{Kind: ActorAgent, ID: a.ID} }

// Validate checks an agent before registration.
func (a Agent) Validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return invalid("name", "required")
	}
	if len(a.Name) > 64 {
		return invalid("name", "at most 64 characters")
	}
	if !harnesses.valid(a.Harness) {
		return invalid("harness", "must be one of: "+joinLabels(harnesses.labels))
	}
	return validAgentJoin(a.AgentJoin, agentJoins.values)
}

// BaseSession strips the subagent suffixes from a session label: subagents
// carry <label>-c<n>, so "mellow-comet-020d-c271-c3" belongs to the chat
// "mellow-comet-020d". Stripping stops once what remains has the shape of a
// generated label, <word>-<word>-<4 hex>, whose hex may itself read as a
// suffix: "witty-lynx-c123" is a chat, not "witty-lynx" plus child 123.
func BaseSession(label string) string {
	for !generatedSession(label) {
		i := strings.LastIndex(label, "-c")
		if i < 0 {
			return label
		}
		digits := label[i+2:]
		if digits == "" || strings.Trim(digits, "0123456789") != "" {
			return label
		}
		label = label[:i]
	}
	return label
}

// generatedSession reports whether label has the shape the CLI generates:
// two lowercase words and four lowercase hex digits.
func generatedSession(label string) bool {
	parts := strings.Split(label, "-")
	if len(parts) != 3 || len(parts[2]) != 4 {
		return false
	}
	isWord := func(r rune) bool { return r < 'a' || r > 'z' }
	isHex := func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') }
	return parts[0] != "" && parts[1] != "" &&
		strings.IndexFunc(parts[0], isWord) < 0 && strings.IndexFunc(parts[1], isWord) < 0 &&
		strings.IndexFunc(parts[2], isHex) < 0
}

// AgentSession is a chat seen using an agent's token, kept so the owner can
// grant that session rights. Session is a base label. Name is what people
// call the chat, for display only: nothing is keyed by it.
type AgentSession struct {
	AgentID    ID
	Session    string
	Name       string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// MaxSessionName is the longest chat name kept, in characters.
const MaxSessionName = 60

// CleanSessionName makes a chat name fit to store and show: invalid UTF-8
// and control characters become a space, format characters (zero-width ones
// and direction overrides, which would hide a name or reorder the text
// around it) are dropped, runs of white space become one space, and the
// rest is cut to MaxSessionName characters. A name is never refused, since
// it comes from whatever a person typed into their harness.
func CleanSessionName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r), r == utf8.RuneError:
			return ' '
		case unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, " "))
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > MaxSessionName {
		s = strings.TrimSpace(string(r[:MaxSessionName]))
	}
	return s
}

// MaxGrantInstructions bounds the text an owner leaves a chat with its grant.
const MaxGrantInstructions = 4000

// AgentSessionGrant is the rights an agent's owner gave one of its sessions
// in one project. Status uses RecordActive and RecordRemoved. A grant with
// EndsAt gives nothing from that moment on, whether or not its row was
// ended yet.
type AgentSessionGrant struct {
	ProjectID    ID
	AgentID      ID
	Session      string // a base label, see BaseSession
	Rights       Rights
	GrantedBy    ID         // user id
	EndsAt       *time.Time // nil: until taken back
	Instructions string     // the owner's words to the chat; empty for none
	Status       RecordStatus
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Live reports whether the grant gives its rights at now: not taken back and
// not past its end.
func (g AgentSessionGrant) Live(now time.Time) bool {
	return g.Status.Alive() && (g.EndsAt == nil || now.Before(*g.EndsAt))
}

// Validate checks a grant before it is stored.
func (g AgentSessionGrant) Validate() error {
	if g.Session == "" || g.Session != BaseSession(g.Session) {
		return invalid("session", "must be a session label without subagent suffixes")
	}
	if !g.Rights.Valid() || g.Rights == 0 {
		return invalid("rights", "must name at least one known right")
	}
	if utf8.RuneCountInString(g.Instructions) > MaxGrantInstructions {
		return invalid("instructions", fmt.Sprintf("must be at most %d characters", MaxGrantInstructions))
	}
	if !g.Status.Alive() && g.Status != RecordRemoved {
		return invalid("status", "must be alive or removed")
	}
	return nil
}

// Token is an API credential bound to one actor. Only the hash is stored.
// Status uses RecordActive, RecordRotated (replaced, valid until ExpiresAt
// which was moved to the end of the grace period) and RecordRevoked.
type Token struct {
	ID         ID
	OrgID      ID
	Actor      ActorRef
	Name       string
	Prefix     string // first characters of the secret, for identification in lists
	Hash       []byte // SHA3-384 of the full secret
	Scope      TokenScope
	Status     RecordStatus
	ExpiresAt  time.Time
	ReplacedBy *ID // successor after rotation
	LastUsedAt *time.Time
	CreatedAt  time.Time
}

// TokenPrefix starts every Lorvik secret so leaked tokens are recognisable
// by scanners.
const TokenPrefix = "lvk_"

// TokenPrefixLen is how much of a secret is stored and listed as its prefix.
const TokenPrefixLen = 12

// NewTokenSecret generates a fresh secret and its storable hash. The secret is
// shown to the caller once and never stored.
func NewTokenSecret(kind ActorKind) (secret, prefix string, hash []byte) {
	secret = TokenPrefix + kind.String()[:1] + "_" + rand.Text()
	return secret, secret[:TokenPrefixLen], HashToken(secret)
}

// NewToken builds an active token for actor, expiring ttl after now, and
// returns it with its secret.
func NewToken(id, orgID ID, actor ActorRef, name string, scope TokenScope, ttl time.Duration, now time.Time) (Token, string) {
	secret, prefix, hash := NewTokenSecret(actor.Kind)
	return Token{ID: id, OrgID: orgID, Actor: actor, Name: name, Prefix: prefix, Hash: hash, Scope: scope,
		Status: RecordActive, ExpiresAt: now.Add(ttl), CreatedAt: now}, secret
}

// HashToken returns the SHA3-384 of a secret, the hash every server-made
// random secret is stored under. Constant-time comparison is unnecessary
// because lookups are by hash, not by comparing secrets.
func HashToken(secret string) []byte {
	sum := sha3.Sum384([]byte(secret))
	return sum[:]
}

// LooksLikeToken reports whether s has the Lorvik secret shape, so the CLI can
// give a clear error before making a request.
func LooksLikeToken(s string) bool {
	return strings.HasPrefix(s, TokenPrefix) && len(s) > len(TokenPrefix)+2
}

// Usable reports whether the token may authenticate at the given time.
func (t Token) Usable(now time.Time) bool {
	return t.Status.Alive() && now.Before(t.ExpiresAt)
}

// OutlivesAgentLimit reports an agent token issued to live longer than
// AgentTokenTTL, such as one minted before the limit existed. It never
// authenticates.
func (t Token) OutlivesAgentLimit() bool {
	return t.Actor.Kind == ActorAgent && t.ExpiresAt.Sub(t.CreatedAt) > AgentTokenTTL
}

// NeedsRotation reports whether a use at now should mint a successor: the
// token is active and inside its last rotation window. Tokens whose whole
// lifetime is under two windows are short-lived on purpose and just expire.
func (t Token) NeedsRotation(now time.Time) bool {
	if t.Status != RecordActive || t.ExpiresAt.Year() >= TokenNoExpiryYear {
		return false
	}
	return t.ExpiresAt.Sub(t.CreatedAt) >= 2*TokenRotationWindow && t.ExpiresAt.Sub(now) < TokenRotationWindow
}
