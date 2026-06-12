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
	if !r.endedAt.IsZero() {
		rs.EndedAtUnixMs = r.endedAt.UnixMilli()
	}
	return rs
}
