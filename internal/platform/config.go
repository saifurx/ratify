// Package platform holds config, auth and HTTP helpers.
package platform

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	Env          string
	Addr         string
	DatabaseURL  string
	OIDCIssuer   string
	OIDCClientID string
	CursorSecret string
}

// LoadConfig reads the environment and validates boot-time invariants.
func LoadConfig() (Config, error) { return configFrom(os.Getenv) }

func configFrom(get func(string) string) (Config, error) {
	c := Config{
		Env:          strings.ToLower(strings.TrimSpace(get("ENV"))),
		Addr:         get("ADDR"),
		DatabaseURL:  get("DATABASE_URL"),
		OIDCIssuer:   strings.TrimSpace(get("OIDC_ISSUER")),
		OIDCClientID: strings.TrimSpace(get("OIDC_CLIENT_ID")),
		CursorSecret: get("CURSOR_SECRET"),
	}
	if c.Env == "" {
		c.Env = "development"
	}
	if c.Addr == "" {
		c.Addr = ":8080"
	}
	if c.DatabaseURL == "" {
		return c, errors.New("config: DATABASE_URL is required")
	}
	if c.CursorSecret == "" {
		if c.Env == "production" {
			return c, errors.New("config: CURSOR_SECRET is required when ENV=production")
		}
		c.CursorSecret = "dev-cursor-secret-do-not-use-in-production"
	}
	if c.Env == "production" && c.OIDCIssuer == "" {
		return c, errors.New("config: OIDC_ISSUER is required when ENV=production")
	}
	if c.OIDCIssuer != "" && c.OIDCClientID == "" {
		return c, errors.New("config: OIDC_CLIENT_ID is required when OIDC_ISSUER is set")
	}
	return c, nil
}

// DevMode trusts X-Tenant-Id / X-User-Id headers. Only when no OIDC issuer
// is configured AND the environment is not production.
func (c Config) DevMode() bool { return c.OIDCIssuer == "" && c.Env != "production" }
