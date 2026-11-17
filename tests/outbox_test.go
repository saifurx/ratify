//go:build dbtest

package tests

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"ratify/internal/account"
	"ratify/internal/db"
	"ratify/internal/events"
	"ratify/internal/outbox"
	"ratify/internal/tenant"
)

func brokers(t *testing.T) []string { return strings.Split(env(t, "KAFKA_BROKERS"), ",") }

func uniq(p string) string { return p + "-" + strings.ReplaceAll(newUUID(nil), "-", "")[:8] }

func appPool(t *testing.T) (*db.Pool, *db.TenantTx) {
	t.Helper()
	p, err := db.NewPool(context.Background(), db.RoleApp, env(t, "DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p, db.NewTenantTx(p)
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", d, what)
}

// newRelay builds a relay with fast timings. Compose's relay is stopped by
// `make test`, and test relays use their own transactional ids.
func newRelay(t *testing.T, txid string, hooks outbox.Hooks) *outbox.Relay {
	t.Helper()
	rp, err := db.NewPool(context.Background(), db.RoleRelay, env(t, "RELAY_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rp.Close)
	r, err := outbox.NewRelay(context.Background(), rp, outbox.RelayConfig{
		Brokers: brokers(t), TransactionalID: txid, Hooks: hooks,
		PollInterval: 20 * time.Millisecond, BackoffBase: time.Millisecond, BackoffMax: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}

// runRelay runs the relay in the background until the returned stop func.
func runRelay(r *outbox.Relay) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = r.Run(ctx) }()
	return func() { cancel(); wg.Wait() }
}

func appendRow(t *testing.T, tx *db.TenantTx, tid, topic, key string, payload any) string {
	t.Helper()
	var id string
	err := tx.WithTenant(tenant.With(context.Background(), tid), func(c context.Context, q db.Tx) (err error) {
		id, err = outbox.Append(c, q, topic, key, payload)
		return
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type rowState struct {
	dispatched, dead bool
	attempts         int
	lastErr          string
}

func state(t *testing.T, tx *db.TenantTx, tid, id string) rowState {
	t.Helper()
	var s rowState
	err := tx.WithTenant(tenant.With(context.Background(), tid), func(c context.Context, q db.Tx) error {
		return q.QueryRow(c, `SELECT dispatched_at IS NOT NULL, dead_lettered_at IS NOT NULL, attempts, coalesce(last_error,'')
		                        FROM outbox WHERE id = $1::uuid`, id).Scan(&s.dispatched, &s.dead, &s.attempts, &s.lastErr)
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func read(t *testing.T, topic string) []events.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	evs, err := events.ReadCommitted(ctx, brokers(t), topic, 1500*time.Millisecond)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	return evs
}

func byKey(evs []events.Event, key string) (out []events.Event) {
	for _, e := range evs {
		if e.Key == key {
			out = append(out, e)
		}
	}
	return
}

func count(t *testing.T, tx *db.TenantTx, tid, sql string, args ...any) (n int) {
	t.Helper()
	err := tx.WithTenant(tenant.With(context.Background(), tid), func(c context.Context, q db.Tx) error {
		return q.QueryRow(c, sql, args...).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestOutbox(t *testing.T) {
	ctx := context.Background()
	_, tx := appPool(t)
	repo := account.Repository{}

	t.Run("1 account creation writes exactly one outbox row in the same tx", func(t *testing.T) {
		tid := seed(t)
		boom := errors.New("boom")
		err := tx.WithTenant(tenant.With(ctx, tid), func(c context.Context, q db.Tx) error {
			if _, err := repo.Create(c, q, "rolled-back", nil); err != nil {
				return err
			}
			return boom // failure AFTER Append
		})
		if !errors.Is(err, boom) {
			t.Fatalf("want boom, got %v", err)
		}
		if a, o := count(t, tx, tid, "SELECT count(*) FROM accounts"), count(t, tx, tid, "SELECT count(*) FROM outbox"); a != 0 || o != 0 {
			t.Fatalf("rollback left accounts=%d outbox=%d", a, o)
		}
		var acct account.Account
		if err := tx.WithTenant(tenant.With(ctx, tid), func(c context.Context, q db.Tx) (err error) {
			acct, err = repo.Create(c, q, "committed", nil)
			return
		}); err != nil {
			t.Fatal(err)
		}
		if o := count(t, tx, tid, "SELECT count(*) FROM outbox WHERE topic='ratify.account.created' AND key=$1", acct.ID); o != 1 {
			t.Fatalf("want exactly 1 outbox row, got %d", o)
		}
	})

	t.Run("2 relay publishes within 2s with event_id and tenant_id headers", func(t *testing.T) {
		tid := seed(t)
		stop := runRelay(newRelay(t, uniq("t2"), outbox.Hooks{}))
		defer stop()
		// Warm-up: the first publish pays producer init + topic auto-creation,
		// which a long-running relay has already paid. Measure steady state.
		warm := appendRow(t, tx, tid, "ratify.account.created", "warmup", map[string]string{"warm": "up"})
		waitFor(t, 30*time.Second, "warm-up dispatch", func() bool { return state(t, tx, tid, warm).dispatched })
		var acct account.Account
		start := time.Now()
		if err := tx.WithTenant(tenant.With(ctx, tid), func(c context.Context, q db.Tx) (err error) {
			acct, err = repo.Create(c, q, "published", nil)
			return
		}); err != nil {
			t.Fatal(err)
		}
		var id string
		_ = tx.WithTenant(tenant.With(ctx, tid), func(c context.Context, q db.Tx) error {
			return q.QueryRow(c, "SELECT id::text FROM outbox WHERE key=$1", acct.ID).Scan(&id)
		})
		waitFor(t, 2*time.Second, "dispatched_at", func() bool { return state(t, tx, tid, id).dispatched })
		t.Logf("dispatched %s after commit", time.Since(start))
		got := byKey(read(t, "ratify.account.created"), acct.ID)
		if len(got) != 1 || got[0].ID != id || got[0].TenantID != tid || got[0].Type != "ratify.account.created" || got[0].OccurredAt == "" {
			t.Fatalf("unexpected events: %+v (want event_id %s tenant %s)", got, id, tid)
		}
	})

	t.Run("3 crash before Kafka commit: old tx fenced and invisible, row republished once", func(t *testing.T) {
		tid := seed(t)
		topic, txid := uniq("t3.topic"), uniq("t3")
		id := appendRow(t, tx, tid, topic, "k", map[string]string{"n": "1"})

		a := newRelay(t, txid, outbox.Hooks{BeforeKafkaCommit: func() error { return outbox.ErrSimulatedCrash }})
		if err := a.Run(ctx); !errors.Is(err, outbox.ErrSimulatedCrash) {
			t.Fatalf("relay A should crash, got %v", err)
		}
		a.Close() // no abort: the Kafka transaction stays open, like kill -9
		if st := state(t, tx, tid, id); st.dispatched {
			t.Fatal("row marked dispatched by a crashed relay")
		}
		if evs := read(t, topic); len(evs) != 0 {
			t.Fatalf("uncommitted message visible to read_committed consumer: %+v", evs)
		}

		stop := runRelay(newRelay(t, txid, outbox.Hooks{})) // same transactional.id: fences A
		defer stop()
		waitFor(t, 15*time.Second, "republish", func() bool { return state(t, tx, tid, id).dispatched })
		if evs := read(t, topic); len(evs) != 1 || evs[0].ID != id {
			t.Fatalf("want exactly one republished message, got %+v", evs)
		}
	})

	t.Run("4 crash after Kafka commit: duplicate expected, consumer processes once", func(t *testing.T) {
		tid := seed(t)
		txid := uniq("t4")
		var acct account.Account
		if err := tx.WithTenant(tenant.With(ctx, tid), func(c context.Context, q db.Tx) (err error) {
			acct, err = repo.Create(c, q, "dup", nil)
			return
		}); err != nil {
			t.Fatal(err)
		}

		pool, _ := appPool(t)
		var mu sync.Mutex
		handled := 0
		cons, err := events.NewConsumer(pool, events.Config{
			Brokers: brokers(t), Group: uniq("g4"), Topics: []string{"ratify.account.created"},
		})
		if err != nil {
			t.Fatal(err)
		}
		cctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = cons.Run(cctx, func(_ context.Context, _ db.Tx, ev events.Event) error {
				if ev.Key == acct.ID {
					mu.Lock()
					handled++
					mu.Unlock()
				}
				return nil
			})
		}()
		defer func() { cancel(); <-done; cons.Close() }()

		a := newRelay(t, txid, outbox.Hooks{AfterKafkaCommit: func() error { return outbox.ErrSimulatedCrash }})
		if err := a.Run(ctx); !errors.Is(err, outbox.ErrSimulatedCrash) {
			t.Fatalf("relay A should crash, got %v", err)
		}
		a.Close()
		stop := runRelay(newRelay(t, txid, outbox.Hooks{}))
		defer stop()

		var id string
		_ = tx.WithTenant(tenant.With(ctx, tid), func(c context.Context, q db.Tx) error {
			return q.QueryRow(c, "SELECT id::text FROM outbox WHERE key=$1", acct.ID).Scan(&id)
		})
		waitFor(t, 15*time.Second, "republish", func() bool { return state(t, tx, tid, id).dispatched })

		if evs := byKey(read(t, "ratify.account.created"), acct.ID); len(evs) != 2 || evs[0].ID != id || evs[1].ID != id {
			t.Fatalf("a duplicate on the topic is EXPECTED (2 copies, same event_id), got %+v", evs)
		}
		waitFor(t, 20*time.Second, "consumer to see the duplicate", func() bool { return cons.Duplicates.Load() >= 1 })
		time.Sleep(time.Second)
		mu.Lock()
		defer mu.Unlock()
		if handled != 1 {
			t.Fatalf("consumer handled the event %d times, want exactly 1", handled)
		}
		if n := count(t, tx, tid, "SELECT count(*) FROM processed_events WHERE event_id=$1::uuid", id); n != 1 {
			t.Fatalf("processed_events rows = %d, want 1", n)
		}
	})

	t.Run("5 poison row is dead-lettered; later row with another key still published", func(t *testing.T) {
		tid := seed(t)
		topic := uniq("t5.topic")
		bad := appendRow(t, tx, tid, topic, "k-bad", map[string]string{"blob": strings.Repeat("x", 2<<20)}) // > producer batch limit
		good := appendRow(t, tx, tid, topic, "k-good", map[string]string{"ok": "yes"})

		stop := runRelay(newRelay(t, uniq("t5"), outbox.Hooks{}))
		defer stop()
		waitFor(t, 30*time.Second, "dead letter + good dispatch", func() bool {
			return state(t, tx, tid, bad).dead && state(t, tx, tid, good).dispatched
		})
		b := state(t, tx, tid, bad)
		if b.attempts != 10 || b.dispatched || b.lastErr == "" {
			t.Fatalf("poison row state: %+v", b)
		}
		if evs := read(t, topic); len(evs) != 1 || evs[0].ID != good {
			t.Fatalf("want only the good message on the topic, got %+v", evs)
		}
	})
}

func TestOutboxGrants(t *testing.T) {
	ctx := context.Background()
	rp, err := db.RawPoolForTest(ctx, env(t, "RELAY_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer rp.Close()
	denied := func(err error) bool {
		var pe *pgconn.PgError
		return errors.As(err, &pe) && pe.Code == "42501"
	}
	t.Run("relay_user cannot read any table other than outbox", func(t *testing.T) {
		for _, tbl := range []string{"accounts", "tenants", "processed_events"} {
			var n int
			if err := rp.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&n); !denied(err) {
				t.Fatalf("relay_user read %s: %v", tbl, err)
			}
		}
		var n int
		if err := rp.QueryRow(ctx, "SELECT count(*) FROM outbox").Scan(&n); err != nil {
			t.Fatalf("relay_user must read outbox: %v", err)
		}
		if _, err := rp.Exec(ctx, "UPDATE outbox SET payload = '{}'"); !denied(err) {
			t.Fatalf("relay_user must not update payload: %v", err)
		}
		if _, err := rp.Exec(ctx, "DELETE FROM outbox"); !denied(err) {
			t.Fatalf("relay_user must not delete: %v", err)
		}
	})
	t.Run("app_user cannot update or delete outbox", func(t *testing.T) {
		ap, err := db.RawPoolForTest(ctx, env(t, "DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		defer ap.Close()
		for _, q := range []string{"UPDATE outbox SET attempts = 0", "DELETE FROM outbox"} {
			if _, err := ap.Exec(ctx, q); !denied(err) {
				t.Fatalf("%s: %v", q, err)
			}
		}
	})
}
