package platform

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"ratify/internal/tenant"
)

// Identity is what handlers may know about the caller.
type Identity struct {
	TenantID string
	UserID   string
}

type identityKey struct{}

func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// Claims are the verified token claims Ratify relies on.
type Claims struct {
	Subject  string
	TenantID string
}

type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (Claims, error)
}

type oidcVerifier struct{ v *oidc.IDTokenVerifier }

// NewOIDCVerifier discovers the issuer; failure is a boot error.
func NewOIDCVerifier(ctx context.Context, issuer, clientID string) (TokenVerifier, error) {
	p, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	return &oidcVerifier{v: p.Verifier(&oidc.Config{ClientID: clientID})}, nil
}

func (o *oidcVerifier) Verify(ctx context.Context, raw string) (Claims, error) {
	t, err := o.v.Verify(ctx, raw)
	if err != nil {
		return Claims{}, err
	}
	var c struct {
		TenantID string `json:"tenant_id"`
	}
	if err := t.Claims(&c); err != nil {
		return Claims{}, err
	}
	return Claims{Subject: t.Subject, TenantID: c.TenantID}, nil
}

type Auth struct {
	dev      bool
	verifier TokenVerifier
}

// NewAuth fails (boot error) if production-mode auth has no verifier.
func NewAuth(cfg Config, v TokenVerifier) (*Auth, error) {
	if !cfg.DevMode() && v == nil {
		return nil, errors.New("auth: OIDC verifier required outside dev mode")
	}
	return &Auth{dev: cfg.DevMode(), verifier: v}, nil
}

// Middleware binds the verified tenant to the request context. A missing or
// invalid tenant is 403, never an implicit global grant.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var tenantID, userID string
		if a.dev {
			tenantID = r.Header.Get("X-Tenant-Id")
			userID = r.Header.Get("X-User-Id")
		} else {
			h := r.Header.Get("Authorization")
			raw, ok := strings.CutPrefix(h, "Bearer ")
			if !ok || raw == "" {
				WriteError(w, http.StatusUnauthorized, "missing bearer token")
				return
			}
			c, err := a.verifier.Verify(r.Context(), raw)
			if err != nil {
				WriteError(w, http.StatusUnauthorized, "invalid token")
				return
			}
			tenantID, userID = c.TenantID, c.Subject
		}
		if !tenant.Valid(tenantID) {
			WriteError(w, http.StatusForbidden, "missing or invalid tenant")
			return
		}
		ctx := tenant.With(r.Context(), tenantID)
		ctx = context.WithValue(ctx, identityKey{}, Identity{TenantID: strings.ToLower(tenantID), UserID: userID})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
