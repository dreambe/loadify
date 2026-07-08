package apisrv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dreambe/loadify/internal/auth"
	"github.com/dreambe/loadify/internal/store"
	"github.com/dreambe/loadify/internal/store/postgres"
)

// capSeriesStore records the time window QuerySeries is called with.
type capSeriesStore struct {
	from, to time.Time
}

func (c *capSeriesStore) Summary(context.Context, string) (store.SeriesPoint, int64, error) {
	return store.SeriesPoint{}, 0, nil
}
func (c *capSeriesStore) QuerySeries(_ context.Context, _, _ string, from, to time.Time, _ int) ([]store.SeriesPoint, error) {
	c.from, c.to = from, to
	return []store.SeriesPoint{{RPS: 1}}, nil
}
func (c *capSeriesStore) QuerySamples(context.Context, string, store.SampleFilter) ([]store.Sample, error) {
	return nil, nil
}
func (c *capSeriesStore) DeleteRun(context.Context, string) error { return nil }

// TestRunSeriesUsesRunWindow guards the regression where a finished run older
// than 24h showed blank charts: the series window must come from the run, not a
// fixed last-24h window.
func TestRunSeriesUsesRunWindow(t *testing.T) {
	started := time.Now().Add(-72 * time.Hour)
	ended := started.Add(2 * time.Minute)
	meta := newFakeMeta()
	meta.runOverride = &postgres.Run{ID: "run-old", Status: "completed", CreatedAt: started, StartedAt: &started, EndedAt: &ended}
	cap := &capSeriesStore{}
	srv := New(Config{Postgres: meta, ClickHouse: cap, Coordinator: &fakeCoord{}, JWTSecret: "test-secret"})

	req := httptest.NewRequest("GET", "/api/v1/runs/run-old/series?group=*&res=1", nil)
	req.Header.Set("Authorization", "Bearer "+token(t, auth.RoleViewer))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() == "[]\n" || rr.Body.String() == "[]" {
		t.Fatalf("series empty for an old run (window regression): %s", rr.Body.String())
	}
	// The query window must straddle the run (72h ago), not the last 24h.
	if cap.from.After(time.Now().Add(-48 * time.Hour)) {
		t.Errorf("series 'from' = %v is within the last 48h; expected ~run start (72h ago) — fixed-24h window regression", cap.from)
	}
}
