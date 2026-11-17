// Package lead: inbound lead capture, dedupe, scoring read-model and
// lead-to-opportunity conversion.
//
// Visibility: leads are not owner-scoped (an inbound lead has no owner until
// someone converts it), so every role, including service principals, may
// read them; rep/manager/admin may convert or delete; finance/legal and
// service principals are read-only.
package lead

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"ratify/internal/consent"
	"ratify/internal/crud"
	"ratify/internal/db"
	"ratify/internal/pagination"
	"ratify/internal/platform"
)

type Score struct {
	Score        int             `json:"score"`
	Reasons      json.RawMessage `json:"reasons"`
	ModelVersion string          `json:"model_version"`
	ProducedAt   time.Time       `json:"produced_at"`
	// Preliminary is true for models trained on synthetic data
	// (model_version prefix "synthetic-"): the UI must label such scores.
	Preliminary bool `json:"preliminary"`
}

type Lead struct {
	platform.TenantOwned
	ID                     string            `json:"id"`
	Email                  string            `json:"email"`
	FirstName              string            `json:"first_name"`
	LastName               string            `json:"last_name"`
	Company                *string           `json:"company"`
	Domain                 *string           `json:"domain"`
	Source                 string            `json:"source"`
	SourceDetail           json.RawMessage   `json:"source_detail"`
	ConsentStatus          string            `json:"consent_status"`
	LawfulBasis            *string           `json:"lawful_basis"`
	ConsentSource          *string           `json:"consent_source"`
	ConsentCapturedAt      *time.Time        `json:"consent_captured_at"`
	FirstCapturedAt        time.Time         `json:"first_captured_at"`
	PossibleDuplicateOf    *string           `json:"possible_duplicate_of"`
	ConvertedAt            *time.Time        `json:"converted_at"`
	ConvertedAccountID     *string           `json:"converted_account_id"`
	ConvertedContactID     *string           `json:"converted_contact_id"`
	ConvertedOpportunityID *string           `json:"converted_opportunity_id"`
	CreatedAt              time.Time         `json:"created_at"`
	UpdatedAt              time.Time         `json:"updated_at"`
	Score                  *Score            `json:"score"`
	CanContact             *consent.Decision `json:"can_contact,omitempty"` // detail only
}

// raw_payload is deliberately not part of the DTO (it can hold anything the
// submitter typed); it is stored for provenance only.
const cols = `id::text, tenant_id::text, email::text, first_name, last_name, company, domain, source, source_detail,
	consent_status, lawful_basis, consent_source, consent_captured_at, first_captured_at, possible_duplicate_of::text,
	converted_at, converted_account_id::text, converted_contact_id::text, converted_opportunity_id::text,
	created_at, updated_at,
	(SELECT row_to_json(x) FROM (SELECT score, reasons, model_version, produced_at FROM lead_scores
	   WHERE lead_id = leads.id ORDER BY produced_at DESC LIMIT 1) x)`

func scan(r pgx.Row) (Lead, error) {
	var l Lead
	var sc []byte
	err := r.Scan(&l.ID, &l.TenantID, &l.Email, &l.FirstName, &l.LastName, &l.Company, &l.Domain, &l.Source, &l.SourceDetail,
		&l.ConsentStatus, &l.LawfulBasis, &l.ConsentSource, &l.ConsentCapturedAt, &l.FirstCapturedAt, &l.PossibleDuplicateOf,
		&l.ConvertedAt, &l.ConvertedAccountID, &l.ConvertedContactID, &l.ConvertedOpportunityID,
		&l.CreatedAt, &l.UpdatedAt, &sc)
	if err != nil {
		return l, err
	}
	if len(sc) > 0 {
		var s Score
		if json.Unmarshal(sc, &s) == nil {
			s.Preliminary = len(s.ModelVersion) >= 10 && s.ModelVersion[:10] == "synthetic-"
			l.Score = &s
		}
	}
	return l, nil
}

type Filter struct {
	Source            string
	PossibleDuplicate bool
}

func list(ctx context.Context, tx db.Tx, f Filter, after *pagination.Position, limit int) ([]Lead, *pagination.Position, error) {
	q := &crud.Q{}
	q.And("deleted_at IS NULL")
	if f.Source != "" {
		q.And("source = " + q.Arg(f.Source))
	}
	if f.PossibleDuplicate {
		q.And("possible_duplicate_of IS NOT NULL")
	}
	return crud.List(ctx, tx, "SELECT "+cols+" FROM leads", q, after, limit, scan,
		func(l Lead) pagination.Position { return pagination.Position{CreatedAt: l.CreatedAt, ID: l.ID} })
}

func get(ctx context.Context, tx db.Tx, id string, lock bool) (Lead, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE OF leads"
	}
	return scan(tx.QueryRow(ctx, "SELECT "+cols+" FROM leads WHERE id = $1::uuid AND deleted_at IS NULL"+suffix, id))
}
