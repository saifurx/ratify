//go:build dbtest

package tests

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ratify/internal/db"
	"ratify/internal/tenant"
)

// Proves set_tenant is transaction-scoped under PgBouncer transaction
// pooling: 20 concurrent goroutines alternating 4 tenants share only 2
// server connections, and no request ever sees another tenant's rows.
// (The rest of the suite also runs through PgBouncer: DATABASE_URL and
// RELAY_URL point at it.)
func TestPgBouncerIsolation(t *testing.T) {
	ctx := context.Background()
	const tenants, goroutines, iterations = 4, 20, 40

	ids := make([]string, tenants)
	for i := range ids {
		names := make([]string, 5)
		for j := range names {
			names[j] = fmt.Sprintf("t%d-acct%d", i, j)
		}
		ids[i] = seed(t, names...)
	}

	pool, err := db.NewPool(ctx, db.RoleApp, env(t, "PGBOUNCER_SMALL_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.AssertNotPrivileged(ctx, db.RoleApp); err != nil {
		t.Fatal(err)
	}
	tx := db.NewTenantTx(pool)
	raw, err := db.RawPoolForTest(ctx, env(t, "PGBOUNCER_SMALL_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()

	var (
		mu       sync.Mutex
		backends = map[int]bool{}
		leaks    atomic.Int64
		wg       sync.WaitGroup
		errs     = make(chan error, goroutines)
	)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				ti := (g + i) % tenants
				err := tx.WithTenant(tenant.With(ctx, ids[ti]), func(c context.Context, q db.Tx) error {
					var pid int
					if err := q.QueryRow(c, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
						return err
					}
					mu.Lock()
					backends[pid] = true
					mu.Unlock()
					rows, err := q.Query(c, "SELECT name FROM accounts")
					if err != nil {
						return err
					}
					defer rows.Close()
					n := 0
					for rows.Next() {
						var name string
						if err := rows.Scan(&name); err != nil {
							return err
						}
						n++
						if !strings.HasPrefix(name, fmt.Sprintf("t%d-", ti)) {
							leaks.Add(1)
						}
					}
					if n != 5 {
						leaks.Add(1)
					}
					// Hold the server connection so goroutines interleave.
					_, err = q.Exec(c, "SELECT pg_sleep(0.002)")
					return err
				})
				if err != nil {
					errs <- err
					return
				}
				// Unbound query on a (very likely reused) server connection:
				// must see nothing, i.e. no tenant binding survived.
				var n int
				if err := raw.QueryRow(ctx, "SELECT count(*) FROM accounts").Scan(&n); err != nil {
					errs <- err
					return
				}
				if n != 0 {
					leaks.Add(1)
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if n := leaks.Load(); n != 0 {
		t.Fatalf("%d cross-tenant observations through PgBouncer", n)
	}
	if len(backends) > 2 {
		t.Fatalf("saw %d server backends, pool_size=2 not in effect", len(backends))
	}
	t.Logf("%d goroutines x %d iterations over %d server connections: no leaks", goroutines, iterations, len(backends))
}
