package domain

import (
	"fmt"
	"slices"
	"strings"
)

// Every enumeration is stored as a smallint and travels as its name on the
// wire. Numbering follows one rule: negative is terminal, zero is initial or
// pending, positive is alive, and larger means further along. Gaps of a few
// values are left where a state could plausibly be added later.

// names maps the values of one enumeration to their wire names, in the
// order the values are listed; that order is the display order.
type names[T ~int16] struct {
	what   string
	values []T
	labels []string
}

func (n names[T]) name(v T) string {
	if i := slices.Index(n.values, v); i >= 0 {
		return n.labels[i]
	}
	return fmt.Sprintf("%s(%d)", n.what, int16(v))
}

func (n names[T]) parse(s string) (T, error) {
	if i := slices.Index(n.labels, s); i >= 0 {
		return n.values[i], nil
	}
	var zero T
	return zero, invalid(n.what, "must be one of: "+joinLabels(n.labels))
}

func (n names[T]) valid(v T) bool { return slices.Contains(n.values, v) }

func joinLabels(labels []string) string {
	var out strings.Builder
	for i, l := range labels {
		if i > 0 {
			out.WriteString(", ")
		}
		out.WriteString(l)
	}
	return out.String()
}

// ActorKind distinguishes humans from agents. Both are first-class.
type ActorKind int16

// ActorKind values.
const (
	ActorUser  ActorKind = 1
	ActorAgent ActorKind = 2
)

var actorKinds = names[ActorKind]{"kind", []ActorKind{ActorUser, ActorAgent}, []string{"user", "agent"}}

func (k ActorKind) String() string { return actorKinds.name(k) }

// ParseActorKind reads "user" or "agent".
func ParseActorKind(s string) (ActorKind, error) { return actorKinds.parse(s) }

// CardKind says what a card is. Questions are cards so links, events and the
// CLI work unchanged; they add choices and an answer. A story groups the
// cards of one piece of work and closes by folding them into the requirements
// documents; a decision records a choice and follows its own transitions.
type CardKind int16

// CardKind values.
const (
	KindTask     CardKind = 1
	KindQuestion CardKind = 2
	KindStory    CardKind = 3
	KindDecision CardKind = 4
)

var cardKinds = names[CardKind]{"kind", []CardKind{KindTask, KindQuestion, KindStory, KindDecision}, []string{"task", "question", "story", "decision"}}

func (k CardKind) String() string { return cardKinds.name(k) }

// ParseCardKind reads a card kind name.
func ParseCardKind(s string) (CardKind, error) { return cardKinds.parse(s) }

// Status is the card state. Negative is closed, zero is backlog, positive is
// actionable; higher means further along.
type Status int16

// Status values.
const (
	StatusDropped Status = -20
	StatusDone    Status = -10
	StatusBacklog Status = 0
	StatusReady   Status = 5
	StatusWaiting Status = 7
	StatusWorking Status = 10
)

var statuses = names[Status]{"status",
	[]Status{StatusBacklog, StatusReady, StatusWaiting, StatusWorking, StatusDone, StatusDropped},
	[]string{"backlog", "ready", "waiting", "working", "done", "dropped"}}

// AllStatuses in display order.
var AllStatuses = statuses.values

func (s Status) String() string { return statuses.name(s) }

// ParseStatus reads a status name.
func ParseStatus(str string) (Status, error) { return statuses.parse(str) }

// Terminal reports whether no further work happens in this status.
func (s Status) Terminal() bool { return s < 0 }

// Open is the opposite of Terminal.
func (s Status) Open() bool { return s >= 0 }

// DropReason is required when a card is dropped. Rejected, revoked and
// superseded exist only on decisions; superseded is set by the system.
type DropReason int16

// DropReason values. Zero means not dropped.
const (
	DropNone       DropReason = 0
	DropObsolete   DropReason = 1
	DropDuplicate  DropReason = 2
	DropWontFix    DropReason = 3
	DropRejected   DropReason = 4
	DropRevoked    DropReason = 5
	DropSuperseded DropReason = 6
)

var dropReasons = names[DropReason]{"drop_reason",
	[]DropReason{DropObsolete, DropDuplicate, DropWontFix, DropRejected, DropRevoked, DropSuperseded},
	[]string{"obsolete", "duplicate", "wontfix", "rejected", "revoked", "superseded"}}

// DecisionOnly reports whether the reason applies to decisions alone.
func (r DropReason) DecisionOnly() bool { return r >= DropRejected }

func (r DropReason) String() string {
	if r == DropNone {
		return ""
	}
	return dropReasons.name(r)
}

// ParseDropReason reads a reason name; "" is DropNone.
func ParseDropReason(s string) (DropReason, error) {
	if s == "" {
		return DropNone, nil
	}
	return dropReasons.parse(s)
}

// Priority orders the ready queue. Higher is more urgent.
type Priority int16

// Priority values.
const (
	PriorityLow    Priority = 0
	PriorityNormal Priority = 1
	PriorityHigh   Priority = 2
	PriorityUrgent Priority = 3
)

var priorities = names[Priority]{"priority", []Priority{PriorityLow, PriorityNormal, PriorityHigh, PriorityUrgent}, []string{"low", "normal", "high", "urgent"}}

func (p Priority) String() string { return priorities.name(p) }

// ParsePriority reads a priority name.
func ParsePriority(s string) (Priority, error) { return priorities.parse(s) }

// CardLinkKind relates two cards.
type CardLinkKind int16

// CardLinkKind values.
const (
	LinkBlocks     CardLinkKind = 1
	LinkRelates    CardLinkKind = 2
	LinkDuplicates CardLinkKind = 3
	LinkSupersedes CardLinkKind = 4
	LinkApplies    CardLinkKind = 5
)

var cardLinkKinds = names[CardLinkKind]{"kind",
	[]CardLinkKind{LinkBlocks, LinkRelates, LinkDuplicates, LinkSupersedes, LinkApplies},
	[]string{"blocks", "relates", "duplicates", "supersedes", "applies"}}

func (k CardLinkKind) String() string { return cardLinkKinds.name(k) }

// ParseCardLinkKind reads a link kind name.
func ParseCardLinkKind(s string) (CardLinkKind, error) { return cardLinkKinds.parse(s) }

// RecordStatus is the lifecycle shared by orgs, users, projects, notes,
// agents, memberships and tokens: alive, pending, or gone.
type RecordStatus int16

// RecordStatus values. Not every record uses every value; the doc comment on
// each type says which apply.
const (
	RecordForgotten RecordStatus = -20 // agents: removed for good; the row stays so its past work keeps a name
	RecordDeleted   RecordStatus = -10 // orgs, users: deleted
	RecordArchived  RecordStatus = -10 // projects, notes: archived
	RecordRevoked   RecordStatus = -10 // agents, tokens: revoked
	RecordRemoved   RecordStatus = -10 // memberships: removed
	RecordSuspended RecordStatus = -5  // orgs, users: suspended
	RecordPending   RecordStatus = 0   // agents and memberships: awaiting approval or an invite
	RecordRotated   RecordStatus = 5   // tokens: replaced, still valid during the grace period
	RecordActive    RecordStatus = 10
)

// Alive reports whether the record is usable.
func (s RecordStatus) Alive() bool { return s > 0 }

// Provider is where a user's identity comes from.
type Provider int16

// Provider values.
const (
	ProviderGitHub    Provider = 1
	ProviderPassword  Provider = 2
	ProviderGoogle    Provider = 3
	ProviderMicrosoft Provider = 4
	ProviderPasskey   Provider = 5
)

var providers = names[Provider]{"provider",
	[]Provider{ProviderGitHub, ProviderPassword, ProviderGoogle, ProviderMicrosoft, ProviderPasskey},
	[]string{"github", "password", "google", "microsoft", "passkey"}}

func (p Provider) String() string { return providers.name(p) }

// ParseProvider reads a provider name.
func ParseProvider(s string) (Provider, error) { return providers.parse(s) }

// Harness is the agent runtime, recorded for display and support.
type Harness int16

// Harness values. Other is the escape hatch for runtimes not listed.
const (
	HarnessOther      Harness = 0
	HarnessClaudeCode Harness = 1
	HarnessCodex      Harness = 2
	HarnessOpenCode   Harness = 3
	HarnessGeminiCLI  Harness = 4
	HarnessCopilotCLI Harness = 5
	HarnessCursor     Harness = 6
	HarnessPi         Harness = 7
	HarnessQwen       Harness = 8
	HarnessDsh        Harness = 9
	HarnessGoose      Harness = 10
)

var harnesses = names[Harness]{"harness",
	[]Harness{HarnessOther, HarnessClaudeCode, HarnessCodex, HarnessOpenCode, HarnessGeminiCLI, HarnessCopilotCLI, HarnessCursor,
		HarnessPi, HarnessQwen, HarnessDsh, HarnessGoose},
	[]string{"other", "claude-code", "codex", "opencode", "gemini-cli", "copilot-cli", "cursor", "pi", "qwen", "dsh", "goose"}}

func (h Harness) String() string { return harnesses.name(h) }

// ParseHarness reads a harness name.
func ParseHarness(s string) (Harness, error) { return harnesses.parse(s) }

// AgentJoin is one say over whether an agent joins a project without its
// owner being asked: the owner's account switch, the agent's own setting,
// the owner's setting for one project, and a project admin's setting.
// Always ask from any say that applies wins, then allow from any; with
// neither, the owner is asked.
type AgentJoin int16

// AgentJoin values. Follow leaves the decision to the other says.
const (
	AgentJoinFollow AgentJoin = 0
	AgentJoinAllow  AgentJoin = 10
	AgentJoinAsk    AgentJoin = 20
)

var agentJoins = names[AgentJoin]{"agent_join", []AgentJoin{AgentJoinFollow, AgentJoinAllow, AgentJoinAsk}, []string{"follow", "allow", "ask"}}

func (j AgentJoin) String() string { return agentJoins.name(j) }

// ParseAgentJoin reads "follow", "allow" or "ask".
func ParseAgentJoin(s string) (AgentJoin, error) { return agentJoins.parse(s) }

// The says each level may give: the account switch only allows, a project
// admin only asks, an agent's setting and an owner's rule for one project
// do either.
var (
	accountAgentJoins = []AgentJoin{AgentJoinFollow, AgentJoinAllow}
	projectAgentJoins = []AgentJoin{AgentJoinFollow, AgentJoinAsk}
)

func validAgentJoin(j AgentJoin, allowed []AgentJoin) error {
	if slices.Contains(allowed, j) {
		return nil
	}
	labels := make([]string, len(allowed))
	for i, a := range allowed {
		labels[i] = a.String()
	}
	return invalid("agent_join", "must be one of: "+joinLabels(labels))
}

// DecideAgentJoin weighs the says that apply to one agent joining one
// project and returns AgentJoinAllow or AgentJoinAsk.
func DecideAgentJoin(says ...AgentJoin) AgentJoin {
	switch {
	case slices.Contains(says, AgentJoinAsk):
		return AgentJoinAsk
	case slices.Contains(says, AgentJoinAllow):
		return AgentJoinAllow
	}
	return AgentJoinAsk
}

// Visibility says who in the org may see a project.
type Visibility int16

// Visibility values.
const (
	VisibilityOrg     Visibility = 10 // every org member
	VisibilityPrivate Visibility = 20 // project members, admins and owners only
)

var visibilities = names[Visibility]{"visibility", []Visibility{VisibilityOrg, VisibilityPrivate}, []string{"org", "private"}}

func (v Visibility) String() string { return visibilities.name(v) }

// ParseVisibility reads "org" or "private".
func ParseVisibility(s string) (Visibility, error) { return visibilities.parse(s) }

// Concurrency says whether a lead fans a project's child cards out to
// parallel workers or works them in order.
type Concurrency int16

// Concurrency values.
const (
	ConcurrencyParallel Concurrency = 10 // fan out when the harness can
	ConcurrencySerial   Concurrency = 20 // one card at a time, in order
)

var concurrencies = names[Concurrency]{"concurrency", []Concurrency{ConcurrencyParallel, ConcurrencySerial}, []string{"parallel", "serial"}}

func (c Concurrency) String() string { return concurrencies.name(c) }

// ParseConcurrency reads "parallel" or "serial".
func ParseConcurrency(s string) (Concurrency, error) { return concurrencies.parse(s) }

// ProjectScope says whether a project is an ordinary project or the org's
// global project, which holds what applies across projects.
type ProjectScope int16

// ProjectScope values.
const (
	ProjectScopeProject ProjectScope = 1
	ProjectScopeGlobal  ProjectScope = 2
)

var projectScopes = names[ProjectScope]{"scope", []ProjectScope{ProjectScopeProject, ProjectScopeGlobal}, []string{"project", "global"}}

func (s ProjectScope) String() string { return projectScopes.name(s) }

// ParseProjectScope reads "project" or "global".
func ParseProjectScope(s string) (ProjectScope, error) { return projectScopes.parse(s) }

// TokenScope limits what a token may do on behalf of its actor.
type TokenScope int16

// TokenScope values.
const (
	// ScopeSignIn is a person who signed in and has no org chosen yet. The
	// token belongs to no org and opens only the routes that list, create
	// and enter orgs.
	ScopeSignIn TokenScope = 5
	ScopeRead   TokenScope = 10 // read only
	ScopeWrite  TokenScope = 20 // everything the actor's role allows
)

// ScopeSignInName is ScopeSignIn on the wire.
const ScopeSignInName = "signin"

// tokenScopes are the scopes a caller may ask for; ScopeSignIn is only ever
// given by a sign-in, so it is left out and cannot be parsed.
var tokenScopes = names[TokenScope]{"scope", []TokenScope{ScopeRead, ScopeWrite}, []string{"read", "write"}}

func (s TokenScope) String() string {
	if s == ScopeSignIn {
		return ScopeSignInName
	}
	return tokenScopes.name(s)
}

// ParseTokenScope reads "read" or "write".
func ParseTokenScope(str string) (TokenScope, error) { return tokenScopes.parse(str) }

// SubjectType is the entity an event is about.
type SubjectType int16

// SubjectType values.
const (
	SubjectCard       SubjectType = 1
	SubjectNote       SubjectType = 3
	SubjectProject    SubjectType = 4
	SubjectAgent      SubjectType = 5
	SubjectMembership SubjectType = 6
	SubjectToken      SubjectType = 7
	SubjectLock       SubjectType = 8
)

var subjectTypes = names[SubjectType]{"subject_type",
	[]SubjectType{SubjectCard, SubjectNote, SubjectProject, SubjectAgent, SubjectMembership, SubjectToken, SubjectLock},
	[]string{"card", "note", "project", "agent", "membership", "token", "lock"}}

func (t SubjectType) String() string { return subjectTypes.name(t) }

// ParseSubjectType reads a subject type name.
func ParseSubjectType(s string) (SubjectType, error) { return subjectTypes.parse(s) }

// RefType is what a [[type:key]] reference points at. It shares values with
// SubjectType for the two referenceable entities.
type RefType = SubjectType

// RefType values.
const (
	RefCard RefType = SubjectCard
	RefNote RefType = SubjectNote
)
