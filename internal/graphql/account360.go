// Package graphql is the read-only GraphQL layer: a single account-360
// query (account + contacts + open opportunities + last 20 activities)
// executed in ONE tenant transaction under the same authz scope as REST.
// The schema is flat (no recursive types), so depth is bounded by design.
package graphql

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	gql "github.com/graphql-go/graphql"

	"ratify/internal/account"
	"ratify/internal/activity"
	"ratify/internal/authz"
	"ratify/internal/contact"
	"ratify/internal/db"
	"ratify/internal/opportunity"
	"ratify/internal/platform"
)

type txKey struct{}

type Handler struct {
	schema gql.Schema
	tx     *db.TenantTx
	log    *slog.Logger
}

func str(name string) *gql.Field { return &gql.Field{Name: name, Type: gql.String} }

func obj(name string, fields gql.Fields) *gql.Object {
	return gql.NewObject(gql.ObjectConfig{Name: name, Fields: fields})
}

func New(tx *db.TenantTx, log *slog.Logger) (*Handler, error) {
	contactT := obj("Contact", gql.Fields{
		"id": {Type: gql.ID}, "firstName": {Type: gql.String}, "lastName": {Type: gql.String},
		"email": {Type: gql.String}, "title": {Type: gql.String}, "ownerId": {Type: gql.ID},
	})
	oppT := obj("Opportunity", gql.Fields{
		"id": {Type: gql.ID}, "name": {Type: gql.String}, "amountCents": {Type: gql.Float}, "currency": {Type: gql.String},
		"stageId": {Type: gql.ID}, "status": {Type: gql.String}, "closeDate": {Type: gql.String}, "ownerId": {Type: gql.ID},
	})
	actT := obj("Activity", gql.Fields{
		"id": {Type: gql.ID}, "type": {Type: gql.String}, "subject": {Type: gql.String},
		"occurredAt": {Type: gql.String}, "createdBy": {Type: gql.String},
	})
	acctT := obj("Account", gql.Fields{
		"id": {Type: gql.ID}, "name": {Type: gql.String}, "domain": {Type: gql.String}, "industry": {Type: gql.String},
		"ownerId": {Type: gql.ID}, "lastContactAt": {Type: gql.String},
		"contacts":          {Type: gql.NewList(contactT)},
		"openOpportunities": {Type: gql.NewList(oppT)},
		"recentActivities":  {Type: gql.NewList(actT)},
	})
	h := &Handler{tx: tx, log: log}
	schema, err := gql.NewSchema(gql.SchemaConfig{Query: obj("Query", gql.Fields{
		"account": {
			Type: acctT,
			Args: gql.FieldConfigArgument{"id": {Type: gql.NewNonNull(gql.ID)}},
			Resolve: func(p gql.ResolveParams) (any, error) {
				q, _ := p.Context.Value(txKey{}).(db.Tx)
				sc, err := authz.Scope(p.Context)
				if q == nil || err != nil {
					return nil, platform.ErrForbidden
				}
				id, _ := p.Args["id"].(string)
				return account360(p.Context, q, sc, id)
			},
		},
	})})
	h.schema = schema
	return h, err
}

func account360(ctx context.Context, tx db.Tx, sc authz.ScopeInfo, id string) (any, error) {
	if len(id) != 36 {
		return nil, nil
	}
	a, err := account.Repository{}.Get(ctx, tx, sc, id)
	if err != nil { // not found or out of scope: null, never an existence leak
		return nil, nil
	}
	cs, err := contact.Repository{}.ByAccount(ctx, tx, sc, id, 200)
	if err != nil {
		return nil, err
	}
	os, err := opportunity.Repository{}.OpenByAccount(ctx, tx, sc, id)
	if err != nil {
		return nil, err
	}
	as, err := activity.Repository{}.LastByAccount(ctx, tx, sc, id, 20)
	if err != nil {
		return nil, err
	}
	m := map[string]any{"id": a.ID, "name": a.Name, "domain": a.Domain, "industry": a.Industry, "ownerId": a.OwnerID}
	if a.LastContactAt != nil {
		m["lastContactAt"] = a.LastContactAt.Format("2006-01-02T15:04:05Z07:00")
	}
	contacts := []any{}
	for _, c := range cs {
		contacts = append(contacts, map[string]any{"id": c.ID, "firstName": c.FirstName, "lastName": c.LastName,
			"email": c.Email, "title": c.Title, "ownerId": c.OwnerID})
	}
	opps := []any{}
	for _, o := range os {
		opps = append(opps, map[string]any{"id": o.ID, "name": o.Name, "amountCents": float64(o.AmountCents), "currency": o.Currency,
			"stageId": o.StageID, "status": o.Status, "closeDate": o.CloseDate, "ownerId": o.OwnerID})
	}
	acts := []any{}
	for _, x := range as {
		acts = append(acts, map[string]any{"id": x.ID, "type": x.Type, "subject": x.Subject,
			"occurredAt": x.OccurredAt.Format("2006-01-02T15:04:05Z07:00"), "createdBy": x.CreatedBy})
	}
	m["contacts"], m["openOpportunities"], m["recentActivities"] = contacts, opps, acts
	return m, nil
}

func (h *Handler) Register(mux *http.ServeMux, protect func(http.Handler) http.Handler) {
	mux.Handle("POST /api/v1/graphql", protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := platform.DecodeJSON(w, r, &req); err != nil || req.Query == "" {
			platform.WriteError(w, http.StatusBadRequest, "invalid GraphQL request")
			return
		}
		var res *gql.Result
		err := h.tx.WithTenant(r.Context(), func(ctx context.Context, tx db.Tx) error {
			res = gql.Do(gql.Params{Schema: h.schema, RequestString: req.Query, VariableValues: req.Variables,
				Context: context.WithValue(ctx, txKey{}, tx)})
			return nil
		})
		if err != nil {
			platform.WriteErr(w, h.log, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})))
}
