package apisrv

import (
	"context"
	"encoding/json"
	"time"

	"github.com/dreambe/loadify/internal/store"
	"github.com/dreambe/loadify/internal/store/postgres"
)

// metaStore is the metadata store surface apisrv depends on. *postgres.Store
// satisfies it; tests substitute a fake. Keeping this an interface lets the
// handlers and the run reaper be unit-tested without a database.
type metaStore interface {
	CreateTestDefinition(ctx context.Context, td *postgres.TestDefinition) (string, error)
	UpdateTestDefinition(ctx context.Context, td *postgres.TestDefinition) error
	ArchiveTestDefinition(ctx context.Context, id string) error
	GetTestDefinition(ctx context.Context, id string) (*postgres.TestDefinition, error)
	ListTestDefinitions(ctx context.Context, limit int) ([]postgres.TestDefinition, error)

	CreateRun(ctx context.Context, testDefID string, desiredWorkers int, name string, createdBy *string, snapshot json.RawMessage) (string, error)
	SetRunRunning(ctx context.Context, id string) error
	SetRunStatus(ctx context.Context, id, status string) error
	FinishRun(ctx context.Context, id, status string, summary json.RawMessage) (bool, error)
	GetRun(ctx context.Context, id string) (*postgres.Run, error)
	ListRuns(ctx context.Context, limit int) ([]postgres.Run, error)
	ListActiveRuns(ctx context.Context) ([]postgres.Run, error)

	GetUserByEmail(ctx context.Context, email string) (*postgres.User, error)
	GetUserByID(ctx context.Context, id string) (*postgres.User, error)
	UpsertFeishuUser(ctx context.Context, openID, email, name, avatarURL string) (*postgres.User, error)
	TouchLogin(ctx context.Context, id string) error
	ListUsers(ctx context.Context, limit int) ([]postgres.User, error)
	CreateUser(ctx context.Context, email, name, role, passwordHash string) (*postgres.User, error)
	UpdateUserRole(ctx context.Context, id, role string) error
	SetUserPassword(ctx context.Context, id, passwordHash string) error
	SetUserDisabled(ctx context.Context, id string, disabled bool) error
	DeleteUser(ctx context.Context, id string) error

	CreateSchedule(ctx context.Context, testDefID string, intervalMin, desiredWorkers int) (string, error)
	ListSchedules(ctx context.Context, limit int) ([]postgres.Schedule, error)
	SetScheduleEnabled(ctx context.Context, id string, enabled bool) error
	ClaimDueSchedule(ctx context.Context) (*postgres.Schedule, error)
	SetScheduleLastRun(ctx context.Context, id, runID string) error
}

// metricsStore is the metrics-query surface apisrv depends on (*clickhouse.Store).
type metricsStore interface {
	Summary(ctx context.Context, runID string) (store.SeriesPoint, int64, error)
	QuerySeries(ctx context.Context, runID, group string, from, to time.Time, resSeconds int) ([]store.SeriesPoint, error)
}
