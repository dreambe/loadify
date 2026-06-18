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
	"github.com/jackc/pgx/v5"
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
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Protocol    string          `json:"protocol"`
	PlanJSON    json.RawMessage `json:"plan"`
	RampJSON    json.RawMessage `json:"ramp"`
	ScriptJS    string          `json:"script,omitempty"`
	Thresholds  json.RawMessage `json:"thresholds,omitempty"`
	DataJSON    json.RawMessage `json:"dataset,omitempty"`
	CreatedBy     *string         `json:"created_by,omitempty"`
	CreatorName   string          `json:"creator_name,omitempty"`
	Archived      bool            `json:"archived,omitempty"`
	BaselineRunID *string         `json:"baseline_run_id,omitempty"`
	Tags          []string        `json:"tags,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

// SetBaseline marks (or clears, with empty runID) a run as the test's baseline.
func (s *Store) SetBaseline(ctx context.Context, testID, runID string) error {
	var rid any
	if runID != "" {
		rid = runID
	}
	tag, err := s.pool.Exec(ctx, `UPDATE test_definitions SET baseline_run_id=$2 WHERE id=$1`, testID, rid)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

// ListRunsByTest returns a test's recent runs (newest first) for trend lines.
func (s *Store) ListRunsByTest(ctx context.Context, testID string, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.test_def_id, coalesce(r.name,''), r.status, r.desired_workers, r.started_at, r.ended_at, coalesce(r.summary,'{}'), r.created_by, coalesce(nullif(u.name,''), u.email, ''), coalesce(r.test_snapshot,'null'), r.created_at, coalesce(r.source,'manual')
		FROM runs r LEFT JOIN users u ON u.id = r.created_by
		WHERE r.test_def_id=$1 ORDER BY r.created_at DESC LIMIT $2`, testID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuns(rows)
}

// CreateTestDefinition inserts a test definition and returns its id.
func (s *Store) CreateTestDefinition(ctx context.Context, td *TestDefinition) (string, error) {
	var id string
	thresholds := td.Thresholds
	if len(thresholds) == 0 {
		thresholds = json.RawMessage("[]")
	}
	var dataset any
	if len(td.DataJSON) > 0 {
		dataset = td.DataJSON
	}
	tags := td.Tags
	if tags == nil {
		tags = []string{}
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO test_definitions (name, protocol, plan_json, ramp_json, script_js, thresholds_json, data_json, created_by, tags)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		td.Name, td.Protocol, td.PlanJSON, td.RampJSON, td.ScriptJS, thresholds, dataset, td.CreatedBy, tags).Scan(&id)
	return id, err
}

// UpdateTestDefinition rewrites an existing test definition's editable fields.
func (s *Store) UpdateTestDefinition(ctx context.Context, td *TestDefinition) error {
	thresholds := td.Thresholds
	if len(thresholds) == 0 {
		thresholds = json.RawMessage("[]")
	}
	var dataset any
	if len(td.DataJSON) > 0 {
		dataset = td.DataJSON
	}
	tags := td.Tags
	if tags == nil {
		tags = []string{}
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE test_definitions
		SET name=$2, protocol=$3, plan_json=$4, ramp_json=$5, script_js=$6, thresholds_json=$7, data_json=$8, tags=$9
		WHERE id=$1 AND NOT archived`,
		td.ID, td.Name, td.Protocol, td.PlanJSON, td.RampJSON, td.ScriptJS, thresholds, dataset, tags)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

// ArchiveTestDefinition soft-deletes a test definition (runs keep their
// reference) and disables any schedules that point at it.
func (s *Store) ArchiveTestDefinition(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE test_definitions SET archived=true WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	_, err = s.pool.Exec(ctx, `UPDATE schedules SET enabled=false WHERE test_def_id=$1`, id)
	return err
}

// GetTestDefinition fetches a test definition by id.
func (s *Store) GetTestDefinition(ctx context.Context, id string) (*TestDefinition, error) {
	td := &TestDefinition{}
	err := s.pool.QueryRow(ctx, `
		SELECT t.id, t.name, t.protocol, t.plan_json, t.ramp_json, coalesce(t.script_js,''), coalesce(t.thresholds_json,'[]'), coalesce(t.data_json,'null'), t.created_by, coalesce(nullif(u.name,''), u.email, ''), t.archived, t.baseline_run_id, coalesce(t.tags,'{}'), t.created_at
		FROM test_definitions t LEFT JOIN users u ON u.id = t.created_by
		WHERE t.id = $1`, id).
		Scan(&td.ID, &td.Name, &td.Protocol, &td.PlanJSON, &td.RampJSON, &td.ScriptJS, &td.Thresholds, &td.DataJSON, &td.CreatedBy, &td.CreatorName, &td.Archived, &td.BaselineRunID, &td.Tags, &td.CreatedAt)
	if err != nil {
		return nil, err
	}
	return td, nil
}

// ListTestDefinitions returns recent, non-archived test definitions.
func (s *Store) ListTestDefinitions(ctx context.Context, limit int) ([]TestDefinition, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.name, t.protocol, t.plan_json, t.ramp_json, coalesce(t.script_js,''), coalesce(t.thresholds_json,'[]'), coalesce(t.data_json,'null'), t.created_by, coalesce(nullif(u.name,''), u.email, ''), t.archived, t.baseline_run_id, coalesce(t.tags,'{}'), t.created_at
		FROM test_definitions t LEFT JOIN users u ON u.id = t.created_by
		WHERE NOT t.archived
		ORDER BY t.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TestDefinition{}
	for rows.Next() {
		var td TestDefinition
		if err := rows.Scan(&td.ID, &td.Name, &td.Protocol, &td.PlanJSON, &td.RampJSON, &td.ScriptJS, &td.Thresholds, &td.DataJSON, &td.CreatedBy, &td.CreatorName, &td.Archived, &td.BaselineRunID, &td.Tags, &td.CreatedAt); err != nil {
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
	Name           string          `json:"name"`
	Status         string          `json:"status"`
	DesiredWorkers int             `json:"desired_workers"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	EndedAt        *time.Time      `json:"ended_at,omitempty"`
	Summary        json.RawMessage `json:"summary,omitempty"`
	CreatedBy      *string         `json:"created_by,omitempty"`
	CreatorName    string          `json:"creator_name,omitempty"`
	Snapshot       json.RawMessage `json:"test_snapshot,omitempty"`
	Source         string          `json:"source,omitempty"` // manual | schedule
	CreatedAt      time.Time       `json:"created_at"`
}

// CreateRun inserts a pending run. createdBy is the accountable user (for a
// scheduled run, the schedule's creator); source records how it was triggered
// ("manual" or "schedule") independently of who owns it.
func (s *Store) CreateRun(ctx context.Context, testDefID string, desiredWorkers int, name string, createdBy *string, source string, snapshot json.RawMessage) (string, error) {
	var id string
	var snap any
	if len(snapshot) > 0 {
		snap = snapshot
	}
	if source == "" {
		source = "manual"
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO runs (test_def_id, status, desired_workers, name, created_by, source, test_snapshot)
		VALUES ($1, 'pending', $2, $3, $4, $5, $6) RETURNING id`, testDefID, desiredWorkers, name, createdBy, source, snap).Scan(&id)
	return id, err
}

// SetRunRunning marks a run running with its start time (only from a
// non-terminal, not-already-running state so started_at isn't reset).
func (s *Store) SetRunRunning(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE runs SET status='running', started_at=now()
		WHERE id=$1 AND status IN ('pending','queued')`, id)
	return err
}

// SetRunStatus sets a run's status (used for the queued state).
func (s *Store) SetRunStatus(ctx context.Context, id, status string) error {
	_, err := s.pool.Exec(ctx, `UPDATE runs SET status=$2 WHERE id=$1`, id, status)
	return err
}

// FinishRun marks a run terminal with a summary. It is idempotent: a run that
// is already terminal is left untouched, so the watcher and the reaper can race
// safely. Returns true when this call performed the transition.
func (s *Store) FinishRun(ctx context.Context, id, status string, summary json.RawMessage) (bool, error) {
	// Clear the dispatch payload: a terminal run is never replayed.
	tag, err := s.pool.Exec(ctx, `
		UPDATE runs SET status=$2, ended_at=now(), summary=$3, dispatch_payload=NULL
		WHERE id=$1 AND status NOT IN ('completed','failed','aborted')`, id, status, summary)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// SetRunDispatch stores the marshaled StartRun payload so a restarted
// coordinator's in-memory queue can be replayed from Postgres.
func (s *Store) SetRunDispatch(ctx context.Context, id string, payload []byte) error {
	_, err := s.pool.Exec(ctx, `UPDATE runs SET dispatch_payload=$2 WHERE id=$1`, id, payload)
	return err
}

// GetRunDispatch returns the stored StartRun payload for a run, or nil when none
// is stored (already dispatched-and-finalized, or an older run).
func (s *Store) GetRunDispatch(ctx context.Context, id string) ([]byte, error) {
	var payload []byte
	err := s.pool.QueryRow(ctx, `SELECT dispatch_payload FROM runs WHERE id=$1`, id).Scan(&payload)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

// ListActiveRuns returns runs that are still pending or running, oldest first,
// so a reaper can reconcile orphans left by an apisrv restart.
func (s *Store) ListActiveRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.test_def_id, coalesce(r.name,''), r.status, r.desired_workers, r.started_at, r.ended_at, coalesce(r.summary,'{}'), r.created_by, coalesce(nullif(u.name,''), u.email, ''), coalesce(r.test_snapshot,'null'), r.created_at, coalesce(r.source,'manual')
		FROM runs r LEFT JOIN users u ON u.id = r.created_by
		WHERE r.status IN ('pending','queued','running') ORDER BY r.created_at ASC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuns(rows)
}

// GetRun fetches a run.
func (s *Store) GetRun(ctx context.Context, id string) (*Run, error) {
	r := &Run{}
	err := s.pool.QueryRow(ctx, `
		SELECT r.id, r.test_def_id, coalesce(r.name,''), r.status, r.desired_workers, r.started_at, r.ended_at, coalesce(r.summary,'{}'), r.created_by, coalesce(nullif(u.name,''), u.email, ''), coalesce(r.test_snapshot,'null'), r.created_at, coalesce(r.source,'manual')
		FROM runs r LEFT JOIN users u ON u.id = r.created_by
		WHERE r.id=$1`, id).
		Scan(&r.ID, &r.TestDefID, &r.Name, &r.Status, &r.DesiredWorkers, &r.StartedAt, &r.EndedAt, &r.Summary, &r.CreatedBy, &r.CreatorName, &r.Snapshot, &r.CreatedAt, &r.Source)
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
		SELECT r.id, r.test_def_id, coalesce(r.name,''), r.status, r.desired_workers, r.started_at, r.ended_at, coalesce(r.summary,'{}'), r.created_by, coalesce(nullif(u.name,''), u.email, ''), coalesce(r.test_snapshot,'null'), r.created_at, coalesce(r.source,'manual')
		FROM runs r LEFT JOIN users u ON u.id = r.created_by
		ORDER BY r.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuns(rows)
}

func scanRuns(rows pgx.Rows) ([]Run, error) {
	out := []Run{}
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.TestDefID, &r.Name, &r.Status, &r.DesiredWorkers, &r.StartedAt, &r.EndedAt, &r.Summary, &r.CreatedBy, &r.CreatorName, &r.Snapshot, &r.CreatedAt, &r.Source); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
