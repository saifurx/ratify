// Package authz enforces in-tenant visibility. RLS is the tenant boundary
// only; this package is the owner/team/tenant scope on top of it, applied in
// the service layer to every list/get/update/delete.
//
//	rep                      owner_id = self
//	manager                  owner_id IN members of teams they manage (+ self)
//	admin                    whole tenant, may write
//	finance, legal           whole tenant, read-only
//	agent, system (services) whole tenant, read-only until a later sprint
package authz

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"ratify/internal/db"
	"ratify/internal/platform"
	"ratify/internal/user"
)

type ScopeInfo struct {
	UserID   string // users.id; empty for service principals
	Role     string
	Service  bool
	All      bool     // whole-tenant visibility
	OwnerIDs []string // when !All: owner ids visible (and writable if Writable)
	Writable bool
}

type scopeKey struct{}

// Scope returns the caller's visibility scope. Missing = error: fail closed.
func Scope(ctx context.Context) (ScopeInfo, error) {
	s, ok := ctx.Value(scopeKey{}).(ScopeInfo)
	if !ok {
		return ScopeInfo{}, platform.ErrForbidden
	}
	return s, nil
}

// WithScope is exported for tests that need to construct a scope directly.
func WithScope(ctx context.Context, s ScopeInfo) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}

// SQL returns a predicate limiting col (an owner_id column) to the scope,
// using q-style numbered arguments. It is an OWNER filter, never a tenant one.
func (s ScopeInfo) SQL(col string, arg func(any) string) string {
	if s.All {
		return "TRUE"
	}
	return fmt.Sprintf("%s = ANY(%s::uuid[])", col, arg(s.OwnerIDs))
}

// CanWriteOwner reports whether the caller may create/assign records owned by ownerID.
func (s ScopeInfo) CanWriteOwner(ownerID string) bool {
	if !s.Writable {
		return false
	}
	if s.All {
		return true
	}
	for _, id := range s.OwnerIDs {
		if id == ownerID {
			return true
		}
	}
	return false
}

func (s ScopeInfo) IsAdmin() bool { return s.Role == "admin" && !s.Service }

// RequireWrite errors unless the caller may write at all.
func (s ScopeInfo) RequireWrite() error {
	if !s.Writable {
		return platform.ErrForbidden
	}
	return nil
}

// RequireAdmin errors unless the caller is a tenant admin.
func (s ScopeInfo) RequireAdmin() error {
	if !s.IsAdmin() {
		return platform.ErrForbidden
	}
	return nil
}

// Middleware resolves the authenticated identity into a user record (upsert
// on first request) and a Scope, and binds both to the request context.
// Unknown roles, missing subjects and inactive users are 403.
func Middleware(tx *db.TenantTx, log *slog.Logger) func(http.Handler) http.Handler {
	var users user.Repository
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := platform.IdentityFrom(r.Context())
			if !ok {
				platform.WriteError(w, http.StatusForbidden, "forbidden")
				return
			}
			var sc ScopeInfo
			switch {
			case platform.IsServiceRole(id.Role):
				sc = ScopeInfo{Role: id.Role, Service: true, All: true}
			case platform.IsHumanRole(id.Role) && id.UserID != "":
				err := tx.WithTenant(r.Context(), func(ctx context.Context, q db.Tx) error {
					u, err := users.Sync(ctx, q, id.UserID, id.Email, id.Name, id.Role)
					if err != nil {
						return err
					}
					if !u.Active {
						return platform.ErrForbidden
					}
					sc = ScopeInfo{UserID: u.ID, Role: u.Role}
					switch u.Role {
					case "admin":
						sc.All, sc.Writable = true, true
					case "finance", "legal":
						sc.All = true
					case "manager":
						ids, err := users.ManagedUserIDs(ctx, q, u.ID)
						if err != nil {
							return err
						}
						sc.OwnerIDs, sc.Writable = append([]string{u.ID}, ids...), true
					default: // rep
						sc.OwnerIDs, sc.Writable = []string{u.ID}, true
					}
					return nil
				})
				if platform.IsFKViolation(err) { // the tenant itself does not exist
					err = platform.ErrForbidden
				}
				if err != nil {
					platform.WriteErr(w, log, err)
					return
				}
			default:
				platform.WriteError(w, http.StatusForbidden, "unknown role or missing subject")
				return
			}
			next.ServeHTTP(w, r.WithContext(WithScope(r.Context(), sc)))
		})
	}
}

// Visible reports whether the record `id` in table is inside the caller's
// scope. table must be one of the owner-scoped tables (never user input).
func (s ScopeInfo) Visible(ctx context.Context, tx db.Tx, table, id string) (bool, error) {
	switch table {
	case "accounts", "contacts", "opportunities":
	default:
		return false, fmt.Errorf("authz: %q is not an owner-scoped table", table)
	}
	args := []any{id}
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	var ok bool
	err := tx.QueryRow(ctx, fmt.Sprintf(
		"SELECT EXISTS (SELECT 1 FROM %s WHERE id = $1::uuid AND %s)", table, s.SQL("owner_id", arg)), args...).Scan(&ok)
	return ok, err
}

// RequireVisible returns ErrNotFound (never "forbidden": do not reveal
// existence) unless the record is in scope.
func (s ScopeInfo) RequireVisible(ctx context.Context, tx db.Tx, table, id string) error {
	ok, err := s.Visible(ctx, tx, table, id)
	if err != nil {
		return err
	}
	if !ok {
		return platform.ErrNotFound
	}
	return nil
}
