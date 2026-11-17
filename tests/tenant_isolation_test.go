//go:build dbtest

// REQUIRED GATE: must pass before any business logic. Runs as app_user;
// seeding runs as app_migrator.
package tests

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"ratify/internal/db"
	"ratify/internal/tenant"
)

func env(t *testing.T, k string) string {
	t.Helper()
	v := os.Getenv(k)
	if v == "" {
		t.Fatalf("%s must be set (run via make test)", k)
	}
	return v
}

func newUUID(t *testing.T) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// seed creates a tenant with the given account names as app_migrator.
func seed(t *testing.T, names ...string) string {
	t.Helper()
	ctx := context.Background()
	m, err := db.RawPoolForTest(ctx, env(t, "MIGRATOR_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	id := newUUID(t)
	tx, err := m.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	must := func(_ any, err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tx.Exec(ctx, "SELECT set_tenant($1::uuid)", id))
	must(tx.Exec(ctx, "INSERT INTO tenants (id, name) VALUES ($1, 'test')", id))
	for _, n := range names {
		must(tx.Exec(ctx, "INSERT INTO accounts (tenant_id, name) VALUES (current_tenant(), $1)", n))
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTenantIsolation(t *testing.T) {
	ctx := context.Background()
	a := seed(t, "a1", "a2")
	b := seed(t, "b1")

	pool, err := db.NewPool(ctx, db.RoleApp, env(t, "DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx := db.NewTenantTx(pool)

	names := func(c context.Context) ([]string, error) {
		var out []string
		err := tx.WithTenant(c, func(c context.Context, q db.Tx) error {
			rows, err := q.Query(c, "SELECT name FROM accounts ORDER BY name")
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var n string
				if err := rows.Scan(&n); err != nil {
					return err
				}
				out = append(out, n)
			}
			return rows.Err()
		})
		return out, err
	}

	t.Run("sees only own rows", func(t *testing.T) {
		got, err := names(tenant.With(ctx, a))
		if err != nil || len(got) != 2 || got[0] != "a1" || got[1] != "a2" {
			t.Fatalf("tenant A: %v %v", got, err)
		}
		got, err = names(tenant.With(ctx, b))
		if err != nil || len(got) != 1 || got[0] != "b1" {
			t.Fatalf("tenant B: %v %v", got, err)
		}
	})

	t.Run("no tenant context fails closed", func(t *testing.T) {
		if _, err := names(ctx); !errors.Is(err, db.ErrNoTenant) {
			t.Fatalf("want ErrNoTenant, got %v", err)
		}
		if _, err := names(tenant.With(ctx, "not-a-uuid")); !errors.Is(err, db.ErrNoTenant) {
			t.Fatalf("invalid tenant must fail closed, got %v", err)
		}
	})

	t.Run("raw query outside wrapper sees nothing", func(t *testing.T) {
		raw, err := db.RawPoolForTest(ctx, env(t, "DATABASE_URL")) // 1 connection
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		count := func() int {
			var n int
			if err := raw.QueryRow(ctx, "SELECT count(*) FROM accounts").Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		if n := count(); n != 0 {
			t.Fatalf("unbound query saw %d rows", n)
		}
		// Binding inside a transaction must not survive it on the same
		// pooled connection (proves set_tenant is transaction-scoped).
		btx, _ := raw.Begin(ctx)
		if _, err := btx.Exec(ctx, "SELECT set_tenant($1::uuid)", a); err != nil {
			t.Fatal(err)
		}
		var inTx int
		_ = btx.QueryRow(ctx, "SELECT count(*) FROM accounts").Scan(&inTx)
		_ = btx.Commit(ctx)
		if inTx != 2 {
			t.Fatalf("bound tx saw %d rows, want 2", inTx)
		}
		if n := count(); n != 0 {
			t.Fatalf("tenant binding leaked past the transaction: %d rows", n)
		}
	})

	t.Run("cannot insert into another tenant", func(t *testing.T) {
		err := tx.WithTenant(tenant.With(ctx, a), func(c context.Context, q db.Tx) error {
			_, err := q.Exec(c, "INSERT INTO accounts (tenant_id, name) VALUES ($1, 'evil')", b)
			return err
		})
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "42501" {
			t.Fatalf("want RLS violation 42501, got %v", err)
		}
		got, _ := names(tenant.With(ctx, b))
		if len(got) != 1 {
			t.Fatalf("tenant B gained rows: %v", got)
		}
	})
}
