package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"ratify/internal/tenant"
)

// ErrNoTenant is returned when WithTenant is called without tenant context.
var ErrNoTenant = tenant.ErrNoTenant

// Tx is the transaction handed to repositories.
type Tx = pgx.Tx

// TenantTx is the ONLY path to the database for request handling.
type TenantTx struct{ pool *Pool }

func NewTenantTx(p *Pool) *TenantTx { return &TenantTx{pool: p} }

// WithTenant runs fn in a transaction bound to the context's tenant via
// set_tenant(), which uses set_config(..., true): transaction-scoped, so the
// binding disappears at commit/rollback and cannot leak across pooled
// connections.
func (t *TenantTx) WithTenant(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error {
	id, err := tenant.From(ctx)
	if err != nil {
		return ErrNoTenant
	}
	tx, err := t.pool.p.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after commit
	if _, err := tx.Exec(ctx, "SELECT set_tenant($1::uuid)", id); err != nil {
		return fmt.Errorf("db: set_tenant: %w", err)
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}
