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

	"ratify/internal/account"
	"ratify/internal/db"
	"ratify/internal/platform"
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

	metrics := platform.NewMetrics()
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		platform.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("GET /api/whoami", auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := platform.IdentityFrom(r.Context())
		platform.WriteJSON(w, http.StatusOK, map[string]string{"tenant_id": id.TenantID, "user_id": id.UserID})
	})))
	account.NewHandler(db.NewTenantTx(pool), log).Register(mux, auth.Middleware)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           metrics.Instrument(platform.RequestLogger(log, mux)),
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
