//go:build dbtest

package tests

import (
	"context"
	"fmt"
	"testing"

	"ratify/internal/db"
	"ratify/internal/tenant"
)

func TestActivities(t *testing.T) {
	f := newFixture(t)
	a := f.a
	acctA, acctB := f.account(f.repA, "A"), f.account(f.repB, "B")
	log := func(who actor, body map[string]any) map[string]any { return a.want(201, who, "POST", "/api/v1/activities", body) }

	t.Run("log a note; created_by is the caller and cannot be forged", func(t *testing.T) {
		m := log(f.repA, map[string]any{"type": "note", "subject": "hi", "account_id": acctA})
		if sv(m, "created_by") != f.repA.id || sv(m, "tenant_id") != f.T1 {
			t.Fatalf("%v", m)
		}
		a.want(400, f.repA, "POST", "/api/v1/activities", map[string]any{"type": "note", "account_id": acctA, "created_by": "ai:x@1"})
	})
	t.Run("invalid type 422, missing account 400", func(t *testing.T) {
		a.want(422, f.repA, "POST", "/api/v1/activities", map[string]any{"type": "tweet", "account_id": acctA})
		a.want(400, f.repA, "POST", "/api/v1/activities", map[string]any{"type": "note"})
	})
	t.Run("cannot log on an account, contact or opportunity outside scope", func(t *testing.T) {
		a.want(404, f.repA, "POST", "/api/v1/activities", map[string]any{"type": "note", "account_id": acctB})
		cB := sv(a.want(201, f.repB, "POST", "/api/v1/contacts", map[string]any{"email": "b@x.io"}), "id")
		a.want(404, f.repA, "POST", "/api/v1/activities", map[string]any{"type": "note", "account_id": acctA, "contact_id": cB})
		oB := sv(f.opp(f.repB, acctB, "ob", "discovery"), "id")
		a.want(404, f.repA, "POST", "/api/v1/activities", map[string]any{"type": "note", "account_id": acctA, "opportunity_id": oB})
	})
	t.Run("rep lists only activities of accounts in scope; manager sees team's", func(t *testing.T) {
		mine := sv(log(f.repA, map[string]any{"type": "call", "account_id": acctA}), "id")
		theirs := sv(log(f.repB, map[string]any{"type": "call", "account_id": acctB}), "id")
		g := ids(a.want(200, f.repA, "GET", "/api/v1/activities?limit=200", nil))
		if !g[mine] || g[theirs] {
			t.Fatal("rep scope wrong")
		}
		a.want(404, f.repA, "GET", "/api/v1/activities/"+theirs, nil)
		gm := ids(a.want(200, f.mgr, "GET", "/api/v1/activities?limit=200", nil))
		if !gm[mine] || gm[theirs] {
			t.Fatal("manager scope wrong")
		}
	})
	t.Run("authors edit their own; others (even with visibility) cannot; admin can", func(t *testing.T) {
		id := sv(log(f.admin, map[string]any{"type": "note", "subject": "by admin", "account_id": acctA}), "id")
		a.want(200, f.repA, "GET", "/api/v1/activities/"+id, nil) // visible: repA owns the account
		a.want(403, f.repA, "PATCH", "/api/v1/activities/"+id, map[string]any{"subject": "edit"})
		a.want(403, f.repA, "DELETE", "/api/v1/activities/"+id, nil)
		own := sv(log(f.repA, map[string]any{"type": "note", "subject": "mine", "account_id": acctA}), "id")
		a.want(200, f.repA, "PATCH", "/api/v1/activities/"+own, map[string]any{"subject": "edited"})
		a.want(200, f.admin, "PATCH", "/api/v1/activities/"+own, map[string]any{"subject": "admin edit"})
		a.want(400, f.repA, "PATCH", "/api/v1/activities/"+own, map[string]any{"type": "call"}) // type immutable
		a.want(204, f.repA, "DELETE", "/api/v1/activities/"+own, nil)
	})
	t.Run("activity.logged emitted exactly once", func(t *testing.T) {
		id := sv(log(f.repA, map[string]any{"type": "meeting", "account_id": acctA}), "id")
		if n := f.outboxCount(f.T1, "ratify.activity.logged", id); n != 1 {
			t.Fatalf("rows = %d", n)
		}
	})
	t.Run("filter by type and account", func(t *testing.T) {
		id := sv(log(f.repA, map[string]any{"type": "task", "account_id": acctA}), "id")
		if !ids(a.want(200, f.repA, "GET", "/api/v1/activities?type=task&account_id="+acctA+"&limit=200", nil))[id] {
			t.Fatal("filter missed")
		}
		if ids(a.want(200, f.repA, "GET", "/api/v1/activities?type=note&limit=200", nil))[id] {
			t.Fatal("type filter leaked")
		}
	})
	t.Run("metadata round-trips as JSON and can be updated", func(t *testing.T) {
		m := log(f.repA, map[string]any{"type": "call", "account_id": acctA, "metadata": map[string]any{"duration_s": 120, "direction": "out"}})
		meta, _ := m["metadata"].(map[string]any)
		if meta["direction"] != "out" || meta["duration_s"].(float64) != 120 {
			t.Fatalf("%v", m["metadata"])
		}
		u := a.want(200, f.repA, "PATCH", "/api/v1/activities/"+sv(m, "id"), map[string]any{"metadata": map[string]any{"direction": "in"}})
		if u["metadata"].(map[string]any)["direction"] != "in" {
			t.Fatalf("%v", u["metadata"])
		}
	})
	t.Run("occurred_at is honoured; invalid ids are 404", func(t *testing.T) {
		m := log(f.repA, map[string]any{"type": "meeting", "account_id": acctA, "occurred_at": "2024-01-02T03:04:05Z"})
		if sv(m, "occurred_at") != "2024-01-02T03:04:05Z" {
			t.Fatalf("%v", m["occurred_at"])
		}
		a.want(404, f.repA, "GET", "/api/v1/activities/nope", nil)
		a.want(400, f.repA, "GET", "/api/v1/activities?contact_id=bad", nil)
	})
	t.Run("cross-tenant invisible; cursor rejected", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			log(f.admin, map[string]any{"type": "note", "account_id": acctA})
		}
		id := sv(log(f.admin, map[string]any{"type": "note", "account_id": acctA}), "id")
		a.want(404, f.admin2, "GET", "/api/v1/activities/"+id, nil)
		cur := sv(a.want(200, f.admin, "GET", "/api/v1/activities?limit=1", nil), "next_cursor")
		a.want(400, f.admin2, "GET", "/api/v1/activities?limit=1&cursor="+cur, nil)
	})
	t.Run("audit log: written, append-only even for the app role", func(t *testing.T) {
		var n int
		err := a.tx.WithTenant(tenant.With(context.Background(), f.T1), func(c context.Context, q db.Tx) error {
			return q.QueryRow(c, `SELECT count(*) FROM audit_log WHERE action = 'activity.logged'`).Scan(&n)
		})
		if err != nil || n == 0 {
			t.Fatalf("audit rows=%d err=%v", n, err)
		}
		for _, stmt := range []string{`UPDATE audit_log SET action = 'x'`, `DELETE FROM audit_log`} {
			err := a.tx.WithTenant(tenant.With(context.Background(), f.T1), func(c context.Context, q db.Tx) error {
				_, err := q.Exec(c, stmt)
				return err
			})
			if pgCode(err) != "42501" {
				t.Fatalf("%s: want 42501, got %v", stmt, err)
			}
		}
	})
}

func TestIdentityAndTeams(t *testing.T) {
	f := newFixture(t)
	a := f.a
	t.Run("whoami returns the synced user and role", func(t *testing.T) {
		m := a.want(200, f.repA, "GET", "/api/whoami", nil)
		if sv(m, "user_id") != f.repA.id || sv(m, "role") != "rep" || sv(m, "tenant_id") != f.T1 {
			t.Fatalf("%v", m)
		}
	})
	t.Run("same subject keeps one user; role changes sync from the token", func(t *testing.T) {
		who := f.repA
		who.role = "manager"
		m := a.want(200, who, "GET", "/api/whoami", nil)
		if sv(m, "user_id") != f.repA.id || sv(m, "role") != "manager" {
			t.Fatalf("%v", m)
		}
		who.role = "rep"
		a.want(200, who, "GET", "/api/whoami", nil)
	})
	t.Run("a well-formed but unknown tenant is 403, not a server error", func(t *testing.T) {
		a.want(403, actor{tenant: "22222222-2222-2222-2222-222222222222", subject: "x", role: "admin"}, "GET", "/api/v1/accounts", nil)
	})
	t.Run("same subject in another tenant is a different user", func(t *testing.T) {
		other := actor{tenant: f.T2, subject: f.repA.subject, role: "rep"}
		if sv(a.want(200, other, "GET", "/api/whoami", nil), "user_id") == f.repA.id {
			t.Fatal("user ids collided across tenants")
		}
	})
	t.Run("reps cannot list users or teams; managers and admins can", func(t *testing.T) {
		a.want(403, f.repA, "GET", "/api/v1/users", nil)
		a.want(403, f.repA, "GET", "/api/v1/teams", nil)
		a.want(200, f.mgr, "GET", "/api/v1/users", nil)
		if n := len(items(a.want(200, f.admin, "GET", "/api/v1/users", nil))); n != 5 {
			t.Fatalf("tenant 1 users = %d, want 5", n)
		}
		for _, u := range items(a.want(200, f.admin2, "GET", "/api/v1/users", nil)) {
			if sv(u, "tenant_id") != f.T2 {
				t.Fatal("tenant 2 user list contains another tenant's user")
			}
		}
	})
	t.Run("team membership drives the manager's scope immediately", func(t *testing.T) {
		acct := f.account(f.repB, "later-on-team")
		a.want(404, f.mgr, "GET", "/api/v1/accounts/"+acct, nil)
		teams := items(a.want(200, f.admin, "GET", "/api/v1/teams", nil))
		team := sv(teams[0], "id")
		a.want(200, f.admin, "POST", "/api/v1/teams/"+team+"/members", map[string]any{"user_id": f.repB.id})
		a.want(200, f.mgr, "GET", "/api/v1/accounts/"+acct, nil)
		a.want(204, f.admin, "DELETE", "/api/v1/teams/"+team+"/members/"+f.repB.id, nil)
		a.want(404, f.mgr, "GET", "/api/v1/accounts/"+acct, nil)
		a.want(404, f.admin, "DELETE", "/api/v1/teams/"+team+"/members/"+f.repB.id, nil)
	})
	t.Run("only admins manage teams; cross-tenant user cannot join a team (composite FK)", func(t *testing.T) {
		teams := items(a.want(200, f.admin, "GET", "/api/v1/teams", nil))
		team := sv(teams[0], "id")
		a.want(403, f.mgr, "POST", "/api/v1/teams", map[string]any{"name": "x"})
		a.want(403, f.mgr, "POST", "/api/v1/teams/"+team+"/members", map[string]any{"user_id": f.repB.id})
		a.want(422, f.admin, "POST", "/api/v1/teams/"+team+"/members", map[string]any{"user_id": f.admin2.id})
		a.want(409, f.admin, "POST", "/api/v1/teams", map[string]any{"name": "east"})
	})
	t.Run("inactive users are refused", func(t *testing.T) {
		m, err := dbMigrator(t)
		if err != nil {
			t.Fatal(err)
		}
		defer m.Close()
		tx, _ := m.Begin(context.Background())
		_, _ = tx.Exec(context.Background(), `SELECT set_tenant($1::uuid)`, f.T1)
		_, err = tx.Exec(context.Background(), `UPDATE users SET active = false WHERE id = $1::uuid`, f.fin.id)
		if err != nil {
			t.Fatal(err)
		}
		_ = tx.Commit(context.Background())
		a.want(403, f.fin, "GET", "/api/v1/accounts", nil)
	})
	t.Run("public tenant lookups work without tenant context and reveal only the id", func(t *testing.T) {
		m, err := dbMigrator(t)
		if err != nil {
			t.Fatal(err)
		}
		defer m.Close()
		tx, _ := m.Begin(context.Background())
		_, _ = tx.Exec(context.Background(), `SELECT set_tenant($1::uuid)`, f.T1)
		var tok string
		var visible int
		_ = tx.QueryRow(context.Background(), `SELECT count(*) FROM tenants`).Scan(&visible)
		if visible != 1 {
			t.Fatalf("app_migrator sees %d tenants under set_tenant, want 1 (must not inherit the resolver policy)", visible)
		}
		if err := tx.QueryRow(context.Background(), `SELECT public_token FROM tenants`).Scan(&tok); err != nil {
			t.Fatal(err)
		}
		_, _ = tx.Exec(context.Background(), `UPDATE tenants SET slug = $1`, "acme-"+f.T1[:8])
		_ = tx.Commit(context.Background())

		ap, _ := db.RawPoolForTest(context.Background(), env(t, "DATABASE_URL")) // app_user, NO tenant bound
		defer ap.Close()
		var got *string
		if err := ap.QueryRow(context.Background(), `SELECT resolve_tenant_token($1)::text`, tok).Scan(&got); err != nil || got == nil || *got != f.T1 {
			t.Fatalf("token lookup: %v %v", got, err)
		}
		if err := ap.QueryRow(context.Background(), `SELECT resolve_tenant_slug($1)::text`, "ACME-"+f.T1[:8]).Scan(&got); err != nil || got == nil || *got != f.T1 {
			t.Fatalf("slug lookup (case-insensitive): %v %v", got, err)
		}
		if err := ap.QueryRow(context.Background(), `SELECT resolve_tenant_token('nope')::text`).Scan(&got); err != nil || got != nil {
			t.Fatalf("unknown token must be NULL: %v %v", got, err)
		}
		var n int
		if err := ap.QueryRow(context.Background(), `SELECT count(*) FROM tenants`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("app_user without tenant context sees %d tenants (err %v)", n, err)
		}
	})
}

func TestGraphQLAccount360(t *testing.T) {
	f := newFixture(t)
	a := f.a
	acct := f.account(f.repA, "G-co")
	a.want(201, f.repA, "POST", "/api/v1/contacts", map[string]any{"first_name": "Gina", "email": "g@x.io", "account_id": acct})
	open := sv(f.opp(f.repA, acct, "open-deal", "discovery"), "id")
	f.opp(f.repA, acct, "won-deal", "closed_won")
	for i := 0; i < 25; i++ {
		a.want(201, f.repA, "POST", "/api/v1/activities", map[string]any{"type": "note", "subject": fmt.Sprintf("n%d", i), "account_id": acct})
	}
	q := `query($id: ID!){ account(id:$id){ id name contacts{ firstName email } openOpportunities{ id status } recentActivities{ subject } } }`
	run := func(who actor, id string) map[string]any {
		m := a.want(200, who, "POST", "/api/v1/graphql", map[string]any{"query": q, "variables": map[string]any{"id": id}})
		if m["errors"] != nil {
			t.Fatalf("graphql errors: %v", m["errors"])
		}
		d, _ := m["data"].(map[string]any)
		acc, _ := d["account"].(map[string]any)
		return acc
	}
	t.Run("one query returns account, contacts, only open opportunities, last 20 activities", func(t *testing.T) {
		acc := run(f.repA, acct)
		if sv(acc, "name") != "G-co" || len(acc["contacts"].([]any)) != 1 {
			t.Fatalf("%v", acc)
		}
		opps := acc["openOpportunities"].([]any)
		if len(opps) != 1 || sv(opps[0].(map[string]any), "id") != open {
			t.Fatalf("open opps: %v", opps)
		}
		if n := len(acc["recentActivities"].([]any)); n != 20 {
			t.Fatalf("activities = %d, want 20", n)
		}
	})
	t.Run("same authz scope as REST: other rep and other tenant get null", func(t *testing.T) {
		if run(f.repB, acct) != nil || run(f.admin2, acct) != nil {
			t.Fatal("account-360 leaked outside scope")
		}
		if run(f.mgr, acct) == nil || run(f.admin, acct) == nil || run(f.fin, acct) == nil {
			t.Fatal("in-scope callers must see the account")
		}
	})
	t.Run("malformed queries are GraphQL errors, not crashes", func(t *testing.T) {
		m := a.want(200, f.repA, "POST", "/api/v1/graphql", map[string]any{"query": "{ nope }"})
		if m["errors"] == nil {
			t.Fatal("expected errors")
		}
		a.want(400, f.repA, "POST", "/api/v1/graphql", map[string]any{})
	})
}
