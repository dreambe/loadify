package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrEnvNotFound is returned when an environment lookup matches no row.
var ErrEnvNotFound = errors.New("postgres: environment not found")

// Environment is a user-defined named set of variables (KEY -> value) that a
// run can resolve into a test's {{KEY}} placeholders. Users define their own
// environments freely (e.g. "dev", "prod") — there are no fixed names.
type Environment struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Vars        map[string]string `json:"vars"`
	CreatedBy   *string           `json:"created_by,omitempty"`
	CreatorName string            `json:"creator_name,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
}

const envCols = `e.id, e.name, coalesce(e.vars_json,'{}'), e.created_by, coalesce(nullif(u.name,''), u.email, ''), e.created_at`

func scanEnv(row pgx.Row) (*Environment, error) {
	e := &Environment{}
	var raw []byte
	if err := row.Scan(&e.ID, &e.Name, &raw, &e.CreatedBy, &e.CreatorName, &e.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEnvNotFound
		}
		return nil, err
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &e.Vars)
	}
	if e.Vars == nil {
		e.Vars = map[string]string{}
	}
	return e, nil
}

// CreateEnvironment inserts an environment and returns its id.
func (s *Store) CreateEnvironment(ctx context.Context, name string, vars map[string]string, createdBy *string) (string, error) {
	if vars == nil {
		vars = map[string]string{}
	}
	b, _ := json.Marshal(vars)
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO environments (name, vars_json, created_by)
		VALUES ($1,$2,$3) RETURNING id`, name, b, createdBy).Scan(&id)
	return id, err
}

// UpdateEnvironment rewrites an environment's name and variables.
func (s *Store) UpdateEnvironment(ctx context.Context, id, name string, vars map[string]string) error {
	if vars == nil {
		vars = map[string]string{}
	}
	b, _ := json.Marshal(vars)
	tag, err := s.pool.Exec(ctx, `UPDATE environments SET name=$2, vars_json=$3 WHERE id=$1`, id, name, b)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrEnvNotFound
	}
	return err
}

// DeleteEnvironment removes an environment.
func (s *Store) DeleteEnvironment(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM environments WHERE id=$1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrEnvNotFound
	}
	return err
}

// GetEnvironment fetches an environment by id.
func (s *Store) GetEnvironment(ctx context.Context, id string) (*Environment, error) {
	return scanEnv(s.pool.QueryRow(ctx, `
		SELECT `+envCols+` FROM environments e LEFT JOIN users u ON u.id = e.created_by
		WHERE e.id = $1`, id))
}

// ListEnvironments returns all environments, newest first.
func (s *Store) ListEnvironments(ctx context.Context, limit int) ([]Environment, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+envCols+` FROM environments e LEFT JOIN users u ON u.id = e.created_by
		ORDER BY e.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Environment{}
	for rows.Next() {
		e, err := scanEnv(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}
