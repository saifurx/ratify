// Package account: accounts CRUD. Queries carry no tenant predicate; RLS
// supplies it. All methods run inside TenantTx.WithTenant.
package account

import (
	"context"
	"time"

	"ratify/internal/db"
)

type Account struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name"`
	Domain    *string   `json:"domain"`
	CreatedAt time.Time `json:"created_at"`
}

type Repository struct{}

func (Repository) List(ctx context.Context, tx db.Tx) ([]Account, error) {
	rows, err := tx.Query(ctx,
		`SELECT id::text, tenant_id::text, name, domain, created_at
		   FROM accounts ORDER BY created_at DESC, id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Name, &a.Domain, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Create takes tenant_id from current_tenant(), never from client input.
func (Repository) Create(ctx context.Context, tx db.Tx, name string, domain *string) (Account, error) {
	var a Account
	err := tx.QueryRow(ctx,
		`INSERT INTO accounts (tenant_id, name, domain)
		 VALUES (current_tenant(), $1, $2)
		 RETURNING id::text, tenant_id::text, name, domain, created_at`,
		name, domain).Scan(&a.ID, &a.TenantID, &a.Name, &a.Domain, &a.CreatedAt)
	return a, err
}
