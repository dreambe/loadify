package postgres

import (
	"context"
	"time"
)

// AuditEntry is one recorded mutating action (who did what, when, outcome).
type AuditEntry struct {
	ID       string    `json:"id"`
	TS       time.Time `json:"ts"`
	UserID   *string   `json:"user_id,omitempty"`
	UserName string    `json:"user_name"`
	Method   string    `json:"method"`
	Path     string    `json:"path"`
	Status   int       `json:"status"`
}

// WriteAudit records one audit entry (best-effort; callers ignore the error).
func (s *Store) WriteAudit(ctx context.Context, e AuditEntry) error {
	var uid any
	if e.UserID != nil && *e.UserID != "" {
		uid = *e.UserID
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_log (user_id, user_name, method, path, status)
		VALUES ($1,$2,$3,$4,$5)`, uid, e.UserName, e.Method, e.Path, e.Status)
	return err
}

// ListAudit returns recent audit entries, newest first.
func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, ts, user_id, user_name, method, path, status
		FROM audit_log ORDER BY ts DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.UserID, &e.UserName, &e.Method, &e.Path, &e.Status); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
