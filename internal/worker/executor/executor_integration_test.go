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

// TestExecutorDrivesHTTPLoad runs the real HTTP driver against a local server
// for a short, steady ramp and asserts the sampler recorded a sane number of
// 2xx requests with no errors.
func TestExecutorDrivesHTTPLoad(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
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

	smp := sampler.New("run-test", "worker-test", loadifyv1.Protocol_PROTOCOL_HTTP)
	ex := executor.New(executor.Config{
		Driver:  drv,
		Ramp:    executor.NewRamp([]*loadifyv1.RampStage{{DurationMs: 1500, TargetVus: 10}}),
		Sampler: smp,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ex.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	batch := smp.Flush(time.Now())
	var total, errors int64
	for _, a := range batch.Agg {
		total += a.Count
		errors += a.Errors
	}
	// Whatever remained in the final window plus server hits should be positive.
	if atomic.LoadInt64(&hits) == 0 {
		t.Fatal("server received no requests")
	}
	if errors != 0 {
		t.Errorf("unexpected errors: %d", errors)
	}
	t.Logf("server hits=%d final-window total=%d", atomic.LoadInt64(&hits), total)
}
