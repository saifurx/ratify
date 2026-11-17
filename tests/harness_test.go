//go:build dbtest

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"

	"ratify/internal/db"
	"ratify/internal/platform"
	"ratify/internal/ratelimit"
	"ratify/internal/server"
	"ratify/internal/tenant"
)

type api struct {
	t  *testing.T
	h  http.Handler
	tx *db.TenantTx
}

type actor struct{ tenant, subject, role, id string }

type apiOpts struct {
	logTo   io.Writer
	observe func(sql string)
	ipLimit int
}

func newAPI(t *testing.T) *api { return newAPIOpts(t, apiOpts{}) }

func newAPIOpts(t *testing.T, o apiOpts) *api {
	t.Helper()
	ctx := context.Background()
	pool, err := db.NewObservedPool(ctx, db.RoleApp, env(t, "DATABASE_URL"), o.observe)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if o.ipLimit == 0 {
		o.ipLimit = 100000
	}
	if o.logTo == nil {
		o.logTo = io.Discard
	}
	cfg := platform.Config{Env: "development", CursorSecret: "test-secret", InboundIPPerMin: o.ipLimit, InboundTenantPerMin: 100000}
	auth, err := platform.NewAuth(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	lim, err := ratelimit.NewFromURL(env(t, "REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := server.New(server.Deps{Cfg: cfg, Pool: pool, Auth: auth, Limiter: lim, Log: slog.New(slog.NewTextHandler(o.logTo, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	if err != nil {
		t.Fatal(err)
	}
	return &api{t: t, h: h, tx: db.NewTenantTx(pool)}
}

// do performs a request as the actor (dev-mode headers) and decodes JSON.
func (a *api) do(who actor, method, path string, body any) (int, map[string]any) {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("X-Tenant-Id", who.tenant)
	req.Header.Set("X-User-Id", who.subject)
	req.Header.Set("X-Role", who.role)
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m
}

func (a *api) want(code int, who actor, method, path string, body any) map[string]any {
	a.t.Helper()
	got, m := a.do(who, method, path, body)
	if got != code {
		a.t.Fatalf("%s %s as %s: want %d got %d (%v)", method, path, who.subject, code, got, m)
	}
	return m
}

func sv(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func items(m map[string]any) []map[string]any {
	var out []map[string]any
	l, _ := m["items"].([]any)
	for _, x := range l {
		out = append(out, x.(map[string]any))
	}
	return out
}

func ids(m map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, it := range items(m) {
		out[sv(it, "id")] = true
	}
	return out
}

// fixture: one tenant with admin, manager (managing repA), repA, repB,
// finance, plus a second tenant with its own admin. A default pipeline with
// one stage per interesting phase exists.
type fixture struct {
	a                                   *api
	T1, T2                              string
	admin, mgr, repA, repB, fin, admin2 actor
	pipeline                            string
	stage                               map[string]string // phase -> stage id
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	a := newAPI(t)
	f := &fixture{a: a, T1: seed(t), T2: seed(t), stage: map[string]string{}}
	mk := func(tid, sub, role string) actor {
		who := actor{tenant: tid, subject: sub + "-" + tid[:8], role: role}
		m := a.want(200, who, "GET", "/api/whoami", nil)
		who.id = sv(m, "user_id")
		return who
	}
	f.admin, f.mgr, f.repA = mk(f.T1, "admin", "admin"), mk(f.T1, "mgr", "manager"), mk(f.T1, "repA", "rep")
	f.repB, f.fin, f.admin2 = mk(f.T1, "repB", "rep"), mk(f.T1, "fin", "finance"), mk(f.T2, "admin2", "admin")

	team := a.want(201, f.admin, "POST", "/api/v1/teams", map[string]any{"name": "east", "manager_user_id": f.mgr.id})
	a.want(200, f.admin, "POST", "/api/v1/teams/"+sv(team, "id")+"/members", map[string]any{"user_id": f.repA.id})

	p := a.want(201, f.admin, "POST", "/api/v1/pipelines", map[string]any{"name": "Sales", "stages": []map[string]any{
		{"name": "New", "phase": "prospecting"}, {"name": "Qualified", "phase": "discovery"},
		{"name": "Quote Sent", "phase": "proposal"}, {"name": "Won", "phase": "closed_won"}, {"name": "Lost", "phase": "closed_lost"}}})
	f.pipeline = sv(p, "id")
	for _, s := range p["stages"].([]any) {
		sm := s.(map[string]any)
		f.stage[sv(sm, "phase")] = sv(sm, "id")
	}
	return f
}

func (f *fixture) account(who actor, name string) string {
	f.a.t.Helper()
	return sv(f.a.want(201, who, "POST", "/api/v1/accounts", map[string]any{"name": name}), "id")
}

func (f *fixture) opp(who actor, acct, name, phase string) map[string]any {
	f.a.t.Helper()
	return f.a.want(201, who, "POST", "/api/v1/opportunities", map[string]any{"name": name, "account_id": acct, "stage_id": f.stage[phase], "amount_cents": 1000})
}

// outboxCount counts outbox rows for (topic, key) as tenant tid.
func (f *fixture) outboxCount(tid, topic, key string) (n int) {
	f.a.t.Helper()
	err := f.a.tx.WithTenant(tenant.With(context.Background(), tid), func(c context.Context, q db.Tx) error {
		return q.QueryRow(c, "SELECT count(*) FROM outbox WHERE topic = $1 AND key = $2", topic, key).Scan(&n)
	})
	if err != nil {
		f.a.t.Fatal(err)
	}
	return
}

// dbMigrator returns a single-connection pool as app_migrator (test seeding).
func dbMigrator(t *testing.T) (interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Begin(context.Context) (pgx.Tx, error)
	Close()
}, error) {
	t.Helper()
	return db.RawPoolForTest(context.Background(), env(t, "MIGRATOR_URL"))
}

func newRedis(t *testing.T) *redis.Client {
	t.Helper()
	l, err := ratelimit.NewFromURL(env(t, "REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	return l.Client()
}

func dbRaw(t *testing.T, url string) (interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Close()
}, error) {
	t.Helper()
	return db.RawPoolForTest(context.Background(), url)
}
