package lead

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"

	"ratify/internal/account"
	"ratify/internal/audit"
	"ratify/internal/authz"
	"ratify/internal/consent"
	"ratify/internal/contact"
	"ratify/internal/crud"
	"ratify/internal/db"
	"ratify/internal/opportunity"
	"ratify/internal/outbox"
	"ratify/internal/pagination"
	"ratify/internal/platform"
)

var jsonMarshal = json.Marshal

type Service struct {
	tx       *db.TenantTx
	accounts *account.Service
	contacts *contact.Service
	opps     *opportunity.Service
}

func NewService(tx *db.TenantTx, a *account.Service, c *contact.Service, o *opportunity.Service) *Service {
	return &Service{tx: tx, accounts: a, contacts: c, opps: o}
}

func (s *Service) List(ctx context.Context, r *http.Request, after *pagination.Position, limit int) (items []Lead, next *pagination.Position, err error) {
	if _, err = authz.Scope(ctx); err != nil {
		return nil, nil, err
	}
	f := Filter{Source: r.URL.Query().Get("source"), PossibleDuplicate: r.URL.Query().Get("possible_duplicate") == "true"}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) (err error) {
		items, next, err = list(ctx, tx, f, after, limit)
		return
	})
	return
}

// Get returns the lead plus the consent decision (the UI explains a disabled
// Convert button with can_contact.reason).
func (s *Service) Get(ctx context.Context, id string) (l Lead, err error) {
	if _, err = authz.Scope(ctx); err != nil {
		return l, err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		if l, err = get(ctx, tx, id, false); err != nil {
			return err
		}
		d, err := consent.CanContact(ctx, tx, l.Email)
		l.CanContact = &d
		return err
	})
	return
}

// Delete soft-deletes (leads are never hard-deleted).
func (s *Service) Delete(ctx context.Context, id string) error {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return err
	}
	if err := sc.RequireWrite(); err != nil {
		return err
	}
	return s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE leads SET deleted_at = now() WHERE id = $1::uuid AND deleted_at IS NULL`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return platform.ErrNotFound
		}
		return audit.Write(ctx, tx, sc.UserID, "lead.deleted", "lead", id, nil, nil)
	})
}

type ConvertInput struct {
	AccountName     string `json:"account_name"`
	OpportunityName string `json:"opportunity_name"`
	StageID         string `json:"stage_id"`
	AmountCents     int64  `json:"amount_cents"`
}

type ConvertResult struct {
	LeadID        string `json:"lead_id"`
	AccountID     string `json:"account_id"`
	ContactID     string `json:"contact_id"`
	OpportunityID string `json:"opportunity_id"`
}

// Convert creates an Account, Contact and Opportunity in ONE transaction.
// It requires consent.CanContact; otherwise 403 with a reason code.
func (s *Service) Convert(ctx context.Context, id string, in ConvertInput) (res ConvertResult, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return res, err
	}
	if err := sc.RequireWrite(); err != nil {
		return res, err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		l, err := get(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if l.ConvertedAt != nil {
			return platform.Conflict{Msg: "lead already converted"}
		}
		dec, err := consent.CanContact(ctx, tx, l.Email)
		if err != nil {
			return err
		}
		if !dec.Allowed {
			return platform.Coded{Status: http.StatusForbidden, Code: dec.Reason,
				Msg: "this lead cannot be converted: no valid consent basis (" + dec.Reason + ")"}
		}

		name := firstNonEmpty(in.AccountName, deref(l.Company), deref(l.Domain), consent.DomainOf(l.Email), l.Email)
		acct, err := s.accounts.CreateInTx(ctx, tx, sc, account.Input{Name: name, Domain: l.Domain})
		if err != nil {
			return err
		}
		// The contact copies the CURRENT consent state (latest ledger statement).
		var cStatus string
		var cBasis, cSource *string
		var cAt *string
		if err := tx.QueryRow(ctx, `
			SELECT status, lawful_basis, source, captured_at::text FROM consent_ledger
			 WHERE email = $1::citext AND (status <> 'none' OR lawful_basis IS NOT NULL)
			 ORDER BY captured_at DESC, id DESC LIMIT 1`, l.Email).Scan(&cStatus, &cBasis, &cSource, &cAt); err != nil && err != pgx.ErrNoRows {
			return err
		}
		if cStatus == "" {
			cStatus = "none"
		}
		em := l.Email
		ct, err := s.contacts.CreateInTx(ctx, tx, sc, contact.Input{
			FirstName: l.FirstName, LastName: l.LastName, Email: &em, AccountID: &acct.ID,
			ConsentStatus: cStatus, LawfulBasis: cBasis, ConsentSource: cSource, ConsentCapturedAt: l.ConsentCapturedAt})
		if err != nil {
			return err
		}
		if err := consent.Record(ctx, tx, consent.Entry{Email: l.Email, ContactID: &ct.ID, Status: cStatus,
			LawfulBasis: cBasis, Source: "lead_conversion"}); err != nil {
			return err
		}

		stage := in.StageID
		if stage == "" {
			err := tx.QueryRow(ctx, `
				SELECT s.id::text FROM stages s JOIN pipelines p ON p.tenant_id = s.tenant_id AND p.id = s.pipeline_id
				 WHERE p.is_default ORDER BY s.ordinal LIMIT 1`).Scan(&stage)
			if err == pgx.ErrNoRows {
				return platform.Coded{Status: http.StatusUnprocessableEntity, Code: "no_default_pipeline",
					Msg: "no default pipeline with stages exists; create one or pass stage_id"}
			}
			if err != nil {
				return err
			}
		}
		oppName := firstNonEmpty(in.OpportunityName, name+" - opportunity")
		op, err := s.opps.CreateInTx(ctx, tx, sc, opportunity.Input{Name: oppName, AccountID: acct.ID, StageID: stage, AmountCents: in.AmountCents})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE leads SET converted_at = now(), converted_account_id = $2::uuid,
			       converted_contact_id = $3::uuid, converted_opportunity_id = $4::uuid WHERE id = $1::uuid`,
			id, acct.ID, ct.ID, op.ID); err != nil {
			return err
		}
		res = ConvertResult{LeadID: id, AccountID: acct.ID, ContactID: ct.ID, OpportunityID: op.ID}
		if err := outbox.RecordEvent(ctx, tx, "lead.converted", id, map[string]any{
			"lead_id": id, "account_id": acct.ID, "contact_id": ct.ID, "opportunity_id": op.ID}); err != nil {
			return err
		}
		return audit.Write(ctx, tx, sc.UserID, "lead.converted", "lead", id, nil, res)
	})
	return
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ensure crud import is used by the handler file in this package.
var _ = crud.DefaultLimit
