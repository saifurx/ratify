// Command relay publishes outbox rows to Kafka as relay_user.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"ratify/internal/db"
	"ratify/internal/outbox"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err.Error())
		os.Exit(1) // fail closed
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	url, brokers := os.Getenv("DATABASE_URL"), os.Getenv("KAFKA_BROKERS")
	if url == "" || brokers == "" {
		return errMissing
	}
	id := os.Getenv("RELAY_INSTANCE_ID")
	if id == "" {
		id = "relay-1"
	}
	pool, err := db.NewPool(ctx, db.RoleRelay, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}
	if err := pool.AssertNotPrivileged(ctx, db.RoleRelay); err != nil {
		return err
	}
	r, err := outbox.NewRelay(ctx, pool, outbox.RelayConfig{
		Brokers: strings.Split(brokers, ","), TransactionalID: "ratify-outbox-" + id, Log: log,
	})
	if err != nil {
		return err
	}
	defer r.Close()
	log.Info("relay started", "instance", id)
	return r.Run(ctx)
}

type missingErr struct{}

func (missingErr) Error() string { return "DATABASE_URL and KAFKA_BROKERS are required" }

var errMissing = missingErr{}
