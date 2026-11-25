package cli

import (
	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/build"
	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

func newVersion(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Long: `Print the lorvik version, commit and Go version. Works offline.

Example:
  lorvik version
  lorvik version --json`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(_ *cobra.Command, _ []string) error {
			info := build.Current()
			return a.emit(info, func(p *cmdutil.Printer) {
				line := "lorvik " + info.Version
				if info.Commit != "" {
					line += " (" + info.Commit + ")"
				}
				p.Line("%s %s", line, info.Go)
			})
		},
	}
}
