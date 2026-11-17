// Package pipeline: pipelines and stages. A tenant may have many pipelines
// and exactly one default (partial unique index). Stage.phase is the
// tenant-independent mapping later sprints use; names are free text.
// Reads are open to every role; writes are admin-only.
package pipeline

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"ratify/internal/audit"
	"ratify/internal/authz"
	"ratify/internal/crud"
	"ratify/internal/db"
	"ratify/internal/pagination"
	"ratify/internal/platform"
)

type Stage struct {
	platform.TenantOwned
	ID         string `json:"id"`
	PipelineID string `json:"pipeline_id"`
	Name       string `json:"name"`
	Ordinal    int    `json:"ordinal"`
	Phase      string `json:"phase"`
}

type Pipeline struct {
	platform.TenantOwned
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	IsDefault bool      `json:"is_default"`
	Stages    []Stage   `json:"stages"`
	CreatedAt time.Time `json:"created_at"`
}

type StageInput struct {
	Name    string `json:"name"`
	Phase   string `json:"phase"`
	Ordinal *int   `json:"ordinal"`
}

type Input struct {
	Name      string       `json:"name"`
	IsDefault bool         `json:"is_default"`
	Stages    []StageInput `json:"stages"`
}

const stageCols = `id::text, tenant_id::text, pipeline_id::text, name, ordinal, phase`

func scanStage(r pgx.Row) (Stage, error) {
	var s Stage
	err := r.Scan(&s.ID, &s.TenantID, &s.PipelineID, &s.Name, &s.Ordinal, &s.Phase)
	return s, err
}

type Service struct{ tx *db.TenantTx }

func NewService(tx *db.TenantTx) *Service { return &Service{tx: tx} }

func (s *Service) pipelines(ctx context.Context, tx db.Tx, id string) ([]Pipeline, error) {
	q := &crud.Q{}
	if id != "" {
		q.And("id = " + q.Arg(id) + "::uuid")
	}
	rows, err := tx.Query(ctx, `SELECT id::text, tenant_id::text, name, is_default, created_at FROM pipelines`+q.Where()+` ORDER BY is_default DESC, name LIMIT 200`, q.Args...)
	if err != nil {
		return nil, err
	}
	out := []Pipeline{}
	idx := map[string]int{}
	for rows.Next() {
		var p Pipeline
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &p.IsDefault, &p.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		p.Stages = []Stage{}
		idx[p.ID] = len(out)
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	srows, err := tx.Query(ctx, `SELECT `+stageCols+` FROM stages ORDER BY pipeline_id, ordinal`)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	for srows.Next() {
		st, err := scanStage(srows)
		if err != nil {
			return nil, err
		}
		if i, ok := idx[st.PipelineID]; ok {
			out[i].Stages = append(out[i].Stages, st)
		}
	}
	return out, srows.Err()
}

func (s *Service) List(ctx context.Context, _ *http.Request, _ *pagination.Position, _ int) (out []Pipeline, _ *pagination.Position, err error) {
	if _, err = authz.Scope(ctx); err != nil {
		return nil, nil, err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) (err error) {
		out, err = s.pipelines(ctx, tx, "")
		return
	})
	return
}

func (s *Service) Get(ctx context.Context, id string) (p Pipeline, err error) {
	if _, err = authz.Scope(ctx); err != nil {
		return p, err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		l, err := s.pipelines(ctx, tx, id)
		if err != nil {
			return err
		}
		if len(l) == 0 {
			return platform.ErrNotFound
		}
		p = l[0]
		return nil
	})
	return
}

func (s *Service) Create(ctx context.Context, in Input) (p Pipeline, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return p, err
	}
	if err := sc.RequireAdmin(); err != nil {
		return p, err
	}
	if n := len(in.Name); n == 0 || n > 200 {
		return p, platform.Invalid{Msg: "name must be 1-200 characters"}
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM pipelines`).Scan(&n); err != nil {
			return err
		}
		def := in.IsDefault || n == 0 // the first pipeline is always the default
		if def {
			if _, err := tx.Exec(ctx, `UPDATE pipelines SET is_default = false WHERE is_default`); err != nil {
				return err
			}
		}
		var id string
		if err := tx.QueryRow(ctx,
			`INSERT INTO pipelines (tenant_id, name, is_default) VALUES (current_tenant(), $1, $2) RETURNING id::text`,
			in.Name, def).Scan(&id); err != nil {
			return err
		}
		for i, st := range in.Stages {
			ord := i + 1
			if st.Ordinal != nil {
				ord = *st.Ordinal
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO stages (tenant_id, pipeline_id, name, ordinal, phase) VALUES (current_tenant(), $1::uuid, $2, $3, $4)`,
				id, st.Name, ord, st.Phase); err != nil {
				return err
			}
		}
		l, err := s.pipelines(ctx, tx, id)
		if err != nil {
			return err
		}
		p = l[0]
		return audit.Write(ctx, tx, sc.UserID, "pipeline.created", "pipeline", id, nil, p)
	})
	return
}

func (s *Service) Update(ctx context.Context, id string, body map[string]json.RawMessage) (p Pipeline, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return p, err
	}
	if err := sc.RequireAdmin(); err != nil {
		return p, err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		cur, err := s.pipelines(ctx, tx, id)
		if err != nil {
			return err
		}
		if len(cur) == 0 {
			return platform.ErrNotFound
		}
		for k, raw := range body {
			switch k {
			case "name":
				var n string
				if json.Unmarshal(raw, &n) != nil || len(n) == 0 || len(n) > 200 {
					return platform.Invalid{Msg: "name must be 1-200 characters"}
				}
				if _, err := tx.Exec(ctx, `UPDATE pipelines SET name = $2 WHERE id = $1::uuid`, id, n); err != nil {
					return err
				}
			case "is_default":
				var d bool
				if json.Unmarshal(raw, &d) != nil {
					return platform.Invalid{Msg: "is_default must be a boolean"}
				}
				if !d && cur[0].IsDefault {
					return platform.Conflict{Msg: "a tenant must have exactly one default pipeline; set another as default instead"}
				}
				if d {
					if _, err := tx.Exec(ctx, `UPDATE pipelines SET is_default = false WHERE is_default AND id <> $1::uuid`, id); err != nil {
						return err
					}
					if _, err := tx.Exec(ctx, `UPDATE pipelines SET is_default = true WHERE id = $1::uuid`, id); err != nil {
						return err
					}
				}
			default:
				return platform.Invalid{Msg: "field not updatable: " + k}
			}
		}
		l, err := s.pipelines(ctx, tx, id)
		if err != nil {
			return err
		}
		p = l[0]
		return audit.Write(ctx, tx, sc.UserID, "pipeline.updated", "pipeline", id, cur[0], p)
	})
	return
}

func (s *Service) Delete(ctx context.Context, id string) error {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return err
	}
	if err := sc.RequireAdmin(); err != nil {
		return err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		cur, err := s.pipelines(ctx, tx, id)
		if err != nil {
			return err
		}
		if len(cur) == 0 {
			return platform.ErrNotFound
		}
		if cur[0].IsDefault {
			return platform.Conflict{Msg: "cannot delete the default pipeline"}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM stages WHERE pipeline_id = $1::uuid`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM pipelines WHERE id = $1::uuid`, id); err != nil {
			return err
		}
		return audit.Write(ctx, tx, sc.UserID, "pipeline.deleted", "pipeline", id, cur[0], nil)
	})
	if platform.IsFKViolation(err) {
		return platform.Conflict{Msg: "pipeline is used by opportunities"}
	}
	return err
}

func (s *Service) CreateStage(ctx context.Context, pipelineID string, in StageInput) (st Stage, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return st, err
	}
	if err := sc.RequireAdmin(); err != nil {
		return st, err
	}
	if n := len(in.Name); n == 0 || n > 200 {
		return st, platform.Invalid{Msg: "name must be 1-200 characters"}
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pipelines WHERE id = $1::uuid)`, pipelineID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return platform.ErrNotFound
		}
		st, err = scanStage(tx.QueryRow(ctx, `
			INSERT INTO stages (tenant_id, pipeline_id, name, ordinal, phase)
			VALUES (current_tenant(), $1::uuid, $2,
			        COALESCE($3, (SELECT COALESCE(max(ordinal), 0) + 1 FROM stages WHERE pipeline_id = $1::uuid)), $4)
			RETURNING `+stageCols, pipelineID, in.Name, in.Ordinal, in.Phase))
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, sc.UserID, "stage.created", "stage", st.ID, nil, st)
	})
	return
}

var stageUpdatable = map[string]string{"name": "text", "ordinal": "int", "phase": "text"}

func (s *Service) UpdateStage(ctx context.Context, id string, body map[string]json.RawMessage) (st Stage, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return st, err
	}
	if err := sc.RequireAdmin(); err != nil {
		return st, err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		q := &crud.Q{}
		q.And("id = " + q.Arg(id) + "::uuid")
		sql, err := crud.BuildUpdate("stages", stageCols, stageUpdatable, body, q)
		if err != nil {
			return err
		}
		if st, err = scanStage(tx.QueryRow(ctx, sql, q.Args...)); err != nil {
			return err
		}
		return audit.Write(ctx, tx, sc.UserID, "stage.updated", "stage", id, nil, st)
	})
	return
}

func (s *Service) DeleteStage(ctx context.Context, id string) error {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return err
	}
	if err := sc.RequireAdmin(); err != nil {
		return err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM stages WHERE id = $1::uuid`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return platform.ErrNotFound
		}
		return audit.Write(ctx, tx, sc.UserID, "stage.deleted", "stage", id, nil, nil)
	})
	if platform.IsFKViolation(err) {
		return platform.Conflict{Msg: "stage is used by opportunities"}
	}
	return err
}

func Register(mux *http.ServeMux, protect func(http.Handler) http.Handler, codec pagination.Codec, log *slog.Logger, svc *Service) {
	crud.Register(mux, protect, codec, log, "/api/v1/pipelines", crud.Ops[Pipeline, Input]{
		List: svc.List, Get: svc.Get, Create: svc.Create, Update: svc.Update, Delete: svc.Delete,
	})
	crud.Register(mux, protect, codec, log, "/api/v1/stages", crud.Ops[Stage, struct{}]{
		Update: svc.UpdateStage, Delete: svc.DeleteStage,
	})
	mux.Handle("POST /api/v1/pipelines/{id}/stages", protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := crud.PathID(r, "id")
		var in StageInput
		if err == nil && platform.DecodeJSON(w, r, &in) != nil {
			err = platform.Invalid{Msg: "invalid JSON body"}
		}
		if err == nil {
			var st Stage
			if st, err = svc.CreateStage(r.Context(), id, in); err == nil {
				platform.WriteJSON(w, http.StatusCreated, st)
				return
			}
		}
		platform.WriteErr(w, log, err)
	})))
}
