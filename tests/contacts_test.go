//go:build dbtest

package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"ratify/internal/db"
	"ratify/internal/outbox"
	"ratify/internal/tenant"
)

func pgCode(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

func TestContacts(t *testing.T) {
	f := newFixture(t)
	a := f.a
	acctA := f.account(f.repA, "A-co")
	acctB := f.account(f.repB, "B-co")
	acctT2 := f.account(f.admin2, "T2-co")

	mk := func(who actor, body map[string]any) map[string]any { return a.want(201, who, "POST", "/api/v1/contacts", body) }

	t.Run("create with consent fields; owner defaults to caller", func(t *testing.T) {
		m := mk(f.repA, map[string]any{"first_name": "Ada", "last_name": "L", "email": "ada@acme.io", "account_id": acctA,
			"consent_status": "granted", "lawful_basis": "consent", "consent_source": "web"})
		if sv(m, "owner_id") != f.repA.id || sv(m, "consent_status") != "granted" || sv(m, "tenant_id") != f.T1 {
			t.Fatalf("%v", m)
		}
	})
	t.Run("defaults: consent none, do_not_contact false", func(t *testing.T) {
		m := mk(f.repA, map[string]any{"email": "d@x.io"})
		if sv(m, "consent_status") != "none" || m["do_not_contact"] != false {
			t.Fatalf("%v", m)
		}
	})
	t.Run("validation: email, name-or-email, consent enums", func(t *testing.T) {
		a.want(400, f.repA, "POST", "/api/v1/contacts", map[string]any{"email": "nope"})
		a.want(400, f.repA, "POST", "/api/v1/contacts", map[string]any{})
		a.want(422, f.repA, "POST", "/api/v1/contacts", map[string]any{"email": "e@x.io", "consent_status": "maybe"})
		a.want(422, f.repA, "POST", "/api/v1/contacts", map[string]any{"email": "e@x.io", "lawful_basis": "vibes"})
	})
	t.Run("update do_not_contact and consent; emits contact.updated once", func(t *testing.T) {
		id := sv(mk(f.repA, map[string]any{"email": "u@x.io"}), "id")
		m := a.want(200, f.repA, "PATCH", "/api/v1/contacts/"+id, map[string]any{"do_not_contact": true, "consent_status": "revoked"})
		if m["do_not_contact"] != true || sv(m, "consent_status") != "revoked" {
			t.Fatalf("%v", m)
		}
		if n := f.outboxCount(f.T1, "ratify.contact.updated", id); n != 1 {
			t.Fatalf("contact.updated rows = %d", n)
		}
	})
	t.Run("event contact.created emitted exactly once", func(t *testing.T) {
		id := sv(mk(f.repA, map[string]any{"email": "ev@x.io"}), "id")
		if n := f.outboxCount(f.T1, "ratify.contact.created", id); n != 1 {
			t.Fatalf("contact.created rows = %d", n)
		}
	})
	t.Run("rep cannot read another rep's contact", func(t *testing.T) {
		id := sv(mk(f.repB, map[string]any{"email": "b@x.io"}), "id")
		a.want(404, f.repA, "GET", "/api/v1/contacts/"+id, nil)
		a.want(404, f.repA, "PATCH", "/api/v1/contacts/"+id, map[string]any{"title": "x"})
		a.want(404, f.repA, "DELETE", "/api/v1/contacts/"+id, nil)
	})
	t.Run("manager reads team member's contact; not an outsider's", func(t *testing.T) {
		mine := sv(mk(f.repA, map[string]any{"email": "tm@x.io"}), "id")
		out := sv(mk(f.repB, map[string]any{"email": "out@x.io"}), "id")
		a.want(200, f.mgr, "GET", "/api/v1/contacts/"+mine, nil)
		a.want(404, f.mgr, "GET", "/api/v1/contacts/"+out, nil)
	})
	t.Run("cannot attach a contact to an account the caller cannot see", func(t *testing.T) {
		a.want(404, f.repA, "POST", "/api/v1/contacts", map[string]any{"email": "z@x.io", "account_id": acctB})
		a.want(404, f.repA, "POST", "/api/v1/contacts", map[string]any{"email": "z@x.io", "account_id": acctT2})
		id := sv(mk(f.repA, map[string]any{"email": "mv@x.io"}), "id")
		a.want(404, f.repA, "PATCH", "/api/v1/contacts/"+id, map[string]any{"account_id": acctB})
	})
	t.Run("composite FK rejects a contact pointing at another tenant's account (DB level)", func(t *testing.T) {
		err := a.tx.WithTenant(tenant.With(context.Background(), f.T1), func(c context.Context, q db.Tx) error {
			_, err := q.Exec(c, `INSERT INTO contacts (tenant_id, email, account_id) VALUES (current_tenant(), 'x@y.io', $1::uuid)`, acctT2)
			return err
		})
		if pgCode(err) != "23503" {
			t.Fatalf("want FK violation 23503, got %v", err)
		}
	})
	t.Run("RLS WITH CHECK rejects a cross-tenant insert (DB level)", func(t *testing.T) {
		err := a.tx.WithTenant(tenant.With(context.Background(), f.T1), func(c context.Context, q db.Tx) error {
			_, err := q.Exec(c, `INSERT INTO contacts (tenant_id, email) VALUES ($1::uuid, 'x@y.io')`, f.T2)
			return err
		})
		if pgCode(err) != "42501" {
			t.Fatalf("want 42501, got %v", err)
		}
	})
	t.Run("cross-tenant: invisible and filtered", func(t *testing.T) {
		id := sv(mk(f.admin, map[string]any{"email": "t1@x.io"}), "id")
		a.want(404, f.admin2, "GET", "/api/v1/contacts/"+id, nil)
		if ids(a.want(200, f.admin2, "GET", "/api/v1/contacts", nil))[id] {
			t.Fatal("tenant B lists tenant A contact")
		}
	})
	t.Run("pagination cursor from tenant A is rejected under tenant B", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			mk(f.admin, map[string]any{"email": "pg" + string(rune('a'+i)) + "@x.io"})
		}
		cur := sv(a.want(200, f.admin, "GET", "/api/v1/contacts?limit=1", nil), "next_cursor")
		a.want(400, f.admin2, "GET", "/api/v1/contacts?limit=1&cursor="+cur, nil)
	})
	t.Run("filter by account_id and owner_id; delete", func(t *testing.T) {
		id := sv(mk(f.repA, map[string]any{"email": "fl@x.io", "account_id": acctA}), "id")
		if !ids(a.want(200, f.repA, "GET", "/api/v1/contacts?account_id="+acctA, nil))[id] {
			t.Fatal("account filter missed")
		}
		if ids(a.want(200, f.admin, "GET", "/api/v1/contacts?owner_id="+f.repB.id+"&limit=200", nil))[id] {
			t.Fatal("owner filter leaked")
		}
		a.want(204, f.repA, "DELETE", "/api/v1/contacts/"+id, nil)
		a.want(404, f.repA, "GET", "/api/v1/contacts/"+id, nil)
	})
}

// Events reach Kafka with the right tenant_id and event_id headers.
func TestCRMEventsReachKafka(t *testing.T) {
	f := newFixture(t)
	stop := runRelay(newRelay(t, uniq("crm-ev"), outbox.Hooks{}))
	defer stop()
	acct := f.account(f.repA, "kafka-co")
	id := sv(f.a.want(201, f.repA, "POST", "/api/v1/contacts", map[string]any{"email": "k@x.io", "account_id": acct}), "id")

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var pending int
		_ = f.a.tx.WithTenant(tenant.With(context.Background(), f.T1), func(c context.Context, q db.Tx) error {
			return q.QueryRow(c, "SELECT count(*) FROM outbox WHERE dispatched_at IS NULL").Scan(&pending)
		})
		if pending == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	got := byKey(read(t, "ratify.contact.created"), id)
	if len(got) != 1 || got[0].TenantID != f.T1 || got[0].ID == "" || got[0].Type != "ratify.contact.created" {
		t.Fatalf("kafka event: %+v", got)
	}
}
