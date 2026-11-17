package consent

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"ratify/internal/audit"
	"ratify/internal/authz"
	"ratify/internal/db"
	"ratify/internal/platform"
)

type Suppression struct {
	platform.TenantOwned
	ID      string    `json:"id"`
	Email   *string   `json:"email"`
	Domain  *string   `json:"domain"`
	Reason  string    `json:"reason"`
	AddedBy string    `json:"added_by"`
	AddedAt time.Time `json:"added_at"`
}

// Suppress adds an email or a domain (idempotent). Used by the admin API now
// and by bounce/complaint/opt-out handling in later sprints.
func Suppress(ctx context.Context, tx db.Tx, email, domain, reason, addedBy string) error {
	var e, d *string
	if email != "" {
		n := Normalize(email)
		e = &n
	}
	if domain != "" {
		n := strings.ToLower(strings.TrimSpace(domain))
		d = &n
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO suppression_list (tenant_id, email, domain, reason, added_by)
		VALUES (current_tenant(), $1::citext, $2, $3, $4) ON CONFLICT DO NOTHING`, e, d, reason, addedBy)
	return err
}

// RegisterAdmin mounts the (admin-only) suppression endpoints. There is
// deliberately no delete: suppression is permanent.
func RegisterAdmin(mux *http.ServeMux, protect func(http.Handler) http.Handler, tx *db.TenantTx, log *slog.Logger) {
	mux.Handle("GET /api/v1/suppressions", protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc, err := authz.Scope(r.Context())
		if err == nil {
			err = sc.RequireAdmin()
		}
		var out []Suppression
		if err == nil {
			err = tx.WithTenant(r.Context(), func(ctx context.Context, q db.Tx) error {
				rows, err := q.Query(ctx, `SELECT id::text, tenant_id::text, email::text, domain, reason, added_by, added_at
				                             FROM suppression_list ORDER BY added_at DESC LIMIT 500`)
				if err != nil {
					return err
				}
				defer rows.Close()
				out = []Suppression{}
				for rows.Next() {
					var s Suppression
					if err := rows.Scan(&s.ID, &s.TenantID, &s.Email, &s.Domain, &s.Reason, &s.AddedBy, &s.AddedAt); err != nil {
						return err
					}
					out = append(out, s)
				}
				return rows.Err()
			})
		}
		if err != nil {
			platform.WriteErr(w, log, err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
	})))
	mux.Handle("POST /api/v1/suppressions", protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc, err := authz.Scope(r.Context())
		if err == nil {
			err = sc.RequireAdmin()
		}
		var in struct {
			Email  string `json:"email"`
			Domain string `json:"domain"`
		}
		if err == nil && platform.DecodeJSON(w, r, &in) != nil {
			err = platform.Invalid{Msg: "invalid JSON body"}
		}
		if err == nil && ((in.Email == "") == (in.Domain == "") || (in.Email != "" && !strings.Contains(in.Email, "@"))) {
			err = platform.Invalid{Msg: "provide exactly one of a valid email or a domain"}
		}
		if err == nil {
			err = tx.WithTenant(r.Context(), func(ctx context.Context, q db.Tx) error {
				if err := Suppress(ctx, q, in.Email, in.Domain, "manual", sc.UserID); err != nil {
					return err
				}
				return audit.Write(ctx, q, sc.UserID, "suppression.added", "suppression", "", nil, map[string]string{"scope": map[bool]string{true: "email", false: "domain"}[in.Email != ""]})
			})
		}
		if err != nil {
			platform.WriteErr(w, log, err)
			return
		}
		platform.WriteJSON(w, http.StatusCreated, map[string]string{"status": "suppressed"})
	})))
}
