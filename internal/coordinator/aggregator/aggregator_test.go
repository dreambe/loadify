package aggregator

import (
	"testing"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
)

// TestPerBucketActiveVUs verifies each finalized second carries the VU count
// reported FOR that second, not the current pool size — so a ramp/drain shows
// historically accurate concurrency rather than the latest value everywhere.
func TestPerBucketActiveVUs(t *testing.T) {
	a := New("run-vus", loadifyv1.Protocol_PROTOCOL_HTTP, nil, nil)
	ch := a.Subscribe()

	b0 := batch("run-vus", "g", 0, 10, 0)
	b0.ActiveVus = 5
	b1 := batch("run-vus", "g", 1, 10, 0)
	b1.ActiveVus = 9
	a.Ingest(b0)
	a.Ingest(b1)
	a.finalize(time.Unix(20, 0)) // flush both seconds

	got := map[int64]int64{}
	for drained := false; !drained; {
		select {
		case tk := <-ch:
			got[tk.TsUnixMs/1000] = tk.ActiveVus
		default:
			drained = true
		}
	}
	if got[0] != 5 {
		t.Errorf("sec 0 ActiveVus = %d, want 5 (per-bucket, not current)", got[0])
	}
	if got[1] != 9 {
		t.Errorf("sec 1 ActiveVus = %d, want 9", got[1])
	}
}

// TestLateBatchDroppedAndCounted verifies a batch arriving for an already
// finalized second is counted as late and does not resurrect a zombie bucket
// that would silently never emit.
func TestLateBatchDroppedAndCounted(t *testing.T) {
	a := New("run-late", loadifyv1.Protocol_PROTOCOL_HTTP, nil, nil)
	a.Ingest(batch("run-late", "g", 5, 10, 0))
	a.finalize(time.Unix(20, 0)) // finalizes sec 5; lastEmitted = 5

	a.Ingest(batch("run-late", "g", 5, 7, 0)) // late: sec 5 already emitted

	a.mu.Lock()
	late := a.lateBatches
	buckets := len(a.buckets)
	a.mu.Unlock()
	if late != 1 {
		t.Errorf("lateBatches = %d, want 1", late)
	}
	if buckets != 0 {
		t.Errorf("late batch resurrected %d zombie bucket(s), want 0", buckets)
	}
}
