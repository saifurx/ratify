// Package audit writes the append-only audit_log inside the caller's tx.
package audit

import (
	"context"
	"encoding/json"

	"ratify/internal/db"
)

// Write records who did what. before/after may be nil. Callers pass DTOs;
// never put secrets in them.
func Write(ctx context.Context, tx db.Tx, actor, action, subjectType, subjectID string, before, after any) error {
	var b, a *string
	enc := func(v any) *string {
		if v == nil {
			return nil
		}
		j, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		s := string(j)
		return &s
	}
	b, a = enc(before), enc(after)
	var sid *string
	if subjectID != "" {
		sid = &subjectID
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO audit_log (tenant_id, actor, action, subject_type, subject_id, before, after)
		 VALUES (current_tenant(), $1, $2, $3, $4::uuid, $5::jsonb, $6::jsonb)`,
		actor, action, subjectType, sid, b, a)
	return err
}
