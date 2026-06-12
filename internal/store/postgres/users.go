package postgres

import (
	"context"
	"encoding/json"
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
	AvatarURL    string     `json:"avatar_url,omitempty"`
	WebhookURLs  []string   `json:"webhook_urls,omitempty"`
	Disabled     bool       `json:"disabled"`
	CreatedAt    time.Time  `json:"created_at"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
}

// ErrUserNotFound is returned when a lookup matches no row.
var ErrUserNotFound = errors.New("postgres: user not found")

const userCols = `id, email, name, role, coalesce(password_hash,''), coalesce(feishu_open_id,''), coalesce(avatar_url,''), coalesce(webhook_urls,'[]'), disabled, created_at, last_login_at`

func scanUser(row pgx.Row) (*User, error) {
	u := &User{}
	var webhooks []byte
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.PasswordHash, &u.FeishuOpenID, &u.AvatarURL, &webhooks, &u.Disabled, &u.CreatedAt, &u.LastLoginAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	if len(webhooks) > 0 {
		_ = json.Unmarshal(webhooks, &u.WebhookURLs)
	}
	return u, nil
}

// SetUserWebhooks replaces a user's notification webhook URLs.
func (s *Store) SetUserWebhooks(ctx context.Context, id string, urls []string) error {
	if urls == nil {
		urls = []string{}
	}
	b, _ := json.Marshal(urls)
	tag, err := s.pool.Exec(ctx, `UPDATE users SET webhook_urls=$2 WHERE id=$1`, id, b)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return err
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
func (s *Store) UpsertFeishuUser(ctx context.Context, openID, email, name, avatarURL string) (*User, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, role, feishu_open_id, avatar_url)
		VALUES ($1,$2,'viewer',$3,$4)
		ON CONFLICT (feishu_open_id) DO UPDATE
		  SET name = EXCLUDED.name, avatar_url = EXCLUDED.avatar_url, last_login_at = now()
		RETURNING `+userCols, email, name, openID, avatarURL)
	return scanUser(row)
}

// TouchLogin records a successful login time.
func (s *Store) TouchLogin(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET last_login_at=now() WHERE id=$1`, id)
	return err
}

// UpdateUserRole changes a user's role.
func (s *Store) UpdateUserRole(ctx context.Context, id, role string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET role=$2 WHERE id=$1`, id, role)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return err
}

// SetUserPassword replaces a user's password hash.
func (s *Store) SetUserPassword(ctx context.Context, id, passwordHash string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET password_hash=nullif($2,'') WHERE id=$1`, id, passwordHash)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return err
}

// SetUserDisabled toggles whether an account may sign in.
func (s *Store) SetUserDisabled(ctx context.Context, id string, disabled bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET disabled=$2 WHERE id=$1`, id, disabled)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return err
}

// DeleteUser removes an account. Owned runs/tests keep their rows (created_by
// is set NULL by the FK).
func (s *Store) DeleteUser(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
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
	out := []User{}
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
