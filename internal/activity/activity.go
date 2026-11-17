package activity

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
	"ratify/internal/outbox"
	"ratify/internal/pagination"
	"ratify/internal/platform"
)

type Activity struct {
	platform.TenantOwned
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	Subject       string          `json:"subject"`
	Body          string          `json:"body"`
	OccurredAt    time.Time       `json:"occurred_at"`
	ContactID     *string         `json:"contact_id"`
	OpportunityID *string         `json:"opportunity_id"`
	AccountID     string          `json:"account_id"`
	Metadata      json.RawMessage `json:"metadata"`
	CreatedBy     string          `json:"created_by"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

const cols = `id::text, tenant_id::text, type, subject, body, occurred_at, contact_id::text, opportunity_id::text,
	account_id::text, metadata, created_by, created_at, updated_at`

func scan(r pgx.Row) (Activity, error) {
	var a Activity
	err := r.Scan(&a.ID, &a.TenantID, &a.Type, &a.Subject, &a.Body, &a.OccurredAt, &a.ContactID, &a.OpportunityID,
		&a.AccountID, &a.Metadata, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

var updatable = map[string]string{"subject": "text", "body": "text", "occurred_at": "timestamptz", "metadata": "jsonb"}

type Input struct {
	Type          string          `json:"type"`
	Subject       string          `json:"subject"`
	Body          string          `json:"body"`
	OccurredAt    *time.Time      `json:"occurred_at"`
	ContactID     *string         `json:"contact_id"`
	OpportunityID *string         `json:"opportunity_id"`
	AccountID     string          `json:"account_id"`
	Metadata      json.RawMessage `json:"metadata"`
}

type Filter struct{ AccountID, ContactID, OpportunityID, Type string }

type Repository struct{}

// visible restricts activities to those on accounts inside the caller's scope.
func visible(q *crud.Q, sc authz.ScopeInfo) {
	if sc.All {
		return
	}
	q.And("EXISTS (SELECT 1 FROM accounts a WHERE a.id = activities.account_id AND " + sc.SQL("a.owner_id", q.Arg) + ")")
}

func (Repository) List(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, f Filter, after *pagination.Position, limit int) ([]Activity, *pagination.Position, error) {
	q := &crud.Q{}
	visible(q, sc)
	for col, v := range map[string]string{"account_id": f.AccountID, "contact_id": f.ContactID, "opportunity_id": f.OpportunityID} {
		if v != "" {
			q.And(col + " = " + q.Arg(v) + "::uuid")
		}
	}
	if f.Type != "" {
		q.And("type = " + q.Arg(f.Type))
	}
	return crud.List(ctx, tx, "SELECT "+cols+" FROM activities", q, after, limit, scan,
		func(a Activity) pagination.Position { return pagination.Position{CreatedAt: a.CreatedAt, ID: a.ID} })
}

// LastByAccount returns the n most recent activities (by occurred_at) of an account within scope.
func (Repository) LastByAccount(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, accountID string, n int) ([]Activity, error) {
	q := &crud.Q{}
	visible(q, sc)
	q.And("account_id = " + q.Arg(accountID) + "::uuid")
	rows, err := tx.Query(ctx, "SELECT "+cols+" FROM activities"+q.Where()+" ORDER BY occurred_at DESC, id DESC LIMIT "+itoa(n), q.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func (Repository) Get(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, id string) (Activity, error) {
	q := &crud.Q{}
	q.And("id = " + q.Arg(id) + "::uuid")
	visible(q, sc)
	return scan(tx.QueryRow(ctx, "SELECT "+cols+" FROM activities"+q.Where(), q.Args...))
}

type Service struct {
	tx   *db.TenantTx
	repo Repository
}

func NewService(tx *db.TenantTx) *Service { return &Service{tx: tx} }

func (s *Service) List(ctx context.Context, r *http.Request, after *pagination.Position, limit int) (items []Activity, next *pagination.Position, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return nil, nil, err
	}
	f := Filter{Type: r.URL.Query().Get("type")}
	if f.AccountID, err = crud.OptionalUUID(r, "account_id"); err != nil {
		return nil, nil, err
	}
	if f.ContactID, err = crud.OptionalUUID(r, "contact_id"); err != nil {
		return nil, nil, err
	}
	if f.OpportunityID, err = crud.OptionalUUID(r, "opportunity_id"); err != nil {
		return nil, nil, err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) (err error) {
		items, next, err = s.repo.List(ctx, tx, sc, f, after, limit)
		return
	})
	return
}

func (s *Service) Get(ctx context.Context, id string) (a Activity, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return a, err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) (err error) {
		a, err = s.repo.Get(ctx, tx, sc, id)
		return
	})
	return
}

// Create logs an activity as the calling user. created_by is always the
// caller; clients cannot forge another user or an 'ai:' producer.
func (s *Service) Create(ctx context.Context, in Input) (a Activity, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return a, err
	}
	if err := sc.RequireWrite(); err != nil {
		return a, err
	}
	if in.AccountID == "" {
		return a, platform.Invalid{Msg: "account_id is required"}
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		if err := sc.RequireVisible(ctx, tx, "accounts", in.AccountID); err != nil {
			return err
		}
		if in.ContactID != nil {
			if err := sc.RequireVisible(ctx, tx, "contacts", *in.ContactID); err != nil {
				return err
			}
		}
		if in.OpportunityID != nil {
			if err := sc.RequireVisible(ctx, tx, "opportunities", *in.OpportunityID); err != nil {
				return err
			}
		}
		var err error
		a, err = LogInTx(ctx, tx, sc, in)
		return err
	})
	return
}

// LogInTx inserts an activity authored by the caller inside an existing tenant
// transaction and emits activity.logged. It performs no visibility checks:
// callers either did them (Service.Create) or are acting on a record they just
// modified (stage-change logging).
func LogInTx(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, in Input) (Activity, error) {
	occurred := time.Now().UTC()
	if in.OccurredAt != nil {
		occurred = *in.OccurredAt
	}
	meta := "{}"
	if len(in.Metadata) > 0 {
		meta = string(in.Metadata)
	}
	a, err := scan(tx.QueryRow(ctx, `
		INSERT INTO activities (tenant_id, type, subject, body, occurred_at, contact_id, opportunity_id, account_id, metadata, created_by)
		VALUES (current_tenant(), $1, $2, $3, $4, $5::uuid, $6::uuid, $7::uuid, $8::jsonb, $9)
		RETURNING `+cols, in.Type, in.Subject, in.Body, occurred, in.ContactID, in.OpportunityID, in.AccountID, meta, sc.UserID))
	if err != nil {
		return a, err
	}
	if err := outbox.RecordEvent(ctx, tx, "activity.logged", a.ID, map[string]any{
		"activity_id": a.ID, "type": a.Type, "account_id": a.AccountID, "contact_id": a.ContactID, "opportunity_id": a.OpportunityID}); err != nil {
		return a, err
	}
	return a, audit.Write(ctx, tx, sc.UserID, "activity.logged", "activity", a.ID, nil, nil)
}

// mutable restricts edits/deletes to the author (admins may touch any).
func mutable(q *crud.Q, sc authz.ScopeInfo) {
	if !sc.IsAdmin() {
		q.And("created_by = " + q.Arg(sc.UserID))
	}
}

func (s *Service) Update(ctx context.Context, id string, body map[string]json.RawMessage) (a Activity, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return a, err
	}
	if err := sc.RequireWrite(); err != nil {
		return a, err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		if _, err := s.repo.Get(ctx, tx, sc, id); err != nil {
			return err
		}
		q := &crud.Q{}
		q.And("id = " + q.Arg(id) + "::uuid")
		visible(q, sc)
		mutable(q, sc)
		sql, err := crud.BuildUpdate("activities", cols, updatable, body, q)
		if err != nil {
			return err
		}
		if a, err = scan(tx.QueryRow(ctx, sql, q.Args...)); err != nil {
			if err == pgx.ErrNoRows {
				return platform.ErrForbidden // visible but not authored by the caller
			}
			return err
		}
		return audit.Write(ctx, tx, sc.UserID, "activity.updated", "activity", id, nil, nil)
	})
	return
}

func (s *Service) Delete(ctx context.Context, id string) error {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return err
	}
	if err := sc.RequireWrite(); err != nil {
		return err
	}
	return s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		if _, err := s.repo.Get(ctx, tx, sc, id); err != nil {
			return err
		}
		q := &crud.Q{}
		q.And("id = " + q.Arg(id) + "::uuid")
		visible(q, sc)
		mutable(q, sc)
		tag, err := tx.Exec(ctx, "DELETE FROM activities"+q.Where(), q.Args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return platform.ErrForbidden
		}
		return audit.Write(ctx, tx, sc.UserID, "activity.deleted", "activity", id, nil, nil)
	})
}

func Register(mux *http.ServeMux, protect func(http.Handler) http.Handler, codec pagination.Codec, log *slog.Logger, svc *Service) {
	crud.Register(mux, protect, codec, log, "/api/v1/activities", crud.Ops[Activity, Input]{
		List: svc.List, Get: svc.Get, Create: svc.Create, Update: svc.Update, Delete: svc.Delete,
	})
}
