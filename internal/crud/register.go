package crud

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"ratify/internal/pagination"
	"ratify/internal/platform"
)

// Ops are the per-resource operations; a nil op means the route is not served.
type Ops[T any, In any] struct {
	List   func(ctx context.Context, r *http.Request, after *pagination.Position, limit int) ([]T, *pagination.Position, error)
	Get    func(ctx context.Context, id string) (T, error)
	Create func(ctx context.Context, in In) (T, error)
	Update func(ctx context.Context, id string, body map[string]json.RawMessage) (T, error)
	Delete func(ctx context.Context, id string) error
}

// Register mounts standard CRUD routes for path (e.g. "/api/v1/contacts").
func Register[T any, In any](mux *http.ServeMux, protect func(http.Handler) http.Handler, codec pagination.Codec, log *slog.Logger, path string, ops Ops[T, In]) {
	fail := func(w http.ResponseWriter, err error) { platform.WriteErr(w, log, err) }
	if ops.List != nil {
		mux.Handle("GET "+path, protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit, after, err := ParsePage(r, codec)
			if err != nil {
				fail(w, err)
				return
			}
			items, next, err := ops.List(r.Context(), r, after, limit)
			if err != nil {
				fail(w, err)
				return
			}
			WriteList(w, r, codec, items, next)
		})))
	}
	if ops.Create != nil {
		mux.Handle("POST "+path, protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var in In
			if err := platform.DecodeJSON(w, r, &in); err != nil {
				platform.WriteError(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
			out, err := ops.Create(r.Context(), in)
			if err != nil {
				fail(w, err)
				return
			}
			platform.WriteJSON(w, http.StatusCreated, out)
		})))
	}
	if ops.Get != nil {
		mux.Handle("GET "+path+"/{id}", protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := PathID(r, "id")
			if err != nil {
				fail(w, err)
				return
			}
			out, err := ops.Get(r.Context(), id)
			if err != nil {
				fail(w, err)
				return
			}
			platform.WriteJSON(w, http.StatusOK, out)
		})))
	}
	if ops.Update != nil {
		mux.Handle("PATCH "+path+"/{id}", protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := PathID(r, "id")
			if err != nil {
				fail(w, err)
				return
			}
			body, err := DecodeBody(w, r)
			if err != nil {
				fail(w, err)
				return
			}
			out, err := ops.Update(r.Context(), id, body)
			if err != nil {
				fail(w, err)
				return
			}
			platform.WriteJSON(w, http.StatusOK, out)
		})))
	}
	if ops.Delete != nil {
		mux.Handle("DELETE "+path+"/{id}", protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := PathID(r, "id")
			if err == nil {
				err = ops.Delete(r.Context(), id)
			}
			if err != nil {
				fail(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})))
	}
}
