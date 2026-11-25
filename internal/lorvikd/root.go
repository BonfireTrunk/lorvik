// Package lorvikd defines the lorvikd server command tree.
package lorvikd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/build"
	"github.com/BonfireTrunk/lorvik/internal/config"
	"github.com/BonfireTrunk/lorvik/internal/logging"
	"github.com/BonfireTrunk/lorvik/internal/server"
	"github.com/BonfireTrunk/lorvik/internal/store"
	"github.com/BonfireTrunk/lorvik/web"
)

// Main is the entry point called by cmd/lorvikd. It returns the exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := newRoot(stdout, stderr)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(stderr, "lorvikd:", err)
		return 1
	}
	return 0
}

func newRoot(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "lorvikd",
		Short:         "Lorvik server: API, dashboard, and database migrations",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(newServe(stderr), newVersion(stdout), newMigrate(stdout), newAdmin(stdout, stderr))
	return root
}

func newVersion(stdout io.Writer) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			info := build.Current()
			if asJSON {
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(info)
			}
			fmt.Fprintf(stdout, "lorvikd %s", info.Version)
			if info.Commit != "" {
				fmt.Fprintf(stdout, " (%s)", info.Commit)
			}
			fmt.Fprintf(stdout, " %s ui=%t\n", info.Go, web.Embedded)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func newServe(stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP server",
		Long: `Run the HTTP server. Configuration comes from LORVIK_* environment
variables; see "lorvikd serve --help" for the list.

  LORVIK_HTTP_ADDR         listen address (default :8080)
  LORVIK_DATABASE_URL      Postgres connection string (required from phase 1)
  LORVIK_LOG_LEVEL         debug, info, warn, error (default info)
  LORVIK_LOG_FORMAT        text or json (default text)
  LORVIK_AUTO_MIGRATE      apply pending migrations on start (default false)
  LORVIK_SHUTDOWN_TIMEOUT  graceful shutdown bound (default 15s)
  LORVIK_DEV_UI_PROXY      proxy the UI to a Vite dev server, e.g. http://localhost:5173
  LORVIK_CORS_ORIGINS      comma-separated browser origins allowed cross-origin
  LORVIK_AUTH_PASSWORD     enable username/password login at POST /login (default false)
  LORVIK_GITHUB_CLIENT_ID      GitHub OAuth app client id; GitHub sign-in needs it and the secret
  LORVIK_GITHUB_CLIENT_SECRET  GitHub OAuth app client secret
  LORVIK_PUBLIC_URL            external base URL, for the GitHub callback (default: from the request)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cmd.Context())
			if err != nil {
				return err
			}
			logger := logging.New(stderr, cfg.LogLevel, cfg.LogFormat)
			ui, err := web.FS()
			if err != nil {
				return fmt.Errorf("embedded ui: %w", err)
			}
			deps := server.Deps{Config: cfg, Logger: logger, UI: ui}
			if cfg.DatabaseURL != "" {
				st, err := store.Open(cmd.Context(), cfg.DatabaseURL)
				if err != nil {
					return fmt.Errorf("open database: %w", err)
				}
				defer st.Close()
				if cfg.AutoMigrate {
					if err := st.Migrate(cmd.Context()); err != nil {
						return err
					}
				}
				deps.Store = serverStore{st}
			}
			srv, err := server.New(deps)
			if err != nil {
				return err
			}
			return srv.Run(cmd.Context())
		},
	}
}
