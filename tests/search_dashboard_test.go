//go:build dbtest

package tests

import (
	"net/url"
	"testing"
)

func TestSearch(t *testing.T) {
	f := newFixture(t)
	a := f.a
	acctA := sv(a.want(201, f.repA, "POST", "/api/v1/accounts", map[string]any{"name": "Zebrafish Dynamics", "domain": "zebrafish.test", "industry": "biotech"}), "id")
	acctB := sv(a.want(201, f.repB, "POST", "/api/v1/accounts", map[string]any{"name": "Zebra Logistics"}), "id")
	ctA := sv(a.want(201, f.repA, "POST", "/api/v1/contacts", map[string]any{"first_name": "Zora", "last_name": "Quill", "email": "zora@zebrafish.test", "account_id": acctA}), "id")
	opA := sv(f.opp(f.repA, acctA, "Zebrafish renewal", "discovery"), "id")
	types := func(m map[string]any) map[string]string {
		out := map[string]string{}
		for _, it := range items(m) {
			out[sv(it, "id")] = sv(it, "type")
		}
		return out
	}

	t.Run("finds accounts, contacts and opportunities (prefix match)", func(t *testing.T) {
		got := types(a.want(200, f.repA, "GET", "/api/v1/search?q=zebraf", nil))
		if got[acctA] != "account" || got[ctA] != "contact" || got[opA] != "opportunity" {
			t.Fatalf("%v", got)
		}
	})
	t.Run("multi-word queries AND their terms; email and domain are searchable", func(t *testing.T) {
		if got := types(a.want(200, f.repA, "GET", "/api/v1/search?q="+url.QueryEscape("zora quill"), nil)); got[ctA] != "contact" {
			t.Fatalf("%v", got)
		}
		if got := types(a.want(200, f.repA, "GET", "/api/v1/search?q=zebrafish.test", nil)); got[acctA] != "account" {
			t.Fatalf("%v", got)
		}
		if got := types(a.want(200, f.repA, "GET", "/api/v1/search?q="+url.QueryEscape("zebrafish logistics"), nil)); len(got) != 0 {
			t.Fatalf("terms must be ANDed: %v", got)
		}
	})
	t.Run("a rep cannot find another rep's records", func(t *testing.T) {
		got := types(a.want(200, f.repA, "GET", "/api/v1/search?q=zebra", nil))
		if _, leaked := got[acctB]; leaked {
			t.Fatal("search leaked another rep's account")
		}
		if got := types(a.want(200, f.repB, "GET", "/api/v1/search?q=zebra", nil)); got[acctB] != "account" || got[acctA] != "" {
			t.Fatalf("%v", got)
		}
	})
	t.Run("manager sees team members' records; admin everything", func(t *testing.T) {
		if got := types(a.want(200, f.mgr, "GET", "/api/v1/search?q=zebra", nil)); got[acctA] != "account" || got[acctB] != "" {
			t.Fatalf("%v", got)
		}
		if got := types(a.want(200, f.admin, "GET", "/api/v1/search?q=zebra", nil)); got[acctA] == "" || got[acctB] == "" {
			t.Fatalf("%v", got)
		}
	})
	t.Run("another tenant finds nothing", func(t *testing.T) {
		if n := len(items(a.want(200, f.admin2, "GET", "/api/v1/search?q=zebra", nil))); n != 0 {
			t.Fatalf("tenant B found %d results", n)
		}
	})
	t.Run("hostile input is harmless; empty query is an empty result", func(t *testing.T) {
		for _, q := range []string{"'; DROP TABLE accounts;--", "&& || !", ")(", "a:* & b", ""} {
			a.want(200, f.repA, "GET", "/api/v1/search?q="+url.QueryEscape(q), nil)
		}
		a.want(400, f.repA, "GET", "/api/v1/search?q=x"+repeat("y", 300), nil)
		if n := len(items(a.want(200, f.repA, "GET", "/api/v1/search", nil))); n != 0 {
			t.Fatal("empty query returned results")
		}
	})
	t.Run("results carry tenant_id (cross-tenant response guard) and titles", func(t *testing.T) {
		for _, it := range items(a.want(200, f.repA, "GET", "/api/v1/search?q=zebraf", nil)) {
			if sv(it, "tenant_id") != f.T1 || sv(it, "title") == "" {
				t.Fatalf("%v", it)
			}
		}
	})
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func TestDashboardAndTimeline(t *testing.T) {
	f := newFixture(t)
	a := f.a
	acct := f.account(f.repA, "Dash Co")
	f.opp(f.repA, acct, "d1", "prospecting")
	f.opp(f.repA, acct, "d2", "discovery")
	won := f.opp(f.repA, acct, "d3", "closed_won")
	bAcct := f.account(f.repB, "B Dash")
	f.opp(f.repB, bAcct, "b1", "prospecting")

	t.Run("pipeline value by stage counts only OPEN deals inside the caller's scope", func(t *testing.T) {
		d := a.want(200, f.repA, "GET", "/api/v1/dashboard", nil)
		if d["open_count"].(float64) != 2 || d["open_value_cents"].(float64) != 2000 || len(d["stages"].([]any)) != 5 {
			t.Fatalf("%v", d)
		}
		for _, s := range d["stages"].([]any) {
			sm := s.(map[string]any)
			if sv(sm, "phase") == "closed_won" && sm["count"].(float64) != 0 {
				t.Fatal("a won deal counted as open")
			}
		}
		if len(d["my_open_deals"].([]any)) != 2 {
			t.Fatalf("my_open_deals: %v", d["my_open_deals"])
		}
	})
	t.Run("a manager's totals include the team; admin sees the tenant", func(t *testing.T) {
		if a.want(200, f.mgr, "GET", "/api/v1/dashboard", nil)["open_count"].(float64) != 2 {
			t.Fatal("manager scope wrong")
		}
		if a.want(200, f.admin, "GET", "/api/v1/dashboard", nil)["open_count"].(float64) != 3 {
			t.Fatal("admin scope wrong")
		}
		if a.want(200, f.admin2, "GET", "/api/v1/dashboard", nil)["open_count"].(float64) != 0 {
			t.Fatal("tenant B sees tenant A deals")
		}
	})
	t.Run("recent activities are scoped", func(t *testing.T) {
		a.want(201, f.repA, "POST", "/api/v1/activities", map[string]any{"type": "note", "subject": "mine", "account_id": acct})
		a.want(201, f.repB, "POST", "/api/v1/activities", map[string]any{"type": "note", "subject": "theirs", "account_id": bAcct})
		for _, x := range a.want(200, f.repA, "GET", "/api/v1/dashboard", nil)["recent_activities"].([]any) {
			if sv(x.(map[string]any), "subject") == "theirs" {
				t.Fatal("dashboard leaked another rep's activity")
			}
		}
	})
	t.Run("a stage change logs an activity on the timeline", func(t *testing.T) {
		id := sv(f.opp(f.repA, acct, "mover", "prospecting"), "id")
		a.want(200, f.repA, "PATCH", "/api/v1/opportunities/"+id, map[string]any{"stage_id": f.stage["discovery"]})
		got := items(a.want(200, f.repA, "GET", "/api/v1/activities?opportunity_id="+id, nil))
		if len(got) != 1 || sv(got[0], "subject") != "Stage changed: New → Qualified" || sv(got[0], "created_by") != f.repA.id {
			t.Fatalf("%v", got)
		}
		if n := f.outboxCount(f.T1, "ratify.activity.logged", sv(got[0], "id")); n != 1 {
			t.Fatalf("activity.logged rows = %d", n)
		}
		a.want(200, f.repA, "PATCH", "/api/v1/opportunities/"+id, map[string]any{"amount_cents": 7}) // not a stage change
		if n := len(items(a.want(200, f.repA, "GET", "/api/v1/activities?opportunity_id="+id, nil))); n != 1 {
			t.Fatalf("non-stage update logged an activity: %d", n)
		}
	})
	t.Run("opportunities can be filtered by pipeline", func(t *testing.T) {
		got := ids(a.want(200, f.repA, "GET", "/api/v1/opportunities?pipeline_id="+f.pipeline+"&limit=200", nil))
		if !got[sv(won, "id")] {
			t.Fatal("pipeline filter missed")
		}
		if n := len(items(a.want(200, f.repA, "GET", "/api/v1/opportunities?pipeline_id="+f.T1, nil))); n != 0 {
			t.Fatal("unknown pipeline returned rows")
		}
		a.want(400, f.repA, "GET", "/api/v1/opportunities?pipeline_id=zzz", nil)
	})
	t.Run("lead list rows explain whether Convert is possible", func(t *testing.T) {
		tok := tokenFor(t, f.T1)
		a.inbound(tok, map[string]any{"email": "ok@dash.test", "consent": true}, randIP(), "", nil)
		a.inbound(tok, map[string]any{"email": "no@dash.test"}, randIP(), "", nil)
		reasons := map[string]string{}
		for _, l := range items(a.want(200, f.repA, "GET", "/api/v1/leads?limit=200", nil)) {
			cc, _ := l["can_contact"].(map[string]any)
			reasons[sv(l, "email")] = sv(cc, "reason")
		}
		if reasons["ok@dash.test"] != "ok" || reasons["no@dash.test"] != "no_consent" {
			t.Fatalf("%v", reasons)
		}
	})
}
