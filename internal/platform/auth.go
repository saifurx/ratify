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
	UserID   string // token subject (FerrisKey sub); dev: X-User-Id
	Role     string // from token roles (dev: X-Role, default "rep")
	Email    string
	Name     string
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
	Roles    []string
	Email    string
	Name     string
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
		TenantID string   `json:"tenant_id"`
		Roles    []string `json:"roles"`
		Email    string   `json:"email"`
		Name     string   `json:"name"`
	}
	if err := t.Claims(&c); err != nil {
		return Claims{}, err
	}
	return Claims{Subject: t.Subject, TenantID: c.TenantID, Roles: c.Roles, Email: c.Email, Name: c.Name}, nil
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
		var tenantID, userID, role, email, name string
		if a.dev {
			tenantID = r.Header.Get("X-Tenant-Id")
			userID = r.Header.Get("X-User-Id")
			role = r.Header.Get("X-Role")
			if role == "" {
				role = "rep"
			}
			email, name = r.Header.Get("X-User-Email"), r.Header.Get("X-User-Name")
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
			tenantID, userID, email, name = c.TenantID, c.Subject, c.Email, c.Name
			role = PickRole(c.Roles)
		}
		if !tenant.Valid(tenantID) {
			WriteError(w, http.StatusForbidden, "missing or invalid tenant")
			return
		}
		ctx := tenant.With(r.Context(), tenantID)
		ctx = context.WithValue(ctx, identityKey{}, Identity{TenantID: strings.ToLower(tenantID), UserID: userID, Role: role, Email: email, Name: name})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Roles is the ONLY role vocabulary. Human roles are stored in users.role;
// agent and system are service principals (never stored in users).
var humanRoles = []string{"admin", "manager", "finance", "legal", "rep"}
var serviceRoles = []string{"agent", "system"}

func IsHumanRole(r string) bool   { return contains(humanRoles, r) }
func IsServiceRole(r string) bool { return contains(serviceRoles, r) }

// PickRole returns the highest-privilege recognised role in claims, or ""
// (which authz rejects with 403: unknown role is never a default grant).
func PickRole(roles []string) string {
	for _, want := range append(append([]string{}, humanRoles...), serviceRoles...) {
		if contains(roles, want) {
			return want
		}
	}
	return ""
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
