// Package server wires the HTTP API; cmd/api and the integration tests both
// build it from here, so tests exercise exactly what production runs.
package server

import (
	"log/slog"
	"net/http"

	"ratify/internal/account"
	"ratify/internal/activity"
	"ratify/internal/authz"
	"ratify/internal/contact"
	"ratify/internal/db"
	"ratify/internal/directory"
	"ratify/internal/graphql"
	"ratify/internal/opportunity"
	"ratify/internal/pagination"
	"ratify/internal/pipeline"
	"ratify/internal/platform"
)

type Deps struct {
	Cfg     platform.Config
	Pool    *db.Pool
	Auth    *platform.Auth
	Metrics *platform.Metrics // optional
	Log     *slog.Logger
}

func New(d Deps) (http.Handler, error) {
	tx := db.NewTenantTx(d.Pool)
	codec := pagination.NewCodec(d.Cfg.CursorSecret)
	az := authz.Middleware(tx, d.Log)
	// protect = authenticate (tenant from the verified token) then resolve
	// the user + in-tenant scope. Every /api route sits behind it.
	protect := func(h http.Handler) http.Handler { return d.Auth.Middleware(az(h)) }

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		platform.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	if d.Metrics != nil {
		mux.Handle("GET /metrics", d.Metrics.Handler())
	}
	mux.Handle("GET /api/whoami", protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := platform.IdentityFrom(r.Context())
		sc, _ := authz.Scope(r.Context())
		platform.WriteJSON(w, http.StatusOK, map[string]string{"tenant_id": id.TenantID, "user_id": sc.UserID, "role": sc.Role})
	})))

	account.NewHandler(account.NewService(tx), codec, d.Log).Register(mux, protect)
	contact.Register(mux, protect, codec, d.Log, contact.NewService(tx))
	opportunity.Register(mux, protect, codec, d.Log, opportunity.NewService(tx))
	pipeline.Register(mux, protect, codec, d.Log, pipeline.NewService(tx))
	activity.Register(mux, protect, codec, d.Log, activity.NewService(tx))
	directory.Register(mux, protect, d.Log, directory.NewService(tx))
	gh, err := graphql.New(tx, d.Log)
	if err != nil {
		return nil, err
	}
	gh.Register(mux, protect)

	var h http.Handler = platform.RequestLogger(d.Log, mux)
	if d.Metrics != nil {
		h = d.Metrics.Instrument(h)
	}
	return h, nil
}
