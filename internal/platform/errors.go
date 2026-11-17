package platform

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"ratify/internal/tenant"
)

// TenantOwned is embedded in every response DTO of a tenant-owned record so
// the cross-tenant response check (Sprint 1.4b) can compare tenant_id without
// any database lookup.
type TenantOwned struct {
	TenantID string `json:"tenant_id"`
}

var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
)

// Invalid is a client error with a message safe to return.
type Invalid struct{ Msg string }

func (e Invalid) Error() string { return e.Msg }

// Conflict is a 409 with a message safe to return.
type Conflict struct{ Msg string }

func (e Conflict) Error() string { return e.Msg }

func IsFKViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23503"
}

// WriteErr maps domain and database errors to HTTP. Unknown errors are
// logged (message only) and returned as an opaque 500.
func WriteErr(w http.ResponseWriter, log *slog.Logger, err error) {
	var inv Invalid
	var con Conflict
	var pe *pgconn.PgError
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, pgx.ErrNoRows):
		WriteError(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrForbidden):
		WriteError(w, http.StatusForbidden, "forbidden")
	case errors.As(err, &inv):
		WriteError(w, http.StatusBadRequest, inv.Msg)
	case errors.As(err, &con):
		WriteError(w, http.StatusConflict, con.Msg)
	case errors.As(err, &pe) && pe.Code == "23503":
		WriteError(w, http.StatusUnprocessableEntity, "referenced record does not exist")
	case errors.As(err, &pe) && pe.Code == "23505":
		WriteError(w, http.StatusConflict, "already exists")
	case errors.As(err, &pe) && (pe.Code == "23514" || pe.Code == "22P02" || pe.Code == "22007" || pe.Code == "22001"):
		WriteError(w, http.StatusUnprocessableEntity, "invalid value")
	case errors.As(err, &pe) && pe.Code == "42501":
		WriteError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, tenant.ErrNoTenant):
		WriteError(w, http.StatusForbidden, "missing tenant")
	default:
		log.Error("request failed", "err", err.Error())
		WriteError(w, http.StatusInternalServerError, "internal error")
	}
}

