package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

var projectRoles = []string{"viewer", "member", "reviewer"}

func newProject(a *app) *cobra.Command {
	return group("project", "List, create, change and archive projects",
		`Projects group cards, decisions and notes. A project has a visibility (org:
everyone in the org can read it; private: only its members and org admins), a
lease (how long a claim lives without renewal) and per-project members.

Examples:
  lorvik project ls
  lorvik project create payments --name "Payments" --visibility private --lease 1h
  lorvik project member add 106666050296328877 --role member -p payments
  lorvik project archive payments`,
		newProjectLs(a), newProjectShow(a), newProjectCreate(a), newProjectEdit(a), newProjectArchive(a), newProjectMember(a))
}

// projectMeta is the second line of project output.
func projectMeta(p apitypes.Project) string {
	parts := []string{"visibility " + p.Visibility, "concurrency " + p.Concurrency, "lease " + p.Lease, "status " + p.Status}
	switch {
	case len(p.Rights) > 0:
		parts = append(parts, "rights "+strings.Join(p.Rights, ", "))
	case p.Role != "":
		parts = append(parts, "role "+p.Role)
	}
	return strings.Join(parts, "  ")
}

// slugArg is the optional project argument, defaulting to --project.
func (a *app) slugArg(args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	return a.slug()
}

// allProjects reads every project, for a choice among them.
func allProjects(ctx context.Context, c *client.Client) ([]apitypes.Project, error) {
	return allPages(func(pg client.PageParams) ([]apitypes.Project, apitypes.Page, error) {
		list, err := c.ListProjects(ctx, pg)
		return list.Items, list.Page, err
	})
}

func newProjectLs(a *app) *cobra.Command {
	var pg client.PageParams
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the projects you can see",
		Long: `List projects as: slug  name  visibility  role  status. The org's global
project comes first and carries a trailing "global" marker. Archived projects
are included, marked archived.

Example:
  lorvik project ls`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			list, err := c.ListProjects(cmd.Context(), pg)
			if err != nil {
				return err
			}
			return a.emit(list, func(p *cmdutil.Printer) {
				if len(list.Items) == 0 {
					p.Line("no projects")
				}
				for _, pr := range list.Items {
					row := fmt.Sprintf("%s  %s  %s  %s  %s", pr.Slug, pr.Name, pr.Visibility, orElse(pr.Role, "-"), pr.Status)
					if pr.Scope == "global" {
						row += "  global"
					}
					p.Line("%s", row)
				}
				p.Paging(list.Page, a.nextCommand(cmd))
			})
		},
	}
	pageFlags(cmd, &pg, "Maximum projects to print")
	return cmd
}

func newProjectShow(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show [slug]",
		Short: "Show a project with its card counts",
		Long: `Print a project: name, visibility, lease, your role and card counts per
status. Defaults to the current project.

Example:
  lorvik project show payments`,
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, err := a.slugArg(args)
			if err != nil {
				return err
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			proj, err := c.GetProject(cmd.Context(), slug)
			if err != nil {
				return err
			}
			return a.emit(proj, func(p *cmdutil.Printer) { renderProject(p, proj) })
		},
	}
}

func renderProject(p *cmdutil.Printer, proj apitypes.Project) {
	p.Line("%s  %s", proj.Slug, proj.Name)
	p.Line("%s", projectMeta(proj))
	if proj.Description != "" {
		p.Line("%s", proj.Description)
	}
	if n := proj.Counts; n != nil {
		p.Line("backlog %d  ready %d  working %d  waiting %d  done %d  dropped %d",
			n.Backlog, n.Ready, n.Working, n.Waiting, n.Done, n.Dropped)
		p.Line("open questions: %d", n.OpenQuestions)
	}
}

func newProjectCreate(a *app) *cobra.Command {
	var in apitypes.CreateProject
	cmd := &cobra.Command{
		Use:   "create <slug>",
		Short: "Create a project",
		Long: `Create a project. The slug is its short name in URLs and --project. Name
defaults to the slug. Visibility defaults to the org setting; lease is a Go
duration between 1m and 24h (default 30m). Needs admin.

Examples:
  lorvik project create payments --name "Payments"
  lorvik project create secret --visibility private --lease 1h`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := oneOf("visibility", in.Visibility, "org", "private"); err != nil {
				return err
			}
			in.Slug = args[0]
			if in.Name == "" {
				in.Name = in.Slug
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			proj, err := c.CreateProject(cmd.Context(), in)
			if err != nil {
				return err
			}
			return a.emit(proj, func(p *cmdutil.Printer) { p.Line("created project %s", proj.Slug) })
		},
	}
	f := cmd.Flags()
	f.StringVar(&in.Name, "name", "", "Display name (default: the slug)")
	f.StringVar(&in.Description, "description", "", "Description")
	f.StringVar(&in.Visibility, "visibility", "", "org or private (default from org settings)")
	f.StringVar(&in.Lease, "lease", "", "Claim lease as a Go duration, 1m to 24h (default 30m)")
	return cmd
}

func newProjectEdit(a *app) *cobra.Command {
	var name, description, visibility, concurrency, lease string
	cmd := &cobra.Command{
		Use:     "edit [slug]",
		Aliases: []string{"set"},
		Short:   "Change a project's name, description, visibility, concurrency or lease",
		Long: `Update only the fields you pass (also available as 'project set'). Defaults to
the current project. Needs admin, so a user, never an agent. --concurrency
parallel lets a lead fan child cards out to subagents when the harness can;
serial means work them in order.

Examples:
  lorvik project edit payments --lease 1h
  lorvik project set payments --concurrency serial`,
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			fl := cmd.Flags()
			var in apitypes.UpdateProject
			if fl.Changed("name") {
				in.Name = &name
			}
			if fl.Changed("description") {
				in.Description = &description
			}
			if fl.Changed("visibility") {
				if err := oneOf("visibility", visibility, "org", "private"); err != nil {
					return err
				}
				in.Visibility = &visibility
			}
			if fl.Changed("concurrency") {
				if err := oneOf("concurrency", concurrency, "parallel", "serial"); err != nil {
					return err
				}
				in.Concurrency = &concurrency
			}
			if fl.Changed("lease") {
				in.Lease = &lease
			}
			if in.Name == nil && in.Description == nil && in.Visibility == nil && in.Concurrency == nil && in.Lease == nil {
				return cmdutil.Usagef("nothing to change: pass --name, --description, --visibility, --concurrency or --lease")
			}
			return a.updateProject(cmd, args, in, "updated")
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "New display name")
	f.StringVar(&description, "description", "", "New description")
	f.StringVar(&visibility, "visibility", "", "org or private")
	f.StringVar(&concurrency, "concurrency", "", "parallel or serial")
	f.StringVar(&lease, "lease", "", "Claim lease as a Go duration, 1m to 24h")
	return cmd
}

func (a *app) updateProject(cmd *cobra.Command, args []string, in apitypes.UpdateProject, done string) error {
	slug, err := a.slugArg(args)
	if err != nil {
		return err
	}
	c, err := a.client()
	if err != nil {
		return err
	}
	proj, err := c.UpdateProject(cmd.Context(), slug, in)
	if err != nil {
		return err
	}
	return a.emit(proj, func(p *cmdutil.Printer) { p.Line("%s project %s", done, proj.Slug) })
}

func newProjectArchive(a *app) *cobra.Command {
	var undo bool
	cmd := &cobra.Command{
		Use:   "archive [slug]",
		Short: "Archive a project (or restore it with --undo)",
		Long: `Archive a project: it stays readable but takes no new work. Nothing is
deleted and --undo makes it active again. Defaults to the current project.
Needs admin.

Example:
  lorvik project archive old-service`,
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			status, done := "archived", "archived"
			if undo {
				status, done = "active", "restored"
			}
			return a.updateProject(cmd, args, apitypes.UpdateProject{Status: &status}, done)
		},
	}
	cmd.Flags().BoolVar(&undo, "undo", false, "Restore an archived project")
	return cmd
}

func newProjectMember(a *app) *cobra.Command {
	return group("member", "List and manage a project's members",
		`Project members hold a role in one project: viewer, member or reviewer.
Their effective role is the higher of this and their org role. The project is
the current one (-p). Members are addressed by actor id, or user:<id> and
agent:<id>.

Examples:
  lorvik project member ls -p payments
  lorvik project member add user:106666050296328877 --role reviewer -p payments
  lorvik project member remove 106666050296328877 -p payments`,
		newProjectMemberLs(a), newProjectMemberAdd(a), newProjectMemberRemove(a))
}

func newProjectMemberLs(a *app) *cobra.Command {
	var pg client.PageParams
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the project's members",
		Long: `List members as: kind  id  name  role. Removed members are not listed.

Example:
  lorvik project member ls`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, slug, err := a.api()
			if err != nil {
				return err
			}
			list, err := c.ListProjectMembers(cmd.Context(), slug, pg)
			if err != nil {
				return err
			}
			who := actorsOf(list.Items, func(m apitypes.ProjectMember) apitypes.Actor { return m.Actor })
			a.resolve(cmd.Context(), who...)
			return a.emit(list, func(p *cmdutil.Printer) {
				if len(list.Items) == 0 {
					p.Line("no project members")
				}
				for _, m := range list.Items {
					p.Line("%s  %s  %s  %s", m.Actor.Kind, m.Actor.ID, p.Named(m.Actor), m.Role)
				}
				p.Paging(list.Page, a.nextCommand(cmd))
			})
		},
	}
	pageFlags(cmd, &pg, "Maximum members to print")
	return cmd
}

func newProjectMemberAdd(a *app) *cobra.Command {
	var (
		role   string
		rights []string
	)
	cmd := &cobra.Command{
		Use:   "add <actor> [--role <role> | --rights <rights>]",
		Short: "Add a member to the project or change their role",
		Long: `Give a person a role in the project: viewer, member (default) or reviewer.
Give an agent rights instead (--rights, default work; any of work, ready,
triage, answer, decide). Run again to change it. A bare id means a user. Needs admin.

Examples:
  lorvik project member add user:106666050296328877 --role reviewer
  lorvik project member add agent:106666050296328878 --rights work,triage`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			act, err := parseActor(args[0], "user")
			if err != nil {
				return err
			}
			body, err := membershipBody(cmd, act, role, rights, projectRoles)
			if err != nil {
				return err
			}
			c, slug, err := a.api()
			if err != nil {
				return err
			}
			m, err := c.PutProjectMember(cmd.Context(), slug, act, body)
			if err != nil {
				return err
			}
			return a.emit(m, func(p *cmdutil.Printer) {
				if act.Kind == "agent" {
					p.Line("agent %s has rights %s in %s", m.Actor.ID, rightsText(m.Rights), slug)
					return
				}
				p.Line("%s %s is %s in %s", m.Actor.Kind, m.Actor.ID, m.Role, slug)
			})
		},
	}
	cmd.Flags().StringVar(&role, "role", "member", "A person's role: viewer, member or reviewer")
	cmd.Flags().StringSliceVar(&rights, "rights", []string{"work"}, "An agent's rights, comma-separated: work, ready, triage, answer, decide")
	return cmd
}

func newProjectMemberRemove(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <actor>",
		Short: "Remove a member from the project",
		Long: `Remove an actor's project role. Their org role, if any, is unchanged. A bare
id means a user. Needs admin.

Example:
  lorvik project member remove user:106666050296328877`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			act, err := parseActor(args[0], "user")
			if err != nil {
				return err
			}
			c, slug, err := a.api()
			if err != nil {
				return err
			}
			if err := c.RemoveProjectMember(cmd.Context(), slug, act); err != nil {
				return err
			}
			return a.emit(removedView{Kind: act.Kind, ID: act.ID, Project: slug, Removed: true}, func(p *cmdutil.Printer) {
				p.Line("removed %s %s from %s", act.Kind, act.ID, slug)
			})
		},
	}
}
