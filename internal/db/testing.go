//go:build dbtest

package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RawPoolForTest returns an unwrapped pool (single connection, so pool reuse
// bugs are visible). Compiled only with -tags dbtest; never in the API.
func RawPoolForTest(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 1
	return pgxpool.NewWithConfig(ctx, cfg)
}
