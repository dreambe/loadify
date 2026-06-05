package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// User is an account row. PasswordHash is empty for Feishu-only accounts.
type User struct {
	ID           string     `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	Role         string     `json:"role"`
	PasswordHash string     `json:"-"`
	FeishuOpenID string     `json:"feishu_open_id,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
}

// ErrUserNotFound is returned when a lookup matches no row.
var ErrUserNotFound = errors.New("postgres: user not found")

const userCols = `id, email, name, role, coalesce(password_hash,''), coalesce(feishu_open_id,''), created_at, last_login_at`

func scanUser(row pgx.Row) (*User, error) {
	u := &User{}
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.PasswordHash, &u.FeishuOpenID, &u.CreatedAt, &u.LastLoginAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return u, nil
}

// CreateUser inserts a user with a (possibly empty) password hash.
func (s *Store) CreateUser(ctx context.Context, email, name, role, passwordHash string) (*User, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, role, password_hash)
		VALUES ($1,$2,$3,nullif($4,''))
		RETURNING `+userCols, email, name, role, passwordHash)
	return scanUser(row)
}

// GetUserByEmail fetches a user by email.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email=$1`, email))
}

// GetUserByID fetches a user by id.
func (s *Store) GetUserByID(ctx context.Context, id string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
}

// UpsertFeishuUser creates or updates the account mapped to a Feishu open_id,
// returning the resulting row. Existing roles are preserved; new accounts get
// the viewer role.
func (s *Store) UpsertFeishuUser(ctx context.Context, openID, email, name string) (*User, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, role, feishu_open_id)
		VALUES ($1,$2,'viewer',$3)
		ON CONFLICT (feishu_open_id) DO UPDATE
		  SET name = EXCLUDED.name, last_login_at = now()
		RETURNING `+userCols, email, name, openID)
	return scanUser(row)
}

// TouchLogin records a successful login time.
func (s *Store) TouchLogin(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET last_login_at=now() WHERE id=$1`, id)
	return err
}

// ListUsers returns up to limit users, newest first.
func (s *Store) ListUsers(ctx context.Context, limit int) ([]User, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// EnsureAdmin creates the bootstrap admin if it does not already exist. It is a
// no-op when email is empty. Returns true if a new admin was created.
func (s *Store) EnsureAdmin(ctx context.Context, email, name, passwordHash string) (bool, error) {
	if email == "" {
		return false, nil
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO users (email, name, role, password_hash)
		VALUES ($1,$2,'admin',nullif($3,''))
		ON CONFLICT (email) DO NOTHING`, email, name, passwordHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
