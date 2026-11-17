package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"ratify/internal/db"
)

// ErrSimulatedCrash lets tests make the relay "die" at an exact point
// without cleanup (no Kafka abort, no Postgres commit), like kill -9.
var ErrSimulatedCrash = errors.New("outbox: simulated crash")

// Hooks are fault-injection points for tests; nil in production.
type Hooks struct {
	// BeforeKafkaCommit runs after the records were sent inside the Kafka
	// transaction and before it is committed.
	BeforeKafkaCommit func() error
	// AfterKafkaCommit runs after the Kafka commit, before the Postgres commit.
	AfterKafkaCommit func() error
}

type RelayConfig struct {
	Brokers         []string
	TransactionalID string // stable per worker instance: a restart fences its predecessor
	BatchSize       int
	MaxAttempts     int
	PollInterval    time.Duration
	BackoffBase     time.Duration
	BackoffMax      time.Duration
	Log             *slog.Logger
	Hooks           Hooks
}

func (c *RelayConfig) defaults() {
	if c.BatchSize == 0 {
		c.BatchSize = 100
	}
	if c.MaxAttempts == 0 {
		c.MaxAttempts = 10
	}
	if c.PollInterval == 0 {
		c.PollInterval = 200 * time.Millisecond
	}
	if c.BackoffBase == 0 {
		c.BackoffBase = time.Second
	}
	if c.BackoffMax == 0 {
		c.BackoffMax = 5 * time.Minute
	}
	if c.Log == nil {
		c.Log = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
}

// Relay publishes outbox rows to Kafka with AT-LEAST-ONCE semantics. A
// Postgres and a Kafka transaction cannot commit atomically, so a crash
// between the two commits republishes the row; consumers dedupe on event_id.
type Relay struct {
	cfg RelayConfig
	sys *db.SystemTx
	kc  *kgo.Client
}

// NewRelay connects to Kafka and fails if it is unreachable (fail closed).
func NewRelay(ctx context.Context, pool *db.Pool, cfg RelayConfig) (*Relay, error) {
	cfg.defaults()
	if cfg.TransactionalID == "" {
		return nil, errors.New("outbox: TransactionalID is required")
	}
	sys, err := db.NewSystemTx(pool)
	if err != nil {
		return nil, err
	}
	kc, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.TransactionalID(cfg.TransactionalID), // implies idempotent producer
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.AllowAutoTopicCreation(),
		kgo.RecordDeliveryTimeout(20*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("outbox: kafka client: %w", err)
	}
	if err := kc.Ping(ctx); err != nil {
		kc.Close()
		return nil, fmt.Errorf("outbox: kafka unreachable: %w", err)
	}
	return &Relay{cfg: cfg, sys: sys, kc: kc}, nil
}

// Close closes the Kafka client. It does NOT abort an open Kafka
// transaction: that is left to the next instance with the same
// transactional.id, which fences and aborts it.
func (r *Relay) Close() { r.kc.Close() }

// Run loops until ctx is cancelled (returns nil) or a simulated crash.
func (r *Relay) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		n, err := r.once(ctx)
		switch {
		case errors.Is(err, ErrSimulatedCrash):
			return err
		case err != nil:
			if ctx.Err() != nil {
				return nil
			}
			r.cfg.Log.Error("outbox_relay_error", "err", err.Error())
			sleep(ctx, time.Second)
		case n == 0:
			sleep(ctx, r.cfg.PollInterval)
		}
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

type row struct {
	id       string
	topic    string
	key      *string
	payload  []byte
	headers  map[string]string
	attempts int
}

// once publishes one batch. Rows are locked FOR UPDATE SKIP LOCKED in a
// Postgres tx; dispatched_at is set in that same tx after the Kafka commit.
func (r *Relay) once(ctx context.Context) (int, error) {
	n := 0
	err := r.sys.WithTx(ctx, func(ctx context.Context, tx db.Tx) error {
		rows, err := r.fetch(ctx, tx)
		if err != nil {
			return err
		}
		n = len(rows)
		if n == 0 {
			return nil
		}
		recs := make([]*kgo.Record, n)
		for i, rw := range rows {
			rec := &kgo.Record{Topic: rw.topic, Value: rw.payload}
			if rw.key != nil {
				rec.Key = []byte(*rw.key)
			}
			for k, v := range rw.headers {
				rec.Headers = append(rec.Headers, kgo.RecordHeader{Key: k, Value: []byte(v)})
			}
			recs[i] = rec
		}

		var done []string
		if err := r.publish(ctx, recs); err == nil {
			for _, rw := range rows {
				done = append(done, rw.id)
			}
		} else if errors.Is(err, ErrSimulatedCrash) {
			return err
		} else {
			// Distinguish an outage (retry the whole batch, no attempt is
			// charged) from a poison row (isolate it, charge its attempts).
			if perr := r.kc.Ping(ctx); perr != nil {
				return fmt.Errorf("kafka unavailable: %w", err)
			}
			for i, rw := range rows {
				perr := r.publish(ctx, recs[i:i+1])
				switch {
				case perr == nil:
					done = append(done, rw.id)
				case errors.Is(perr, ErrSimulatedCrash):
					return perr
				default:
					if err := r.recordFailure(ctx, tx, rw, perr); err != nil {
						return err
					}
				}
			}
		}
		if h := r.cfg.Hooks.AfterKafkaCommit; h != nil {
			if err := h(); err != nil {
				return err
			}
		}
		if len(done) > 0 {
			if _, err := tx.Exec(ctx,
				`UPDATE outbox SET dispatched_at = now() WHERE id = ANY($1::uuid[])`, done); err != nil {
				return fmt.Errorf("mark dispatched: %w", err)
			}
		}
		return nil
	})
	return n, err
}

// fetch returns pending rows in seq order. A row waits while an earlier
// pending row with the same key exists (per-key ordering). Dead-lettered
// rows are never selected and never block anything.
func (r *Relay) fetch(ctx context.Context, tx db.Tx) ([]row, error) {
	q, err := tx.Query(ctx, `
		SELECT o.id::text, o.topic, o.key, o.payload, o.headers, o.attempts
		  FROM outbox o
		 WHERE o.dispatched_at IS NULL
		   AND o.dead_lettered_at IS NULL
		   AND o.attempts < $2
		   AND o.next_attempt_at <= now()
		   AND NOT EXISTS (SELECT 1 FROM outbox p
		                    WHERE p.key = o.key AND p.seq < o.seq
		                      AND p.dispatched_at IS NULL
		                      AND p.dead_lettered_at IS NULL)
		 ORDER BY o.seq
		 LIMIT $1
		 FOR UPDATE OF o SKIP LOCKED`, r.cfg.BatchSize, r.cfg.MaxAttempts)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	var out []row
	for q.Next() {
		var rw row
		if err := q.Scan(&rw.id, &rw.topic, &rw.key, &rw.payload, &rw.headers, &rw.attempts); err != nil {
			return nil, err
		}
		out = append(out, rw)
	}
	return out, q.Err()
}

// publish sends recs in one Kafka transaction.
func (r *Relay) publish(ctx context.Context, recs []*kgo.Record) error {
	if err := r.kc.BeginTransaction(); err != nil {
		return err
	}
	if err := r.kc.ProduceSync(ctx, recs...).FirstErr(); err != nil {
		_ = r.kc.EndTransaction(context.WithoutCancel(ctx), kgo.TryAbort)
		return err
	}
	if h := r.cfg.Hooks.BeforeKafkaCommit; h != nil {
		if err := h(); err != nil {
			return err // crash: transaction deliberately left open
		}
	}
	if err := r.kc.EndTransaction(ctx, kgo.TryCommit); err != nil {
		_ = r.kc.EndTransaction(context.WithoutCancel(ctx), kgo.TryAbort)
		return err
	}
	return nil
}

func (r *Relay) recordFailure(ctx context.Context, tx db.Tx, rw row, cause error) error {
	backoff := r.cfg.BackoffBase << min(rw.attempts, 20)
	if backoff > r.cfg.BackoffMax || backoff <= 0 {
		backoff = r.cfg.BackoffMax
	}
	msg := cause.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	var attempts int
	var dead bool
	err := tx.QueryRow(ctx, `
		UPDATE outbox
		   SET attempts = attempts + 1,
		       last_error = $2,
		       next_attempt_at = now() + make_interval(secs => $3::float8),
		       dead_lettered_at = CASE WHEN attempts + 1 >= $4 THEN now() END
		 WHERE id = $1::uuid
		RETURNING attempts, dead_lettered_at IS NOT NULL`,
		rw.id, msg, backoff.Seconds(), r.cfg.MaxAttempts).Scan(&attempts, &dead)
	if err != nil {
		return fmt.Errorf("record failure: %w", err)
	}
	if dead {
		// Never log the payload (may contain PII).
		r.cfg.Log.Error("outbox_dead_letter", "id", rw.id, "topic", rw.topic, "attempts", attempts, "err", msg)
	}
	return nil
}
