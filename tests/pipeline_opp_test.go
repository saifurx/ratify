//go:build dbtest

package tests

import (
	"context"
	"testing"

	"ratify/internal/db"
	"ratify/internal/tenant"
)

func TestPipelines(t *testing.T) {
	f := newFixture(t)
	a := f.a
	t.Run("first pipeline is the default", func(t *testing.T) {
		m := a.want(200, f.repA, "GET", "/api/v1/pipelines/"+f.pipeline, nil)
		if m["is_default"] != true || len(m["stages"].([]any)) != 5 {
			t.Fatalf("%v", m)
		}
	})
	t.Run("many pipelines allowed, exactly one default", func(t *testing.T) {
		p2 := sv(a.want(201, f.admin, "POST", "/api/v1/pipelines", map[string]any{"name": "Renewals"}), "id")
		list := items(a.want(200, f.repA, "GET", "/api/v1/pipelines", nil))
		defaults := 0
		for _, p := range list {
			if p["is_default"] == true {
				defaults++
			}
		}
		if len(list) < 2 || defaults != 1 {
			t.Fatalf("pipelines=%d defaults=%d", len(list), defaults)
		}
		a.want(200, f.admin, "PATCH", "/api/v1/pipelines/"+p2, map[string]any{"is_default": true})
		if a.want(200, f.admin, "GET", "/api/v1/pipelines/"+f.pipeline, nil)["is_default"] != false {
			t.Fatal("old default not cleared")
		}
		a.want(200, f.admin, "PATCH", "/api/v1/pipelines/"+f.pipeline, map[string]any{"is_default": true}) // restore
	})
	t.Run("the only default cannot be unset or deleted (409)", func(t *testing.T) {
		a.want(409, f.admin, "PATCH", "/api/v1/pipelines/"+f.pipeline, map[string]any{"is_default": false})
		a.want(409, f.admin, "DELETE", "/api/v1/pipelines/"+f.pipeline, nil)
	})
	t.Run("everyone reads, only admin writes", func(t *testing.T) {
		a.want(200, f.repA, "GET", "/api/v1/pipelines", nil)
		a.want(200, f.fin, "GET", "/api/v1/pipelines", nil)
		for _, who := range []actor{f.repA, f.mgr, f.fin} {
			a.want(403, who, "POST", "/api/v1/pipelines", map[string]any{"name": "x"})
			a.want(403, who, "PATCH", "/api/v1/pipelines/"+f.pipeline, map[string]any{"name": "x"})
			a.want(403, who, "POST", "/api/v1/pipelines/"+f.pipeline+"/stages", map[string]any{"name": "x", "phase": "discovery"})
		}
	})
	t.Run("stage ordinals auto-assign; duplicate ordinal 409; bad phase 422", func(t *testing.T) {
		p := sv(a.want(201, f.admin, "POST", "/api/v1/pipelines", map[string]any{"name": "Stages"}), "id")
		s1 := a.want(201, f.admin, "POST", "/api/v1/pipelines/"+p+"/stages", map[string]any{"name": "one", "phase": "prospecting"})
		s2 := a.want(201, f.admin, "POST", "/api/v1/pipelines/"+p+"/stages", map[string]any{"name": "two", "phase": "discovery"})
		if s1["ordinal"].(float64) != 1 || s2["ordinal"].(float64) != 2 {
			t.Fatalf("ordinals %v %v", s1["ordinal"], s2["ordinal"])
		}
		a.want(409, f.admin, "POST", "/api/v1/pipelines/"+p+"/stages", map[string]any{"name": "dup", "phase": "discovery", "ordinal": 1})
		a.want(422, f.admin, "POST", "/api/v1/pipelines/"+p+"/stages", map[string]any{"name": "bad", "phase": "limbo"})
		a.want(404, f.admin, "POST", "/api/v1/pipelines/"+f.T1+"/stages", map[string]any{"name": "x", "phase": "discovery"})
		m := a.want(200, f.admin, "PATCH", "/api/v1/stages/"+sv(s1, "id"), map[string]any{"name": "renamed"})
		if sv(m, "name") != "renamed" {
			t.Fatal("stage rename failed")
		}
		a.want(204, f.admin, "DELETE", "/api/v1/stages/"+sv(s2, "id"), nil)
		a.want(204, f.admin, "DELETE", "/api/v1/pipelines/"+p, nil)
	})
	t.Run("a pipeline in use by opportunities cannot be deleted (409)", func(t *testing.T) {
		p := sv(a.want(201, f.admin, "POST", "/api/v1/pipelines", map[string]any{"name": "Used", "stages": []map[string]any{{"name": "s", "phase": "discovery"}}}), "id")
		stage := sv(a.want(200, f.admin, "GET", "/api/v1/pipelines/"+p, nil)["stages"].([]any)[0].(map[string]any), "id")
		acct := f.account(f.admin, "uses-pipeline")
		a.want(201, f.admin, "POST", "/api/v1/opportunities", map[string]any{"name": "o", "account_id": acct, "stage_id": stage})
		a.want(409, f.admin, "DELETE", "/api/v1/pipelines/"+p, nil)
	})
	t.Run("one default per tenant is enforced by the database (partial unique index)", func(t *testing.T) {
		err := a.tx.WithTenant(tenant.With(context.Background(), f.T1), func(c context.Context, q db.Tx) error {
			_, err := q.Exec(c, `INSERT INTO pipelines (tenant_id, name, is_default) VALUES (current_tenant(), 'second default', true)`)
			return err
		})
		if pgCode(err) != "23505" {
			t.Fatalf("want 23505, got %v", err)
		}
	})
	t.Run("rename and validation", func(t *testing.T) {
		p := sv(a.want(201, f.admin, "POST", "/api/v1/pipelines", map[string]any{"name": "Rename me"}), "id")
		if sv(a.want(200, f.admin, "PATCH", "/api/v1/pipelines/"+p, map[string]any{"name": "Renamed"}), "name") != "Renamed" {
			t.Fatal("rename failed")
		}
		a.want(400, f.admin, "PATCH", "/api/v1/pipelines/"+p, map[string]any{"name": ""})
		a.want(400, f.admin, "PATCH", "/api/v1/pipelines/"+p, map[string]any{"tenant_id": f.T2})
		a.want(400, f.admin, "POST", "/api/v1/pipelines", map[string]any{"name": ""})
	})
	t.Run("unknown ids are 404", func(t *testing.T) {
		a.want(404, f.admin, "GET", "/api/v1/pipelines/not-a-uuid", nil)
		a.want(404, f.admin, "DELETE", "/api/v1/stages/"+f.T1, nil)
	})
	t.Run("stage updates reorder and re-phase", func(t *testing.T) {
		m := a.want(200, f.admin, "PATCH", "/api/v1/stages/"+f.stage["discovery"], map[string]any{"phase": "discovery", "name": "Qualified!"})
		if sv(m, "name") != "Qualified!" {
			t.Fatal("stage update failed")
		}
		a.want(422, f.admin, "PATCH", "/api/v1/stages/"+f.stage["discovery"], map[string]any{"phase": "limbo"})
		a.want(400, f.admin, "PATCH", "/api/v1/stages/"+f.stage["discovery"], map[string]any{"pipeline_id": f.pipeline})
	})
	t.Run("another tenant's pipeline is invisible", func(t *testing.T) {
		a.want(404, f.admin2, "GET", "/api/v1/pipelines/"+f.pipeline, nil)
		a.want(404, f.admin2, "PATCH", "/api/v1/pipelines/"+f.pipeline, map[string]any{"name": "x"})
		a.want(404, f.admin2, "DELETE", "/api/v1/stages/"+f.stage["discovery"], nil)
	})
}

func TestOpportunities(t *testing.T) {
	f := newFixture(t)
	a := f.a
	acctA, acctB := f.account(f.repA, "A"), f.account(f.repB, "B")
	acctT2 := f.account(f.admin2, "T2")

	t.Run("create derives pipeline from stage; status open", func(t *testing.T) {
		m := f.opp(f.repA, acctA, "deal", "prospecting")
		if sv(m, "pipeline_id") != f.pipeline || sv(m, "status") != "open" || sv(m, "owner_id") != f.repA.id || sv(m, "currency") != "USD" {
			t.Fatalf("%v", m)
		}
	})
	t.Run("closed_won stage sets status won; closed_lost sets lost", func(t *testing.T) {
		if sv(f.opp(f.repA, acctA, "w", "closed_won"), "status") != "won" || sv(f.opp(f.repA, acctA, "l", "closed_lost"), "status") != "lost" {
			t.Fatal("status derivation wrong")
		}
	})
	t.Run("stage change emits opportunity.stage_changed exactly once with phases", func(t *testing.T) {
		id := sv(f.opp(f.repA, acctA, "mv", "prospecting"), "id")
		if n := f.outboxCount(f.T1, "ratify.opportunity.created", id); n != 1 {
			t.Fatalf("created rows = %d", n)
		}
		m := a.want(200, f.repA, "PATCH", "/api/v1/opportunities/"+id, map[string]any{"stage_id": f.stage["closed_won"]})
		if sv(m, "status") != "won" {
			t.Fatalf("status after move: %v", m)
		}
		if n := f.outboxCount(f.T1, "ratify.opportunity.stage_changed", id); n != 1 {
			t.Fatalf("stage_changed rows = %d", n)
		}
		a.want(200, f.repA, "PATCH", "/api/v1/opportunities/"+id, map[string]any{"amount_cents": 5}) // not a stage change
		if n := f.outboxCount(f.T1, "ratify.opportunity.stage_changed", id); n != 1 {
			t.Fatalf("non-stage update re-emitted: %d", n)
		}
	})
	t.Run("a stage from another pipeline is rejected (API and composite FK)", func(t *testing.T) {
		p2 := a.want(201, f.admin, "POST", "/api/v1/pipelines", map[string]any{"name": "Other", "stages": []map[string]any{{"name": "x", "phase": "discovery"}}})
		stage2 := sv(p2["stages"].([]any)[0].(map[string]any), "id")
		a.want(400, f.repA, "POST", "/api/v1/opportunities", map[string]any{"name": "x", "account_id": acctA, "stage_id": stage2, "pipeline_id": f.pipeline})
		err := a.tx.WithTenant(tenant.With(context.Background(), f.T1), func(c context.Context, q db.Tx) error {
			_, err := q.Exec(c, `INSERT INTO opportunities (tenant_id, name, account_id, pipeline_id, stage_id)
				VALUES (current_tenant(), 'x', $1::uuid, $2::uuid, $3::uuid)`, acctA, f.pipeline, stage2)
			return err
		})
		if pgCode(err) != "23503" {
			t.Fatalf("want 23503, got %v", err)
		}
	})
	t.Run("rep cannot read another rep's opportunity", func(t *testing.T) {
		id := sv(f.opp(f.repB, acctB, "b-deal", "discovery"), "id")
		a.want(404, f.repA, "GET", "/api/v1/opportunities/"+id, nil)
		a.want(404, f.repA, "PATCH", "/api/v1/opportunities/"+id, map[string]any{"name": "x"})
		a.want(404, f.repA, "DELETE", "/api/v1/opportunities/"+id, nil)
		if ids(a.want(200, f.repA, "GET", "/api/v1/opportunities?limit=200", nil))[id] {
			t.Fatal("list leaked")
		}
	})
	t.Run("manager can read team members' opportunities, not outsiders'", func(t *testing.T) {
		mine := sv(f.opp(f.repA, acctA, "team-deal", "discovery"), "id")
		out := sv(f.opp(f.repB, acctB, "out-deal", "discovery"), "id")
		a.want(200, f.mgr, "GET", "/api/v1/opportunities/"+mine, nil)
		a.want(404, f.mgr, "GET", "/api/v1/opportunities/"+out, nil)
		a.want(200, f.mgr, "PATCH", "/api/v1/opportunities/"+mine, map[string]any{"amount_cents": 99})
	})
	t.Run("cannot create on an account the caller cannot see, or in another tenant", func(t *testing.T) {
		a.want(404, f.repA, "POST", "/api/v1/opportunities", map[string]any{"name": "x", "account_id": acctB, "stage_id": f.stage["discovery"]})
		a.want(404, f.repA, "POST", "/api/v1/opportunities", map[string]any{"name": "x", "account_id": acctT2, "stage_id": f.stage["discovery"]})
		a.want(403, f.repA, "POST", "/api/v1/opportunities", map[string]any{"name": "x", "account_id": acctA, "stage_id": f.stage["discovery"], "owner_id": f.repB.id})
	})
	t.Run("another tenant's stage does not exist for me", func(t *testing.T) {
		p := a.want(201, f.admin2, "POST", "/api/v1/pipelines", map[string]any{"name": "T2", "stages": []map[string]any{{"name": "s", "phase": "discovery"}}})
		foreign := sv(p["stages"].([]any)[0].(map[string]any), "id")
		a.want(400, f.repA, "POST", "/api/v1/opportunities", map[string]any{"name": "x", "account_id": acctA, "stage_id": foreign})
	})
	t.Run("validation", func(t *testing.T) {
		a.want(422, f.repA, "POST", "/api/v1/opportunities", map[string]any{"name": "x", "account_id": acctA, "stage_id": f.stage["discovery"], "amount_cents": -5})
		a.want(422, f.repA, "POST", "/api/v1/opportunities", map[string]any{"name": "x", "account_id": acctA, "stage_id": f.stage["discovery"], "currency": "usd"})
		a.want(400, f.repA, "POST", "/api/v1/opportunities", map[string]any{"name": "x"})
		a.want(400, f.repA, "GET", "/api/v1/opportunities?status=weird", nil)
	})
	t.Run("filter by status and owner_id", func(t *testing.T) {
		won := sv(f.opp(f.repA, acctA, "fw", "closed_won"), "id")
		open := sv(f.opp(f.repA, acctA, "fo", "discovery"), "id")
		g := ids(a.want(200, f.admin, "GET", "/api/v1/opportunities?status=won&limit=200", nil))
		if !g[won] || g[open] {
			t.Fatal("status filter wrong")
		}
		o := ids(a.want(200, f.admin, "GET", "/api/v1/opportunities?owner_id="+f.repB.id+"&limit=200", nil))
		if o[won] || o[open] {
			t.Fatal("owner filter wrong")
		}
	})
	t.Run("pagination cursor from tenant A returns nothing under tenant B", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			f.opp(f.admin, acctA, "pg", "discovery")
		}
		cur := sv(a.want(200, f.admin, "GET", "/api/v1/opportunities?limit=1", nil), "next_cursor")
		a.want(200, f.admin, "GET", "/api/v1/opportunities?limit=1&cursor="+cur, nil)
		a.want(400, f.admin2, "GET", "/api/v1/opportunities?limit=1&cursor="+cur, nil)
	})
	t.Run("delete", func(t *testing.T) {
		id := sv(f.opp(f.repA, acctA, "del", "discovery"), "id")
		a.want(204, f.repA, "DELETE", "/api/v1/opportunities/"+id, nil)
		a.want(404, f.repA, "GET", "/api/v1/opportunities/"+id, nil)
	})
}
