// Package account: accounts. Queries carry no tenant predicate (RLS supplies
// it); the only extra predicate is the authz owner scope.
package account

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"ratify/internal/authz"
	"ratify/internal/crud"
	"ratify/internal/db"
	"ratify/internal/outbox"
	"ratify/internal/pagination"
	"ratify/internal/platform"
)

type Account struct {
	platform.TenantOwned
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Domain        *string    `json:"domain"`
	Industry      *string    `json:"industry"`
	EmployeeCount *int       `json:"employee_count"`
	Website       *string    `json:"website"`
	OwnerID       *string    `json:"owner_id"`
	LastContactAt *time.Time `json:"last_contact_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

const cols = `id::text, tenant_id::text, name, domain, industry, employee_count, website, owner_id::text, last_contact_at, created_at, updated_at`

func scan(r pgx.Row) (Account, error) {
	var a Account
	err := r.Scan(&a.ID, &a.TenantID, &a.Name, &a.Domain, &a.Industry, &a.EmployeeCount, &a.Website, &a.OwnerID, &a.LastContactAt, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

var updatable = map[string]string{
	"name": "text", "domain": "text", "industry": "text", "employee_count": "int",
	"website": "text", "owner_id": "uuid",
}

type Repository struct{}

type Filter struct{ OwnerID string }

func (Repository) List(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, f Filter, after *pagination.Position, limit int) ([]Account, *pagination.Position, error) {
	q := &crud.Q{}
	q.And(sc.SQL("owner_id", q.Arg))
	if f.OwnerID != "" {
		q.And("owner_id = " + q.Arg(f.OwnerID) + "::uuid")
	}
	return crud.List(ctx, tx, "SELECT "+cols+" FROM accounts", q, after, limit, scan,
		func(a Account) pagination.Position { return pagination.Position{CreatedAt: a.CreatedAt, ID: a.ID} })
}

func (Repository) Get(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, id string) (Account, error) {
	q := &crud.Q{}
	q.And("id = " + q.Arg(id) + "::uuid")
	q.And(sc.SQL("owner_id", q.Arg))
	return scan(tx.QueryRow(ctx, "SELECT "+cols+" FROM accounts"+q.Where(), q.Args...))
}

type Input struct {
	Name          string  `json:"name"`
	Domain        *string `json:"domain"`
	Industry      *string `json:"industry"`
	EmployeeCount *int    `json:"employee_count"`
	Website       *string `json:"website"`
	OwnerID       *string `json:"owner_id"`
}

// Create takes tenant_id from current_tenant(), never from client input,
// and emits ratify.account.created in the same transaction.
func (Repository) Create(ctx context.Context, tx db.Tx, in Input) (Account, error) {
	a, err := scan(tx.QueryRow(ctx,
		`INSERT INTO accounts (tenant_id, name, domain, industry, employee_count, website, owner_id)
		 VALUES (current_tenant(), $1, $2, $3, $4, $5, $6::uuid)
		 RETURNING `+cols, in.Name, in.Domain, in.Industry, in.EmployeeCount, in.Website, in.OwnerID))
	if err != nil {
		return a, err
	}
	_, err = outbox.Append(ctx, tx, "ratify.account.created", a.ID,
		map[string]any{"account_id": a.ID, "name": a.Name, "domain": a.Domain})
	return a, err
}

func (Repository) Update(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, id string, body map[string]json.RawMessage) (Account, error) {
	q := &crud.Q{}
	q.And("id = " + q.Arg(id) + "::uuid")
	q.And(sc.SQL("owner_id", q.Arg))
	sql, err := crud.BuildUpdate("accounts", cols, updatable, body, q)
	if err != nil {
		return Account{}, err
	}
	return scan(tx.QueryRow(ctx, sql, q.Args...))
}

func (Repository) Delete(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, id string) error {
	q := &crud.Q{}
	q.And("id = " + q.Arg(id) + "::uuid")
	q.And(sc.SQL("owner_id", q.Arg))
	tag, err := tx.Exec(ctx, "DELETE FROM accounts"+q.Where(), q.Args...)
	if err == nil && tag.RowsAffected() == 0 {
		return platform.ErrNotFound
	}
	return err
}
