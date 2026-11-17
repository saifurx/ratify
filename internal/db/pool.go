// Package db is the only place pgxpool is constructed or imported.
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Role is the allow-list of database identities a binary may run as.
type Role string

const (
	RoleApp   Role = "app_user"
	RoleRelay Role = "relay_user"
	// Later sprints add RoleAgentWorker, RoleGate, RoleMLWorker.
)

// Pool wraps pgxpool so nothing outside this package can touch it.
type Pool struct {
	p    *pgxpool.Pool
	role Role
}

// NewPool builds a pool for the given role. The connection's current_user
// is NOT trusted to match: call AssertNotPrivileged before serving.
func NewPool(ctx context.Context, role Role, url string) (*Pool, error) {
	switch role {
	case RoleApp, RoleRelay:
	default:
		return nil, fmt.Errorf("db: unknown role %q", role)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse url: %w", err)
	}
	// Extended protocol without a prepared-statement cache: safe behind
	// PgBouncer in transaction mode (Sprint 1.4a).
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: new pool: %w", err)
	}
	return &Pool{p: p, role: role}, nil
}

func (p *Pool) Close()                         { p.p.Close() }
func (p *Pool) Ping(ctx context.Context) error { return p.p.Ping(ctx) }

// AssertNotPrivileged is called at boot by every binary. It fails closed.
func (p *Pool) AssertNotPrivileged(ctx context.Context, expected Role) error {
	return assertNotPrivileged(ctx, p.p, expected)
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func assertNotPrivileged(ctx context.Context, q rowQuerier, expected Role) error {
	var (
		user               string
		super, bypass, own bool
	)
	err := q.QueryRow(ctx, `
		SELECT r.rolname, r.rolsuper, r.rolbypassrls,
		       EXISTS (SELECT 1
		                 FROM pg_class c
		                 JOIN pg_namespace n ON n.oid = c.relnamespace
		                WHERE c.relowner = r.oid
		                  AND c.relkind IN ('r','p','v','m')
		                  AND n.nspname NOT IN ('pg_catalog','information_schema','pg_toast'))
		  FROM pg_roles r
		 WHERE r.rolname = current_user`).Scan(&user, &super, &bypass, &own)
	if err != nil {
		return fmt.Errorf("db: privilege check failed: %w", err)
	}
	switch {
	case super:
		return fmt.Errorf("db: refusing to run as superuser %q", user)
	case user == "app_migrator":
		return errors.New("db: refusing to run as app_migrator")
	case bypass:
		return fmt.Errorf("db: refusing to run as BYPASSRLS role %q", user)
	case own:
		return fmt.Errorf("db: refusing to run as table owner %q", user)
	case user != string(expected):
		return fmt.Errorf("db: connected as %q, expected %q", user, expected)
	}
	return nil
}
