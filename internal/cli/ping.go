package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/apitypes"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func newPing(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "ping",
		Short: "Check that the server is reachable",
		Long: `Call the server health endpoint and print its status. Use it to confirm
LORVIK_API_URL is right before anything else.

Example:
  lorvik ping
  lorvik ping --api-url http://localhost:8080`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client()
			if err != nil {
				return err
			}
			h, err := c.Health(cmd.Context())
			if err != nil {
				return fmt.Errorf("server unreachable: %w", err)
			}
			return a.emit(h, func(p *cmdutil.Printer) { printHealth(p, h) })
		},
	}
}

func printHealth(p *cmdutil.Printer, h apitypes.Health) {
	p.Line("%s server %s database=%s", h.Status, h.Version, h.Database)
}
