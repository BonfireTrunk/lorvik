package cli

import (
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func newMe(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "me",
		Short: "Show who the current token authenticates as",
		Long: `Print the actor (user or agent), the org, your org role, your role in each
project you hold one in, and the token prefix and expiry. Use it to check a
token works and to learn what it may do.

Example:
  lorvik me
  lorvik me set --timezone Asia/Kolkata`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			me, err := c.Me(cmd.Context())
			if err != nil {
				return err
			}
			a.resolve(cmd.Context(), me.Actor)
			return a.emit(me, func(p *cmdutil.Printer) {
				p.Line("%s (%s %s)  org %s  token %s expires %s", p.Named(me.Actor), me.Actor.Kind, me.Actor.ID, me.Org.Name, me.TokenPrefix, p.Ago(me.TokenExpiry))
				role := me.Role
				if role == "" {
					role = "none"
				}
				p.Line("org role: %s", role)
				if len(me.Projects) > 0 {
					parts := make([]string, 0, len(me.Projects))
					for _, pr := range me.Projects {
						part := pr.Project + "=" + pr.Role
						if len(pr.Rights) > 0 {
							part += "(" + strings.Join(pr.Rights, ",") + ")"
						}
						parts = append(parts, part)
					}
					p.Line("project roles: %s", strings.Join(parts, " "))
				}
			})
		},
	}
	cmd.AddCommand(newMeSet(a))
	return cmd
}

func newMeSet(a *app) *cobra.Command {
	var name, timezone, locale string
	cmd := &cobra.Command{
		Use:   "set [--name n] [--timezone zone] [--locale tag]",
		Short: "Edit your own profile",
		Long: `Change the name, IANA timezone or BCP 47 locale on your user profile. Only
the flags you pass are changed; a flag given an empty value clears that
field (--timezone "" goes back to displaying times in the viewer's zone).
At least one flag is required. The timezone is checked locally before
anything is sent. Only users have a profile; an agent token is refused.

--json prints the updated user.

Examples:
  lorvik me set --name "Ada" --timezone Asia/Kolkata --locale en-IN
  lorvik me set --locale ""`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var in apitypes.UpdateUser
			f := cmd.Flags()
			if f.Changed("name") {
				in.Name = &name
			}
			if f.Changed("timezone") {
				if timezone != "" {
					if _, err := time.LoadLocation(timezone); err != nil {
						return cmdutil.Usagef("--timezone %q is not an IANA zone such as Asia/Kolkata", timezone)
					}
				}
				in.Timezone = &timezone
			}
			if f.Changed("locale") {
				in.Locale = &locale
			}
			if in.Name == nil && in.Timezone == nil && in.Locale == nil {
				return cmdutil.Usagef("nothing to change: pass --name, --timezone or --locale")
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			u, err := c.UpdateMe(cmd.Context(), in)
			if err != nil {
				return err
			}
			return a.emit(u, func(p *cmdutil.Printer) {
				p.Line("%s  timezone %s  locale %s", userName(u), orElse(u.Timezone, "none"), orElse(u.Locale, "none"))
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "display name; empty clears")
	f.StringVar(&timezone, "timezone", "", "IANA zone, e.g. Asia/Kolkata; empty clears")
	f.StringVar(&locale, "locale", "", "BCP 47 tag, e.g. en-IN; empty clears")
	return cmd
}

// actorView is one line of `actors`: a user or an agent resolved by id.
type actorView struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

func newActors(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "actors <id>...",
		Short: "Resolve actor ids to names",
		Long: `Look up users and agents by id and print kind, id and name, one per line.
Cards, events and decisions carry actor ids only, and --json output keeps them
that way; this is how to find who an id is. Ids not found in this org are
omitted.

Example:
  lorvik actors 106666050296328877 106666050296328901`,
		Args: usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, id := range args {
				if !isID(id) {
					return cmdutil.Usagef("%q is not an actor id", id)
				}
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			got, err := c.Actors(cmd.Context(), args)
			if err != nil {
				return err
			}
			views := make([]actorView, 0, len(got.Users)+len(got.Agents))
			for _, u := range got.Users {
				views = append(views, actorView{Kind: "user", ID: u.ID, Name: userName(u)})
			}
			for _, ag := range got.Agents {
				views = append(views, actorView{Kind: "agent", ID: ag.ID, Name: ag.Name})
			}
			return a.emit(views, func(p *cmdutil.Printer) {
				for _, v := range views {
					p.Line("%s  %s  %s", v.Kind, v.ID, v.Name)
				}
			})
		},
	}
}
