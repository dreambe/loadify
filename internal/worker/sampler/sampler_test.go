package sampler

import (
	"testing"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/worker/protocols"
)

func TestFlushCapsAndResetsSamples(t *testing.T) {
	s := New("run", "worker", loadifyv1.Protocol_PROTOCOL_HTTP)

	// Far more than the caps of both errors and successes.
	for i := 0; i < 500; i++ {
		s.Record(protocols.Result{Group: "g", OK: true, Status: 200, LatencyUs: 1000})
	}
	for i := 0; i < 500; i++ {
		s.Record(protocols.Result{Group: "g", OK: false, Status: 500, ErrorKind: "http_status", LatencyUs: 2000})
	}

	batch := s.Flush(time.Now())
	if got := len(batch.Samples); got != okSampleCap+errSampleCap {
		t.Fatalf("samples = %d, want %d", got, okSampleCap+errSampleCap)
	}
	var errs int
	for _, sm := range batch.Samples {
		if !sm.Ok {
			errs++
		}
	}
	if errs != errSampleCap {
		t.Errorf("error samples = %d, want %d", errs, errSampleCap)
	}

	// After flush the sample buffer and counters reset.
	s.Record(protocols.Result{Group: "g", OK: true, Status: 200, LatencyUs: 1000})
	batch2 := s.Flush(time.Now())
	if len(batch2.Samples) != 1 {
		t.Errorf("post-reset samples = %d, want 1", len(batch2.Samples))
	}
}
