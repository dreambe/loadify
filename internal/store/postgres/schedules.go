package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// Schedule runs a test definition on a fixed interval.
type Schedule struct {
	ID             string     `json:"id"`
	TestDefID      string     `json:"test_def_id"`
	IntervalMin    int        `json:"interval_minutes"`
	DesiredWorkers int        `json:"desired_workers"`
	Enabled        bool       `json:"enabled"`
	NextRunAt      time.Time  `json:"next_run_at"`
	LastRunID      *string    `json:"last_run_id,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// CreateSchedule inserts a schedule, first run due now.
func (s *Store) CreateSchedule(ctx context.Context, testDefID string, intervalMin, desiredWorkers int) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO schedules (test_def_id, interval_minutes, desired_workers)
		VALUES ($1,$2,$3) RETURNING id`, testDefID, intervalMin, desiredWorkers).Scan(&id)
	return id, err
}

// ListSchedules returns all schedules, newest first.
func (s *Store) ListSchedules(ctx context.Context, limit int) ([]Schedule, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, test_def_id, interval_minutes, desired_workers, enabled, next_run_at, last_run_id, created_at
		FROM schedules ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		var sc Schedule
		if err := rows.Scan(&sc.ID, &sc.TestDefID, &sc.IntervalMin, &sc.DesiredWorkers, &sc.Enabled, &sc.NextRunAt, &sc.LastRunID, &sc.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// SetScheduleEnabled toggles a schedule.
func (s *Store) SetScheduleEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE schedules SET enabled=$2 WHERE id=$1`, id, enabled)
	return err
}

// ClaimDueSchedule atomically claims one due schedule and advances its
// next_run_at, so multiple apisrv replicas never double-fire. Returns nil when
// nothing is due.
func (s *Store) ClaimDueSchedule(ctx context.Context) (*Schedule, error) {
	var sc Schedule
	err := s.pool.QueryRow(ctx, `
		UPDATE schedules SET next_run_at = now() + (interval_minutes || ' minutes')::interval
		WHERE id = (
			SELECT id FROM schedules
			WHERE enabled AND next_run_at <= now()
			ORDER BY next_run_at ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING id, test_def_id, interval_minutes, desired_workers, enabled, next_run_at, last_run_id, created_at`).
		Scan(&sc.ID, &sc.TestDefID, &sc.IntervalMin, &sc.DesiredWorkers, &sc.Enabled, &sc.NextRunAt, &sc.LastRunID, &sc.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &sc, nil
}

// SetScheduleLastRun records the run a schedule kicked off.
func (s *Store) SetScheduleLastRun(ctx context.Context, id, runID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE schedules SET last_run_id=$2 WHERE id=$1`, id, runID)
	return err
}
