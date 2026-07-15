package aggregator

import (
	"strings"
	"sync"
	"testing"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
)

// heartbeat builds a MetricBatch carrying only an active-VU count for second
// `sec` — no Agg, so it finalizes into a tick with active_vus>0 and rps=0 (the
// "load generated but nothing completing" signal the stall breaker watches).
func heartbeat(runID string, sec, activeVUs int64) *loadifyv1.MetricBatch {
	return &loadifyv1.MetricBatch{
		RunId:        runID,
		WorkerId:     "w1",
		BucketUnixMs: sec * 1000,
		ActiveVus:    activeVUs,
	}
}

// TestStallBreakerTrips: VUs active but zero completed requests for stallSec
// consecutive seconds must abort — the black-hole case the error-rate breaker
// can't see (it needs completed requests to compute a rate).
func TestStallBreakerTrips(t *testing.T) {
	a := New("run-stall", loadifyv1.Protocol_PROTOCOL_HTTP, nil, nil)
	var mu sync.Mutex
	var fired int
	var reason string
	a.SetAutoStop(enabled(), func(_, r string) {
		mu.Lock()
		fired++
		reason = r
		mu.Unlock()
	})
	a.SetStallSec(3)

	// 4 seconds of active VUs, zero completions → trips at 3.
	for sec := 0; sec < 4; sec++ {
		a.Ingest(heartbeat("run-stall", int64(sec), 5))
	}
	a.finalize(time.Unix(20, 0)) // flush all buckets past the grace window

	mu.Lock()
	defer mu.Unlock()
	if fired != 1 {
		t.Fatalf("stall breaker fired %d times, want exactly 1", fired)
	}
	if !strings.Contains(reason, "not responding") {
		t.Errorf("reason = %q, want it to mention the target not responding", reason)
	}
}

// TestStallBreakerResetsOnCompletion: as long as requests keep completing, no
// stall is declared even if VUs are active — a slow-but-working target must
// never be aborted.
func TestStallBreakerResetsOnCompletion(t *testing.T) {
	a := New("run-live", loadifyv1.Protocol_PROTOCOL_HTTP, nil, nil)
	var fired int
	a.SetAutoStop(enabled(), func(_, _ string) { fired++ })
	a.SetStallSec(3)

	// Pattern per 3s window: two heartbeat-only seconds, then one second with
	// completions (0 errors) — the streak never reaches 3.
	sec := int64(0)
	for cycle := 0; cycle < 4; cycle++ {
		a.Ingest(heartbeat("run-live", sec, 5))
		sec++
		a.Ingest(heartbeat("run-live", sec, 5))
		sec++
		a.Ingest(batch("run-live", "g", sec, 10, 0)) // 10 completions, no errors
		sec++
	}
	a.finalize(time.Unix(sec+10, 0))

	if fired != 0 {
		t.Errorf("stall breaker fired %d times, want 0 (target was completing requests)", fired)
	}
}

// TestStallBreakerDisabled: with stallSec unset (0), the stall path never fires.
func TestStallBreakerDisabled(t *testing.T) {
	a := New("run-nostall", loadifyv1.Protocol_PROTOCOL_HTTP, nil, nil)
	var fired int
	a.SetAutoStop(enabled(), func(_, _ string) { fired++ })
	// stallSec left at 0 (disabled).
	for sec := 0; sec < 10; sec++ {
		a.Ingest(heartbeat("run-nostall", int64(sec), 5))
	}
	a.finalize(time.Unix(30, 0))
	if fired != 0 {
		t.Errorf("stall breaker fired %d times with stallSec=0, want 0", fired)
	}
}
