//go:build dbtest

package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"ratify/internal/outbox"
)

// End to end across Go API -> outbox -> Kafka -> Python worker -> outbox ->
// Kafka: a form POST creates a lead and emits lead.created within 1 s; with a
// cache-hit enrichment, lead.scored follows within 5 s.
func TestLeadCreatedToScoredEndToEnd(t *testing.T) {
	f := newFixture(t)
	tok := tokenFor(t, f.T1)
	stop := runRelay(newRelay(t, uniq("e2e"), outbox.Hooks{}))
	defer stop()
	rdb := newRedis(t)

	prewarm := func(domain string) {
		blob, _ := json.Marshal(map[string]any{"status": "ok", "http_status": 200, "html_sha256": strings.Repeat("a", 64),
			"parsed": map[string]any{"title": "E2E Co", "employees": 500, "domain_age_days": 4000}})
		if err := rdb.Set(context.Background(), "global:enrich:"+domain, blob, time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
	}
	submit := func(email, domain string) (leadID string, at time.Time) {
		at = time.Now()
		r := f.a.inbound(tok, map[string]any{"email": email, "first_name": "E", "last_name": "Two", "domain": domain,
			"company": "E2E Co", "source": "referral", "consent": true}, randIP(), "", nil)
		if r.code != 202 {
			t.Fatalf("%+v", r)
		}
		return f.q(f.T1, `SELECT id::text FROM leads WHERE email = $1::citext`, email)[0]["id"].(string), at
	}
	scoreRows := func(id string) []map[string]any {
		return f.q(f.T1, `SELECT score, model_version, features, reasons, produced_by FROM lead_scores WHERE lead_id = $1::uuid`, id)
	}

	// Warm-up: the first message pays for the consumer-group join. Not timed.
	wd := fmt.Sprintf("warm%d.test", time.Now().UnixNano())
	prewarm(wd)
	wid, _ := submit("warm@"+wd, wd)
	waitFor(t, 40*time.Second, "warm-up score", func() bool { return len(scoreRows(wid)) == 1 })

	// Timed run.
	dom := fmt.Sprintf("e2e%d.test", time.Now().UnixNano())
	prewarm(dom)
	id, t0 := submit("lead@"+dom, dom)

	var createdOutbox string
	_ = f.a.tx // outbox row id for lead.created
	rows := f.q(f.T1, `SELECT id::text FROM outbox WHERE topic = 'ratify.lead.created' AND key = $1`, id)
	if len(rows) != 1 {
		t.Fatalf("lead.created outbox rows = %d", len(rows))
	}
	createdOutbox = rows[0]["id"].(string)
	waitFor(t, time.Second, "lead.created dispatched within 1s", func() bool { return state(t, f.a.tx, f.T1, createdOutbox).dispatched })
	t.Logf("lead.created dispatched %s after the POST", time.Since(t0))

	var scored []map[string]any
	waitFor(t, 5*time.Second, "lead.scored within 5s", func() bool {
		scored = f.q(f.T1, `SELECT id::text FROM outbox WHERE topic = 'ratify.lead.scored' AND key = $1 AND dispatched_at IS NOT NULL`, id)
		return len(scored) == 1
	})
	t.Logf("lead.scored dispatched %s after the POST (cache-hit enrichment)", time.Since(t0))

	// The score row: versioned, synthetic, reproducible inputs recorded.
	sr := scoreRows(id)
	if len(sr) != 1 {
		t.Fatalf("lead_scores rows = %d", len(sr))
	}
	feats := sr[0]["features"].(map[string]any)
	if !strings.HasPrefix(sr[0]["model_version"].(string), "synthetic-") || feats["company_size"].(float64) != 500 ||
		feats["domain_age_days"].(float64) != 4000 || feats["source"] != "referral" || feats["email_domain_type"] != "corporate" ||
		!strings.HasPrefix(sr[0]["produced_by"].(string), "ai:pyworker-lead-scoring@") {
		t.Fatalf("%v", sr[0])
	}
	if er := f.q(f.T1, `SELECT status, parsed FROM enrichment_raw WHERE lead_id = $1::uuid`, id); len(er) != 1 || er[0]["status"] != "cached" {
		t.Fatalf("enrichment_raw: %v", er)
	}

	// The lead shows the score, labelled preliminary because the model is synthetic.
	d := f.a.want(200, f.repA, "GET", "/api/v1/leads/"+id, nil)
	sc, _ := d["score"].(map[string]any)
	if sc == nil || sc["preliminary"] != true || sc["model_version"] != sr[0]["model_version"] {
		t.Fatalf("lead score DTO: %v", d["score"])
	}
	// lead.scored is on Kafka with the right headers.
	evs := byKey(read(t, "ratify.lead.scored"), id)
	if len(evs) != 1 || evs[0].TenantID != f.T1 || evs[0].ID == "" {
		t.Fatalf("lead.scored on Kafka: %+v", evs)
	}
	// The worker is a derived-annotation writer: audited, and it wrote no business column.
	if n := len(f.q(f.T1, `SELECT 1 FROM audit_log WHERE action = 'lead.scored' AND subject_id = $1::uuid`, id)); n != 1 {
		t.Fatalf("audit rows = %d", n)
	}
}

// ml_worker is tenant-scoped and least-privilege: prove the grants.
func TestMLWorkerPrivileges(t *testing.T) {
	ctx := context.Background()
	ml := strings.Replace(env(t, "DATABASE_URL"), "app_user:app_user_dev", "ml_worker:ml_worker_dev", 1)
	pool, err := dbRaw(t, ml)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var n int
	for _, tbl := range []string{"contacts", "users", "outbox", "consent_ledger", "suppression_list", "tenants", "teams", "pipelines", "activities"} {
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&n); pgCode(err) != "42501" {
			t.Fatalf("ml_worker must not read %s: %v", tbl, err)
		}
	}
	// full-row access to accounts/opportunities is denied (column grants only)
	for _, q := range []string{"SELECT count(name) FROM accounts", "SELECT count(name) FROM opportunities", "SELECT count(amount_cents) FROM opportunities"} {
		if err := pool.QueryRow(ctx, q).Scan(&n); pgCode(err) != "42501" {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, stmt := range []string{"UPDATE leads SET company = 'x'", "DELETE FROM leads", "INSERT INTO accounts (tenant_id, name) VALUES (gen_random_uuid(), 'x')"} {
		if _, err := pool.Exec(ctx, stmt); pgCode(err) != "42501" {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	// no tenant bound: it sees nothing, even in tables it may read
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM leads").Scan(&n); err != nil || n != 0 {
		t.Fatalf("unbound ml_worker saw %d leads (err %v)", n, err)
	}
}
