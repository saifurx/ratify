// Package consent is the single source of truth for "may we contact this
// person": consent.CanContact. Every later sprint (sequences, sender, agents,
// conversion) reuses it; do not re-implement the rule.
//
// A person may be converted to a contact and receive outreach only if their
// CURRENT state is
//
//	(consent_status = 'granted') OR
//	(lawful_basis = 'legitimate_interest' AND consent_status <> 'revoked')
//
// AND they are not suppressed AND do_not_contact = false.
//
// Current state: the most recent explicit ledger statement (granted/revoked)
// wins; a legitimate-interest basis counts only while no revoke stands.
// A ledger row with status 'none' means "no statement made" and never
// overrides anything.
package consent

import (
	"context"
	"strings"
	"time"

	"ratify/internal/db"
)

// Reason codes returned with a refusal (stable API: the UI maps them).
const (
	ReasonOK           = "ok"
	ReasonSuppressed   = "suppressed"
	ReasonDoNotContact = "do_not_contact"
	ReasonRevoked      = "consent_revoked"
	ReasonNoConsent    = "no_consent"
	ReasonNoBasis      = "insufficient_basis"
)

type Decision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

func Normalize(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// DomainOf returns the part after '@' (lowercase) or "".
func DomainOf(email string) string {
	i := strings.LastIndexByte(email, '@')
	if i < 0 || i == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[i+1:])
}

// IsSuppressed reports whether the email, or its domain, is on the
// tenant's suppression list. It only reads.
func IsSuppressed(ctx context.Context, tx db.Tx, email string) (bool, error) {
	email = Normalize(email)
	var hit bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM suppression_list
		                WHERE email = $1::citext
		                   OR (domain IS NOT NULL AND lower(domain) = $2))`,
		email, DomainOf(email)).Scan(&hit)
	return hit, err
}

// CanContact evaluates the rule above for email within the caller's tenant
// transaction. Anything unclear fails closed.
func CanContact(ctx context.Context, tx db.Tx, email string) (Decision, error) {
	email = Normalize(email)
	sup, err := IsSuppressed(ctx, tx, email)
	if err != nil {
		return Decision{}, err
	}
	if sup {
		return Decision{Reason: ReasonSuppressed}, nil
	}
	var dnc bool
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(bool_or(do_not_contact), false) FROM contacts WHERE email = $1::citext`, email).Scan(&dnc); err != nil {
		return Decision{}, err
	}
	if dnc {
		return Decision{Reason: ReasonDoNotContact}, nil
	}
	// Current state: the most recent EXPLICIT statement (granted/revoked) wins;
	// a legitimate-interest basis only counts while no explicit revoke stands.
	var explicit *string
	var legit bool
	err = tx.QueryRow(ctx, `
		SELECT (SELECT status FROM consent_ledger
		         WHERE email = $1::citext AND status IN ('granted','revoked')
		         ORDER BY captured_at DESC, id DESC LIMIT 1),
		       EXISTS (SELECT 1 FROM consent_ledger
		                WHERE email = $1::citext AND lawful_basis = 'legitimate_interest')`, email).Scan(&explicit, &legit)
	if err != nil {
		return Decision{}, err
	}
	switch {
	case explicit != nil && *explicit == "revoked":
		return Decision{Reason: ReasonRevoked}, nil
	case explicit != nil && *explicit == "granted":
		return Decision{Allowed: true, Reason: ReasonOK}, nil
	case legit:
		return Decision{Allowed: true, Reason: ReasonOK}, nil
	}
	return Decision{Reason: ReasonNoConsent}, nil
}

// Entry is one consent ledger row.
type Entry struct {
	Email, Status, Source, IP, UserAgent string
	LawfulBasis                          *string
	LeadID, ContactID                    *string
	CapturedAt                           time.Time
}

// Record appends to the ledger (append-only; keyed by email).
func Record(ctx context.Context, tx db.Tx, e Entry) error {
	if e.CapturedAt.IsZero() {
		e.CapturedAt = time.Now().UTC()
	}
	var ip, ua *string
	if e.IP != "" {
		ip = &e.IP
	}
	if e.UserAgent != "" {
		ua = &e.UserAgent
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO consent_ledger (tenant_id, email, lead_id, contact_id, status, lawful_basis, source, captured_at, ip, user_agent)
		VALUES (current_tenant(), $1::citext, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9)`,
		Normalize(e.Email), e.LeadID, e.ContactID, e.Status, e.LawfulBasis, e.Source, e.CapturedAt, ip, ua)
	return err
}
