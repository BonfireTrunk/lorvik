package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
	"github.com/BonfireTrunk/lorvik/internal/client"
)

func newToken(a *app) *cobra.Command {
	cmd := group("token", "List, create and revoke API tokens",
		`Tokens authenticate you to the server (LORVIK_TOKEN). Secrets are shown
once, at creation. A token close to expiry is replaced by the server on use;
the CLI saves the successor in your config file. When the token came from
LORVIK_TOKEN it cannot, and prints a notice without the secret.

Examples:
  lorvik token ls
  lorvik token create --name laptop --expires-in 2160h
  lorvik token create --agent 106666050296328877 --name ci
  lorvik token revoke 106666050296328877`,
		newTokenLs(a), newTokenCreate(a), newTokenRevoke(a))
	cmd.Aliases = []string{"tokens"}
	return cmd
}

func newTokenLs(a *app) *cobra.Command {
	var pg client.PageParams
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List your tokens",
		Long: `List tokens as: id  prefix  name  scope  status  expiry  last used. Secrets are
never shown.

Example:
  lorvik token ls`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			list, err := c.ListTokens(cmd.Context(), pg)
			if err != nil {
				return err
			}
			return a.emit(list, func(p *cmdutil.Printer) {
				if len(list.Items) == 0 {
					p.Line("no tokens")
				}
				for _, t := range list.Items {
					used := "never used"
					if t.LastUsedAt != nil {
						used = "used " + p.Ago(*t.LastUsedAt)
					}
					p.Line("%s  %s  %s  %s  %s  expires %s  %s", t.ID, t.Prefix, orElse(t.Name, "-"), t.Scope, t.Status, p.Ago(t.ExpiresAt), used)
				}
				p.Paging(list.Page, a.nextCommand(cmd))
			})
		},
	}
	pageFlags(cmd, &pg, "Maximum tokens to print")
	return cmd
}

func newTokenCreate(a *app) *cobra.Command {
	var in apitypes.CreateToken
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a token and print its secret",
		Long: `Create a token and print ONLY the secret on stdout; details go to stderr. The
secret is shown once. It is for you, or with --agent for an agent you manage.
--expires-in takes a Go duration such as 720h (default from org settings);
an agent's token lives at most 1h, the default with --agent. --scope read
makes a token that cannot write.

Examples:
  export LORVIK_TOKEN=$(lorvik token create --name ci --expires-in 720h)
  lorvik token create --agent 106666050296328877`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := oneOf("scope", in.Scope, "read", "write"); err != nil {
				return err
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			res, err := c.CreateToken(cmd.Context(), in)
			if err != nil {
				return err
			}
			if a.asJSON {
				return cmdutil.WriteJSON(a.out(), res)
			}
			fmt.Fprintf(a.errOut(), "lorvik: created token %s (%s); the secret below is shown once\n", res.Token.ID, res.Token.Prefix)
			return a.payload(res.Secret + "\n")
		},
	}
	f := cmd.Flags()
	f.StringVar(&in.Name, "name", "", "Label for the token")
	f.StringVar(&in.Scope, "scope", "", "read or write (default write)")
	f.StringVar(&in.ExpiresIn, "expires-in", "", "Lifetime as a Go duration, e.g. 720h (default from org settings; 1h at most for --agent)")
	f.StringVar(&in.Agent, "agent", "", "Mint the token for this agent id")
	return cmd
}

func newTokenRevoke(a *app) *cobra.Command {
	return revokeCmd(a, "token", "Revoke a token", `Revoke a token by id (see 'token ls'). Using it afterwards fails with exit 5.

Example:
  lorvik token revoke 106666050296328877`, (*client.Client).RevokeToken)
}
