package opportunity

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"ratify/internal/activity"
	"ratify/internal/audit"
	"ratify/internal/authz"
	"ratify/internal/crud"
	"ratify/internal/db"
	"ratify/internal/outbox"
	"ratify/internal/pagination"
	"ratify/internal/platform"
)

type Opportunity struct {
	platform.TenantOwned
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	AccountID     string     `json:"account_id"`
	PipelineID    string     `json:"pipeline_id"`
	StageID       string     `json:"stage_id"`
	AmountCents   int64      `json:"amount_cents"`
	Currency      string     `json:"currency"`
	CloseDate     *string    `json:"close_date"`
	OwnerID       *string    `json:"owner_id"`
	Status        string     `json:"status"`
	LastContactAt *time.Time `json:"last_contact_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

const cols = `id::text, tenant_id::text, name, account_id::text, pipeline_id::text, stage_id::text, amount_cents, currency,
	close_date::text, owner_id::text, status, last_contact_at, created_at, updated_at`

func scan(r pgx.Row) (Opportunity, error) {
	var o Opportunity
	err := r.Scan(&o.ID, &o.TenantID, &o.Name, &o.AccountID, &o.PipelineID, &o.StageID, &o.AmountCents, &o.Currency,
		&o.CloseDate, &o.OwnerID, &o.Status, &o.LastContactAt, &o.CreatedAt, &o.UpdatedAt)
	return o, err
}

var updatable = map[string]string{
	"name": "text", "account_id": "uuid", "pipeline_id": "uuid", "stage_id": "uuid", "amount_cents": "bigint",
	"currency": "text", "close_date": "date", "owner_id": "uuid", "status": "text", "last_contact_at": "timestamptz",
}

type Input struct {
	Name        string  `json:"name"`
	AccountID   string  `json:"account_id"`
	PipelineID  *string `json:"pipeline_id"`
	StageID     string  `json:"stage_id"`
	AmountCents int64   `json:"amount_cents"`
	Currency    string  `json:"currency"`
	CloseDate   *string `json:"close_date"`
	OwnerID     *string `json:"owner_id"`
}

type Filter struct{ OwnerID, Status, AccountID, PipelineID string }

type Repository struct{}

func (Repository) List(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, f Filter, after *pagination.Position, limit int) ([]Opportunity, *pagination.Position, error) {
	q := &crud.Q{}
	q.And(sc.SQL("owner_id", q.Arg))
	if f.OwnerID != "" {
		q.And("owner_id = " + q.Arg(f.OwnerID) + "::uuid")
	}
	if f.Status != "" {
		q.And("status = " + q.Arg(f.Status))
	}
	if f.AccountID != "" {
		q.And("account_id = " + q.Arg(f.AccountID) + "::uuid")
	}
	if f.PipelineID != "" {
		q.And("pipeline_id = " + q.Arg(f.PipelineID) + "::uuid")
	}
	return crud.List(ctx, tx, "SELECT "+cols+" FROM opportunities", q, after, limit, scan,
		func(o Opportunity) pagination.Position { return pagination.Position{CreatedAt: o.CreatedAt, ID: o.ID} })
}

// OpenByAccount returns an account's open opportunities within scope.
func (Repository) OpenByAccount(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, accountID string) ([]Opportunity, error) {
	items, _, err := Repository{}.List(ctx, tx, sc, Filter{AccountID: accountID, Status: "open"}, nil, 200)
	return items, err
}

func (Repository) Get(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, id string, lock bool) (Opportunity, error) {
	q := &crud.Q{}
	q.And("id = " + q.Arg(id) + "::uuid")
	q.And(sc.SQL("owner_id", q.Arg))
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	return scan(tx.QueryRow(ctx, "SELECT "+cols+" FROM opportunities"+q.Where()+suffix, q.Args...))
}

type Service struct {
	tx   *db.TenantTx
	repo Repository
}

func NewService(tx *db.TenantTx) *Service { return &Service{tx: tx} }

func statusFor(phase string) string {
	switch phase {
	case "closed_won":
		return "won"
	case "closed_lost":
		return "lost"
	}
	return "open"
}

func stageInfo(ctx context.Context, tx db.Tx, stageID string) (pipelineID, phase string, err error) {
	err = tx.QueryRow(ctx, `SELECT pipeline_id::text, phase FROM stages WHERE id = $1::uuid`, stageID).Scan(&pipelineID, &phase)
	if err == pgx.ErrNoRows {
		err = platform.Invalid{Msg: "stage does not exist"}
	}
	return
}

func (s *Service) List(ctx context.Context, r *http.Request, after *pagination.Position, limit int) (items []Opportunity, next *pagination.Position, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return nil, nil, err
	}
	f := Filter{Status: r.URL.Query().Get("status")}
	if f.Status != "" && f.Status != "open" && f.Status != "won" && f.Status != "lost" {
		return nil, nil, platform.Invalid{Msg: "status must be open, won or lost"}
	}
	if f.OwnerID, err = crud.OptionalUUID(r, "owner_id"); err != nil {
		return nil, nil, err
	}
	if f.AccountID, err = crud.OptionalUUID(r, "account_id"); err != nil {
		return nil, nil, err
	}
	if f.PipelineID, err = crud.OptionalUUID(r, "pipeline_id"); err != nil {
		return nil, nil, err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) (err error) {
		items, next, err = s.repo.List(ctx, tx, sc, f, after, limit)
		return
	})
	return
}

func (s *Service) Get(ctx context.Context, id string) (o Opportunity, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return o, err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) (err error) {
		o, err = s.repo.Get(ctx, tx, sc, id, false)
		return
	})
	return
}

// CreateInTx validates and creates an opportunity inside the caller's tenant
// transaction (used by lead conversion). The caller has checked RequireWrite.
func (s *Service) CreateInTx(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, in Input) (Opportunity, error) {
	in.Name = strings.TrimSpace(in.Name)
	if n := len(in.Name); n == 0 || n > 200 {
		return Opportunity{}, platform.Invalid{Msg: "name must be 1-200 characters"}
	}
	if in.AccountID == "" || in.StageID == "" {
		return Opportunity{}, platform.Invalid{Msg: "account_id and stage_id are required"}
	}
	if in.Currency == "" {
		in.Currency = "USD"
	}
	if in.OwnerID == nil {
		in.OwnerID = &sc.UserID
	}
	if !sc.CanWriteOwner(*in.OwnerID) {
		return Opportunity{}, platform.ErrForbidden
	}
	if err := sc.RequireVisible(ctx, tx, "accounts", in.AccountID); err != nil {
		return Opportunity{}, err
	}
	pipelineID, phase, err := stageInfo(ctx, tx, in.StageID)
	if err != nil {
		return Opportunity{}, err
	}
	if in.PipelineID != nil && *in.PipelineID != pipelineID {
		return Opportunity{}, platform.Invalid{Msg: "stage does not belong to pipeline_id"}
	}
	o, err := scan(tx.QueryRow(ctx, `
		INSERT INTO opportunities (tenant_id, name, account_id, pipeline_id, stage_id, amount_cents, currency, close_date, owner_id, status)
		VALUES (current_tenant(), $1, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7::date, $8::uuid, $9)
		RETURNING `+cols, in.Name, in.AccountID, pipelineID, in.StageID, in.AmountCents, in.Currency, in.CloseDate, in.OwnerID, statusFor(phase)))
	if err != nil {
		return o, err
	}
	if err := outbox.RecordEvent(ctx, tx, "opportunity.created", o.ID, map[string]any{
		"opportunity_id": o.ID, "account_id": o.AccountID, "stage_id": o.StageID, "owner_id": o.OwnerID, "amount_cents": o.AmountCents}); err != nil {
		return o, err
	}
	return o, audit.Write(ctx, tx, sc.UserID, "opportunity.created", "opportunity", o.ID, nil, o)
}

func (s *Service) Create(ctx context.Context, in Input) (o Opportunity, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return o, err
	}
	if err := sc.RequireWrite(); err != nil {
		return o, err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) (err error) {
		o, err = s.CreateInTx(ctx, tx, sc, in)
		return
	})
	return
}

func (s *Service) Update(ctx context.Context, id string, body map[string]json.RawMessage) (o Opportunity, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return o, err
	}
	if err := sc.RequireWrite(); err != nil {
		return o, err
	}
	if raw, ok := body["owner_id"]; ok {
		var owner *string
		if json.Unmarshal(raw, &owner) != nil {
			return o, platform.Invalid{Msg: "owner_id must be a UUID or null"}
		}
		if (owner == nil && !sc.All) || (owner != nil && !sc.CanWriteOwner(*owner)) {
			return o, platform.ErrForbidden
		}
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		before, err := s.repo.Get(ctx, tx, sc, id, true)
		if err != nil {
			return err
		}
		if raw, ok := body["account_id"]; ok {
			var acct string
			if json.Unmarshal(raw, &acct) != nil {
				return platform.Invalid{Msg: "account_id must be a UUID"}
			}
			if err := sc.RequireVisible(ctx, tx, "accounts", acct); err != nil {
				return err
			}
		}
		var newPhase string
		if raw, ok := body["stage_id"]; ok {
			var stage string
			if json.Unmarshal(raw, &stage) != nil {
				return platform.Invalid{Msg: "stage_id must be a UUID"}
			}
			pipelineID, phase, err := stageInfo(ctx, tx, stage)
			if err != nil {
				return err
			}
			newPhase = phase
			// The stage decides the pipeline; an explicit conflicting pipeline_id is an error.
			if p, ok := crud.Str(body, "pipeline_id"); ok && p != pipelineID {
				return platform.Invalid{Msg: "stage does not belong to pipeline_id"}
			}
			body["pipeline_id"], _ = json.Marshal(pipelineID)
			if _, explicit := body["status"]; !explicit {
				body["status"], _ = json.Marshal(statusFor(phase))
			}
		}
		if o, err = s.repoUpdate(ctx, tx, sc, id, body); err != nil {
			return err
		}
		if o.StageID != before.StageID {
			var fromPhase string
			_ = tx.QueryRow(ctx, `SELECT phase FROM stages WHERE id = $1::uuid`, before.StageID).Scan(&fromPhase)
			if err := outbox.RecordEvent(ctx, tx, "opportunity.stage_changed", id, map[string]any{
				"opportunity_id": id, "from_stage_id": before.StageID, "to_stage_id": o.StageID,
				"from_phase": fromPhase, "to_phase": newPhase, "status": o.Status}); err != nil {
				return err
			}
			// A human moved the deal: log it on the account/opportunity timeline.
			var fromName, toName string
			_ = tx.QueryRow(ctx, `SELECT name FROM stages WHERE id = $1::uuid`, before.StageID).Scan(&fromName)
			_ = tx.QueryRow(ctx, `SELECT name FROM stages WHERE id = $1::uuid`, o.StageID).Scan(&toName)
			meta, _ := json.Marshal(map[string]any{"kind": "stage_change", "from_stage_id": before.StageID, "to_stage_id": o.StageID})
			if _, err := activity.LogInTx(ctx, tx, sc, activity.Input{Type: "note",
				Subject: "Stage changed: " + fromName + " → " + toName, AccountID: o.AccountID,
				OpportunityID: &id, Metadata: meta}); err != nil {
				return err
			}
		}
		return audit.Write(ctx, tx, sc.UserID, "opportunity.updated", "opportunity", id, before, o)
	})
	return
}

func (s *Service) repoUpdate(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, id string, body map[string]json.RawMessage) (Opportunity, error) {
	q := &crud.Q{}
	q.And("id = " + q.Arg(id) + "::uuid")
	q.And(sc.SQL("owner_id", q.Arg))
	sql, err := crud.BuildUpdate("opportunities", cols, updatable, body, q)
	if err != nil {
		return Opportunity{}, err
	}
	return scan(tx.QueryRow(ctx, sql, q.Args...))
}

func (s *Service) Delete(ctx context.Context, id string) error {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return err
	}
	if err := sc.RequireWrite(); err != nil {
		return err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		before, err := s.repo.Get(ctx, tx, sc, id, true)
		if err != nil {
			return err
		}
		q := &crud.Q{}
		q.And("id = " + q.Arg(id) + "::uuid")
		q.And(sc.SQL("owner_id", q.Arg))
		if _, err := tx.Exec(ctx, "DELETE FROM opportunities"+q.Where(), q.Args...); err != nil {
			return err
		}
		return audit.Write(ctx, tx, sc.UserID, "opportunity.deleted", "opportunity", id, before, nil)
	})
	if platform.IsFKViolation(err) {
		return platform.Conflict{Msg: "opportunity has related records"}
	}
	return err
}
