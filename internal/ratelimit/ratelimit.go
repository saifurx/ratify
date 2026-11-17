// Package ratelimit is a Redis fixed-window limiter. Keys are built only
// through TenantKey / GlobalKey so tenant data is always prefixed
// `t:{tenant_id}:` and intentionally cross-tenant data `global:` (CLAUDE.md).
package ratelimit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"ratify/internal/tenant"
)

func TenantKey(tenantID string, parts ...string) string {
	return "t:" + strings.ToLower(tenantID) + ":" + strings.Join(parts, ":")
}

func GlobalKey(parts ...string) string { return "global:" + strings.Join(parts, ":") }

type Limiter struct{ rdb *redis.Client }

func New(rdb *redis.Client) *Limiter { return &Limiter{rdb: rdb} }

func NewFromURL(url string) (*Limiter, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	return New(redis.NewClient(opt)), nil
}

func (l *Limiter) Ping(ctx context.Context) error { return l.rdb.Ping(ctx).Err() }

// Client exposes the underlying client (tests pre-warm cache keys with it).
func (l *Limiter) Client() *redis.Client { return l.rdb }

// Allow counts one hit for key in the current window and reports whether the
// caller is still within limit. A Redis error is returned as an error so the
// caller can fail closed.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	slot := time.Now().Unix() / int64(window.Seconds())
	k := fmt.Sprintf("%s:%d", key, slot)
	pipe := l.rdb.TxPipeline()
	incr := pipe.Incr(ctx, k)
	pipe.Expire(ctx, k, window+time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, err
	}
	return incr.Val() <= int64(limit), nil
}

// AllowTenant is Allow with a tenant-prefixed key; it refuses an invalid tenant id.
func (l *Limiter) AllowTenant(ctx context.Context, tenantID string, limit int, window time.Duration, parts ...string) (bool, error) {
	if !tenant.Valid(tenantID) {
		return false, tenant.ErrNoTenant
	}
	return l.Allow(ctx, TenantKey(tenantID, parts...), limit, window)
}
