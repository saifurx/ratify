// Package dashboard serves GET /api/v1/dashboard: pipeline value by stage,
// the caller's open deals and recent activities, all inside the caller's
// authz scope.
package dashboard

import (
	"context"
	"log/slog"
	"net/http"

	"ratify/internal/activity"
	"ratify/internal/authz"
	"ratify/internal/db"
	"ratify/internal/opportunity"
	"ratify/internal/platform"
)

type StageTotal struct {
	StageID    string `json:"stage_id"`
	Name       string `json:"name"`
	Phase      string `json:"phase"`
	Ordinal    int    `json:"ordinal"`
	Count      int    `json:"count"`
	ValueCents int64  `json:"value_cents"`
}

type Dashboard struct {
	PipelineID      string                    `json:"pipeline_id"`
	PipelineName    string                    `json:"pipeline_name"`
	Stages          []StageTotal              `json:"stages"`
	OpenCount       int                       `json:"open_count"`
	OpenValueCents  int64                     `json:"open_value_cents"`
	MyOpenDeals     []opportunity.Opportunity `json:"my_open_deals"`
	RecentActivites []activity.Activity       `json:"recent_activities"`
}

type Service struct{ tx *db.TenantTx }

func NewService(tx *db.TenantTx) *Service { return &Service{tx: tx} }

func (s *Service) Get(ctx context.Context) (d Dashboard, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return d, err
	}
	d.Stages, d.MyOpenDeals, d.RecentActivites = []StageTotal{}, []opportunity.Opportunity{}, []activity.Activity{}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		_ = tx.QueryRow(ctx, `SELECT id::text, name FROM pipelines WHERE is_default`).Scan(&d.PipelineID, &d.PipelineName)
		args := []any{}
		scope := sc.SQL("o.owner_id", func(v any) string { args = append(args, v); return "$1" })
		rows, err := tx.Query(ctx, `
			SELECT s.id::text, s.name, s.phase, s.ordinal, count(o.id)::int, coalesce(sum(o.amount_cents), 0)::bigint
			  FROM stages s
			  JOIN pipelines p ON p.tenant_id = s.tenant_id AND p.id = s.pipeline_id AND p.is_default
			  LEFT JOIN opportunities o ON o.tenant_id = s.tenant_id AND o.stage_id = s.id
			                           AND o.status = 'open' AND `+scope+`
			 GROUP BY s.id, s.name, s.phase, s.ordinal ORDER BY s.ordinal`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var t StageTotal
			if err := rows.Scan(&t.StageID, &t.Name, &t.Phase, &t.Ordinal, &t.Count, &t.ValueCents); err != nil {
				rows.Close()
				return err
			}
			d.OpenCount += t.Count
			d.OpenValueCents += t.ValueCents
			d.Stages = append(d.Stages, t)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if sc.UserID != "" {
			if d.MyOpenDeals, _, err = (opportunity.Repository{}).List(ctx, tx, sc,
				opportunity.Filter{OwnerID: sc.UserID, Status: "open"}, nil, 8); err != nil {
				return err
			}
		}
		d.RecentActivites, _, err = (activity.Repository{}).List(ctx, tx, sc, activity.Filter{}, nil, 10)
		return err
	})
	return
}

func Register(mux *http.ServeMux, protect func(http.Handler) http.Handler, log *slog.Logger, svc *Service) {
	mux.Handle("GET /api/v1/dashboard", protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d, err := svc.Get(r.Context())
		if err != nil {
			platform.WriteErr(w, log, err)
			return
		}
		platform.WriteJSON(w, http.StatusOK, d)
	})))
}
