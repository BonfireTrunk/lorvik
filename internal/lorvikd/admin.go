package lorvikd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/BonfireTrunk/lorvik/internal/config"
	"github.com/BonfireTrunk/lorvik/internal/domain"
	"github.com/BonfireTrunk/lorvik/internal/server"
	"github.com/BonfireTrunk/lorvik/internal/store"
)

// openStore loads configuration and connects to Postgres.
func openStore(cmd *cobra.Command) (*store.Store, error) {
	cfg, err := config.Load(cmd.Context())
	if err != nil {
		return nil, err
	}
	if err := cfg.RequireDatabase(); err != nil {
		return nil, err
	}
	st, err := store.Open(cmd.Context(), cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return st, nil
}

func newMigrate(stdout io.Writer) *cobra.Command {
	cmd := &cobra.Command{Use: "migrate", Short: "Manage database migrations"}
	cmd.AddCommand(&cobra.Command{
		Use:   "up",
		Short: "Apply all pending migrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openStore(cmd)
			if err != nil {
				return err
			}
			defer st.Close()
			if err := st.Migrate(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "migrations applied")
			return nil
		},
	}, &cobra.Command{
		Use:   "status",
		Short: "List migrations and whether they are applied",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openStore(cmd)
			if err != nil {
				return err
			}
			defer st.Close()
			infos, err := st.MigrationStatus(cmd.Context())
			if err != nil {
				return err
			}
			for _, m := range infos {
				state := "pending"
				if m.Applied {
					state = "applied " + m.AppliedAt.UTC().Format(time.RFC3339)
				}
				fmt.Fprintf(stdout, "%05d  %-8s  %s\n", m.Version, state, m.Name)
			}
			return nil
		},
	})
	return cmd
}

// bootstrapOpts are the inputs of `lorvikd admin bootstrap`.
type bootstrapOpts struct {
	OrgName  string
	Login    string
	Name     string
	Email    string
	Password string
	Locale   string
	Timezone string
}

func newAdmin(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{Use: "admin", Short: "Administrative commands"}
	var o bootstrapOpts
	bootstrap := &cobra.Command{
		Use:   "bootstrap",
		Short: "Create the first org, its owner and a user token",
		Long: `Create the first org, a user who owns it and a token for that user.
The token secret is printed once on stdout; everything else goes to stderr.
With --password (or LORVIK_BOOTSTRAP_PASSWORD) the user can also sign in at
POST /login when the server runs with LORVIK_AUTH_PASSWORD=true.
Refuses to run if any org already exists.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.Password == "" {
				o.Password = os.Getenv("LORVIK_BOOTSTRAP_PASSWORD")
			}
			st, err := openStore(cmd)
			if err != nil {
				return err
			}
			defer st.Close()
			ids, err := domain.NewIDGen(-1, time.Now)
			if err != nil {
				return err
			}
			secret, err := bootstrap(cmd, serverStore{st}, ids, o, stderr)
			if err != nil {
				return err
			}
			fmt.Fprintln(stdout, secret)
			return nil
		},
	}
	bootstrap.Flags().StringVar(&o.OrgName, "org-name", "", "org display name")
	bootstrap.Flags().StringVar(&o.Login, "user", "", "login of the first user")
	bootstrap.Flags().StringVar(&o.Name, "name", "", "display name of the first user (default: the login)")
	bootstrap.Flags().StringVar(&o.Email, "email", "", "email of the first user")
	bootstrap.Flags().StringVar(&o.Password, "password", "", "password for POST /login (or LORVIK_BOOTSTRAP_PASSWORD); omit for token-only access")
	bootstrap.Flags().StringVar(&o.Locale, "locale", "", "BCP 47 locale of the first user (optional)")
	bootstrap.Flags().StringVar(&o.Timezone, "timezone", "", "IANA time zone of the first user (optional; clients use their own zone otherwise)")
	for _, f := range []string{"org-name", "user"} {
		_ = bootstrap.MarkFlagRequired(f)
	}
	cmd.AddCommand(bootstrap)
	return cmd
}

// bootstrap creates the org, its owner, an optional password identity and a
// token in one transaction and returns the token secret.
func bootstrap(cmd *cobra.Command, st server.Store, ids *domain.IDGen, o bootstrapOpts, stderr io.Writer) (string, error) {
	ctx := cmd.Context()
	o.OrgName, o.Login = strings.TrimSpace(o.OrgName), strings.TrimSpace(o.Login)
	if o.Login == "" || o.OrgName == "" {
		return "", errors.New("--org-name and --user must not be empty")
	}
	if o.Name == "" {
		o.Name = o.Login
	}
	var hash []byte
	if o.Password != "" {
		var err error
		if hash, err = domain.HashPassword(o.Password); err != nil {
			return "", err
		}
	}
	now := time.Now()
	u := domain.User{ID: ids.New(), Login: o.Login, Name: o.Name, Email: o.Email, Locale: o.Locale, Timezone: o.Timezone,
		Status: domain.RecordActive, CreatedAt: now, UpdatedAt: now}
	if err := u.Validate(); err != nil {
		return "", err
	}
	var secret string
	err := st.WithTx(ctx, func(tx server.Store) error {
		orgs, err := tx.ListOrgs(ctx)
		if err != nil {
			return fmt.Errorf("check orgs: %w", err)
		}
		if len(orgs) > 0 {
			return fmt.Errorf("org %q already exists; bootstrap only creates the first org", orgs[0].Name)
		}
		if u, err = tx.CreateUser(ctx, u); err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		if hash != nil {
			id := domain.Identity{ID: ids.New(), UserID: u.ID, Provider: domain.ProviderPassword, ProviderID: domain.PasswordKey(o.Login),
				Secret: hash, Login: o.Login, CreatedAt: now}
			if _, err := tx.CreateIdentity(ctx, id); err != nil {
				return fmt.Errorf("create password identity: %w", err)
			}
		}
		org, _, err := server.NewOrg(ctx, tx, ids, o.OrgName, u.Ref(), now)
		if err != nil {
			return err
		}
		t, sec := domain.NewToken(ids.New(), org.ID, u.Ref(), "bootstrap", domain.ScopeWrite, org.Settings.TokenTTLDefaultOrDefault(), now)
		if _, err := tx.CreateToken(ctx, t); err != nil {
			return fmt.Errorf("create token: %w", err)
		}
		secret = sec
		fmt.Fprintf(stderr, "created org %s (%s) and user %s (%s)\n", org.Name, org.ID, u.Login, u.ID)
		if hash != nil {
			fmt.Fprintln(stderr, "password login enabled for the user when the server runs with LORVIK_AUTH_PASSWORD=true")
		}
		fmt.Fprintf(stderr, "token secret follows on stdout; it is shown once and expires %s\n", t.ExpiresAt.UTC().Format(time.RFC3339))
		return nil
	})
	return secret, err
}
