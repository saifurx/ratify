// Package outbox implements the transactional outbox: handlers append rows
// in their own transaction; the relay publishes them to Kafka. Handlers
// never talk to Kafka.
package outbox

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"ratify/internal/db"
	"ratify/internal/tenant"
)

// ErrNoTenant is returned when the context carries no tenant.
var ErrNoTenant = tenant.ErrNoTenant

func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// Append inserts an outbox row inside the caller's transaction and returns
// the event id (= outbox.id, carried in the Kafka event_id header). The
// event exists if and only if the caller's transaction commits.
func Append(ctx context.Context, tx db.Tx, topic, key string, payload any) (string, error) {
	tid, err := tenant.From(ctx)
	if err != nil {
		return "", ErrNoTenant
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("outbox: marshal payload: %w", err)
	}
	id, err := newUUID()
	if err != nil {
		return "", err
	}
	headers, _ := json.Marshal(map[string]string{
		"event_id":    id,
		"tenant_id":   tid,
		"event_type":  topic,
		"occurred_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	var k *string
	if key != "" {
		k = &key
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (id, tenant_id, topic, key, payload, headers)
		 VALUES ($1::uuid, current_tenant(), $2, $3, $4::jsonb, $5::jsonb)`,
		id, topic, k, string(body), string(headers))
	if err != nil {
		return "", fmt.Errorf("outbox: insert: %w", err)
	}
	return id, nil
}

// RecordEvent appends a domain event ("contact.created" becomes topic
// "ratify.contact.created") inside the caller's transaction. Payloads carry
// ids and non-PII facts only.
func RecordEvent(ctx context.Context, tx db.Tx, eventType, aggregateID string, payload any) error {
	_, err := Append(ctx, tx, "ratify."+eventType, aggregateID, payload)
	return err
}
