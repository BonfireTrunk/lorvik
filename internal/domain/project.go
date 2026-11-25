package domain

import (
	"regexp"
	"time"
)

// Project groups cards and notes. A repo binds to one project
// through .lorvik.toml. Status uses RecordActive and RecordArchived.
type Project struct {
	ID          ID
	OrgID       ID
	Slug        string
	Name        string
	Description string
	Visibility  Visibility
	// Concurrency is the project's policy for working child cards.
	Concurrency Concurrency
	// Scope is ProjectScopeGlobal for the org's one global project.
	Scope ProjectScope
	// Lease is how long a claim lives without renewal.
	Lease          time.Duration
	NextCardNumber int64
	CreatedBy      ActorRef
	Status         RecordStatus
	// AgentJoin is follow, or ask when a project admin wants every agent
	// asked in before it joins.
	AgentJoin AgentJoin
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GlobalSlug is the slug of every org's global project; no other project may
// use it.
const GlobalSlug = "global"

// Lease bounds.
const (
	DefaultLeaseDuration = 30 * time.Minute
	MinLeaseDuration     = time.Minute
	MaxLeaseDuration     = 24 * time.Hour
)

var slugRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// ValidLease checks the bounds every lease obeys: a project's, and the one
// a chat states for a single claim.
func ValidLease(d time.Duration) error {
	if d < MinLeaseDuration || d > MaxLeaseDuration {
		return invalid("lease", "must be between 1m and 24h")
	}
	return nil
}

// ValidateSlug checks the shape shared by project and note slugs.
func ValidateSlug(field, s string) error {
	if !slugRE.MatchString(s) {
		return invalid(field, "must be 1 to 64 lowercase letters, digits or hyphens, not starting or ending with a hyphen")
	}
	return nil
}

// ValidateProjectSlug is ValidateSlug plus the reservation of GlobalSlug,
// which only the project created with the org may carry.
func ValidateProjectSlug(s string) error {
	if err := ValidateSlug("slug", s); err != nil {
		return err
	}
	if s == GlobalSlug {
		return invalid("slug", `"global" is reserved for the org's global project`)
	}
	return nil
}

// Validate checks a project before create or update.
func (p Project) Validate() error {
	if p.Scope == ProjectScopeGlobal {
		if p.Slug != GlobalSlug {
			return invalid("slug", `the global project's slug is "global"`)
		}
	} else if err := ValidateProjectSlug(p.Slug); err != nil {
		return err
	}
	if p.Name == "" {
		return invalid("name", "required")
	}
	if err := ValidLease(p.Lease); err != nil {
		return err
	}
	if !visibilities.valid(p.Visibility) {
		return invalid("visibility", "must be org or private")
	}
	if !concurrencies.valid(p.Concurrency) {
		return invalid("concurrency", "must be parallel or serial")
	}
	return validAgentJoin(p.AgentJoin, projectAgentJoins)
}

// Standing is what an actor holds that decides their view of one project:
// the org role, the role in that project, and whether they belong to any
// project of the org (InAnyProject), which only the global project asks.
type Standing struct {
	OrgRole        Role
	HasOrgRole     bool
	ProjectRole    Role
	HasProjectRole bool
	InAnyProject   bool
}

// seesEveryProject reports an org role that sees past visibility.
func (s Standing) seesEveryProject() bool { return s.HasOrgRole && s.OrgRole.SeesEveryProject() }

// Visible reports whether an actor with standing s may see the project.
// Admins and owners see everything; an org-visible project is open to anyone
// with any role in the org or the project; a private project needs a project
// role. The global project is open to anyone with an org role or a role in
// any project of the org.
func (p Project) Visible(s Standing) bool {
	if p.Scope == ProjectScopeGlobal {
		return s.HasOrgRole || s.InAnyProject
	}
	if s.seesEveryProject() {
		return true
	}
	switch p.Visibility {
	case VisibilityPrivate:
		return s.HasProjectRole
	default:
		return s.HasOrgRole || s.HasProjectRole
	}
}

// UsesOrgRole reports whether the actor's org role counts in the project, the
// way EffectiveRole uses it: everywhere except a private project the org role
// does not see into. An agent's org-wide rights apply under the same
// condition.
func (p Project) UsesOrgRole(s Standing) bool {
	switch {
	case !s.HasOrgRole || !p.Visible(s):
		return false
	case p.Scope == ProjectScopeGlobal, s.seesEveryProject():
		return true
	}
	return p.Visibility != VisibilityPrivate
}

// EffectiveRole is the role an actor acts with inside the project: the higher
// of the org role and the project role, or zero and false when the project
// is not visible to them. In the global project the org role applies as is,
// and an actor with only project roles reads it as a viewer.
func (p Project) EffectiveRole(s Standing) (Role, bool) {
	if !p.Visible(s) {
		return 0, false
	}
	if p.Scope == ProjectScopeGlobal {
		if s.HasOrgRole {
			return s.OrgRole, true
		}
		return RoleViewer, true
	}
	if s.seesEveryProject() {
		return s.OrgRole, true
	}
	if p.Visibility == VisibilityPrivate {
		return s.ProjectRole, true
	}
	switch {
	case s.HasOrgRole && s.HasProjectRole:
		return max(s.OrgRole, s.ProjectRole), true
	case s.HasOrgRole:
		return s.OrgRole, true
	default:
		return s.ProjectRole, true
	}
}
