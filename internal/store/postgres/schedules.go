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
	ID             string    `json:"id"`
	TestDefID      string    `json:"test_def_id"`
	IntervalMin    int       `json:"interval_minutes"`
	DesiredWorkers int       `json:"desired_workers"`
	Enabled        bool      `json:"enabled"`
	NextRunAt      time.Time `json:"next_run_at"`
	LastRunID      *string   `json:"last_run_id,omitempty"`
	CreatedBy      *string   `json:"created_by,omitempty"`
	CreatorName    string    `json:"creator_name,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// CreateSchedule inserts a schedule, first run due now.
func (s *Store) CreateSchedule(ctx context.Context, testDefID string, intervalMin, desiredWorkers int, createdBy *string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO schedules (test_def_id, interval_minutes, desired_workers, created_by)
		VALUES ($1,$2,$3,$4) RETURNING id`, testDefID, intervalMin, desiredWorkers, createdBy).Scan(&id)
	return id, err
}

// ListSchedules returns all schedules, newest first.
func (s *Store) ListSchedules(ctx context.Context, limit int) ([]Schedule, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT s.id, s.test_def_id, s.interval_minutes, s.desired_workers, s.enabled, s.next_run_at, s.last_run_id, s.created_by, coalesce(nullif(u.name,''), u.email, ''), s.created_at
		FROM schedules s LEFT JOIN users u ON u.id = s.created_by
		ORDER BY s.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		var sc Schedule
		if err := rows.Scan(&sc.ID, &sc.TestDefID, &sc.IntervalMin, &sc.DesiredWorkers, &sc.Enabled, &sc.NextRunAt, &sc.LastRunID, &sc.CreatedBy, &sc.CreatorName, &sc.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// GetSchedule fetches one schedule (including its owner) for authorization.
func (s *Store) GetSchedule(ctx context.Context, id string) (*Schedule, error) {
	var sc Schedule
	err := s.pool.QueryRow(ctx, `
		SELECT s.id, s.test_def_id, s.interval_minutes, s.desired_workers, s.enabled, s.next_run_at, s.last_run_id, s.created_by, coalesce(nullif(u.name,''), u.email, ''), s.created_at
		FROM schedules s LEFT JOIN users u ON u.id = s.created_by
		WHERE s.id=$1`, id).
		Scan(&sc.ID, &sc.TestDefID, &sc.IntervalMin, &sc.DesiredWorkers, &sc.Enabled, &sc.NextRunAt, &sc.LastRunID, &sc.CreatedBy, &sc.CreatorName, &sc.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return nil, ErrScheduleNotFound
		}
		return nil, err
	}
	return &sc, nil
}

// SetScheduleEnabled toggles a schedule.
func (s *Store) SetScheduleEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE schedules SET enabled=$2 WHERE id=$1`, id, enabled)
	return err
}

// UpdateSchedule changes a schedule's interval and worker count, re-basing the
// next run off the new interval so the change takes effect promptly.
func (s *Store) UpdateSchedule(ctx context.Context, id string, intervalMin, desiredWorkers int) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE schedules
		SET interval_minutes=$2, desired_workers=$3,
		    next_run_at = now() + ($2 || ' minutes')::interval
		WHERE id=$1`, id, intervalMin, desiredWorkers)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrScheduleNotFound
	}
	return err
}

// DeleteSchedule removes a schedule.
func (s *Store) DeleteSchedule(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM schedules WHERE id=$1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrScheduleNotFound
	}
	return err
}

// ErrScheduleNotFound is returned when a schedule id matches no row.
var ErrScheduleNotFound = errors.New("postgres: schedule not found")

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
