// Package directory: read access to users and teams, and team management.
// Users themselves are synced from token claims (internal/authz); there is
// no endpoint that creates a user or changes a role. Reps cannot list users.
package directory

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"ratify/internal/audit"
	"ratify/internal/authz"
	"ratify/internal/crud"
	"ratify/internal/db"
	"ratify/internal/platform"
	"ratify/internal/user"
)

type Service struct {
	tx   *db.TenantTx
	repo user.Repository
}

func NewService(tx *db.TenantTx) *Service { return &Service{tx: tx} }

func readScope(ctx context.Context) (authz.ScopeInfo, error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return sc, err
	}
	if sc.Role == "rep" {
		return sc, platform.ErrForbidden
	}
	return sc, nil
}

func Register(mux *http.ServeMux, protect func(http.Handler) http.Handler, log *slog.Logger, svc *Service) {
	h := func(fn func(w http.ResponseWriter, r *http.Request) (any, int, error)) http.Handler {
		return protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			out, status, err := fn(w, r)
			if err != nil {
				platform.WriteErr(w, log, err)
				return
			}
			if out == nil {
				w.WriteHeader(status)
				return
			}
			platform.WriteJSON(w, status, out)
		}))
	}
	mux.Handle("GET /api/v1/users", h(func(w http.ResponseWriter, r *http.Request) (any, int, error) {
		if _, err := readScope(r.Context()); err != nil {
			return nil, 0, err
		}
		var items []user.User
		err := svc.tx.WithTenant(r.Context(), func(ctx context.Context, tx db.Tx) (err error) {
			items, err = svc.repo.List(ctx, tx)
			return
		})
		return map[string]any{"items": items}, http.StatusOK, err
	}))
	mux.Handle("GET /api/v1/teams", h(func(w http.ResponseWriter, r *http.Request) (any, int, error) {
		if _, err := readScope(r.Context()); err != nil {
			return nil, 0, err
		}
		var items []user.Team
		err := svc.tx.WithTenant(r.Context(), func(ctx context.Context, tx db.Tx) (err error) {
			items, err = svc.repo.ListTeams(ctx, tx)
			return
		})
		return map[string]any{"items": items}, http.StatusOK, err
	}))
	mux.Handle("POST /api/v1/teams", h(func(w http.ResponseWriter, r *http.Request) (any, int, error) {
		sc, err := authz.Scope(r.Context())
		if err == nil {
			err = sc.RequireAdmin()
		}
		var in struct {
			Name          string  `json:"name"`
			ManagerUserID *string `json:"manager_user_id"`
		}
		if err == nil && platform.DecodeJSON(w, r, &in) != nil {
			err = platform.Invalid{Msg: "invalid JSON body"}
		}
		if err == nil && (len(in.Name) == 0 || len(in.Name) > 200) {
			err = platform.Invalid{Msg: "name must be 1-200 characters"}
		}
		if err != nil {
			return nil, 0, err
		}
		var t user.Team
		err = svc.tx.WithTenant(r.Context(), func(ctx context.Context, tx db.Tx) (err error) {
			if t, err = svc.repo.CreateTeam(ctx, tx, in.Name, in.ManagerUserID); err != nil {
				return err
			}
			return audit.Write(ctx, tx, sc.UserID, "team.created", "team", t.ID, nil, t)
		})
		return t, http.StatusCreated, err
	}))
	mux.Handle("POST /api/v1/teams/{id}/members", h(func(w http.ResponseWriter, r *http.Request) (any, int, error) {
		sc, err := authz.Scope(r.Context())
		if err == nil {
			err = sc.RequireAdmin()
		}
		var teamID string
		if err == nil {
			teamID, err = crud.PathID(r, "id")
		}
		var in struct {
			UserID string `json:"user_id"`
		}
		if err == nil && (platform.DecodeJSON(w, r, &in) != nil || in.UserID == "") {
			err = platform.Invalid{Msg: "user_id is required"}
		}
		if err != nil {
			return nil, 0, err
		}
		var t user.Team
		err = svc.tx.WithTenant(r.Context(), func(ctx context.Context, tx db.Tx) (err error) {
			if t, err = svc.repo.AddMember(ctx, tx, teamID, in.UserID); err != nil {
				return err
			}
			return audit.Write(ctx, tx, sc.UserID, "team.member_added", "team", teamID, nil, map[string]string{"user_id": in.UserID})
		})
		return t, http.StatusOK, err
	}))
	mux.Handle("DELETE /api/v1/teams/{id}/members/{user_id}", h(func(w http.ResponseWriter, r *http.Request) (any, int, error) {
		sc, err := authz.Scope(r.Context())
		if err == nil {
			err = sc.RequireAdmin()
		}
		var teamID, userID string
		if err == nil {
			if teamID, err = crud.PathID(r, "id"); err == nil {
				userID, err = crud.PathID(r, "user_id")
			}
		}
		if err != nil {
			return nil, 0, err
		}
		err = svc.tx.WithTenant(r.Context(), func(ctx context.Context, tx db.Tx) error {
			if err := svc.repo.RemoveMember(ctx, tx, teamID, userID); err != nil {
				return err
			}
			return audit.Write(ctx, tx, sc.UserID, "team.member_removed", "team", teamID, map[string]string{"user_id": userID}, nil)
		})
		return nil, http.StatusNoContent, err
	}))
}

var _ = json.Marshal
