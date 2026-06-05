// Package sampler collects per-iteration results into 1-second mergeable
// rollups and emits them as MetricBatch messages.
package sampler

import (
	"sync"
	"sync/atomic"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/metrics"
	"github.com/dreambe/loadify/internal/worker/protocols"
)

// Sampler is safe for concurrent Record from many VUs and periodic Flush.
type Sampler struct {
	runID    string
	workerID string
	protocol loadifyv1.Protocol

	mu        sync.Mutex
	rec       *metrics.Recorder
	activeVUs atomic.Int64
}

// New creates a Sampler for a run.
func New(runID, workerID string, proto loadifyv1.Protocol) *Sampler {
	return &Sampler{runID: runID, workerID: workerID, protocol: proto, rec: metrics.NewRecorder()}
}

// Record ingests one iteration result.
func (s *Sampler) Record(r protocols.Result) {
	s.mu.Lock()
	s.rec.Record(r.Group, r.Status, r.OK, r.ErrorKind, r.LatencyUs, r.SentBytes, r.RecvBytes)
	s.mu.Unlock()
}

// SetActiveVUs records the current active VU count for the next batch.
func (s *Sampler) SetActiveVUs(n int64) { s.activeVUs.Store(n) }

// Flush swaps out the current window and returns it as a MetricBatch, or nil if
// no samples were recorded.
func (s *Sampler) Flush(bucket time.Time) *loadifyv1.MetricBatch {
	s.mu.Lock()
	prev := s.rec
	s.rec = metrics.NewRecorder()
	s.mu.Unlock()

	buckets := prev.Buckets()
	if len(buckets) == 0 {
		// Still emit a heartbeat batch carrying active VU count.
		return &loadifyv1.MetricBatch{
			RunId:        s.runID,
			WorkerId:     s.workerID,
			BucketUnixMs: bucket.UnixMilli(),
			ActiveVus:    s.activeVUs.Load(),
		}
	}

	batch := &loadifyv1.MetricBatch{
		RunId:        s.runID,
		WorkerId:     s.workerID,
		BucketUnixMs: bucket.UnixMilli(),
		ActiveVus:    s.activeVUs.Load(),
	}
	for k, b := range buckets {
		batch.Agg = append(batch.Agg, &loadifyv1.AggSlice{
			Group:        k.Group,
			Protocol:     s.protocol,
			StatusClass:  k.StatusClass,
			Count:        b.Count,
			Errors:       b.Errors,
			SentBytes:    b.SentBytes,
			RecvBytes:    b.RecvBytes,
			HdrHistogram: metrics.EncodeHistogram(b.Hist),
		})
	}
	return batch
}
