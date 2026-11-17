package opportunity

import (
	"log/slog"
	"net/http"

	"ratify/internal/crud"
	"ratify/internal/pagination"
)

func Register(mux *http.ServeMux, protect func(http.Handler) http.Handler, codec pagination.Codec, log *slog.Logger, svc *Service) {
	crud.Register(mux, protect, codec, log, "/api/v1/opportunities", crud.Ops[Opportunity, Input]{
		List: svc.List, Get: svc.Get, Create: svc.Create, Update: svc.Update, Delete: svc.Delete,
	})
}
