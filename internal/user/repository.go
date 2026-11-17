// Package user: users, teams and team membership (identity records synced
// from FerrisKey claims). Data access only; authorization lives in authz.
package user

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"ratify/internal/db"
	"ratify/internal/platform"
)

type User struct {
	platform.TenantOwned
	ID          string    `json:"id"`
	Subject     string    `json:"external_subject"`
	Email       *string   `json:"email"`
	DisplayName *string   `json:"display_name"`
	Role        string    `json:"role"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
}

const userCols = `id::text, tenant_id::text, external_subject, email::text, display_name, role, active, created_at`

func scan(r pgx.Row) (User, error) {
	var u User
	err := r.Scan(&u.ID, &u.TenantID, &u.Subject, &u.Email, &u.DisplayName, &u.Role, &u.Active, &u.CreatedAt)
	return u, err
}

type Repository struct{}

// Sync returns the user for a token subject, creating or updating it when
// the claims changed (first authenticated request upserts).
func (Repository) Sync(ctx context.Context, tx db.Tx, subject, email, name, role string) (User, error) {
	var nz = func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	u, err := scan(tx.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE external_subject = $1`, subject))
	if err == nil && u.Role == role && (nz(email) == nil || (u.Email != nil && *u.Email == email)) &&
		(nz(name) == nil || (u.DisplayName != nil && *u.DisplayName == name)) {
		return u, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return User{}, err
	}
	return scan(tx.QueryRow(ctx, `
		INSERT INTO users (tenant_id, external_subject, email, display_name, role)
		VALUES (current_tenant(), $1, $2, $3, $4)
		ON CONFLICT (tenant_id, external_subject) DO UPDATE
		   SET email = COALESCE(EXCLUDED.email, users.email),
		       display_name = COALESCE(EXCLUDED.display_name, users.display_name),
		       role = EXCLUDED.role
		RETURNING `+userCols, subject, nz(email), nz(name), role))
}

// ManagedUserIDs returns the members of teams managed by managerID.
func (Repository) ManagedUserIDs(ctx context.Context, tx db.Tx, managerID string) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT tm.user_id::text
		  FROM team_members tm
		  JOIN teams t ON t.tenant_id = tm.tenant_id AND t.id = tm.team_id
		 WHERE t.manager_user_id = $1::uuid`, managerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (Repository) List(ctx context.Context, tx db.Tx) ([]User, error) {
	rows, err := tx.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY display_name NULLS LAST, external_subject LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type Team struct {
	platform.TenantOwned
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	ManagerUserID *string   `json:"manager_user_id"`
	MemberIDs     []string  `json:"member_ids"`
	CreatedAt     time.Time `json:"created_at"`
}

const teamSel = `SELECT t.id::text, t.tenant_id::text, t.name, t.manager_user_id::text, t.created_at,
	COALESCE((SELECT array_agg(tm.user_id::text ORDER BY tm.user_id) FROM team_members tm
	           WHERE tm.tenant_id = t.tenant_id AND tm.team_id = t.id), '{}')
	FROM teams t`

func scanTeam(r pgx.Row) (Team, error) {
	var t Team
	err := r.Scan(&t.ID, &t.TenantID, &t.Name, &t.ManagerUserID, &t.CreatedAt, &t.MemberIDs)
	return t, err
}

func (Repository) CreateTeam(ctx context.Context, tx db.Tx, name string, manager *string) (Team, error) {
	var id string
	if err := tx.QueryRow(ctx,
		`INSERT INTO teams (tenant_id, name, manager_user_id) VALUES (current_tenant(), $1, $2::uuid) RETURNING id::text`,
		name, manager).Scan(&id); err != nil {
		return Team{}, err
	}
	return scanTeam(tx.QueryRow(ctx, teamSel+` WHERE t.id = $1::uuid`, id))
}

func (Repository) ListTeams(ctx context.Context, tx db.Tx) ([]Team, error) {
	rows, err := tx.Query(ctx, teamSel+` ORDER BY t.name LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Team{}
	for rows.Next() {
		t, err := scanTeam(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (Repository) AddMember(ctx context.Context, tx db.Tx, teamID, userID string) (Team, error) {
	if _, err := tx.Exec(ctx,
		`INSERT INTO team_members (tenant_id, team_id, user_id) VALUES (current_tenant(), $1::uuid, $2::uuid)
		 ON CONFLICT DO NOTHING`, teamID, userID); err != nil {
		return Team{}, err
	}
	return scanTeam(tx.QueryRow(ctx, teamSel+` WHERE t.id = $1::uuid`, teamID))
}

func (Repository) RemoveMember(ctx context.Context, tx db.Tx, teamID, userID string) error {
	tag, err := tx.Exec(ctx, `DELETE FROM team_members WHERE team_id = $1::uuid AND user_id = $2::uuid`, teamID, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return platform.ErrNotFound
	}
	return err
}
