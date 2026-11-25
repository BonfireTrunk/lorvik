package domain

import "slices"

// Role is what an actor may do in an org or a project. Roles are cumulative
// and ordered, so "at least admin" is one comparison. An agent's rows hold
// RoleViewer only; its Rights carry what it may do beyond reading.
type Role int16

// Role values.
const (
	RoleViewer   Role = 10
	RoleMember   Role = 20
	RoleReviewer Role = 30
	RoleAdmin    Role = 40
	RoleOwner    Role = 50
)

var roles = names[Role]{"role",
	[]Role{RoleViewer, RoleMember, RoleReviewer, RoleAdmin, RoleOwner},
	[]string{"viewer", "member", "reviewer", "admin", "owner"}}

func (r Role) String() string { return roles.name(r) }

// ParseRole reads a role name.
func ParseRole(s string) (Role, error) { return roles.parse(s) }

// Valid reports whether r is a defined role.
func (r Role) Valid() bool { return roles.valid(r) }

// Permission is one verb the authorisation table grants or refuses.
type Permission int16

// Permission values. The comments give the role that first grants each.
const (
	PermRead            Permission = iota + 1 // viewer: read cards, decisions, notes, events
	PermCardWrite                             // member: create and edit own cards; claim, work, finish
	PermQuestionAsk                           // member
	PermDecisionPropose                       // member
	PermNoteWrite                             // member: create and edit notes
	PermCommentWrite                          // member
	PermAgentRegister                         // member: register an agent for own use
	PermCardTriage                            // reviewer: edit and drop any card
	PermQuestionAnswer                        // reviewer
	PermDecisionAccept                        // reviewer: accept, reject, supersede, revoke
	PermAgentManage                           // admin: approve, widen, revoke any agent
	PermProjectManage                         // admin: create, edit, archive; manage project members
	PermMemberManage                          // admin: invite and remove members up to reviewer; revoke anyone's tokens
	PermOrgManage                             // owner: grant admin, billing, delete org, transfer ownership
	PermCardReady                             // reviewer: move any card from backlog to ready
)

// Mutating reports whether the permission changes state, so a read-only token
// may not use it. Only reading is not mutating.
func (p Permission) Mutating() bool { return p != PermRead }

// minRole is the lowest role that holds each permission. Every check goes
// through this table; nothing else in the code compares role values for
// authorisation.
var minRole = map[Permission]Role{
	PermRead:            RoleViewer,
	PermCardWrite:       RoleMember,
	PermQuestionAsk:     RoleMember,
	PermDecisionPropose: RoleMember,
	PermNoteWrite:       RoleMember,
	PermCommentWrite:    RoleMember,
	PermAgentRegister:   RoleMember,
	PermCardTriage:      RoleReviewer,
	PermQuestionAnswer:  RoleReviewer,
	PermCardReady:       RoleReviewer,
	PermDecisionAccept:  RoleReviewer,
	PermAgentManage:     RoleAdmin,
	PermProjectManage:   RoleAdmin,
	PermMemberManage:    RoleAdmin,
	PermOrgManage:       RoleOwner,
}

// Can reports whether a role holds a permission.
func (r Role) Can(p Permission) bool {
	min, ok := minRole[p]
	return ok && r >= min
}

// SeesEveryProject reports whether an org role sees every project of the
// org, private ones included, with its own role.
func (r Role) SeesEveryProject() bool { return r >= RoleAdmin }

// CanGrant reports whether an actor with role r may grant role target to
// someone else. Admins grant up to reviewer; owners grant anything.
func (r Role) CanGrant(target Role) bool {
	switch {
	case r >= RoleOwner:
		return target.Valid()
	case r >= RoleAdmin:
		return target.Valid() && target <= RoleReviewer
	default:
		return false
	}
}

var permissions = names[Permission]{"permission",
	[]Permission{PermRead, PermCardWrite, PermQuestionAsk, PermDecisionPropose, PermNoteWrite, PermCommentWrite,
		PermAgentRegister, PermCardTriage, PermQuestionAnswer, PermDecisionAccept, PermAgentManage,
		PermProjectManage, PermMemberManage, PermOrgManage, PermCardReady},
	[]string{"read", "write cards", "ask questions", "propose decisions", "write notes", "write comments",
		"register agents", "triage cards", "answer questions", "accept decisions", "manage agents",
		"manage projects", "manage members", "manage the org", "ready cards"}}

// String names the permission as a verb phrase for error messages.
func (p Permission) String() string { return permissions.name(p) }

// Rights is the bitmask of what an agent may do in a project, on top of
// reading. Each right bundles permissions; people carry no rights because
// their role decides.
type Rights int16

// Rights values.
const (
	RightWork   Rights = 1 << iota // create and edit own cards; ask, propose, note, comment
	RightReady                     // move any card from backlog to ready
	RightTriage                    // edit and drop any card
	RightAnswer                    // answer questions
	RightDecide                    // accept or reject a proposed decision for its owner

	AllRights = RightWork | RightReady | RightTriage | RightAnswer | RightDecide
)

var rightNames = []string{"work", "ready", "triage", "answer", "decide"}

// ParseRights reads right names into a mask. Duplicates are fine; an empty
// list is no rights.
func ParseRights(names []string) (Rights, error) {
	var r Rights
	for _, n := range names {
		i := slices.Index(rightNames, n)
		if i < 0 {
			return 0, invalid("rights", n+" is not a right; must be one of: "+joinLabels(rightNames))
		}
		r |= 1 << i
	}
	return r, nil
}

// Names lists the rights in bit order; never nil.
func (r Rights) Names() []string {
	out := []string{}
	for i, n := range rightNames {
		if r&(1<<i) != 0 {
			out = append(out, n)
		}
	}
	return out
}

// Valid reports whether r sets no bit outside AllRights.
func (r Rights) Valid() bool { return r&^AllRights == 0 }

// Has reports whether every right in x is set.
func (r Rights) Has(x Rights) bool { return r&x == x }

// RightFor returns the right that bundles a permission, or 0 when no right
// does: reading comes from membership, and registering agents and the admin
// and owner verbs are never granted to an agent. RightDecide bundles none:
// it covers two of the verdicts decision-accept holds, checked where a
// verdict is given, so nothing else that asks for decision-accept opens to
// an agent.
func RightFor(p Permission) Rights {
	switch p {
	case PermCardWrite, PermQuestionAsk, PermDecisionPropose, PermNoteWrite, PermCommentWrite:
		return RightWork
	case PermCardReady:
		return RightReady
	case PermCardTriage:
		return RightTriage
	case PermQuestionAnswer:
		return RightAnswer
	}
	return 0
}

// Grants reports whether the rights cover a permission.
func (r Rights) Grants(p Permission) bool {
	need := RightFor(p)
	return need != 0 && r.Has(need)
}

// RightsHeldBy returns the rights a person with the role holds, which cap
// what their agent can be given.
func RightsHeldBy(role Role) Rights {
	var r Rights
	for _, c := range []struct {
		right Rights
		perm  Permission
	}{{RightWork, PermCardWrite}, {RightReady, PermCardReady}, {RightTriage, PermCardTriage}, {RightAnswer, PermQuestionAnswer}, {RightDecide, PermDecisionAccept}} {
		if role.Can(c.perm) {
			r |= c.right
		}
	}
	return r
}
