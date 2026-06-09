// Package postgres implements the metadata store (test definitions, runs).
// It uses pgx directly with hand-written SQL; queries are small and stable.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dreambe/loadify/internal/config"
	"github.com/dreambe/loadify/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is a PostgreSQL-backed metadata store.
type Store struct {
	pool *pgxpool.Pool
}

// Connect opens a pgx pool.
func Connect(ctx context.Context, cfg config.Postgres) (*Store, error) {
	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// Migrate applies the embedded Postgres DDL (idempotent).
func (s *Store) Migrate(ctx context.Context) error {
	stmts, err := migrations.Statements("postgres")
	if err != nil {
		return err
	}
	for _, stmt := range stmts {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("postgres: migrate: %w", err)
		}
	}
	return nil
}

// TestDefinition is a stored declarative test.
type TestDefinition struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Protocol   string          `json:"protocol"`
	PlanJSON   json.RawMessage `json:"plan"`
	RampJSON   json.RawMessage `json:"ramp"`
	ScriptJS   string          `json:"script,omitempty"`
	Thresholds json.RawMessage `json:"thresholds,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// CreateTestDefinition inserts a test definition and returns its id.
func (s *Store) CreateTestDefinition(ctx context.Context, td *TestDefinition) (string, error) {
	var id string
	thresholds := td.Thresholds
	if len(thresholds) == 0 {
		thresholds = json.RawMessage("[]")
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO test_definitions (name, protocol, plan_json, ramp_json, script_js, thresholds_json)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		td.Name, td.Protocol, td.PlanJSON, td.RampJSON, td.ScriptJS, thresholds).Scan(&id)
	return id, err
}

// GetTestDefinition fetches a test definition by id.
func (s *Store) GetTestDefinition(ctx context.Context, id string) (*TestDefinition, error) {
	td := &TestDefinition{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, protocol, plan_json, ramp_json, coalesce(script_js,''), coalesce(thresholds_json,'[]'), created_at
		FROM test_definitions WHERE id = $1`, id).
		Scan(&td.ID, &td.Name, &td.Protocol, &td.PlanJSON, &td.RampJSON, &td.ScriptJS, &td.Thresholds, &td.CreatedAt)
	if err != nil {
		return nil, err
	}
	return td, nil
}

// ListTestDefinitions returns recent test definitions.
func (s *Store) ListTestDefinitions(ctx context.Context, limit int) ([]TestDefinition, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, protocol, plan_json, ramp_json, coalesce(script_js,''), coalesce(thresholds_json,'[]'), created_at
		FROM test_definitions ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TestDefinition
	for rows.Next() {
		var td TestDefinition
		if err := rows.Scan(&td.ID, &td.Name, &td.Protocol, &td.PlanJSON, &td.RampJSON, &td.ScriptJS, &td.Thresholds, &td.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, td)
	}
	return out, rows.Err()
}

// Run is a stored test execution.
type Run struct {
	ID             string          `json:"id"`
	TestDefID      string          `json:"test_def_id"`
	Status         string          `json:"status"`
	DesiredWorkers int             `json:"desired_workers"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	EndedAt        *time.Time      `json:"ended_at,omitempty"`
	Summary        json.RawMessage `json:"summary,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

// CreateRun inserts a pending run.
func (s *Store) CreateRun(ctx context.Context, testDefID string, desiredWorkers int) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO runs (test_def_id, status, desired_workers)
		VALUES ($1, 'pending', $2) RETURNING id`, testDefID, desiredWorkers).Scan(&id)
	return id, err
}

// SetRunRunning marks a run running with its start time.
func (s *Store) SetRunRunning(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE runs SET status='running', started_at=now() WHERE id=$1`, id)
	return err
}

// FinishRun marks a run terminal with a summary.
func (s *Store) FinishRun(ctx context.Context, id, status string, summary json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `UPDATE runs SET status=$2, ended_at=now(), summary=$3 WHERE id=$1`, id, status, summary)
	return err
}

// GetRun fetches a run.
func (s *Store) GetRun(ctx context.Context, id string) (*Run, error) {
	r := &Run{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, test_def_id, status, desired_workers, started_at, ended_at, coalesce(summary,'{}'), created_at
		FROM runs WHERE id=$1`, id).
		Scan(&r.ID, &r.TestDefID, &r.Status, &r.DesiredWorkers, &r.StartedAt, &r.EndedAt, &r.Summary, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// ListRuns returns recent runs.
func (s *Store) ListRuns(ctx context.Context, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, test_def_id, status, desired_workers, started_at, ended_at, coalesce(summary,'{}'), created_at
		FROM runs ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.TestDefID, &r.Status, &r.DesiredWorkers, &r.StartedAt, &r.EndedAt, &r.Summary, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
