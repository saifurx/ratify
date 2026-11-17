package lead

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"ratify/internal/consent"
	"ratify/internal/db"
	"ratify/internal/outbox"
	"ratify/internal/tenant"
)

type IngestInput struct {
	Email, FirstName, LastName, Company, Domain, Source string
	Consent                                             bool
	Raw                                                 map[string]any
	UTM                                                 map[string]string
	Referrer, IP, UserAgent, SessionID                  string
}

type IngestResult int

const (
	Accepted IngestResult = iota
	Suppressed
)

var errRetry = errors.New("retry after unique violation")

// Ingest stores a public submission for tenantID. ORDER MATTERS: the
// suppression check is the first statement and happens before any write; a
// suppressed submission performs no write at all (no lead, no ledger row, no
// outbox event) and returns Suppressed, which the HTTP layer renders
// identically to Accepted.
func (s *Service) Ingest(ctx context.Context, tenantID string, in IngestInput) (res IngestResult, err error) {
	ctx = tenant.With(ctx, tenantID)
	for attempt := 0; attempt < 3; attempt++ {
		err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
			var err error
			res, err = s.ingestTx(ctx, tx, in)
			return err
		})
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" { // concurrent same-email insert: retry as a merge
			continue
		}
		return res, err
	}
	return res, errRetry
}

func (s *Service) ingestTx(ctx context.Context, tx db.Tx, in IngestInput) (IngestResult, error) {
	email := consent.Normalize(in.Email)

	sup, err := consent.IsSuppressed(ctx, tx, email) // read only, before any write
	if err != nil {
		return Accepted, err
	}
	if sup {
		return Suppressed, nil
	}

	domain := CleanDomain(in.Domain)
	if domain == "" {
		if d := consent.DomainOf(email); EmailDomainType(d) == "corporate" {
			domain = d
		}
	}
	source := in.Source
	if source == "" {
		source = "web_form"
	}
	granted := in.Consent
	var basis *string
	status := "none"
	if granted {
		b := "consent"
		basis, status = &b, "granted"
	}
	detail := map[string]any{}
	if len(in.UTM) > 0 {
		detail["utm"] = in.UTM
	}
	if in.Referrer != "" {
		detail["referrer"] = in.Referrer
	}
	if in.SessionID != "" {
		detail["session_id"] = in.SessionID
	}
	detailJSON, _ := jsonMarshal(detail)
	rawJSON, _ := jsonMarshal(in.Raw)

	var existing string
	err = tx.QueryRow(ctx, `SELECT id::text FROM leads WHERE email = $1::citext AND deleted_at IS NULL FOR UPDATE`, email).Scan(&existing)
	switch {
	case err == nil:
		return Accepted, s.merge(ctx, tx, existing, email, domain, status, basis, source, in, string(rawJSON))
	case !errors.Is(err, pgx.ErrNoRows):
		return Accepted, err
	}

	// New email. Same company domain + same name under a DIFFERENT email is
	// only FLAGGED for human review: consent belongs to an email address and
	// must never transfer between addresses.
	var dup *string
	if domain != "" && strings.TrimSpace(in.FirstName+in.LastName) != "" {
		var d string
		switch err := tx.QueryRow(ctx, `
			SELECT id::text FROM leads
			 WHERE deleted_at IS NULL AND lower(domain) = $1
			   AND lower(first_name || last_name) = lower($2)
			 ORDER BY created_at LIMIT 1`, domain, in.FirstName+in.LastName).Scan(&d); {
		case err == nil:
			dup = &d
		case !errors.Is(err, pgx.ErrNoRows):
			return Accepted, err
		}
	}
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO leads (tenant_id, email, first_name, last_name, company, domain, source, source_detail,
		                   consent_status, lawful_basis, consent_source, consent_captured_at, raw_payload, possible_duplicate_of)
		VALUES (current_tenant(), $1::citext, $2, $3, NULLIF($4,''), NULLIF($5,''), $6, $7::jsonb,
		        $8, $9, $10, CASE WHEN $8 = 'granted' THEN now() END, $11::jsonb, $12::uuid)
		RETURNING id::text`,
		email, in.FirstName, in.LastName, in.Company, domain, source, string(detailJSON),
		status, basis, source, string(rawJSON), dup).Scan(&id)
	if err != nil {
		return Accepted, err
	}
	if err := consent.Record(ctx, tx, consent.Entry{Email: email, LeadID: &id, Status: status, LawfulBasis: basis,
		Source: source, IP: in.IP, UserAgent: in.UserAgent}); err != nil {
		return Accepted, err
	}
	return Accepted, outbox.RecordEvent(ctx, tx, "lead.created", id,
		map[string]any{"lead_id": id, "source": source, "domain": nullable(domain)})
}

// merge folds a repeat submission into the existing lead: first_captured_at
// is preserved, blanks are filled, an explicit new consent supersedes, and
// the consent event is appended to the ledger.
func (s *Service) merge(ctx context.Context, tx db.Tx, id, email, domain, status string, basis *string, source string, in IngestInput, rawJSON string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE leads SET
		    first_name = CASE WHEN first_name = '' THEN $2 ELSE first_name END,
		    last_name  = CASE WHEN last_name  = '' THEN $3 ELSE last_name  END,
		    company    = COALESCE(company, NULLIF($4,'')),
		    domain     = COALESCE(domain,  NULLIF($5,'')),
		    consent_status      = CASE WHEN $6 = 'granted' THEN 'granted' ELSE consent_status END,
		    lawful_basis        = CASE WHEN $6 = 'granted' THEN 'consent' ELSE lawful_basis END,
		    consent_source      = CASE WHEN $6 = 'granted' THEN $7 ELSE consent_source END,
		    consent_captured_at = CASE WHEN $6 = 'granted' THEN now() ELSE consent_captured_at END,
		    raw_payload = raw_payload || $8::jsonb
		 WHERE id = $1::uuid`,
		id, in.FirstName, in.LastName, in.Company, domain, status, source, rawJSON); err != nil {
		return err
	}
	if err := consent.Record(ctx, tx, consent.Entry{Email: email, LeadID: &id, Status: status, LawfulBasis: basis,
		Source: source, IP: in.IP, UserAgent: in.UserAgent}); err != nil {
		return err
	}
	return outbox.RecordEvent(ctx, tx, "lead.updated", id, map[string]any{"lead_id": id})
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
