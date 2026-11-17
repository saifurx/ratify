package contact

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"ratify/internal/audit"
	"ratify/internal/authz"
	"ratify/internal/crud"
	"ratify/internal/db"
	"ratify/internal/outbox"
	"ratify/internal/pagination"
	"ratify/internal/platform"
)

type Service struct {
	tx   *db.TenantTx
	repo Repository
}

func NewService(tx *db.TenantTx) *Service { return &Service{tx: tx} }

func (s *Service) List(ctx context.Context, r *http.Request, after *pagination.Position, limit int) (items []Contact, next *pagination.Position, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return nil, nil, err
	}
	owner, err := crud.OptionalUUID(r, "owner_id")
	if err != nil {
		return nil, nil, err
	}
	acct, err := crud.OptionalUUID(r, "account_id")
	if err != nil {
		return nil, nil, err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) (err error) {
		items, next, err = s.repo.List(ctx, tx, sc, Filter{OwnerID: owner, AccountID: acct}, after, limit)
		return
	})
	return
}

func (s *Service) Get(ctx context.Context, id string) (c Contact, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return c, err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) (err error) {
		c, err = s.repo.Get(ctx, tx, sc, id)
		return
	})
	return
}

func (s *Service) Create(ctx context.Context, in Input) (c Contact, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return c, err
	}
	if err := sc.RequireWrite(); err != nil {
		return c, err
	}
	in.Email = trimPtr(in.Email)
	if in.Email != nil && !strings.Contains(*in.Email, "@") {
		return c, platform.Invalid{Msg: "email is not valid"}
	}
	if strings.TrimSpace(in.FirstName+in.LastName) == "" && in.Email == nil {
		return c, platform.Invalid{Msg: "a name or an email is required"}
	}
	if in.ConsentStatus == "" {
		in.ConsentStatus = "none"
	}
	if in.OwnerID == nil {
		in.OwnerID = &sc.UserID
	}
	if !sc.CanWriteOwner(*in.OwnerID) {
		return c, platform.ErrForbidden
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		if in.AccountID != nil {
			if err := sc.RequireVisible(ctx, tx, "accounts", *in.AccountID); err != nil {
				return err
			}
		}
		var err error
		if c, err = s.repo.Create(ctx, tx, in); err != nil {
			return err
		}
		if err := outbox.RecordEvent(ctx, tx, "contact.created", c.ID,
			map[string]any{"contact_id": c.ID, "account_id": c.AccountID, "owner_id": c.OwnerID}); err != nil {
			return err
		}
		return audit.Write(ctx, tx, sc.UserID, "contact.created", "contact", c.ID, nil, c)
	})
	return
}

func (s *Service) Update(ctx context.Context, id string, body map[string]json.RawMessage) (c Contact, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return c, err
	}
	if err := sc.RequireWrite(); err != nil {
		return c, err
	}
	if raw, ok := body["owner_id"]; ok {
		var owner *string
		if json.Unmarshal(raw, &owner) != nil {
			return c, platform.Invalid{Msg: "owner_id must be a UUID or null"}
		}
		if (owner == nil && !sc.All) || (owner != nil && !sc.CanWriteOwner(*owner)) {
			return c, platform.ErrForbidden
		}
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		before, err := s.repo.Get(ctx, tx, sc, id)
		if err != nil {
			return err
		}
		if raw, ok := body["account_id"]; ok {
			var acct *string
			if json.Unmarshal(raw, &acct) != nil {
				return platform.Invalid{Msg: "account_id must be a UUID or null"}
			}
			if acct != nil {
				if err := sc.RequireVisible(ctx, tx, "accounts", *acct); err != nil {
					return err
				}
			}
		}
		if c, err = s.repo.Update(ctx, tx, sc, id, body); err != nil {
			return err
		}
		fields := make([]string, 0, len(body))
		for k := range body {
			fields = append(fields, k)
		}
		sort.Strings(fields)
		if err := outbox.RecordEvent(ctx, tx, "contact.updated", id, map[string]any{"contact_id": id, "fields": fields}); err != nil {
			return err
		}
		return audit.Write(ctx, tx, sc.UserID, "contact.updated", "contact", id, before, c)
	})
	return
}

func (s *Service) Delete(ctx context.Context, id string) error {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return err
	}
	if err := sc.RequireWrite(); err != nil {
		return err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		before, err := s.repo.Get(ctx, tx, sc, id)
		if err != nil {
			return err
		}
		if err := s.repo.Delete(ctx, tx, sc, id); err != nil {
			return err
		}
		return audit.Write(ctx, tx, sc.UserID, "contact.deleted", "contact", id, before, nil)
	})
	if platform.IsFKViolation(err) {
		return platform.Conflict{Msg: "contact has related records"}
	}
	return err
}

func trimPtr(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}
