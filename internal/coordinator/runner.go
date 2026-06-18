package coordinator

import (
	"context"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/coordinator/aggregator"
)

// runState tracks one in-flight run.
type runState struct {
	runID     string
	protocol  loadifyv1.Protocol
	agg       *aggregator.Aggregator
	aggCancel context.CancelFunc
	assigned  map[string]bool
	finished  map[string]bool
	status    loadifyv1.RunStatus
	reason    string
	startedAt time.Time
	endedAt   time.Time
	plannedMs int64 // total ramp duration, for queue-ETA estimation
	// Generator-saturation accounting, aggregated across the run's workers.
	droppedIterations int64
	droppedMetrics    int64
	peakCPUPct        float64 // peak per-node utilization (0-100 of total capacity)
}

// remainingMs estimates how much of a running run is left, from its planned
// ramp duration and elapsed time. 0 once it's past plan (about to finish).
func (r *runState) remainingMs(now time.Time) int64 {
	if r.plannedMs <= 0 || r.startedAt.IsZero() {
		return 0
	}
	rem := r.plannedMs - now.Sub(r.startedAt).Milliseconds()
	if rem < 0 {
		return 0
	}
	return rem
}

func (r *runState) toProto(activeVUs int64) *loadifyv1.RunState {
	rs := &loadifyv1.RunState{
		RunId:           r.runID,
		Status:          r.status,
		ActiveVus:       activeVUs,
		StartedAtUnixMs: r.startedAt.UnixMilli(),
	}
	rs.ActiveWorkers = int32(len(r.assigned) - len(r.finished))
	rs.Reason = r.reason
	rs.DroppedIterations = r.droppedIterations
	rs.DroppedMetrics = r.droppedMetrics
	rs.PeakCpuPct = r.peakCPUPct
	if !r.endedAt.IsZero() {
		rs.EndedAtUnixMs = r.endedAt.UnixMilli()
	}
	return rs
}
