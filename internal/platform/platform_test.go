package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"ratify/internal/tenant"
)

const tid = "11111111-1111-1111-1111-111111111111"

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestConfigProductionRequiresOIDC(t *testing.T) {
	_, err := configFrom(env(map[string]string{"ENV": "Production", "DATABASE_URL": "x"}))
	if err == nil {
		t.Fatal("production without OIDC_ISSUER must be a boot error")
	}
	c, err := configFrom(env(map[string]string{"DATABASE_URL": "x"}))
	if err != nil || !c.DevMode() {
		t.Fatalf("default env should be dev mode: %v", err)
	}
	c, _ = configFrom(env(map[string]string{"DATABASE_URL": "x", "OIDC_ISSUER": "https://i", "OIDC_CLIENT_ID": "c"}))
	if c.DevMode() {
		t.Fatal("dev mode must be off when OIDC_ISSUER is set")
	}
	if _, err := configFrom(env(map[string]string{"DATABASE_URL": "x", "OIDC_ISSUER": "https://i"})); err == nil {
		t.Fatal("issuer without client id must fail")
	}
}

func TestNewAuthRefusesNoVerifierOutsideDev(t *testing.T) {
	if _, err := NewAuth(Config{Env: "production", OIDCIssuer: "https://i"}, nil); err == nil {
		t.Fatal("expected error")
	}
}

type fakeVerifier struct {
	c   Claims
	err error
}

func (f fakeVerifier) Verify(context.Context, string) (Claims, error) { return f.c, f.err }

func serve(a *Auth, req *http.Request) (*httptest.ResponseRecorder, string) {
	var got string
	rec := httptest.NewRecorder()
	a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = tenant.From(r.Context())
	})).ServeHTTP(rec, req)
	return rec, got
}

func TestDevModeHeaders(t *testing.T) {
	a, _ := NewAuth(Config{Env: "development"}, nil)
	req := httptest.NewRequest("GET", "/", nil)
	if rec, _ := serve(a, req); rec.Code != 403 {
		t.Fatalf("missing tenant want 403 got %d", rec.Code)
	}
	req.Header.Set("X-Tenant-Id", "not-a-uuid")
	if rec, _ := serve(a, req); rec.Code != 403 {
		t.Fatalf("bad tenant want 403 got %d", rec.Code)
	}
	req.Header.Set("X-Tenant-Id", tid)
	rec, got := serve(a, req)
	if rec.Code != 200 || got != tid {
		t.Fatalf("code=%d tenant=%q", rec.Code, got)
	}
}

func TestOIDCMode(t *testing.T) {
	cfg := Config{Env: "production", OIDCIssuer: "https://i", OIDCClientID: "c"}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant-Id", tid) // must be ignored
	a, _ := NewAuth(cfg, fakeVerifier{c: Claims{Subject: "u", TenantID: tid}})
	if rec, _ := serve(a, req); rec.Code != 401 {
		t.Fatalf("no bearer want 401 got %d", rec.Code)
	}
	req.Header.Set("Authorization", "Bearer tok")
	if rec, got := serve(a, req); rec.Code != 200 || got != tid {
		t.Fatalf("valid token: %d %q", rec.Code, got)
	}
	a, _ = NewAuth(cfg, fakeVerifier{c: Claims{Subject: "u"}}) // authenticated, no tenant claim
	if rec, _ := serve(a, req); rec.Code != 403 {
		t.Fatalf("missing tenant claim want 403 got %d", rec.Code)
	}
	a, _ = NewAuth(cfg, fakeVerifier{err: errors.New("bad")})
	if rec, _ := serve(a, req); rec.Code != 401 {
		t.Fatalf("bad token want 401 got %d", rec.Code)
	}
}
