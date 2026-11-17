package account

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"ratify/internal/crud"
	"ratify/internal/pagination"
	"ratify/internal/platform"
)

type Handler struct {
	svc   *Service
	codec pagination.Codec
	log   *slog.Logger
}

func NewHandler(svc *Service, codec pagination.Codec, log *slog.Logger) *Handler {
	return &Handler{svc: svc, codec: codec, log: log}
}

func (h *Handler) Register(mux *http.ServeMux, protect func(http.Handler) http.Handler) {
	f := func(fn http.HandlerFunc) http.Handler { return protect(fn) }
	mux.Handle("GET /api/v1/accounts", f(h.list))
	mux.Handle("POST /api/v1/accounts", f(h.create))
	mux.Handle("GET /api/v1/accounts/{id}", f(h.get))
	mux.Handle("PATCH /api/v1/accounts/{id}", f(h.update))
	mux.Handle("DELETE /api/v1/accounts/{id}", f(h.delete))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, after, err := crud.ParsePage(r, h.codec)
	if err != nil {
		platform.WriteErr(w, h.log, err)
		return
	}
	owner, err := crud.OptionalUUID(r, "owner_id")
	if err != nil {
		platform.WriteErr(w, h.log, err)
		return
	}
	items, next, err := h.svc.List(r.Context(), Filter{OwnerID: owner}, after, limit)
	if err != nil {
		platform.WriteErr(w, h.log, err)
		return
	}
	crud.WriteList(w, r, h.codec, items, next)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := crud.PathID(r, "id")
	if err == nil {
		var a Account
		if a, err = h.svc.Get(r.Context(), id); err == nil {
			platform.WriteJSON(w, http.StatusOK, a)
			return
		}
	}
	platform.WriteErr(w, h.log, err)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in Input
	if err := platform.DecodeJSON(w, r, &in); err != nil {
		platform.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	a, err := h.svc.Create(r.Context(), in)
	if err != nil {
		platform.WriteErr(w, h.log, err)
		return
	}
	platform.WriteJSON(w, http.StatusCreated, a)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := crud.PathID(r, "id")
	if err == nil {
		var body map[string]json.RawMessage
		if body, err = crud.DecodeBody(w, r); err == nil {
			var a Account
			if a, err = h.svc.Update(r.Context(), id, body); err == nil {
				platform.WriteJSON(w, http.StatusOK, a)
				return
			}
		}
	}
	platform.WriteErr(w, h.log, err)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := crud.PathID(r, "id")
	if err == nil {
		if err = h.svc.Delete(r.Context(), id); err == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	platform.WriteErr(w, h.log, err)
}
