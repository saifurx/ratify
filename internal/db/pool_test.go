//go:build dbtest

package db

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func mustEnv(t *testing.T, k string) string {
	t.Helper()
	v := os.Getenv(k)
	if v == "" {
		t.Fatalf("%s must be set (run via make test)", k)
	}
	return v
}

func withUser(t *testing.T, base, user, pw string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(user, pw)
	return u.String()
}

func TestAssertNotPrivileged(t *testing.T) {
	ctx := context.Background()
	adminURL := mustEnv(t, "ADMIN_URL")
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close) // registered first => runs last, after dropProbes

	dropProbes := func() {
		for _, r := range []string{"probe_owner", "probe_bypass"} {
			if _, err := admin.Exec(ctx, `DO $$ BEGIN IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='`+r+`') THEN DROP OWNED BY `+r+`; END IF; END $$`); err != nil {
				t.Errorf("cleanup %s: %v", r, err)
			}
			if _, err := admin.Exec(ctx, `DROP ROLE IF EXISTS `+r); err != nil {
				t.Errorf("cleanup %s: %v", r, err)
			}
		}
	}
	dropProbes()
	for _, s := range []string{
		`CREATE ROLE probe_owner LOGIN PASSWORD 'pw'`,
		`CREATE ROLE probe_bypass LOGIN PASSWORD 'pw' BYPASSRLS`,
		`GRANT CONNECT ON DATABASE ratify TO probe_owner, probe_bypass`,
		`CREATE TABLE public.probe_owned (id int)`,
		`ALTER TABLE public.probe_owned OWNER TO probe_owner`,
	} {
		if _, err := admin.Exec(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	t.Cleanup(dropProbes)

	cases := []struct{ name, url, wantErr string }{
		{"superuser", adminURL, "superuser"},
		{"app_migrator", mustEnv(t, "MIGRATOR_URL"), "app_migrator"},
		{"table owner", withUser(t, adminURL, "probe_owner", "pw"), "table owner"},
		{"bypassrls", withUser(t, adminURL, "probe_bypass", "pw"), "BYPASSRLS"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := pgxpool.New(ctx, c.url)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			err = assertNotPrivileged(ctx, p, RoleApp)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("want error containing %q, got %v", c.wantErr, err)
			}
		})
	}
	t.Run("app_user passes", func(t *testing.T) {
		p, err := NewPool(ctx, RoleApp, mustEnv(t, "DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		if err := p.AssertNotPrivileged(ctx, RoleApp); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("unknown role rejected", func(t *testing.T) {
		if _, err := NewPool(ctx, Role("nobody"), mustEnv(t, "DATABASE_URL")); err == nil {
			t.Fatal("expected error")
		}
	})
}
