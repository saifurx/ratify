package account

import (
	"context"
	"encoding/json"
	"strings"

	"ratify/internal/audit"
	"ratify/internal/authz"
	"ratify/internal/db"
	"ratify/internal/pagination"
	"ratify/internal/platform"
)

type Service struct {
	tx   *db.TenantTx
	repo Repository
}

func NewService(tx *db.TenantTx) *Service { return &Service{tx: tx} }

func (s *Service) List(ctx context.Context, f Filter, after *pagination.Position, limit int) (items []Account, next *pagination.Position, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return nil, nil, err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) (err error) {
		items, next, err = s.repo.List(ctx, tx, sc, f, after, limit)
		return
	})
	return
}

func (s *Service) Get(ctx context.Context, id string) (a Account, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return a, err
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) (err error) {
		a, err = s.repo.Get(ctx, tx, sc, id)
		return
	})
	return
}

func (s *Service) Create(ctx context.Context, in Input) (a Account, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return a, err
	}
	if err := sc.RequireWrite(); err != nil {
		return a, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if n := len(in.Name); n == 0 || n > 200 {
		return a, platform.Invalid{Msg: "name must be 1-200 characters"}
	}
	if in.OwnerID == nil {
		in.OwnerID = &sc.UserID
	}
	if !sc.CanWriteOwner(*in.OwnerID) {
		return a, platform.ErrForbidden
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) (err error) {
		if a, err = s.repo.Create(ctx, tx, in); err != nil {
			return err
		}
		return audit.Write(ctx, tx, sc.UserID, "account.created", "account", a.ID, nil, a)
	})
	return
}

func (s *Service) Update(ctx context.Context, id string, body map[string]json.RawMessage) (a Account, err error) {
	sc, err := authz.Scope(ctx)
	if err != nil {
		return a, err
	}
	if err := sc.RequireWrite(); err != nil {
		return a, err
	}
	if raw, ok := body["owner_id"]; ok {
		var owner *string
		if json.Unmarshal(raw, &owner) != nil {
			return a, platform.Invalid{Msg: "owner_id must be a UUID or null"}
		}
		if (owner == nil && !sc.All) || (owner != nil && !sc.CanWriteOwner(*owner)) {
			return a, platform.ErrForbidden
		}
	}
	err = s.tx.WithTenant(ctx, func(ctx context.Context, tx db.Tx) error {
		before, err := s.repo.Get(ctx, tx, sc, id)
		if err != nil {
			return err
		}
		if a, err = s.repo.Update(ctx, tx, sc, id, body); err != nil {
			return err
		}
		return audit.Write(ctx, tx, sc.UserID, "account.updated", "account", id, before, a)
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
		return audit.Write(ctx, tx, sc.UserID, "account.deleted", "account", id, before, nil)
	})
	if platform.IsFKViolation(err) {
		return platform.Conflict{Msg: "account has related records"}
	}
	return err
}
