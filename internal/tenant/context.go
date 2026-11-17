// Package tenant carries the verified tenant id through a request context.
package tenant

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// ErrNoTenant means no valid tenant is bound to the context. Callers must
// fail the request: missing context is an error, never "see everything".
var ErrNoTenant = errors.New("tenant: no valid tenant in context")

type ctxKey struct{}

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Valid reports whether s is a canonical-form UUID (any case).
func Valid(s string) bool { return uuidRE.MatchString(strings.ToLower(s)) }

// With binds a tenant id to ctx. It does not validate; From does, so an
// invalid value can never reach the database.
func With(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// From returns the tenant id, or ErrNoTenant when absent or not a UUID.
func From(ctx context.Context) (string, error) {
	id, ok := ctx.Value(ctxKey{}).(string)
	if !ok || !Valid(id) {
		return "", ErrNoTenant
	}
	return strings.ToLower(id), nil
}
