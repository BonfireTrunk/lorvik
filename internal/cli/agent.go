package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

var (
	harnesses   = []string{"other", "claude-code", "codex", "opencode", "gemini-cli", "copilot-cli", "cursor", "pi", "qwen", "dsh", "goose"}
	agentRights = []string{"work", "ready", "triage", "answer", "decide"}
)

// checkRights validates a --rights list against the right names.
func checkRights(rights []string) error {
	if len(rights) == 0 {
		return cmdutil.Usagef("--rights: name at least one of %s", strings.Join(agentRights, ", "))
	}
	for _, r := range rights {
		if err := requiredOneOf("rights", r, agentRights...); err != nil {
			return err
		}
	}
	return nil
}

// rightsText words an agent's rights for a line; none means it only reads.
func rightsText(rights []string) string {
	if len(rights) == 0 {
		return "read only"
	}
	return strings.Join(rights, ", ")
}

func newAgent(a *app) *cobra.Command {
	return group("agent", "Register, list, approve, bind, revoke, restore and remove agent identities",
		`Agents are non-human actors with their own token. Register one per harness
and machine so the board shows who did what. An agent reads where it is bound
and does only what its rights allow: work (create and edit its own cards, ask,
propose, note, comment), ready (move backlog cards to ready), triage (edit and
drop other actors' cards) and answer (answer questions). It acts only in the
projects it is bound to (or, with --org-wide, in every org-visible project),
and never beyond what its owner may do. When the org requires approval, a new
agent is pending until an admin runs 'lorvik agent approve'.

Three ways to stop or bring back an agent:
  revoke   stops it at once and keeps it; its chats cannot bring it back
  restore  brings a revoked agent back with the access it had
  remove   stops it for good and forgets it; the next chat of its harness
           registers a new agent with no access

Examples:
  lorvik agent register --name claude-code --harness claude-code --project web
  lorvik agent ls
  lorvik agent bind 106666050296328877 --project api --rights work,ready
  lorvik agent revoke 106666050296328877`,
		newAgentRegister(a), newAgentLs(a), newAgentShow(a), newAgentApprove(a), newAgentRevoke(a), newAgentRestore(a), newAgentRemove(a), newAgentBind(a), newAgentUnbind(a))
}

func newAgentRegister(a *app) *cobra.Command {
	var (
		in       apitypes.CreateAgent
		projects []string
	)
	cmd := &cobra.Command{
		Use:   "register [name]",
		Short: "Register an agent and print its token",
		Long: `Register an agent and print ONLY its token on stdout, so it can be captured:
  export LORVIK_TOKEN=$(lorvik agent register --name claude-code --harness claude-code --project web)
Everything else goes to stderr. The token is shown once. The name is the
first argument or --name. The agent gets --rights (default work; any of work,
ready, triage and answer that you hold) in each --project (repeatable), or in
every org-visible project with --org-wide. With neither it is bound to nothing yet; use 'lorvik agent bind'.
Agent tokens live at most an hour (the default; --expires-in takes a shorter
Go duration such as 30m). Mint the next one from your own login with
'lorvik token create --agent <id>'. The agent acts only within your current
rights: it stops when you are suspended or leave the org, and never holds a
more rights in a project than you hold. A chat of a harness lorvik recognises
registers its own agent; this is for the rest (lorvik help config).

Examples:
  lorvik agent register ci-bot --harness other --project web
  lorvik agent register --name codex --harness codex --machine build-1 --project web --project api --rights work,ready`,
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				if in.Name != "" && in.Name != args[0] {
					return cmdutil.Usagef("name given twice: %q and --name %q", args[0], in.Name)
				}
				in.Name = args[0]
			}
			if in.Name == "" {
				return cmdutil.Usagef("an agent name is required: pass it as an argument or with --name")
			}
			if err := oneOf("harness", in.Harness, harnesses...); err != nil {
				return err
			}
			if err := checkRights(in.Rights); err != nil {
				return err
			}
			in.Projects = projects
			c, err := a.client()
			if err != nil {
				return err
			}
			res, err := c.CreateAgent(cmd.Context(), in)
			if err != nil {
				return err
			}
			if a.asJSON {
				return cmdutil.WriteJSON(a.out(), res)
			}
			ag := res.Agent
			fmt.Fprintf(a.errOut(), "lorvik: registered agent %s (%s), status %s; the token below is shown once\n", ag.Name, ag.ID, ag.Status)
			switch {
			case ag.Status == "pending":
				fmt.Fprintf(a.errOut(), "lorvik: the agent is pending; an admin must run `lorvik agent approve %s`\n", ag.ID)
			case len(ag.Projects) == 0 && ag.Role == "":
				fmt.Fprintf(a.errOut(), "lorvik: the agent is bound to no project yet; run `lorvik agent bind %s --project <slug>`\n", ag.ID)
			}
			return a.payload(res.Token + "\n")
		},
	}
	f := cmd.Flags()
	f.StringVar(&in.Name, "name", "", "Agent name (or give it as the argument)")
	f.StringVar(&in.Harness, "harness", "", "claude-code, codex, opencode, gemini-cli, copilot-cli, cursor, pi, qwen, dsh, goose or other")
	f.StringVar(&in.Machine, "machine", "", "Machine name, for the activity view")
	f.StringSliceVar(&in.Rights, "rights", []string{"work"}, "Rights to give, comma-separated: work, ready, triage, answer, decide")
	f.StringArrayVarP(&projects, "project", "p", nil, "Project slug to bind the agent to (repeatable)")
	f.BoolVar(&in.OrgWide, "org-wide", false, "Give the rights in every org-visible project")
	f.StringVar(&in.TokenExpiresIn, "expires-in", "", "Token lifetime as a Go duration, at most and by default 1h")
	return cmd
}

func newAgentLs(a *app) *cobra.Command {
	var (
		all bool
		pg  client.PageParams
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List agents",
		Long: `List agents as: id  name  harness  machine  status  created time. Revoked agents
are hidden unless --all. Removed agents are never listed.

Example:
  lorvik agent ls --all`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			list, err := c.ListAgents(cmd.Context(), all, pg)
			if err != nil {
				return err
			}
			return a.emit(list, func(p *cmdutil.Printer) {
				if len(list.Items) == 0 {
					p.Line("no agents")
				}
				for _, ag := range list.Items {
					p.Line("%s  %s  %s  %s  %s  created %s", ag.ID, ag.Name, orElse(ag.Harness, "-"), orElse(ag.Machine, "-"), ag.Status, p.Ago(ag.CreatedAt))
				}
				p.Paging(list.Page, a.nextCommand(cmd))
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Include revoked agents")
	pageFlags(cmd, &pg, "Maximum agents to print")
	return cmd
}

func newAgentShow(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show an agent and where it may act",
		Long: `Print an agent: its line, its org-wide rights and its rights per project, then
the chats seen using its token (newest first, at most 20) with their last
seen time and the rights given to each chat per project.

Example:
  lorvik agent show 106666050296328877`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			ag, err := c.GetAgent(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.emit(ag, func(p *cmdutil.Printer) {
				p.Line("%s  %s  %s  %s  %s  created %s", ag.ID, ag.Name, orElse(ag.Harness, "-"), orElse(ag.Machine, "-"), ag.Status, p.Ago(ag.CreatedAt))
				if ag.Role != "" {
					p.Line("rights %s (org-wide)", rightsText(ag.Rights))
				}
				for _, pr := range ag.Projects {
					p.Line("project %s: %s", pr.Project, rightsText(pr.Rights))
				}
				if ag.Role == "" && len(ag.Projects) == 0 {
					p.Line("bound to no project")
				}
				if len(ag.Sessions) > 0 {
					p.Line("sessions:")
				}
				for _, s := range ag.Sessions {
					var line strings.Builder
					if s.LastSeenAt == nil {
						fmt.Fprintf(&line, "  %s  never seen", s.Label)
					} else {
						fmt.Fprintf(&line, "  %s  seen %s", s.Label, p.Ago(*s.LastSeenAt))
					}
					for _, g := range s.Grants {
						fmt.Fprintf(&line, "  %s: %s", g.Project, strings.Join(g.Rights, ", "))
					}
					p.Line("%s", line.String())
				}
			})
		},
	}
}

func newAgentApprove(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "approve <id>",
		Short: "Approve a pending agent",
		Long: `Approve an agent that is pending because the org requires approval. Needs
admin.

Example:
  lorvik agent approve 106666050296328877`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			ag, err := c.ApproveAgent(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.emit(ag, func(p *cmdutil.Printer) { p.Line("approved agent %s (%s)", ag.Name, ag.ID) })
		},
	}
}

func newAgentRevoke(a *app) *cobra.Command {
	return revokeCmd(a, "agent", "Revoke an agent and all its tokens", `Revoke an agent. Its tokens stop working at once, its cards are unassigned
and its chats lose their extra rights. Its chats cannot register it again by
themselves. Its project access is kept, so 'agent restore' brings it back.
Find ids with 'agent ls'. The agent's past work stays on the board.

Example:
  lorvik agent revoke 106666050296328877`, (*client.Client).RevokeAgent)
}

func newAgentRestore(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "restore <id>",
		Short: "Bring a revoked agent back",
		Long: `Restore a revoked agent with the access it had. Its old tokens stay
revoked: a chat of its harness gets a new one on its next command, and any
other agent needs 'lorvik token create --agent <id>'. Card assignments and
chats' extra rights do not come back. When the org requires approval and you
are not an admin, the agent is pending until an admin approves it. Find
revoked agents with 'agent ls --all'. Needs the agent's creator, while still a
member or above, or an admin.

Example:
  lorvik agent restore 106666050296328877`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			ag, err := c.RestoreAgent(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.emit(ag, func(p *cmdutil.Printer) { p.Line("restored agent %s (%s), status %s", ag.Name, ag.ID, ag.Status) })
		},
	}
}

func newAgentRemove(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove an agent for good",
		Long: `Remove an agent, active or revoked. It stops like a revoked one, loses all
its access and leaves 'agent ls', and its name is free: the next chat of
its harness on that machine registers a new agent that starts with no access.
The agent's past work stays on the board. This cannot be undone; use
'agent revoke' to stop an agent you may want back. Needs the agent's creator
or an admin.

Example:
  lorvik agent remove 106666050296328877`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			if err := c.RemoveAgent(cmd.Context(), args[0]); err != nil {
				return err
			}
			return a.emit(removedView{Kind: "agent", ID: args[0], Removed: true}, func(p *cmdutil.Printer) { p.Line("removed agent %s", args[0]) })
		},
	}
}

func newAgentBind(a *app) *cobra.Command {
	var rights []string
	cmd := &cobra.Command{
		Use:   "bind <id> --project <slug>",
		Short: "Give an agent rights in a project",
		Long: `Bind an agent to a project with rights, comma-separated: work (default),
ready, triage, answer, decide. Run again to change them. The project is --project
(or the bound one). You cannot give a right your own role there lacks.

Example:
  lorvik agent bind 106666050296328877 --project api --rights work,ready`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkRights(rights); err != nil {
				return err
			}
			c, slug, err := a.api()
			if err != nil {
				return err
			}
			ag, err := c.BindAgent(cmd.Context(), args[0], slug, apitypes.PutMembership{Rights: rights})
			if err != nil {
				return err
			}
			return a.emit(ag, func(p *cmdutil.Printer) {
				p.Line("agent %s has rights %s in %s", ag.Name, strings.Join(rights, ", "), slug)
			})
		},
	}
	cmd.Flags().StringSliceVar(&rights, "rights", []string{"work"}, "Rights to give, comma-separated: work, ready, triage, answer, decide")
	return cmd
}

func newAgentUnbind(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "unbind <id> --project <slug>",
		Short: "Remove an agent from a project",
		Long: `Remove an agent from a project. The project is --project (or the bound
one).

Example:
  lorvik agent unbind 106666050296328877 --project api`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, slug, err := a.api()
			if err != nil {
				return err
			}
			if err := c.UnbindAgent(cmd.Context(), args[0], slug); err != nil {
				return err
			}
			return a.emit(removedView{Kind: "agent", ID: args[0], Project: slug, Removed: true}, func(p *cmdutil.Printer) {
				p.Line("agent %s removed from %s", args[0], slug)
			})
		},
	}
}
