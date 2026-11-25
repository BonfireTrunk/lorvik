package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/sethvargo/go-envconfig"
)

func TestLoadFrom(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
		check   func(t *testing.T, c Server)
	}{
		{
			name: "defaults apply when nothing is set",
			env:  map[string]string{},
			check: func(t *testing.T, c Server) {
				if c.HTTPAddr != ":8080" || c.LogLevel != "info" || c.LogFormat != "text" {
					t.Errorf("defaults = %+v", c)
				}
			},
		},
		{
			name:    "bad log level is rejected by name",
			env:     map[string]string{"LORVIK_LOG_LEVEL": "loud"},
			wantErr: "LORVIK_LOG_LEVEL",
		},
		{
			name:    "multiple problems are all reported",
			env:     map[string]string{"LORVIK_LOG_LEVEL": "loud", "LORVIK_LOG_FORMAT": "yaml"},
			wantErr: "LORVIK_LOG_FORMAT",
		},
		{
			name: "github login needs both id and secret",
			env:  map[string]string{"LORVIK_GITHUB_CLIENT_ID": "id"},
			check: func(t *testing.T, c Server) {
				if c.GitHubEnabled() {
					t.Error("GitHubEnabled() with only the id set")
				}
				c.GitHubClientSecret = "secret"
				if !c.GitHubEnabled() {
					t.Error("GitHubEnabled() = false with id and secret")
				}
			},
		},
		{
			name: "trusted proxies take addresses and ranges",
			env:  map[string]string{"LORVIK_TRUSTED_PROXIES": "10.0.0.5, 172.16.0.0/12,2001:db8::/32"},
			check: func(t *testing.T, c Server) {
				got, err := c.Proxies()
				if err != nil || len(got) != 3 || got[0].String() != "10.0.0.5/32" || got[1].String() != "172.16.0.0/12" || got[2].String() != "2001:db8::/32" {
					t.Errorf("Proxies() = %v, %v", got, err)
				}
			},
		},
		{
			name: "a trusted range written as IPv4-mapped IPv6 is its IPv4 range",
			env:  map[string]string{"LORVIK_TRUSTED_PROXIES": "::ffff:10.0.0.0/104, ::ffff:192.0.2.7"},
			check: func(t *testing.T, c Server) {
				got, err := c.Proxies()
				if err != nil || len(got) != 2 || got[0].String() != "10.0.0.0/8" || got[1].String() != "192.0.2.7/32" {
					t.Errorf("Proxies() = %v, %v; want 10.0.0.0/8 and 192.0.2.7/32", got, err)
				}
			},
		},
		{
			name:    "a trusted range covering every IPv4 address is refused",
			env:     map[string]string{"LORVIK_TRUSTED_PROXIES": "0.0.0.0/0"},
			wantErr: "LORVIK_TRUSTED_PROXIES",
		},
		{
			name:    "a trusted range covering every IPv6 address is refused",
			env:     map[string]string{"LORVIK_TRUSTED_PROXIES": "10.0.0.1,::/0"},
			wantErr: "LORVIK_TRUSTED_PROXIES",
		},
		{
			name: "no trusted proxies by default",
			env:  map[string]string{},
			check: func(t *testing.T, c Server) {
				if got, err := c.Proxies(); err != nil || len(got) != 0 {
					t.Errorf("Proxies() = %v, %v; want none", got, err)
				}
			},
		},
		{
			name: "cors origins split on comma",
			env:  map[string]string{"LORVIK_CORS_ORIGINS": "https://a.example,https://b.example"},
			check: func(t *testing.T, c Server) {
				if len(c.CORSOrigins) != 2 || c.CORSOrigins[1] != "https://b.example" {
					t.Errorf("CORSOrigins = %v", c.CORSOrigins)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := LoadFrom(t.Context(), envconfig.MapLookuper(tt.env))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadFrom() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadFrom() error = %v", err)
			}
			tt.check(t, cfg)
		})
	}
}

func TestBadTrustedProxyFailsTheLoadNamingOnlyTheVariable(t *testing.T) {
	t.Parallel()
	_, err := LoadFrom(t.Context(), envconfig.MapLookuper(map[string]string{"LORVIK_TRUSTED_PROXIES": "10.0.0.5,proxy.internal"}))
	if err == nil || !strings.Contains(err.Error(), "LORVIK_TRUSTED_PROXIES") {
		t.Fatalf("LoadFrom() error = %v, want one naming LORVIK_TRUSTED_PROXIES", err)
	}
	for _, value := range []string{"proxy.internal", "10.0.0.5"} {
		if strings.Contains(err.Error(), value) {
			t.Errorf("error %q repeats the value %q", err, value)
		}
	}
}

func TestRequireDatabase(t *testing.T) {
	t.Parallel()
	var c Server
	err := c.RequireDatabase()
	if err == nil || !strings.Contains(err.Error(), "LORVIK_DATABASE_URL") {
		t.Fatalf("RequireDatabase() = %v, want error naming the variable", err)
	}
	c.DatabaseURL = "postgres://x"
	if err := c.RequireDatabase(); err != nil {
		t.Fatalf("RequireDatabase() with URL = %v, want nil", err)
	}
	var joined interface{ Unwrap() []error }
	if errors.As(Server{LogLevel: "x", LogFormat: "y", ShutdownTimeout: 1}.validate(), &joined) && len(joined.Unwrap()) != 2 {
		t.Errorf("validate() joined %d errors, want 2", len(joined.Unwrap()))
	}
}
