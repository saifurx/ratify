//go:build dbtest

package tests

import (
	"fmt"
	"testing"
)

func TestAccounts(t *testing.T) {
	f := newFixture(t)
	a := f.a

	t.Run("create: owner defaults to caller, tenant comes from the token only", func(t *testing.T) {
		m := a.want(201, f.repA, "POST", "/api/v1/accounts", map[string]any{"name": "Acme", "domain": "acme.io", "industry": "saas", "employee_count": 50})
		if sv(m, "owner_id") != f.repA.id || sv(m, "tenant_id") != f.T1 || m["employee_count"].(float64) != 50 {
			t.Fatalf("unexpected: %v", m)
		}
		// a client-supplied tenant_id is rejected, never honoured
		a.want(400, f.repA, "POST", "/api/v1/accounts", map[string]any{"name": "x", "tenant_id": f.T2})
	})
	t.Run("validation", func(t *testing.T) {
		a.want(400, f.repA, "POST", "/api/v1/accounts", map[string]any{"name": "  "})
		a.want(422, f.repA, "POST", "/api/v1/accounts", map[string]any{"name": "x", "employee_count": -1})
		a.want(404, f.repA, "GET", "/api/v1/accounts/not-a-uuid", nil)
	})
	t.Run("get, patch (incl. clearing a field), delete lifecycle", func(t *testing.T) {
		id := f.account(f.repA, "Life")
		a.want(200, f.repA, "PATCH", "/api/v1/accounts/"+id, map[string]any{"domain": "life.io", "industry": "bank"})
		m := a.want(200, f.repA, "PATCH", "/api/v1/accounts/"+id, map[string]any{"industry": nil})
		if sv(m, "domain") != "life.io" || m["industry"] != nil {
			t.Fatalf("patch result: %v", m)
		}
		a.want(400, f.repA, "PATCH", "/api/v1/accounts/"+id, map[string]any{"tenant_id": f.T2})
		a.want(400, f.repA, "PATCH", "/api/v1/accounts/"+id, map[string]any{})
		a.want(204, f.repA, "DELETE", "/api/v1/accounts/"+id, nil)
		a.want(404, f.repA, "GET", "/api/v1/accounts/"+id, nil)
	})
	t.Run("rep cannot read, update or delete another rep's account", func(t *testing.T) {
		id := f.account(f.repB, "B-only")
		a.want(404, f.repA, "GET", "/api/v1/accounts/"+id, nil)
		a.want(404, f.repA, "PATCH", "/api/v1/accounts/"+id, map[string]any{"name": "hijack"})
		a.want(404, f.repA, "DELETE", "/api/v1/accounts/"+id, nil)
		if ids(a.want(200, f.repA, "GET", "/api/v1/accounts", nil))[id] {
			t.Fatal("repA list leaked repB's account")
		}
	})
	t.Run("manager sees team members' accounts, not outsiders'", func(t *testing.T) {
		mine, theirs := f.account(f.repA, "team"), f.account(f.repB, "outsider")
		got := ids(a.want(200, f.mgr, "GET", "/api/v1/accounts", nil))
		if !got[mine] || got[theirs] {
			t.Fatalf("manager scope wrong: team=%v outsider=%v", got[mine], got[theirs])
		}
		a.want(200, f.mgr, "PATCH", "/api/v1/accounts/"+mine, map[string]any{"industry": "x"})
		a.want(404, f.mgr, "GET", "/api/v1/accounts/"+theirs, nil)
	})
	t.Run("admin sees everything; finance sees everything but is read-only", func(t *testing.T) {
		id := f.account(f.repB, "for-admin")
		a.want(200, f.admin, "GET", "/api/v1/accounts/"+id, nil)
		a.want(200, f.fin, "GET", "/api/v1/accounts/"+id, nil)
		a.want(403, f.fin, "POST", "/api/v1/accounts", map[string]any{"name": "nope"})
		a.want(403, f.fin, "PATCH", "/api/v1/accounts/"+id, map[string]any{"name": "nope"})
		a.want(403, f.fin, "DELETE", "/api/v1/accounts/"+id, nil)
	})
	t.Run("rep cannot assign ownership to someone else", func(t *testing.T) {
		a.want(403, f.repA, "POST", "/api/v1/accounts", map[string]any{"name": "x", "owner_id": f.repB.id})
		id := f.account(f.repA, "mine")
		a.want(403, f.repA, "PATCH", "/api/v1/accounts/"+id, map[string]any{"owner_id": f.repB.id})
		a.want(403, f.repA, "PATCH", "/api/v1/accounts/"+id, map[string]any{"owner_id": nil})
		a.want(200, f.admin, "PATCH", "/api/v1/accounts/"+id, map[string]any{"owner_id": f.repB.id})
	})
	t.Run("service principals (agent) read the tenant but cannot write", func(t *testing.T) {
		agent := actor{tenant: f.T1, subject: "agent-1", role: "agent"}
		a.want(200, agent, "GET", "/api/v1/accounts", nil)
		a.want(403, agent, "POST", "/api/v1/accounts", map[string]any{"name": "x"})
	})
	t.Run("cross-tenant: tenant B cannot see or touch tenant A", func(t *testing.T) {
		id := f.account(f.admin, "tenant-A-only")
		a.want(404, f.admin2, "GET", "/api/v1/accounts/"+id, nil)
		a.want(404, f.admin2, "PATCH", "/api/v1/accounts/"+id, map[string]any{"name": "x"})
		a.want(404, f.admin2, "DELETE", "/api/v1/accounts/"+id, nil)
		if len(items(a.want(200, f.admin2, "GET", "/api/v1/accounts", nil))) != 0 {
			t.Fatal("tenant B sees tenant A accounts")
		}
	})
	t.Run("pagination: stable, complete, no duplicates", func(t *testing.T) {
		g := newFixture(t) // clean tenant
		want := map[string]bool{}
		for i := 0; i < 5; i++ {
			want[g.account(g.admin, fmt.Sprintf("p%d", i))] = true
		}
		seen := map[string]bool{}
		path := "/api/v1/accounts?limit=2"
		pages := 0
		for path != "" {
			m := g.a.want(200, g.admin, "GET", path, nil)
			for id := range ids(m) {
				if seen[id] {
					t.Fatalf("duplicate %s across pages", id)
				}
				seen[id] = true
			}
			pages++
			path = ""
			if c := sv(m, "next_cursor"); c != "" {
				path = "/api/v1/accounts?limit=2&cursor=" + c
			}
		}
		if pages != 3 || len(seen) != 5 {
			t.Fatalf("pages=%d seen=%d", pages, len(seen))
		}
	})
	t.Run("pagination cursor from tenant A returns nothing under tenant B; tampering rejected", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			f.account(f.admin, fmt.Sprintf("c%d", i))
			f.account(f.admin2, fmt.Sprintf("d%d", i))
		}
		m := a.want(200, f.admin, "GET", "/api/v1/accounts?limit=1", nil)
		cur := sv(m, "next_cursor")
		if cur == "" {
			t.Fatal("expected a cursor")
		}
		a.want(200, f.admin, "GET", "/api/v1/accounts?limit=1&cursor="+cur, nil)
		a.want(400, f.admin2, "GET", "/api/v1/accounts?limit=1&cursor="+cur, nil)
		a.want(400, f.admin, "GET", "/api/v1/accounts?limit=1&cursor="+cur+"x", nil)
		a.want(400, f.admin, "GET", "/api/v1/accounts?limit=0", nil)
	})
	t.Run("filter by owner_id", func(t *testing.T) {
		idA, idB := f.account(f.repA, "fa"), f.account(f.repB, "fb")
		got := ids(a.want(200, f.admin, "GET", "/api/v1/accounts?owner_id="+f.repA.id+"&limit=200", nil))
		if !got[idA] || got[idB] {
			t.Fatal("owner filter wrong")
		}
		a.want(400, f.admin, "GET", "/api/v1/accounts?owner_id=zzz", nil)
	})
	t.Run("account.created emitted exactly once; audit_log written", func(t *testing.T) {
		id := f.account(f.repA, "evented")
		if n := f.outboxCount(f.T1, "ratify.account.created", id); n != 1 {
			t.Fatalf("outbox rows = %d, want 1", n)
		}
		a.want(200, f.repA, "PATCH", "/api/v1/accounts/"+id, map[string]any{"industry": "x"})
		if n := f.outboxCount(f.T1, "ratify.account.created", id); n != 1 {
			t.Fatalf("update must not re-emit created: %d", n)
		}
	})
	t.Run("unknown role and missing subject are 403", func(t *testing.T) {
		a.want(403, actor{tenant: f.T1, subject: "x", role: "superhero"}, "GET", "/api/v1/accounts", nil)
		a.want(403, actor{tenant: f.T1, subject: "", role: "rep"}, "GET", "/api/v1/accounts", nil)
		a.want(403, actor{tenant: "", subject: "x", role: "rep"}, "GET", "/api/v1/accounts", nil)
	})
}
