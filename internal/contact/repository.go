package contact

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"ratify/internal/authz"
	"ratify/internal/crud"
	"ratify/internal/db"
	"ratify/internal/pagination"
	"ratify/internal/platform"
)

type Contact struct {
	platform.TenantOwned
	ID                string     `json:"id"`
	FirstName         string     `json:"first_name"`
	LastName          string     `json:"last_name"`
	Email             *string    `json:"email"`
	Phone             *string    `json:"phone"`
	Title             *string    `json:"title"`
	AccountID         *string    `json:"account_id"`
	OwnerID           *string    `json:"owner_id"`
	DoNotContact      bool       `json:"do_not_contact"`
	ConsentStatus     string     `json:"consent_status"`
	LawfulBasis       *string    `json:"lawful_basis"`
	ConsentSource     *string    `json:"consent_source"`
	ConsentCapturedAt *time.Time `json:"consent_captured_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

const cols = `id::text, tenant_id::text, first_name, last_name, email::text, phone, title, account_id::text, owner_id::text,
	do_not_contact, consent_status, lawful_basis, consent_source, consent_captured_at, created_at, updated_at`

func scan(r pgx.Row) (Contact, error) {
	var c Contact
	err := r.Scan(&c.ID, &c.TenantID, &c.FirstName, &c.LastName, &c.Email, &c.Phone, &c.Title, &c.AccountID, &c.OwnerID,
		&c.DoNotContact, &c.ConsentStatus, &c.LawfulBasis, &c.ConsentSource, &c.ConsentCapturedAt, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

var updatable = map[string]string{
	"first_name": "text", "last_name": "text", "email": "citext", "phone": "text", "title": "text",
	"account_id": "uuid", "owner_id": "uuid", "do_not_contact": "boolean", "consent_status": "text",
	"lawful_basis": "text", "consent_source": "text", "consent_captured_at": "timestamptz",
}

type Repository struct{}

type Filter struct{ OwnerID, AccountID string }

func (Repository) List(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, f Filter, after *pagination.Position, limit int) ([]Contact, *pagination.Position, error) {
	q := &crud.Q{}
	q.And(sc.SQL("owner_id", q.Arg))
	if f.OwnerID != "" {
		q.And("owner_id = " + q.Arg(f.OwnerID) + "::uuid")
	}
	if f.AccountID != "" {
		q.And("account_id = " + q.Arg(f.AccountID) + "::uuid")
	}
	return crud.List(ctx, tx, "SELECT "+cols+" FROM contacts", q, after, limit, scan,
		func(c Contact) pagination.Position { return pagination.Position{CreatedAt: c.CreatedAt, ID: c.ID} })
}

// ByAccount returns an account's contacts within scope (used by account-360).
func (Repository) ByAccount(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, accountID string, limit int) ([]Contact, error) {
	items, _, err := Repository{}.List(ctx, tx, sc, Filter{AccountID: accountID}, nil, limit)
	return items, err
}

func (Repository) Get(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, id string) (Contact, error) {
	q := &crud.Q{}
	q.And("id = " + q.Arg(id) + "::uuid")
	q.And(sc.SQL("owner_id", q.Arg))
	return scan(tx.QueryRow(ctx, "SELECT "+cols+" FROM contacts"+q.Where(), q.Args...))
}

type Input struct {
	FirstName         string     `json:"first_name"`
	LastName          string     `json:"last_name"`
	Email             *string    `json:"email"`
	Phone             *string    `json:"phone"`
	Title             *string    `json:"title"`
	AccountID         *string    `json:"account_id"`
	OwnerID           *string    `json:"owner_id"`
	DoNotContact      bool       `json:"do_not_contact"`
	ConsentStatus     string     `json:"consent_status"`
	LawfulBasis       *string    `json:"lawful_basis"`
	ConsentSource     *string    `json:"consent_source"`
	ConsentCapturedAt *time.Time `json:"consent_captured_at"`
}

func (Repository) Create(ctx context.Context, tx db.Tx, in Input) (Contact, error) {
	return scan(tx.QueryRow(ctx, `
		INSERT INTO contacts (tenant_id, first_name, last_name, email, phone, title, account_id, owner_id,
		                      do_not_contact, consent_status, lawful_basis, consent_source, consent_captured_at)
		VALUES (current_tenant(), $1, $2, $3::citext, $4, $5, $6::uuid, $7::uuid, $8, $9, $10, $11, $12)
		RETURNING `+cols, in.FirstName, in.LastName, in.Email, in.Phone, in.Title, in.AccountID, in.OwnerID,
		in.DoNotContact, in.ConsentStatus, in.LawfulBasis, in.ConsentSource, in.ConsentCapturedAt))
}

func (Repository) Update(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, id string, body map[string]json.RawMessage) (Contact, error) {
	q := &crud.Q{}
	q.And("id = " + q.Arg(id) + "::uuid")
	q.And(sc.SQL("owner_id", q.Arg))
	sql, err := crud.BuildUpdate("contacts", cols, updatable, body, q)
	if err != nil {
		return Contact{}, err
	}
	return scan(tx.QueryRow(ctx, sql, q.Args...))
}

func (Repository) Delete(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, id string) error {
	q := &crud.Q{}
	q.And("id = " + q.Arg(id) + "::uuid")
	q.And(sc.SQL("owner_id", q.Arg))
	tag, err := tx.Exec(ctx, "DELETE FROM contacts"+q.Where(), q.Args...)
	if err == nil && tag.RowsAffected() == 0 {
		return platform.ErrNotFound
	}
	return err
}
