package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ratify/internal/db"
	"ratify/internal/platform"
	"ratify/internal/ratelimit"
	"ratify/internal/server"
)

func main() {
	health := flag.Bool("healthcheck", false, "probe /healthz and exit")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if *health {
		c := http.Client{Timeout: 2 * time.Second}
		resp, err := c.Get("http://127.0.0.1:8080/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}
	if err := run(log); err != nil {
		log.Error("fatal", "err", err.Error())
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := platform.LoadConfig()
	if err != nil {
		return err
	}
	var verifier platform.TokenVerifier
	if !cfg.DevMode() {
		if verifier, err = platform.NewOIDCVerifier(ctx, cfg.OIDCIssuer, cfg.OIDCClientID); err != nil {
			return err
		}
	} else {
		log.Warn("DEV MODE: trusting X-Tenant-Id / X-User-Id headers")
	}
	auth, err := platform.NewAuth(cfg, verifier)
	if err != nil {
		return err
	}

	pool, err := db.NewPool(ctx, db.RoleApp, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.AssertNotPrivileged(ctx, db.RoleApp); err != nil {
		return err
	}

	if cfg.RedisURL == "" {
		return errors.New("REDIS_URL is required")
	}
	limiter, err := ratelimit.NewFromURL(cfg.RedisURL)
	if err != nil {
		return err
	}
	if err := limiter.Ping(ctx); err != nil { // fail closed at boot
		return err
	}
	handler, err := server.New(server.Deps{Cfg: cfg, Pool: pool, Auth: auth, Metrics: platform.NewMetrics(), Limiter: limiter, Log: log})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("listening", "addr", cfg.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
