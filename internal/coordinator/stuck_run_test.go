package coordinator

import (
	"context"
	"testing"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/coordinator/aggregator"
)

// newRunningRun injects a RUNNING run assigned to workerID (bypassing the gRPC
// dispatch path) so the finalize/reaper logic can be exercised directly.
func newRunningRun(s *Service, runID, workerID string) *runState {
	aggCtx, aggCancel := context.WithCancel(context.Background())
	agg := aggregator.New(runID, loadifyv1.Protocol_PROTOCOL_HTTP, nil, nil)
	go agg.Run(aggCtx)
	rs := &runState{
		runID:     runID,
		protocol:  loadifyv1.Protocol_PROTOCOL_HTTP,
		agg:       agg,
		aggCancel: aggCancel,
		assigned:  map[string]bool{workerID: true},
		finished:  make(map[string]bool),
		status:    loadifyv1.RunStatus_RUN_STATUS_RUNNING,
		startedAt: time.Now(),
		slotHeld:  true,
	}
	s.mu.Lock()
	s.runs[runID] = rs
	s.running++
	s.mu.Unlock()
	return rs
}

// TestForceFinalizeStopsAbortedRun: a run marked ABORTED (as StopRun/auto-stop
// do) whose workers never report Finished must still terminalize — otherwise
// the Stop button does nothing and the run hangs "running" forever. It must
// keep its abort reason and free the admission slot.
func TestForceFinalizeStopsAbortedRun(t *testing.T) {
	s := New(nil, nil)
	rs := newRunningRun(s, "r1", "w1")

	s.mu.Lock()
	rs.status = loadifyv1.RunStatus_RUN_STATUS_ABORTED
	rs.reason = "stopped by user"
	s.mu.Unlock()

	s.forceFinalize("r1", "stopped by user (workers did not acknowledge)")

	s.mu.Lock()
	defer s.mu.Unlock()
	if rs.endedAt.IsZero() {
		t.Fatal("run not finalized: it would hang \"running\" forever")
	}
	if rs.status != loadifyv1.RunStatus_RUN_STATUS_ABORTED {
		t.Errorf("status = %v, want ABORTED", rs.status)
	}
	if rs.reason != "stopped by user" {
		t.Errorf("reason = %q, want the existing verdict preserved", rs.reason)
	}
	if s.running != 0 {
		t.Errorf("admission slot not released: running = %d, want 0", s.running)
	}
}

// TestForceFinalizeIsIdempotent: a second force-finalize (or one racing the
// real RunFinished) is a no-op and doesn't double-decrement the slot counter.
func TestForceFinalizeIsIdempotent(t *testing.T) {
	s := New(nil, nil)
	newRunningRun(s, "r1", "w1")
	s.forceFinalize("r1", "reason a")
	s.forceFinalize("r1", "reason b")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != 0 {
		t.Errorf("running = %d, want 0 (idempotent finalize)", s.running)
	}
}

// TestForceFinalizeSkipsQueuedRun: a queued run holds no admission slot, so the
// stop watchdog must not force-finalize it (which would underflow s.running).
func TestForceFinalizeSkipsQueuedRun(t *testing.T) {
	s := New(nil, nil)
	s.mu.Lock()
	s.runs["q1"] = &runState{
		runID:    "q1",
		protocol: loadifyv1.Protocol_PROTOCOL_HTTP,
		assigned: make(map[string]bool),
		finished: make(map[string]bool),
		status:   loadifyv1.RunStatus_RUN_STATUS_QUEUED,
		// slotHeld stays false — never dispatched.
	}
	s.mu.Unlock()

	s.forceFinalize("q1", "stopped by user (workers did not acknowledge)")

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != 0 {
		t.Errorf("running = %d, want 0 (queued run must not touch the slot counter)", s.running)
	}
	if !s.runs["q1"].endedAt.IsZero() {
		t.Error("queued run was force-finalized; that's the queue's concern, not the watchdog's")
	}
}

// TestReapOrphanedRun: a RUNNING run whose only worker disappears from the
// registry (crash / dropped connection, no Finished) must be aborted after the
// orphan grace — but not before, and not while the worker is still present.
func TestReapOrphanedRun(t *testing.T) {
	s := New(nil, nil)
	send := make(chan *loadifyv1.CoordinatorMessage, 1)
	s.reg.Add(&loadifyv1.RegisterRequest{
		WorkerId:  "w1",
		Supported: []loadifyv1.Protocol{loadifyv1.Protocol_PROTOCOL_HTTP},
	}, send)
	rs := newRunningRun(s, "r1", "w1")

	// Worker present → never reaped.
	s.reapOnce(time.Now())
	s.mu.Lock()
	if !rs.endedAt.IsZero() {
		t.Fatal("reaped a run whose worker is still connected")
	}
	s.mu.Unlock()

	// Worker vanishes.
	s.reg.Remove("w1", send)
	now := time.Now()
	s.reapOnce(now) // first observation only arms the timer
	s.mu.Lock()
	if !rs.endedAt.IsZero() {
		t.Fatal("reaped immediately — orphan grace not respected")
	}
	s.mu.Unlock()

	// Past the grace window → aborted.
	s.reapOnce(now.Add(orphanGrace + time.Second))
	s.mu.Lock()
	defer s.mu.Unlock()
	if rs.endedAt.IsZero() {
		t.Fatal("orphaned run was never reaped")
	}
	if rs.status != loadifyv1.RunStatus_RUN_STATUS_ABORTED {
		t.Errorf("status = %v, want ABORTED", rs.status)
	}
	if s.running != 0 {
		t.Errorf("admission slot not released: running = %d", s.running)
	}
}

// TestReapClearsTimerWhenWorkerReturns: a transient absence (worker reconnects
// within the grace) must not abort the run — the lost-timer resets.
func TestReapClearsTimerWhenWorkerReturns(t *testing.T) {
	s := New(nil, nil)
	send := make(chan *loadifyv1.CoordinatorMessage, 1)
	add := func() {
		s.reg.Add(&loadifyv1.RegisterRequest{
			WorkerId:  "w1",
			Supported: []loadifyv1.Protocol{loadifyv1.Protocol_PROTOCOL_HTTP},
		}, send)
	}
	add()
	rs := newRunningRun(s, "r1", "w1")

	s.reg.Remove("w1", send)
	now := time.Now()
	s.reapOnce(now) // arms lost-timer
	add()           // worker reconnects
	s.reapOnce(now.Add(orphanGrace + time.Second))

	s.mu.Lock()
	defer s.mu.Unlock()
	if !rs.endedAt.IsZero() {
		t.Fatal("run aborted despite the worker reconnecting within grace")
	}
}
