package aggregator

import (
	"sync"
	"testing"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/metrics"
	"github.com/dreambe/loadify/internal/plan"
)

// batch builds a one-second MetricBatch for `group` with count requests, of
// which errs failed, at bucket second `sec`.
func batch(runID, group string, sec, count, errs int64) *loadifyv1.MetricBatch {
	h := metrics.NewHistogram()
	for i := int64(0); i < count; i++ {
		_ = h.RecordValue(1000)
	}
	return &loadifyv1.MetricBatch{
		RunId:        runID,
		WorkerId:     "w1",
		BucketUnixMs: sec * 1000,
		Agg: []*loadifyv1.AggSlice{{
			Group: group, StatusClass: "err", Count: count, Errors: errs,
			HdrHistogram: metrics.EncodeHistogram(h),
		}},
	}
}

func enabled() plan.AutoStopConfig {
	on := true
	return plan.AutoStopConfig{Enabled: &on, ErrorRatePct: 50, WindowSec: 5, MinRequests: 20}
}

// drive ingests buckets sec 0..n and finalizes past the grace window so they
// flush into ticks and the breaker is evaluated.
func drive(a *Aggregator, runID string, seconds int, count, errs int64) {
	for sec := 0; sec < seconds; sec++ {
		a.Ingest(batch(runID, "g", int64(sec), count, errs))
	}
	// finalize() flushes buckets older than the grace window.
	a.finalize(time.Unix(int64(seconds)+10, 0))
}

func TestAutoStopTrips(t *testing.T) {
	a := New("run-trip", loadifyv1.Protocol_PROTOCOL_HTTP, nil, nil)
	var mu sync.Mutex
	var fired int
	var gotReason string
	a.SetAutoStop(enabled(), func(_ /*runID*/, reason string) {
		mu.Lock()
		fired++
		gotReason = reason
		mu.Unlock()
	})
	// 5s × 50 req, all errors → 100% > 50%, well past min_requests.
	drive(a, "run-trip", 5, 50, 50)
	mu.Lock()
	defer mu.Unlock()
	if fired != 1 {
		t.Fatalf("callback fired %d times, want exactly 1", fired)
	}
	if gotReason == "" {
		t.Error("expected a non-empty reason")
	}
}

func TestAutoStopBelowThreshold(t *testing.T) {
	a := New("run-ok", loadifyv1.Protocol_PROTOCOL_HTTP, nil, nil)
	var fired int
	a.SetAutoStop(enabled(), func(_, _ string) { fired++ })
	// 10% errors, never exceeds 50%.
	drive(a, "run-ok", 5, 100, 10)
	if fired != 0 {
		t.Errorf("callback fired %d times, want 0 (below threshold)", fired)
	}
}

func TestAutoStopMinRequests(t *testing.T) {
	a := New("run-min", loadifyv1.Protocol_PROTOCOL_HTTP, nil, nil)
	var fired int
	a.SetAutoStop(enabled(), func(_, _ string) { fired++ })
	// 100% errors but only 3 total requests over the window → below min_requests.
	drive(a, "run-min", 3, 1, 1)
	if fired != 0 {
		t.Errorf("callback fired %d times, want 0 (below min_requests)", fired)
	}
}

func TestAutoStopDisabled(t *testing.T) {
	a := New("run-off", loadifyv1.Protocol_PROTOCOL_HTTP, nil, nil)
	var fired int
	off := false
	a.SetAutoStop(plan.AutoStopConfig{Enabled: &off}, func(_, _ string) { fired++ })
	drive(a, "run-off", 5, 50, 50)
	if fired != 0 {
		t.Errorf("callback fired %d times, want 0 (disabled)", fired)
	}
}
