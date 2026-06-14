package executor_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/worker/executor"
	"github.com/dreambe/loadify/internal/worker/protocols"
	_ "github.com/dreambe/loadify/internal/worker/protocols/httpd"
	"github.com/dreambe/loadify/internal/worker/sampler"
)

// TestArrivalExecutorHitsTargetRate drives the open model at ~200 req/s against
// a fast local server and asserts the achieved throughput is close to target.
func TestArrivalExecutorHitsTargetRate(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p, err := plan.Parse([]byte(`{"protocol":"http","http":{"url":"` + srv.URL + `"}}`))
	if err != nil {
		t.Fatal(err)
	}
	drv, err := protocols.New(loadifyv1.Protocol_PROTOCOL_HTTP, p)
	if err != nil {
		t.Fatal(err)
	}

	const rate = 300
	smp := sampler.New("run", "worker", loadifyv1.Protocol_PROTOCOL_HTTP)
	// Short ramp to the target, then hold steady — a near-constant arrival rate.
	stages := []*loadifyv1.RampStage{
		{DurationMs: 200, TargetRps: rate},
		{DurationMs: 1800, TargetRps: rate},
	}
	ex := executor.NewArrival(executor.ArrivalConfig{Driver: drv, Ramp: executor.NewRamp(stages), Sampler: smp})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ex.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	// Analytic expectation: triangle over the 0.2s ramp + rectangle over 1.8s.
	want := int64(0.5*0.2*rate + 1.8*rate) // ~570
	got := atomic.LoadInt64(&hits)
	if got < want*7/10 || got > want*13/10 {
		t.Errorf("hits = %d, want ~%d (±30%%)", got, want)
	}
	if d := ex.Dropped(); d > want/2 {
		t.Errorf("too many dropped iterations: %d", d)
	}
	t.Logf("hits=%d target=%d dropped=%d", got, want, ex.Dropped())
}

// TestArrivalExecutorRegrowsAfterIdle drives a spike, then a quiet window long
// enough for idle workers to retire (workerIdleTimeout is 3s), then a second
// spike. If the pool failed to re-grow after shrinking, the second spike's
// throughput would collapse — so a healthy total hit count proves both the
// idle-retire and the re-grow paths work.
func TestArrivalExecutorRegrowsAfterIdle(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive; skipped in -short")
	}
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p, err := plan.Parse([]byte(`{"protocol":"http","http":{"url":"` + srv.URL + `"}}`))
	if err != nil {
		t.Fatal(err)
	}
	drv, err := protocols.New(loadifyv1.Protocol_PROTOCOL_HTTP, p)
	if err != nil {
		t.Fatal(err)
	}

	const rate = 300
	smp := sampler.New("run", "worker", loadifyv1.Protocol_PROTOCOL_HTTP)
	stages := []*loadifyv1.RampStage{
		{DurationMs: 200, TargetRps: rate}, // spike up
		{DurationMs: 500, TargetRps: rate}, // hold
		{DurationMs: 100, TargetRps: 0},    // drop to zero
		{DurationMs: 4000, TargetRps: 0},   // quiet > idle timeout: workers retire
		{DurationMs: 300, TargetRps: rate}, // spike back up (pool must re-grow)
		{DurationMs: 1000, TargetRps: rate},
	}
	ex := executor.NewArrival(executor.ArrivalConfig{Driver: drv, Ramp: executor.NewRamp(stages), Sampler: smp})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := ex.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	// First spike alone yields ~210 hits; the second spike adds ~345. A total
	// well above the first-spike ceiling proves the pool re-grew.
	got := atomic.LoadInt64(&hits)
	if got < 350 {
		t.Errorf("hits = %d; second spike did not recover (pool failed to re-grow?)", got)
	}
	t.Logf("hits=%d", got)
}
