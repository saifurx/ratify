// Command seed-demo creates a deterministic demo dataset as app_migrator
// (seeding runs as app_migrator; tests and the API run as app_user):
// 2 tenants, one user per role, 2 teams each, and per tenant 100 accounts,
// 500 contacts, 200 opportunities, activities and inbound leads. It is
// idempotent: ids are fixed, inserts use ON CONFLICT DO NOTHING.
package main

import (
	"context"
	"crypto/sha1"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5"
)

type tenantSpec struct {
	id, slug, name, prefix string
}

var tenants = []tenantSpec{
	{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "demo-a", "Demo Company A", "a"},
	{"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "demo-b", "Demo Company B", "b"},
}

type person struct{ key, role, name string }

var people = []person{
	{"admin", "admin", "Alex Admin"}, {"manager1", "manager", "Morgan East"}, {"manager2", "manager", "Marion West"},
	{"rep1", "rep", "Riley Rep"}, {"rep2", "rep", "Robin Rep"}, {"rep3", "rep", "Rowan Rep"}, {"rep4", "rep", "Remy Rep"},
	{"finance", "finance", "Frankie Finance"}, {"legal", "legal", "Logan Legal"},
}

var (
	words    = []string{"Acme", "Globex", "Initech", "Umbrella", "Hooli", "Stark", "Wayne", "Wonka", "Tyrell", "Cyberdyne", "Soylent", "Vandelay"}
	suffixes = []string{"Systems", "Labs", "Industries", "Logistics", "Dynamics", "Partners", "Holdings", "Works"}
	sectors  = []string{"software", "logistics", "finance", "healthcare", "retail", "energy", "manufacturing"}
	firsts   = []string{"Ana", "Ben", "Chen", "Dara", "Eli", "Fay", "Gus", "Hana", "Ivo", "Jun"}
	lasts    = []string{"Lopez", "Okafor", "Nguyen", "Kowalski", "Haddad", "Singh", "Muller", "Rossi"}
	titles   = []string{"CEO", "CTO", "VP Sales", "Head of Ops", "Procurement Lead", "Engineer"}
)

type stageSpec struct{ name, phase string }

var stages = []stageSpec{
	{"New", "prospecting"}, {"Qualified", "discovery"}, {"Proposal", "proposal"},
	{"Negotiation", "negotiation"}, {"Closed Won", "closed_won"}, {"Closed Lost", "closed_lost"},
}

// det builds a stable UUID from its parts.
func det(parts ...any) string {
	h := sha1.Sum([]byte(fmt.Sprint(append([]any{"ratify-demo"}, parts...)...)))
	b := h[:16]
	b[6] = b[6]&0x0f | 0x50
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func main() {
	url := os.Getenv("MIGRATOR_URL")
	if url == "" {
		log.Fatal("MIGRATOR_URL is required")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close(ctx)
	for _, t := range tenants {
		if err := seedTenant(ctx, conn, t); err != nil {
			log.Fatalf("seed %s: %v", t.slug, err)
		}
	}
}

func seedTenant(ctx context.Context, conn *pgx.Conn, t tenantSpec) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	ex := func(sql string, args ...any) {
		if err == nil {
			_, err = tx.Exec(ctx, sql, args...)
		}
	}
	ex(`SELECT set_tenant($1::uuid)`, t.id)
	ex(`INSERT INTO tenants (id, name, slug) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, t.id, t.name, t.slug)

	// users (ids looked up afterwards: a user created by a login keeps its own id)
	for _, p := range people {
		subject := t.prefix + "-" + p.key
		ex(`INSERT INTO users (id, tenant_id, external_subject, email, display_name, role)
		    VALUES ($1, current_tenant(), $2, $3, $4, $5) ON CONFLICT (tenant_id, external_subject) DO NOTHING`,
			det(t.id, "user", p.key), subject, subject+"@demo-"+t.prefix+".test", p.name+" ("+t.prefix+")", p.role)
	}
	if err != nil {
		return err
	}
	uid := map[string]string{}
	for _, p := range people {
		var id string
		if e := tx.QueryRow(ctx, `SELECT id::text FROM users WHERE external_subject = $1`, t.prefix+"-"+p.key).Scan(&id); e != nil {
			return e
		}
		uid[p.key] = id
	}
	reps := []string{uid["rep1"], uid["rep2"], uid["rep3"], uid["rep4"]}

	// two teams
	for _, tm := range []struct {
		name, mgr string
		members   []string
	}{{"East", "manager1", []string{"rep1", "rep2"}}, {"West", "manager2", []string{"rep3", "rep4"}}} {
		tid := det(t.id, "team", tm.name)
		ex(`INSERT INTO teams (id, tenant_id, name, manager_user_id) VALUES ($1, current_tenant(), $2, $3) ON CONFLICT DO NOTHING`, tid, tm.name, uid[tm.mgr])
		for _, m := range tm.members {
			ex(`INSERT INTO team_members (tenant_id, team_id, user_id) VALUES (current_tenant(), $1, $2) ON CONFLICT DO NOTHING`, tid, uid[m])
		}
	}

	// pipeline + stages
	pid := det(t.id, "pipeline")
	ex(`INSERT INTO pipelines (id, tenant_id, name, is_default) VALUES ($1, current_tenant(), 'Sales', true) ON CONFLICT DO NOTHING`, pid)
	stageIDs := make([]string, len(stages))
	for i, s := range stages {
		stageIDs[i] = det(t.id, "stage", s.name)
		ex(`INSERT INTO stages (id, tenant_id, pipeline_id, name, ordinal, phase) VALUES ($1, current_tenant(), $2, $3, $4, $5) ON CONFLICT (id) DO NOTHING`, // stages' ordinal key is DEFERRABLE
			stageIDs[i], pid, s.name, i+1, s.phase)
	}

	// 100 accounts, 5 contacts each (500), 2 opportunities each for 100 accounts (200)
	accOwner := map[int]string{}
	for i := 1; i <= 100; i++ {
		owner := reps[(i-1)%4]
		accOwner[i] = owner
		name := fmt.Sprintf("%s %s %03d", words[i%len(words)], suffixes[(i/3)%len(suffixes)], i)
		ex(`INSERT INTO accounts (id, tenant_id, name, domain, industry, employee_count, website, owner_id, created_at)
		    VALUES ($1, current_tenant(), $2, $3, $4, $5, $6, $7, now() - make_interval(mins => $8))
		    ON CONFLICT DO NOTHING`,
			det(t.id, "acct", i), name, fmt.Sprintf("acct%d-%s.test", i, t.prefix), sectors[i%len(sectors)],
			10+(i*37)%900, fmt.Sprintf("https://acct%d-%s.test", i, t.prefix), owner, i)
	}
	for j := 1; j <= 500; j++ {
		acc := (j-1)%100 + 1
		status, basis := "none", any(nil)
		switch {
		case j%5 == 0:
			status, basis = "granted", "consent"
		case j%7 == 0:
			basis = "legitimate_interest"
		case j%11 == 0:
			status, basis = "revoked", "consent"
		}
		ex(`INSERT INTO contacts (id, tenant_id, first_name, last_name, email, title, account_id, owner_id, do_not_contact,
		                          consent_status, lawful_basis, consent_source, consent_captured_at, created_at)
		    VALUES ($1, current_tenant(), $2, $3, $4, $5, $6, $7, $8, $9, $10, 'demo seed', now() - interval '30 days', now() - make_interval(mins => $11))
		    ON CONFLICT DO NOTHING`,
			det(t.id, "contact", j), firsts[j%len(firsts)], lasts[(j/2)%len(lasts)], fmt.Sprintf("contact%d@acct%d-%s.test", j, acc, t.prefix),
			titles[j%len(titles)], det(t.id, "acct", acc), accOwner[acc], j%13 == 0, status, basis, j)
	}
	for k := 1; k <= 200; k++ {
		acc := (k-1)%100 + 1
		si := (k * 7) % len(stages)
		st := "open"
		switch stages[si].phase {
		case "closed_won":
			st = "won"
		case "closed_lost":
			st = "lost"
		}
		ex(`INSERT INTO opportunities (id, tenant_id, name, account_id, pipeline_id, stage_id, amount_cents, close_date, owner_id, status, created_at)
		    VALUES ($1, current_tenant(), $2, $3, $4, $5, $6, current_date + $7::int, $8, $9, now() - make_interval(mins => $10))
		    ON CONFLICT DO NOTHING`,
			det(t.id, "opp", k), fmt.Sprintf("Deal %03d – %s", k, []string{"new logo", "expansion", "renewal", "pilot"}[k%4]),
			det(t.id, "acct", acc), pid, stageIDs[si], int64((k*7919)%90+10)*100000, 5+(k*3)%90, accOwner[acc], st, k)
	}
	// activities: a note on 60 opportunities, a call on 30 accounts
	for k := 1; k <= 60; k++ {
		acc := (k-1)%100 + 1
		ex(`INSERT INTO activities (id, tenant_id, type, subject, body, occurred_at, account_id, opportunity_id, created_by, created_at)
		    VALUES ($1, current_tenant(), 'note', $2, 'Seeded demo note.', now() - make_interval(hours => $3), $4, $5, $6, now() - make_interval(hours => $3))
		    ON CONFLICT DO NOTHING`,
			det(t.id, "act-note", k), fmt.Sprintf("Discovery notes for deal %d", k), k, det(t.id, "acct", acc), det(t.id, "opp", k), accOwner[acc])
	}
	for i := 1; i <= 30; i++ {
		ex(`INSERT INTO activities (id, tenant_id, type, subject, body, occurred_at, account_id, created_by, created_at)
		    VALUES ($1, current_tenant(), 'call', $2, 'Seeded call log.', now() - make_interval(hours => $3), $4, $5, now() - make_interval(hours => $3))
		    ON CONFLICT DO NOTHING`,
			det(t.id, "act-call", i), fmt.Sprintf("Intro call with account %d", i), i*2, det(t.id, "acct", i), accOwner[i])
	}
	// inbound leads: a spread of consent states; most carry a (preliminary) synthetic score
	sources := []string{"web_form", "referral", "event", "paid"}
	for n := 1; n <= 12; n++ {
		email := fmt.Sprintf("lead%d@prospect%d-%s.test", n, n, t.prefix)
		status, basis := "none", any(nil)
		switch {
		case n%3 == 1:
			status, basis = "granted", "consent"
		case n%6 == 0:
			status, basis = "revoked", "consent"
		}
		lid := det(t.id, "lead", n)
		ex(`INSERT INTO leads (id, tenant_id, email, first_name, last_name, company, domain, source, consent_status, lawful_basis,
		                       consent_source, consent_captured_at, created_at, first_captured_at)
		    VALUES ($1, current_tenant(), $2, $3, $4, $5, $6, $7, $8, $9, 'web_form', now(), now() - make_interval(mins => $10), now() - make_interval(mins => $10))
		    ON CONFLICT DO NOTHING`,
			lid, email, firsts[n%len(firsts)], lasts[n%len(lasts)], fmt.Sprintf("Prospect %d Inc", n), fmt.Sprintf("prospect%d-%s.test", n, t.prefix),
			sources[n%4], status, basis, n*17)
		ex(`INSERT INTO consent_ledger (id, tenant_id, email, lead_id, status, lawful_basis, source, captured_at)
		    VALUES ($1, current_tenant(), $2, $3, $4, $5, 'web_form', now() - make_interval(mins => $6)) ON CONFLICT DO NOTHING`,
			det(t.id, "ledger", n), email, lid, status, basis, n*17)
		if n <= 9 {
			ex(`INSERT INTO lead_scores (id, tenant_id, lead_id, score, reasons, features, model_version, produced_by, produced_at)
			    VALUES ($1, current_tenant(), $2, $3, $4::jsonb, '{}'::jsonb, 'synthetic-v0-demo', 'ai:seed-demo@0', now()) ON CONFLICT DO NOTHING`,
				det(t.id, "score", n), lid, 20+(n*9)%75,
				fmt.Sprintf(`[{"feature":"source","value":"%s","impact":0.4,"direction":"up"},{"feature":"email_type","value":"corporate","impact":0.2,"direction":"up"}]`, sources[n%4]))
		}
	}
	ex(`INSERT INTO suppression_list (id, tenant_id, email, reason, added_by) VALUES ($1, current_tenant(), $2, 'manual', 'seed-demo') ON CONFLICT DO NOTHING`,
		det(t.id, "suppress"), "blocked@prospect.test")
	if err != nil {
		return err
	}
	var token string
	if e := tx.QueryRow(ctx, `SELECT public_token FROM tenants WHERE id = $1::uuid`, t.id).Scan(&token); e != nil {
		return e
	}
	if e := tx.Commit(ctx); e != nil {
		return e
	}
	fmt.Printf("seeded %s: tenant %s, form token %s\n", t.slug, t.id, token)
	return nil
}
