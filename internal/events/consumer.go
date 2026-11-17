// Package events is the reusable Kafka consumer helper. Delivery is
// at-least-once, so every handler runs behind event_id dedupe
// (processed_events) in the same transaction as the handler's own writes,
// and reads with isolation.level=read_committed.
package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/twmb/franz-go/pkg/kgo"

	"ratify/internal/db"
	"ratify/internal/tenant"
)

type Event struct {
	ID         string
	TenantID   string
	Type       string
	OccurredAt string
	Topic      string
	Key        string
	Payload    []byte
}

func fromRecord(r *kgo.Record) Event {
	h := map[string]string{}
	for _, x := range r.Headers {
		h[x.Key] = string(x.Value)
	}
	return Event{ID: h["event_id"], TenantID: h["tenant_id"], Type: h["event_type"],
		OccurredAt: h["occurred_at"], Topic: r.Topic, Key: string(r.Key), Payload: r.Value}
}

// Handler runs inside the tenant transaction that also records the dedupe
// row; if it returns an error everything rolls back and the offset is not
// committed.
type Handler func(ctx context.Context, tx db.Tx, ev Event) error

type Config struct {
	Brokers    []string
	Group      string
	Consumer   string // dedupe namespace; defaults to Group
	Topics     []string
	StartAtEnd bool // new groups start at the log end instead of the beginning
	Log        *slog.Logger
}

type Consumer struct {
	cfg        Config
	cl         *kgo.Client
	tx         *db.TenantTx
	Processed  atomic.Int64 // handled for the first time
	Duplicates atomic.Int64 // skipped by event_id dedupe
	Malformed  atomic.Int64 // missing event_id/tenant_id headers (skipped)
}

func NewConsumer(pool *db.Pool, cfg Config) (*Consumer, error) {
	if cfg.Consumer == "" {
		cfg.Consumer = cfg.Group
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	start := kgo.NewOffset().AtStart()
	if cfg.StartAtEnd {
		start = kgo.NewOffset().AtEnd()
	}
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ConsumerGroup(cfg.Group),
		kgo.ConsumeTopics(cfg.Topics...),
		kgo.ConsumeResetOffset(start),
		kgo.FetchIsolationLevel(kgo.ReadCommitted()),
		kgo.DisableAutoCommit(),
		kgo.AllowAutoTopicCreation(),
	)
	if err != nil {
		return nil, fmt.Errorf("events: kafka client: %w", err)
	}
	return &Consumer{cfg: cfg, cl: cl, tx: db.NewTenantTx(pool)}, nil
}

func (c *Consumer) Close() { c.cl.Close() }

// Run consumes until ctx is cancelled (returns nil) or a handler fails.
func (c *Consumer) Run(ctx context.Context, h Handler) error {
	for {
		fetches := c.cl.PollFetches(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			c.cfg.Log.Error("events_fetch_error", "err", errs[0].Err.Error())
			continue
		}
		var runErr error
		fetches.EachRecord(func(r *kgo.Record) {
			if runErr != nil {
				return
			}
			if err := c.handle(ctx, r, h); err != nil {
				runErr = err
				return
			}
			if err := c.cl.CommitRecords(ctx, r); err != nil && ctx.Err() == nil {
				runErr = fmt.Errorf("events: commit offset: %w", err)
			}
		})
		if runErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			return runErr
		}
	}
}

func (c *Consumer) handle(ctx context.Context, r *kgo.Record, h Handler) error {
	ev := fromRecord(r)
	if !tenant.Valid(ev.TenantID) || ev.ID == "" {
		c.Malformed.Add(1)
		c.cfg.Log.Error("events_malformed", "topic", r.Topic, "offset", r.Offset)
		return nil
	}
	err := c.tx.WithTenant(tenant.With(ctx, ev.TenantID), func(ctx context.Context, tx db.Tx) error {
		tag, err := tx.Exec(ctx,
			`INSERT INTO processed_events (tenant_id, consumer, event_id)
			 VALUES (current_tenant(), $1, $2::uuid) ON CONFLICT DO NOTHING`,
			c.cfg.Consumer, ev.ID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			c.Duplicates.Add(1)
			return errSkip
		}
		return h(ctx, tx, ev)
	})
	switch {
	case errors.Is(err, errSkip):
		return nil
	case err != nil:
		return err
	}
	c.Processed.Add(1)
	return nil
}

var errSkip = errors.New("duplicate event")
