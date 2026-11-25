// Package config loads server configuration from the environment.
//
// Every setting is an environment variable prefixed with LORVIK_. There are
// no config files and no flags for the server: the same binary runs
// self-hosted and in the cloud, differing only by environment.
package config

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/sethvargo/go-envconfig"
)

// Server holds all lorvikd settings.
type Server struct {
	// HTTPAddr is the listen address, for example ":8080".
	HTTPAddr string `env:"HTTP_ADDR, default=:8080"`
	// DatabaseURL is a pgx connection string. Required by serve and migrate.
	DatabaseURL string `env:"DATABASE_URL"`
	// LogLevel is one of debug, info, warn, error.
	LogLevel string `env:"LOG_LEVEL, default=info"`
	// LogFormat is text or json.
	LogFormat string `env:"LOG_FORMAT, default=text"`
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT, default=15s"`
	// AutoMigrate runs pending migrations when serve starts. Off by default so
	// upgrades are a deliberate `lorvikd migrate up`.
	AutoMigrate bool `env:"AUTO_MIGRATE, default=false"`
	// DevUIProxy, when set, proxies non-API requests to a Vite dev server
	// (for example http://localhost:5173) instead of serving the embedded UI.
	DevUIProxy string `env:"DEV_UI_PROXY"`
	// CORSOrigins lists browser origins allowed to call the API cross-origin.
	// Empty means same-origin only, which is the self-host default.
	CORSOrigins []string `env:"CORS_ORIGINS"`
	// AuthPassword enables POST /login with a username and password. It is
	// meant for development and self-hosting; off by default.
	AuthPassword bool `env:"AUTH_PASSWORD, default=false"`
	// GitHubClientID and GitHubClientSecret are the GitHub OAuth app's
	// credentials; GitHub sign-in is on when both are set.
	GitHubClientID     string `env:"GITHUB_CLIENT_ID"`
	GitHubClientSecret string `env:"GITHUB_CLIENT_SECRET"`
	// PublicURL is the externally visible base URL, for example
	// https://lorvik.example.com. Empty derives it from each request.
	PublicURL string `env:"PUBLIC_URL"`
	// TrustedProxies lists the reverse proxies in front of the server, as IP
	// addresses or CIDR ranges. A request from one of them takes its client
	// address from X-Forwarded-For; from anyone else the header is ignored,
	// since a client could write whatever address it liked into it. Empty
	// means the peer of the connection is the client.
	TrustedProxies []string `env:"TRUSTED_PROXIES"`
}

// Proxies is TrustedProxies as prefixes. The error names the variable only:
// an operator's network layout does not belong in a log.
func (c Server) Proxies() ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range c.TrustedProxies {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		p, err := netip.ParsePrefix(s)
		if err != nil {
			a, aerr := netip.ParseAddr(s)
			if aerr != nil {
				return nil, fmt.Errorf("%sTRUSTED_PROXIES: must be a comma-separated list of IP addresses or CIDR ranges", Prefix)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		// Peers are compared as plain IPv4, so a range written in the
		// IPv4-mapped form (::ffff:10.0.0.0/104) is read as its IPv4 range.
		if a := p.Addr(); a.Is4In6() && p.Bits() >= 96 {
			p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
		}
		if p.Bits() == 0 {
			return nil, fmt.Errorf("%sTRUSTED_PROXIES: a range that covers every address would believe X-Forwarded-For from anyone; list the proxies", Prefix)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// GitHubEnabled reports whether GitHub sign-in is configured.
func (c Server) GitHubEnabled() bool {
	return c.GitHubClientID != "" && c.GitHubClientSecret != ""
}

// Prefix is prepended to every variable name.
const Prefix = "LORVIK_"

// Load reads the server configuration from the process environment.
func Load(ctx context.Context) (Server, error) {
	return load(ctx, envconfig.OsLookuper())
}

// LoadFrom reads configuration from an arbitrary lookuper. Tests use it with
// envconfig.MapLookuper so they can run in parallel without t.Setenv.
func LoadFrom(ctx context.Context, l envconfig.Lookuper) (Server, error) {
	return load(ctx, l)
}

func load(ctx context.Context, l envconfig.Lookuper) (Server, error) {
	var cfg Server
	err := envconfig.ProcessWith(ctx, &envconfig.Config{
		Target:   &cfg,
		Lookuper: envconfig.PrefixLookuper(Prefix, l),
	})
	if err != nil {
		return Server{}, fmt.Errorf("read environment: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return Server{}, err
	}
	return cfg, nil
}

func (c Server) validate() error {
	var errs []error
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("%sLOG_LEVEL: %q is not one of debug, info, warn, error", Prefix, c.LogLevel))
	}
	switch c.LogFormat {
	case "text", "json":
	default:
		errs = append(errs, fmt.Errorf("%sLOG_FORMAT: %q is not text or json", Prefix, c.LogFormat))
	}
	if c.ShutdownTimeout <= 0 {
		errs = append(errs, fmt.Errorf("%sSHUTDOWN_TIMEOUT: must be positive", Prefix))
	}
	if _, err := c.Proxies(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// RequireDatabase returns an error naming the variable when no database URL
// is configured. Commands that need Postgres call it; `version` does not.
func (c Server) RequireDatabase() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("%sDATABASE_URL is required", Prefix)
	}
	return nil
}
