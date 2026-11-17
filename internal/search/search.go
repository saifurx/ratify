// Package search is GET /api/v1/search?q= over accounts, contacts and
// opportunities using PostgreSQL full-text search. Results pass through
// authz.Scope exactly like the list endpoints.
package search

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"ratify/internal/authz"
	"ratify/internal/db"
	"ratify/internal/platform"
)

type Result struct {
	platform.TenantOwned
	Type     string `json:"type"` // account | contact | opportunity
	ID       string `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
}

var wordRE = regexp.MustCompile(`[\p{L}\p{N}]+`)

// tsQuery turns free text into a prefix tsquery ("ann acm" -> "ann:* & acm:*").
// Only letters and digits survive, so the string is safe for to_tsquery.
func tsQuery(q string) string {
	words := wordRE.FindAllString(strings.ToLower(q), 8)
	for i, w := range words {
		words[i] = w + ":*"
	}
	return strings.Join(words, " & ")
}

type Service struct{ tx *db.TenantTx }

func NewService(tx *db.TenantTx) *Service { return &Service{tx: tx} }

func (s *Service) Search(ctx context.Context, q string, per int) (out []Result, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return nil, err
	}
	out = []Result{}
	tq := tsQuery(q)
	if tq == "" {
		return out, nil
	}
	if per < 1 || per > 25 {
		per = 8
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		for _, spec := range []struct{ typ, sql string }{
			{"account", `SELECT id::text, tenant_id::text, name, coalesce(domain, '')
			               FROM accounts, to_tsquery('simple', $1) q
			              WHERE search @@ q AND %s ORDER BY ts_rank(search, q) DESC, name LIMIT %d`},
			{"contact", `SELECT id::text, tenant_id::text,
			                    COALESCE(NULLIF(trim(first_name || ' ' || last_name), ''), email::text, ''), coalesce(email::text, '')
			               FROM contacts, to_tsquery('simple', $1) q
			              WHERE search @@ q AND %s ORDER BY ts_rank(search, q) DESC, last_name LIMIT %d`},
			{"opportunity", `SELECT id::text, tenant_id::text, name, status || ' · ' || (amount_cents / 100)::text || ' ' || currency
			                   FROM opportunities, to_tsquery('simple', $1) q
			                  WHERE search @@ q AND %s ORDER BY ts_rank(search, q) DESC, name LIMIT %d`},
		} {
			args := []any{tq}
			scope := sc.SQL("owner_id", func(v any) string { args = append(args, v); return "$" + itoa(len(args)) })
			rows, err := tx.Query(ctx, sprintf(spec.sql, scope, per), args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				r := Result{Type: spec.typ}
				if err := rows.Scan(&r.ID, &r.TenantID, &r.Title, &r.Subtitle); err != nil {
					rows.Close()
					return err
				}
				out = append(out, r)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		return nil
	})
	return
}

func Register(mux *http.ServeMux, protect func(http.Handler) http.Handler, log *slog.Logger, svc *Service) {
	mux.Handle("GET /api/v1/search", protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if len(q) > 200 {
			platform.WriteError(w, http.StatusBadRequest, "q is too long")
			return
		}
		items, err := svc.Search(r.Context(), q, 8)
		if err != nil {
			platform.WriteErr(w, log, err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})))
}
