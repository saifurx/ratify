package authz

import (
	"context"
	"strings"
	"testing"
)

func capture() (func(any) string, *[]any) {
	var args []any
	return func(v any) string { args = append(args, v); return "$1" }, &args
}

func TestRepScopeIsOwnerFilter(t *testing.T) {
	s := ScopeInfo{UserID: "u1", Role: "rep", OwnerIDs: []string{"u1"}, Writable: true}
	arg, args := capture()
	sql := s.SQL("owner_id", arg)
	if !strings.Contains(sql, "owner_id = ANY(") || strings.Contains(sql, "tenant_id") || len(*args) != 1 {
		t.Fatalf("%q %v", sql, *args)
	}
}

func TestWholeTenantScopeHasNoPredicate(t *testing.T) {
	for _, s := range []ScopeInfo{{Role: "admin", All: true, Writable: true}, {Role: "finance", All: true}, {Role: "agent", Service: true, All: true}} {
		arg, args := capture()
		if s.SQL("owner_id", arg) != "TRUE" || len(*args) != 0 {
			t.Fatalf("%s: wrong predicate", s.Role)
		}
	}
}

func TestCanWriteOwner(t *testing.T) {
	rep := ScopeInfo{UserID: "a", OwnerIDs: []string{"a"}, Writable: true}
	mgr := ScopeInfo{UserID: "m", OwnerIDs: []string{"m", "a"}, Writable: true}
	admin := ScopeInfo{Role: "admin", All: true, Writable: true}
	fin := ScopeInfo{Role: "finance", All: true}
	svc := ScopeInfo{Role: "agent", Service: true, All: true}
	cases := []struct {
		name string
		s    ScopeInfo
		own  string
		want bool
	}{
		{"rep self", rep, "a", true}, {"rep other", rep, "b", false},
		{"mgr team", mgr, "a", true}, {"mgr outsider", mgr, "b", false},
		{"admin anyone", admin, "zzz", true},
		{"finance read-only", fin, "a", false}, {"service read-only", svc, "a", false},
	}
	for _, c := range cases {
		if got := c.s.CanWriteOwner(c.own); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestRequireWriteAndAdmin(t *testing.T) {
	if (ScopeInfo{Role: "finance", All: true}).RequireWrite() == nil {
		t.Fatal("finance must not write")
	}
	if (ScopeInfo{Role: "admin", All: true, Writable: true}).RequireAdmin() != nil {
		t.Fatal("admin must pass")
	}
	if (ScopeInfo{Role: "admin", Service: true}).RequireAdmin() == nil {
		t.Fatal("a service principal is never an admin")
	}
	if (ScopeInfo{Role: "manager", Writable: true}).RequireAdmin() == nil {
		t.Fatal("manager is not admin")
	}
}

func TestMissingScopeFailsClosed(t *testing.T) {
	if _, err := Scope(context.Background()); err == nil {
		t.Fatal("Scope without middleware must error")
	}
	if got, err := Scope(WithScope(context.Background(), ScopeInfo{Role: "rep"})); err != nil || got.Role != "rep" {
		t.Fatal("round trip failed")
	}
}
