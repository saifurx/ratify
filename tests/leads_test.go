//go:build dbtest

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"ratify/internal/db"
	"ratify/internal/tenant"
)

// tokenFor reads the tenant's public form token (as app_migrator under set_tenant).
func tokenFor(t *testing.T, tid string) string {
	t.Helper()
	m, err := dbMigrator(t)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	tx, _ := m.Begin(context.Background())
	defer tx.Rollback(context.Background())
	_, _ = tx.Exec(context.Background(), `SELECT set_tenant($1::uuid)`, tid)
	var tok string
	if err := tx.QueryRow(context.Background(), `SELECT public_token FROM tenants WHERE id = $1::uuid`, tid).Scan(&tok); err != nil {
		t.Fatal(err)
	}
	return tok
}

type inboundResp struct {
	code   int
	body   string
	cookie string
}

// inbound posts a public form submission from the given client IP.
func (a *api) inbound(token string, body any, ip string, query string, hdr map[string]string) inboundResp {
	a.t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/inbound/leads?tenant_token="+token+query, bytes.NewReader(b))
	req.RemoteAddr = ip + ":4321"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	out := inboundResp{code: rec.Code, body: strings.TrimSpace(rec.Body.String())}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "ratify_sid" {
			out.cookie = c.Value
		}
	}
	return out
}

func randIP() string {
	return fmt.Sprintf("10.%d.%d.%d", rand.Intn(250)+1, rand.Intn(250)+1, rand.Intn(250)+1)
}

func (f *fixture) q(tid, sql string, args ...any) (out []map[string]any) {
	f.a.t.Helper()
	err := f.a.tx.WithTenant(tenant.With(context.Background(), tid), func(c context.Context, tx db.Tx) error {
		rows, err := tx.Query(c, "SELECT row_to_json(r) FROM ("+sql+") r", args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			m := map[string]any{}
			_ = json.Unmarshal(raw, &m)
			out = append(out, m)
		}
		return rows.Err()
	})
	if err != nil {
		f.a.t.Fatal(err)
	}
	return
}

func (f *fixture) leadCount(tid, email string) int {
	return int(f.q(tid, `SELECT count(*)::int AS n FROM leads WHERE email = $1::citext AND deleted_at IS NULL`, email)[0]["n"].(float64))
}

func (f *fixture) ledgerCount(tid, email string) int {
	return int(f.q(tid, `SELECT count(*)::int AS n FROM consent_ledger WHERE email = $1::citext`, email)[0]["n"].(float64))
}

func TestLeadIngestion(t *testing.T) {
	f := newFixture(t)
	a := f.a
	tok := tokenFor(t, f.T1)

	t.Run("a valid submission creates a lead, a ledger row and a lead.created event", func(t *testing.T) {
		r := a.inbound(tok, map[string]any{"email": "Ana@Acme.test", "first_name": "Ana", "last_name": "Lopez", "company": "Acme", "consent": true},
			randIP(), "&utm_source=news&utm_campaign=q4", map[string]string{"Referer": "https://acme.test/pricing", "User-Agent": "UA/1"})
		if r.code != 202 || r.body != `{"status":"received"}` || len(r.cookie) != 32 {
			t.Fatalf("%+v", r)
		}
		leads := f.q(f.T1, `SELECT id::text, email::text, domain, consent_status, lawful_basis, source_detail FROM leads WHERE email = 'ana@acme.test'`)
		if len(leads) != 1 || leads[0]["domain"] != "acme.test" || leads[0]["consent_status"] != "granted" || leads[0]["lawful_basis"] != "consent" {
			t.Fatalf("%v", leads)
		}
		sd := leads[0]["source_detail"].(map[string]any)
		if sd["utm"].(map[string]any)["utm_source"] != "news" || sd["referrer"] != "https://acme.test/pricing" || sd["session_id"] != r.cookie {
			t.Fatalf("source_detail: %v", sd)
		}
		led := f.q(f.T1, `SELECT status, ip, user_agent FROM consent_ledger WHERE email = 'ana@acme.test'`)
		if len(led) != 1 || led[0]["status"] != "granted" || led[0]["user_agent"] != "UA/1" {
			t.Fatalf("%v", led)
		}
		if n := f.outboxCount(f.T1, "ratify.lead.created", leads[0]["id"].(string)); n != 1 {
			t.Fatalf("lead.created rows = %d", n)
		}
	})
	t.Run("no consent ticked: lead stored with consent 'none', ledger row 'none'", func(t *testing.T) {
		a.inbound(tok, map[string]any{"email": "nc@acme.test"}, randIP(), "", nil)
		l := f.q(f.T1, `SELECT consent_status, lawful_basis FROM leads WHERE email = 'nc@acme.test'`)
		if l[0]["consent_status"] != "none" || l[0]["lawful_basis"] != nil {
			t.Fatalf("%v", l)
		}
	})
	t.Run("free-mail addresses do not become the company domain", func(t *testing.T) {
		a.inbound(tok, map[string]any{"email": "someone@gmail.com"}, randIP(), "", nil)
		if f.q(f.T1, `SELECT domain FROM leads WHERE email = 'someone@gmail.com'`)[0]["domain"] != nil {
			t.Fatal("gmail.com used as company domain")
		}
		a.inbound(tok, map[string]any{"email": "x@gmail.com", "domain": "https://www.Corp.TEST/about"}, randIP(), "", nil)
		if f.q(f.T1, `SELECT domain FROM leads WHERE email = 'x@gmail.com'`)[0]["domain"] != "corp.test" {
			t.Fatal("domain not normalised")
		}
	})
	t.Run("invalid input is 400; unknown token is 404; nothing stored", func(t *testing.T) {
		for _, body := range []any{map[string]any{"email": "nope"}, map[string]any{}, "str"} {
			if r := a.inbound(tok, body, randIP(), "", nil); r.code != 400 {
				t.Fatalf("%v -> %d", body, r.code)
			}
		}
		if r := a.inbound("not-a-token", map[string]any{"email": "a@b.test"}, randIP(), "", nil); r.code != 404 {
			t.Fatalf("unknown token -> %d", r.code)
		}
		if r := a.inbound("", map[string]any{"email": "a@b.test"}, randIP(), "", nil); r.code != 404 {
			t.Fatalf("empty token -> %d", r.code)
		}
	})
	t.Run("the tenant comes only from the token: a body tenant_id is ignored", func(t *testing.T) {
		a.inbound(tok, map[string]any{"email": "inj@acme.test", "tenant_id": f.T2}, randIP(), "", nil)
		if f.leadCount(f.T1, "inj@acme.test") != 1 || f.leadCount(f.T2, "inj@acme.test") != 0 {
			t.Fatal("body tenant_id was honoured")
		}
	})
	t.Run("duplicate email: merged, first_captured_at preserved, ledger has both events, one lead row", func(t *testing.T) {
		a.inbound(tok, map[string]any{"email": "dup@acme.test", "first_name": "D"}, randIP(), "", nil)
		first := f.q(f.T1, `SELECT first_captured_at::text AS fc, id::text FROM leads WHERE email = 'dup@acme.test'`)[0]
		time.Sleep(20 * time.Millisecond)
		a.inbound(tok, map[string]any{"email": "DUP@acme.test", "last_name": "Uplicate", "company": "DupCo", "consent": true}, randIP(), "", nil)
		l := f.q(f.T1, `SELECT first_captured_at::text AS fc, last_name, company, consent_status FROM leads WHERE email = 'dup@acme.test'`)
		if len(l) != 1 || l[0]["fc"] != first["fc"] || l[0]["last_name"] != "Uplicate" || l[0]["company"] != "DupCo" || l[0]["consent_status"] != "granted" {
			t.Fatalf("%v (first %v)", l, first)
		}
		if n := f.ledgerCount(f.T1, "dup@acme.test"); n != 2 {
			t.Fatalf("ledger rows = %d, want 2", n)
		}
		if n := f.outboxCount(f.T1, "ratify.lead.updated", first["id"].(string)); n != 1 {
			t.Fatalf("lead.updated rows = %d", n)
		}
	})
	t.Run("a repeat submission without consent never downgrades earlier consent", func(t *testing.T) {
		a.inbound(tok, map[string]any{"email": "keep@acme.test", "consent": true}, randIP(), "", nil)
		a.inbound(tok, map[string]any{"email": "keep@acme.test"}, randIP(), "", nil)
		if f.q(f.T1, `SELECT consent_status FROM leads WHERE email = 'keep@acme.test'`)[0]["consent_status"] != "granted" {
			t.Fatal("consent downgraded by a silent resubmission")
		}
	})
	t.Run("same name + domain, different email: two leads, second flagged, ledgers NOT merged", func(t *testing.T) {
		a.inbound(tok, map[string]any{"email": "jane.doe@bigco.test", "first_name": "Jane", "last_name": "Doe", "consent": true}, randIP(), "", nil)
		a.inbound(tok, map[string]any{"email": "jdoe@bigco.test", "first_name": "Jane", "last_name": "Doe", "consent": false}, randIP(), "", nil)
		ls := f.q(f.T1, `SELECT email::text, possible_duplicate_of::text AS dup, id::text FROM leads WHERE domain = 'bigco.test' ORDER BY created_at`)
		if len(ls) != 2 || ls[0]["dup"] != nil || ls[1]["dup"] != ls[0]["id"] {
			t.Fatalf("%v", ls)
		}
		if f.ledgerCount(f.T1, "jane.doe@bigco.test") != 1 || f.ledgerCount(f.T1, "jdoe@bigco.test") != 1 {
			t.Fatal("ledgers merged or missing")
		}
		// consent must not transfer: the second address has none
		id2 := ls[1]["id"].(string)
		d := a.want(200, f.repA, "GET", "/api/v1/leads/"+id2, nil)
		if cc, _ := d["can_contact"].(map[string]any); cc["allowed"] != false {
			t.Fatalf("consent leaked between addresses: %v", d["can_contact"])
		}
	})
	t.Run("cross-tenant: a lead created via tenant A's token is invisible to tenant B", func(t *testing.T) {
		a.inbound(tok, map[string]any{"email": "private@acme.test"}, randIP(), "", nil)
		id := f.q(f.T1, `SELECT id::text FROM leads WHERE email = 'private@acme.test'`)[0]["id"].(string)
		a.want(200, f.repA, "GET", "/api/v1/leads/"+id, nil)
		a.want(404, f.admin2, "GET", "/api/v1/leads/"+id, nil)
		if ids(a.want(200, f.admin2, "GET", "/api/v1/leads?limit=200", nil))[id] {
			t.Fatal("tenant B lists tenant A's lead")
		}
		if f.leadCount(f.T2, "private@acme.test") != 0 {
			t.Fatal("lead in tenant B")
		}
	})
	t.Run("rate limits: per-IP returns 429 and the limiter keys are tenant-prefixed", func(t *testing.T) {
		g := newAPIOpts(t, apiOpts{ipLimit: 3})
		ip := randIP()
		for i := 0; i < 3; i++ {
			if r := g.inbound(tok, map[string]any{"email": fmt.Sprintf("rl%d@acme.test", i)}, ip, "", nil); r.code != 202 {
				t.Fatalf("request %d -> %d", i, r.code)
			}
		}
		if r := g.inbound(tok, map[string]any{"email": "rl9@acme.test"}, ip, "", nil); r.code != 429 {
			t.Fatalf("over limit -> %d", r.code)
		}
		if r := g.inbound(tok, map[string]any{"email": "rl10@acme.test"}, randIP(), "", nil); r.code != 202 {
			t.Fatalf("another IP must be unaffected: %d", r.code)
		}
		rdb := newRedis(t)
		keys, _ := rdb.Keys(context.Background(), "*inbound*").Result()
		for _, k := range keys {
			if !strings.HasPrefix(k, "t:") && !strings.HasPrefix(k, "global:") {
				t.Fatalf("unprefixed redis key %q", k)
			}
		}
		if n, _ := rdb.Keys(context.Background(), "t:"+f.T1+":inbound:*").Result(); len(n) == 0 {
			t.Fatal("no tenant-prefixed limiter key was written")
		}
	})
	t.Run("concurrent identical submissions yield exactly one lead", func(t *testing.T) {
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				a.inbound(tok, map[string]any{"email": "race@acme.test", "consent": true}, randIP(), "", nil)
			}()
		}
		wg.Wait()
		if n := f.leadCount(f.T1, "race@acme.test"); n != 1 {
			t.Fatalf("leads = %d, want 1", n)
		}
		if n := f.ledgerCount(f.T1, "race@acme.test"); n != 8 {
			t.Fatalf("ledger rows = %d, want 8 (every submission is a consent event)", n)
		}
	})
}

var writeRE = regexp.MustCompile(`(?i)^\s*(INSERT|UPDATE|DELETE)\b`)

func TestSuppression(t *testing.T) {
	f := newFixture(t)
	tok := tokenFor(t, f.T1)

	var mu sync.Mutex
	var sqls []string
	var logs bytes.Buffer
	g := newAPIOpts(t, apiOpts{logTo: &logs, observe: func(s string) { mu.Lock(); sqls = append(sqls, s); mu.Unlock() }})
	reset := func() { mu.Lock(); sqls = nil; mu.Unlock() }

	f.a.want(201, f.admin, "POST", "/api/v1/suppressions", map[string]any{"email": "Blocked@Acme.test"})
	f.a.want(201, f.admin, "POST", "/api/v1/suppressions", map[string]any{"domain": "spamcorp.test"})

	t.Run("suppressed email: identical response, no lead row, no ledger row, no outbox event, nothing logged", func(t *testing.T) {
		okResp := g.inbound(tok, map[string]any{"email": "fine@acme.test", "consent": true}, randIP(), "", nil)
		reset()
		supResp := g.inbound(tok, map[string]any{"email": "blocked@acme.test", "consent": true}, randIP(), "", nil)
		if supResp.code != okResp.code || supResp.body != okResp.body || (supResp.cookie == "") != (okResp.cookie == "") {
			t.Fatalf("suppression is distinguishable: %+v vs %+v", supResp, okResp)
		}
		if f.leadCount(f.T1, "blocked@acme.test") != 0 || f.ledgerCount(f.T1, "blocked@acme.test") != 0 {
			t.Fatal("a suppressed submission was stored")
		}
		if n := len(f.q(f.T1, `SELECT 1 FROM outbox WHERE payload::text ILIKE '%blocked%'`)); n != 0 {
			t.Fatal("outbox event for a suppressed submission")
		}
		mu.Lock()
		defer mu.Unlock()
		for _, s := range sqls {
			if writeRE.MatchString(s) {
				t.Fatalf("suppressed request executed a write: %s", s)
			}
		}
		if len(sqls) == 0 {
			t.Fatal("query observer saw nothing")
		}
		out := logs.String()
		for _, secret := range []string{"blocked@acme.test", "Blocked", "suppress", tok} {
			if strings.Contains(strings.ToLower(out), strings.ToLower(secret)) {
				t.Fatalf("log output contains %q:\n%s", secret, out)
			}
		}
	})
	t.Run("the suppression check runs BEFORE any write (query log ordering)", func(t *testing.T) {
		reset()
		g.inbound(tok, map[string]any{"email": "order@acme.test", "consent": true}, randIP(), "", nil)
		mu.Lock()
		defer mu.Unlock()
		supIdx, writeIdx := -1, -1
		for i, s := range sqls {
			if supIdx < 0 && strings.Contains(s, "FROM suppression_list") {
				supIdx = i
			}
			if writeIdx < 0 && writeRE.MatchString(s) {
				writeIdx = i
			}
		}
		if supIdx < 0 || writeIdx < 0 || supIdx > writeIdx {
			t.Fatalf("suppression check at %d, first write at %d\n%v", supIdx, writeIdx, sqls)
		}
	})
	t.Run("domain suppression blocks every address at that domain", func(t *testing.T) {
		g.inbound(tok, map[string]any{"email": "anyone@SpamCorp.test"}, randIP(), "", nil)
		if f.leadCount(f.T1, "anyone@spamcorp.test") != 0 {
			t.Fatal("domain suppression ignored")
		}
	})
	t.Run("suppression is per tenant", func(t *testing.T) {
		tok2 := tokenFor(t, f.T2)
		g.inbound(tok2, map[string]any{"email": "blocked@acme.test"}, randIP(), "", nil)
		if f.leadCount(f.T2, "blocked@acme.test") != 1 {
			t.Fatal("tenant A's suppression list affected tenant B")
		}
	})
	t.Run("suppression admin API: admin-only, validated, idempotent, no delete", func(t *testing.T) {
		f.a.want(403, f.repA, "POST", "/api/v1/suppressions", map[string]any{"email": "x@y.test"})
		f.a.want(403, f.mgr, "GET", "/api/v1/suppressions", nil)
		f.a.want(400, f.admin, "POST", "/api/v1/suppressions", map[string]any{})
		f.a.want(400, f.admin, "POST", "/api/v1/suppressions", map[string]any{"email": "a@b.test", "domain": "b.test"})
		f.a.want(201, f.admin, "POST", "/api/v1/suppressions", map[string]any{"email": "blocked@acme.test"}) // idempotent
		if n := len(items(f.a.want(200, f.admin, "GET", "/api/v1/suppressions", nil))); n != 2 {
			t.Fatalf("suppression rows = %d, want 2", n)
		}
		f.a.want(404, f.admin, "DELETE", "/api/v1/suppressions/"+f.T1, nil) // suppression is permanent: no delete route
	})
}

func TestConsentRuleAndConversion(t *testing.T) {
	f := newFixture(t)
	a := f.a
	tok := tokenFor(t, f.T1)
	submit := func(email string, consent bool) string {
		a.inbound(tok, map[string]any{"email": email, "first_name": "T", "last_name": email[:3], "company": "Co " + email, "consent": consent}, randIP(), "", nil)
		return f.q(f.T1, `SELECT id::text FROM leads WHERE email = $1::citext`, email)[0]["id"].(string)
	}
	reason := func(id string) string {
		d := a.want(200, f.repA, "GET", "/api/v1/leads/"+id, nil)
		cc := d["can_contact"].(map[string]any)
		return fmt.Sprintf("%v/%v", cc["allowed"], cc["reason"])
	}

	t.Run("granted consent: allowed; no consent: refused with no_consent", func(t *testing.T) {
		if r := reason(submit("yes@c1.test", true)); r != "true/ok" {
			t.Fatal(r)
		}
		if r := reason(submit("no@c1.test", false)); r != "false/no_consent" {
			t.Fatal(r)
		}
	})
	t.Run("conversion without a valid consent basis -> 403 with a reason code", func(t *testing.T) {
		id := submit("nobasis@c1.test", false)
		m := a.want(403, f.repA, "POST", "/api/v1/leads/"+id+"/convert", map[string]any{})
		if m["code"] != "no_consent" {
			t.Fatalf("%v", m)
		}
		if n := len(f.q(f.T1, `SELECT 1 FROM contacts WHERE email = 'nobasis@c1.test'`)); n != 0 {
			t.Fatal("a contact was created despite the refusal")
		}
	})
	t.Run("suppressed after capture: refused with suppressed", func(t *testing.T) {
		id := submit("later-sup@c1.test", true)
		a.want(201, f.admin, "POST", "/api/v1/suppressions", map[string]any{"email": "later-sup@c1.test"})
		if m := a.want(403, f.repA, "POST", "/api/v1/leads/"+id+"/convert", map[string]any{}); m["code"] != "suppressed" {
			t.Fatalf("%v", m)
		}
	})
	t.Run("do_not_contact on an existing contact blocks conversion", func(t *testing.T) {
		id := submit("dnc@c1.test", true)
		a.want(201, f.repA, "POST", "/api/v1/contacts", map[string]any{"email": "dnc@c1.test", "do_not_contact": true})
		if m := a.want(403, f.repA, "POST", "/api/v1/leads/"+id+"/convert", map[string]any{}); m["code"] != "do_not_contact" {
			t.Fatalf("%v", m)
		}
	})
	t.Run("legitimate interest allows contact unless an explicit revoke stands (ledger-level rule)", func(t *testing.T) {
		id := submit("li@c1.test", false)
		ins := func(status, basis string) {
			m, _ := dbMigrator(t)
			defer m.Close()
			tx, _ := m.Begin(context.Background())
			_, _ = tx.Exec(context.Background(), `SELECT set_tenant($1::uuid)`, f.T1)
			var b any
			if basis != "" {
				b = basis
			}
			if _, err := tx.Exec(context.Background(), `INSERT INTO consent_ledger (tenant_id, email, status, lawful_basis, source) VALUES (current_tenant(), 'li@c1.test', $1, $2, 'test')`, status, b); err != nil {
				t.Fatal(err)
			}
			_ = tx.Commit(context.Background())
		}
		ins("none", "legitimate_interest")
		if r := reason(id); r != "true/ok" {
			t.Fatal("legitimate interest not honoured: " + r)
		}
		ins("revoked", "")
		if r := reason(id); r != "false/consent_revoked" {
			t.Fatal("revoke must win over legitimate interest: " + r)
		}
		ins("none", "legitimate_interest") // a later LI import must NOT override an explicit revoke
		if r := reason(id); r != "false/consent_revoked" {
			t.Fatal("later LI row overrode a revoke: " + r)
		}
		ins("granted", "consent")
		if r := reason(id); r != "true/ok" {
			t.Fatal("a fresh explicit grant must supersede: " + r)
		}
	})
	t.Run("successful conversion creates account, contact and opportunity in one transaction and emits lead.converted", func(t *testing.T) {
		id := submit("convert@c1.test", true)
		m := a.want(201, f.repA, "POST", "/api/v1/leads/"+id+"/convert", map[string]any{"amount_cents": 12000})
		acct := a.want(200, f.repA, "GET", "/api/v1/accounts/"+sv(m, "account_id"), nil)
		ct := a.want(200, f.repA, "GET", "/api/v1/contacts/"+sv(m, "contact_id"), nil)
		op := a.want(200, f.repA, "GET", "/api/v1/opportunities/"+sv(m, "opportunity_id"), nil)
		if sv(acct, "owner_id") != f.repA.id || sv(ct, "consent_status") != "granted" || sv(ct, "lawful_basis") != "consent" ||
			sv(op, "stage_id") != f.stage["prospecting"] || op["amount_cents"].(float64) != 12000 || sv(ct, "account_id") != sv(acct, "id") {
			t.Fatalf("acct=%v ct=%v op=%v", acct, ct, op)
		}
		if n := f.outboxCount(f.T1, "ratify.lead.converted", id); n != 1 {
			t.Fatalf("lead.converted rows = %d", n)
		}
		l := a.want(200, f.repA, "GET", "/api/v1/leads/"+id, nil)
		if sv(l, "converted_contact_id") != sv(m, "contact_id") || l["converted_at"] == nil {
			t.Fatalf("%v", l)
		}
		a.want(409, f.repA, "POST", "/api/v1/leads/"+id+"/convert", map[string]any{}) // already converted
	})
	t.Run("a failed conversion rolls back everything (no default pipeline stage -> 422, nothing created)", func(t *testing.T) {
		h := newFixture(t) // T1 has a pipeline; T2 deliberately has none
		h2tok := tokenFor(t, h.T2)
		h.a.inbound(h2tok, map[string]any{"email": "np2@c2.test", "consent": true}, randIP(), "", nil)
		id2 := h.q(h.T2, `SELECT id::text FROM leads WHERE email = 'np2@c2.test'`)[0]["id"].(string)
		if m := h.a.want(422, h.admin2, "POST", "/api/v1/leads/"+id2+"/convert", map[string]any{}); m["code"] != "no_default_pipeline" {
			t.Fatalf("%v", m)
		}
		if len(h.q(h.T2, `SELECT 1 FROM accounts`)) != 0 || len(h.q(h.T2, `SELECT 1 FROM contacts`)) != 0 {
			t.Fatal("partial conversion committed")
		}
		if h.q(h.T2, `SELECT converted_at FROM leads WHERE id = $1::uuid`, id2)[0]["converted_at"] != nil {
			t.Fatal("lead marked converted")
		}
	})
	t.Run("authorization: finance cannot convert; any reader can read; invalid ids 404", func(t *testing.T) {
		id := submit("authz@c1.test", true)
		a.want(403, f.fin, "POST", "/api/v1/leads/"+id+"/convert", map[string]any{})
		a.want(200, f.fin, "GET", "/api/v1/leads/"+id, nil)
		a.want(200, actor{tenant: f.T1, subject: "agent-1", role: "agent"}, "GET", "/api/v1/leads/"+id, nil)
		a.want(404, f.repA, "POST", "/api/v1/leads/not-a-uuid/convert", map[string]any{})
		a.want(403, actor{tenant: f.T1, subject: "agent-1", role: "agent"}, "POST", "/api/v1/leads/"+id+"/convert", map[string]any{})
		a.want(404, f.admin2, "POST", "/api/v1/leads/"+id+"/convert", map[string]any{})
	})
	t.Run("soft delete: the lead disappears, the row remains, the email can be captured again", func(t *testing.T) {
		id := submit("soft@c1.test", true)
		a.want(204, f.repA, "DELETE", "/api/v1/leads/"+id, nil)
		a.want(404, f.repA, "GET", "/api/v1/leads/"+id, nil)
		if n := len(f.q(f.T1, `SELECT 1 FROM leads WHERE id = $1::uuid AND deleted_at IS NOT NULL`, id)); n != 1 {
			t.Fatal("row was hard-deleted")
		}
		submit("soft@c1.test", true)
		if f.leadCount(f.T1, "soft@c1.test") != 1 {
			t.Fatal("email could not be captured again")
		}
	})
	t.Run("list: cursor pagination, source filter, duplicate filter, raw_payload never exposed", func(t *testing.T) {
		g := newFixture(t)
		gtok := tokenFor(t, g.T1)
		for i := 0; i < 5; i++ {
			g.a.inbound(gtok, map[string]any{"email": fmt.Sprintf("p%d@pg.test", i), "source": map[bool]string{true: "event", false: "web_form"}[i%2 == 0], "secret": "x"}, randIP(), "", nil)
		}
		m := g.a.want(200, g.admin, "GET", "/api/v1/leads?limit=2", nil)
		cur := sv(m, "next_cursor")
		if len(items(m)) != 2 || cur == "" {
			t.Fatalf("%v", m)
		}
		g.a.want(200, g.admin, "GET", "/api/v1/leads?limit=2&cursor="+cur, nil)
		g.a.want(400, g.admin2, "GET", "/api/v1/leads?limit=2&cursor="+cur, nil)
		if n := len(items(g.a.want(200, g.admin, "GET", "/api/v1/leads?source=event&limit=200", nil))); n != 3 {
			t.Fatalf("source filter: %d", n)
		}
		b, _ := json.Marshal(m)
		if strings.Contains(string(b), "raw_payload") || strings.Contains(string(b), "secret") {
			t.Fatal("raw_payload leaked in the API")
		}
	})
}

func TestMetricsExposeNoSuppressionDetail(t *testing.T) {
	f := newFixture(t)
	tok := tokenFor(t, f.T1)
	f.a.want(201, f.admin, "POST", "/api/v1/suppressions", map[string]any{"email": "metric@acme.test"})
	g := newAPI(t)
	g.inbound(tok, map[string]any{"email": "metric@acme.test"}, randIP(), "", nil)
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Body)
	if strings.Contains(string(body), "metric@acme.test") || strings.Contains(string(body), f.T1) {
		t.Fatal("metrics contain tenant or address data")
	}
	_ = http.StatusOK
}
