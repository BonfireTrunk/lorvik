package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

var orgRoles = []string{"viewer", "member", "reviewer", "admin", "owner"}

func newMember(a *app) *cobra.Command {
	return group("member", "List and manage org members and their roles",
		`Org members hold one of five roles, in order: viewer, member, reviewer,
admin, owner. The org role applies to every project; a project role can raise
it for one project ('lorvik project member'). Removing a member revokes their
tokens. Members are addressed by actor id, or user:<id> and agent:<id>; a bare
id means a user. You cannot grant a role above your own.

Examples:
  lorvik member ls
  lorvik member add 106666050296328877 --role member
  lorvik member role 106666050296328877 reviewer
  lorvik member remove 106666050296328877`,
		newMemberLs(a), newMemberAdd(a), newMemberRole(a), newMemberRemove(a))
}

func newMemberLs(a *app) *cobra.Command {
	var (
		all bool
		pg  client.PageParams
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List org members",
		Long: `List members as: kind  id  name  role  status. Pending and removed members are
hidden unless --all.

Example:
  lorvik member ls --all`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			list, err := c.ListMembers(cmd.Context(), all, pg)
			if err != nil {
				return err
			}
			who := actorsOf(list.Items, func(m apitypes.Membership) apitypes.Actor { return m.Actor })
			a.resolve(cmd.Context(), who...)
			return a.emit(list, func(p *cmdutil.Printer) {
				if len(list.Items) == 0 {
					p.Line("no members")
				}
				for _, m := range list.Items {
					p.Line("%s  %s  %s  %s  %s", m.Actor.Kind, m.Actor.ID, p.Named(m.Actor), m.Role, m.Status)
				}
				p.Paging(list.Page, a.nextCommand(cmd))
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Include pending and removed members")
	pageFlags(cmd, &pg, "Maximum members to print")
	return cmd
}

// membershipBody builds what a member add command sends for the target: a
// role for a person, rights for an agent. The flag for the other kind is a
// usage error.
func membershipBody(cmd *cobra.Command, act apitypes.Actor, role string, rights, roles []string) (apitypes.PutMembership, error) {
	if act.Kind == "agent" {
		if cmd.Flags().Changed("role") {
			return apitypes.PutMembership{}, cmdutil.Usagef("--role is for people; an agent holds --rights (%s)", strings.Join(agentRights, ", "))
		}
		if err := checkRights(rights); err != nil {
			return apitypes.PutMembership{}, err
		}
		return apitypes.PutMembership{Rights: rights}, nil
	}
	if cmd.Flags().Changed("rights") {
		return apitypes.PutMembership{}, cmdutil.Usagef("--rights is for agents; a person holds --role")
	}
	if err := requiredOneOf("role", role, roles...); err != nil {
		return apitypes.PutMembership{}, err
	}
	return apitypes.PutMembership{Role: role}, nil
}

func (a *app) putMember(cmd *cobra.Command, arg, role string, rights []string) error {
	act, err := parseActor(arg, "user")
	if err != nil {
		return err
	}
	var body apitypes.PutMembership
	if cmd.Name() == "role" {
		if act.Kind == "agent" {
			return cmdutil.Usagef("an agent holds rights, not a role; run: lorvik member add agent:%s --rights work,ready,triage,answer,decide", act.ID)
		}
		if err := requiredOneOf("role", role, orgRoles...); err != nil {
			return err
		}
		body = apitypes.PutMembership{Role: role}
	} else if body, err = membershipBody(cmd, act, role, rights, orgRoles); err != nil {
		return err
	}
	c, err := a.client()
	if err != nil {
		return err
	}
	m, err := c.PutMember(cmd.Context(), act, body)
	if err != nil {
		return err
	}
	return a.emit(m, func(p *cmdutil.Printer) {
		if act.Kind == "agent" {
			p.Line("agent %s has rights %s", m.Actor.ID, rightsText(m.Rights))
			return
		}
		p.Line("%s %s is %s", m.Actor.Kind, m.Actor.ID, m.Role)
	})
}

func newMemberAdd(a *app) *cobra.Command {
	var (
		role   string
		rights []string
	)
	cmd := &cobra.Command{
		Use:   "add <actor> [--role <role> | --rights <rights>]",
		Short: "Add an org member",
		Long: `Give a person an org role (default member), or an agent org-wide rights
(--rights, default work; any of work, ready, triage, answer, decide). An agent holds
rights, never a role; a person holds a role, never rights. Run again to
change it. A person must be a member already or have asked to join (pending
in 'lorvik member ls --all'): nobody is made a member of an org they did not
ask for. Anyone else is invited by GitHub username on the dashboard's Access
page. Needs admin.

Examples:
  lorvik member add user:106666050296328877 --role reviewer
  lorvik member add agent:106666050296328878 --rights work,ready`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error { return a.putMember(cmd, args[0], role, rights) },
	}
	cmd.Flags().StringVar(&role, "role", "member", "A person's role: viewer, member, reviewer, admin or owner")
	cmd.Flags().StringSliceVar(&rights, "rights", []string{"work"}, "An agent's rights, comma-separated: work, ready, triage, answer, decide")
	return cmd
}

func newMemberRole(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "role <actor> <role>",
		Short: "Change an org member's role",
		Long: `Set a person's org role: viewer, member, reviewer, admin or owner. Needs admin;
only an owner can change an admin or owner. An agent holds rights, not a role:
use member add with --rights.

Example:
  lorvik member role 106666050296328877 reviewer`,
		Args: usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.putMember(cmd, args[0], args[1], nil)
		},
	}
}

func newMemberRemove(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <actor>",
		Short: "Remove an org member and revoke their tokens",
		Long: `Remove an actor from the org. Their tokens stop working at once. You cannot
remove yourself. Needs admin.

Example:
  lorvik member remove 106666050296328877`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			act, err := parseActor(args[0], "user")
			if err != nil {
				return err
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			if err := c.RemoveMember(cmd.Context(), act); err != nil {
				return err
			}
			return a.emit(removedView{Kind: act.Kind, ID: act.ID, Removed: true}, func(p *cmdutil.Printer) { p.Line("removed %s %s", act.Kind, act.ID) })
		},
	}
}
