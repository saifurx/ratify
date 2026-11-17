package account

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"ratify/internal/db"
	"ratify/internal/platform"
)

type Handler struct {
	tx   *db.TenantTx
	repo Repository
	log  *slog.Logger
}

func NewHandler(tx *db.TenantTx, log *slog.Logger) *Handler {
	return &Handler{tx: tx, log: log}
}

func (h *Handler) Register(mux *http.ServeMux, protect func(http.Handler) http.Handler) {
	mux.Handle("GET /api/accounts", protect(http.HandlerFunc(h.list)))
	mux.Handle("POST /api/accounts", protect(http.HandlerFunc(h.create)))
}

func (h *Handler) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrNoTenant) {
		platform.WriteError(w, http.StatusForbidden, "missing tenant")
		return
	}
	h.log.Error("account request failed", "err", err.Error())
	platform.WriteError(w, http.StatusInternalServerError, "internal error")
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	var items []Account
	err := h.tx.WithTenant(r.Context(), func(ctx context.Context, tx db.Tx) (err error) {
		items, err = h.repo.List(ctx, tx)
		return
	})
	if err != nil {
		h.fail(w, err)
		return
	}
	platform.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string  `json:"name"`
		Domain *string `json:"domain"`
	}
	if err := platform.DecodeJSON(w, r, &in); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if n := len(in.Name); n == 0 || n > 200 {
		platform.WriteError(w, http.StatusBadRequest, "name must be 1-200 characters")
		return
	}
	var a Account
	err := h.tx.WithTenant(r.Context(), func(ctx context.Context, tx db.Tx) (err error) {
		a, err = h.repo.Create(ctx, tx, in.Name, in.Domain)
		return
	})
	if err != nil {
		h.fail(w, err)
		return
	}
	platform.WriteJSON(w, http.StatusCreated, a)
}
