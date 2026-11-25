package cli

import (
	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func newOrg(a *app) *cobra.Command {
	return group("org", "Show and change the org and its settings",
		`The org is the tenant everything else lives in. Settings (admins and owners
see them): whether new agents need approval, the default and maximum token
lifetime, and the default project visibility.

Examples:
  lorvik org show
  lorvik org set --agent-approval admin --token-ttl-max 2160h`,
		newOrgShow(a), newOrgSet(a))
}

func renderOrg(p *cmdutil.Printer, o apitypes.Org) {
	p.Line("%s  %s  %s", o.ID, o.Name, o.Status)
	s := o.Settings
	if s == nil {
		return
	}
	p.Line("agent approval: %s", orElse(s.AgentApproval, "-"))
	p.Line("token ttl: default %s, max %s", orElse(s.TokenTTLDefault, "-"), orElse(s.TokenTTLMax, "-"))
	p.Line("project visibility default: %s", orElse(s.ProjectVisibilityDefault, "-"))
}

func newOrgShow(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the org",
		Long: `Print the org's id, name and status, and its settings when you are an admin
or owner.

Example:
  lorvik org show`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			o, err := c.GetOrg(cmd.Context())
			if err != nil {
				return err
			}
			return a.emit(o, func(p *cmdutil.Printer) { renderOrg(p, o) })
		},
	}
}

func newOrgSet(a *app) *cobra.Command {
	var name, approval, ttlDefault, ttlMax, visibility string
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Change the org name or settings",
		Long: `Update only what you pass. The server replaces the settings document as a
whole, so the current settings are read first and the ones you do not pass
are sent back unchanged. Needs owner.

Examples:
  lorvik org set --name "Acme"
  lorvik org set --agent-approval admin --project-visibility private`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			fl := cmd.Flags()
			settingsChanged := fl.Changed("agent-approval") || fl.Changed("token-ttl-default") ||
				fl.Changed("token-ttl-max") || fl.Changed("project-visibility")
			if !fl.Changed("name") && !settingsChanged {
				return cmdutil.Usagef("nothing to change: pass --name or a setting flag (see --help)")
			}
			if err := oneOf("agent-approval", approval, "auto", "admin"); err != nil {
				return err
			}
			if err := oneOf("project-visibility", visibility, "org", "private"); err != nil {
				return err
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			var in apitypes.UpdateOrg
			if fl.Changed("name") {
				in.Name = &name
			}
			if settingsChanged {
				cur, err := c.GetOrg(cmd.Context())
				if err != nil {
					return err
				}
				s := apitypes.OrgSettings{}
				if cur.Settings != nil {
					s = *cur.Settings
				}
				if fl.Changed("agent-approval") {
					s.AgentApproval = approval
				}
				if fl.Changed("token-ttl-default") {
					s.TokenTTLDefault = ttlDefault
				}
				if fl.Changed("token-ttl-max") {
					s.TokenTTLMax = ttlMax
				}
				if fl.Changed("project-visibility") {
					s.ProjectVisibilityDefault = visibility
				}
				in.Settings = &s
			}
			o, err := c.UpdateOrg(cmd.Context(), in)
			if err != nil {
				return err
			}
			return a.emit(o, func(p *cmdutil.Printer) { renderOrg(p, o) })
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "Org name")
	f.StringVar(&approval, "agent-approval", "", "auto or admin: who approves new agents")
	f.StringVar(&ttlDefault, "token-ttl-default", "", "Default token lifetime as a Go duration, e.g. 720h")
	f.StringVar(&ttlMax, "token-ttl-max", "", "Maximum token lifetime as a Go duration, at most 8760h")
	f.StringVar(&visibility, "project-visibility", "", "org or private: default for new projects")
	return cmd
}
