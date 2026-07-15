// Package coordinator implements the WorkerService (worker connections) and the
// CoordinatorService (control API consumed by apisrv).
package coordinator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"sync"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/coordinator/aggregator"
	"github.com/dreambe/loadify/internal/coordinator/registry"
	"github.com/dreambe/loadify/internal/coordinator/scheduler"
	"github.com/dreambe/loadify/internal/obs"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// assignGrace is the lead time given to workers so they start in sync.
const assignGrace = 2 * time.Second

// stopGrace is how long a stop/auto-stop waits for workers to report Finished
// before the coordinator force-finalizes the run itself. Without this, a run
// whose workers are stuck or gone stays "running" forever and the Stop button
// does nothing.
const stopGrace = 8 * time.Second

// orphanGrace is how long a RUNNING run may have ALL its assigned workers
// missing from the registry before the reaper aborts it. Covers a worker that
// crashed or dropped without ever reporting Finished. Kept comfortably above the
// worker's max reconnect backoff (8s) plus restart/rehydrate time, so a worker
// riding out a transient blip re-attaches and keeps its run instead of being
// reaped.
const orphanGrace = 30 * time.Second

// aggDrainGrace lets the aggregator flush late batches before it is torn down.
const aggDrainGrace = 3 * time.Second

// Service backs both gRPC services.
type Service struct {
	loadifyv1.UnimplementedWorkerServiceServer
	loadifyv1.UnimplementedCoordinatorServiceServer

	reg    *registry.Registry
	writer store.RollupWriter
	log    *slog.Logger

	mu      sync.Mutex
	runs    map[string]*runState
	queue   []*loadifyv1.StartRunRequest
	running int
	maxRuns int
	cpuMax  float64
}

// New creates a coordinator Service. writer may be nil (rollups discarded).
func New(writer store.RollupWriter, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		reg:     registry.New(6 * time.Second),
		writer:  writer,
		log:     log,
		runs:    make(map[string]*runState),
		maxRuns: 64, // effectively unlimited until SetLimits tightens it
		cpuMax:  85, // per-node protection threshold (SetLimits may override)
	}
}

// RegisterMetrics exposes live coordinator state on the Prometheus endpoint:
// active runs, queue depth and connected workers. Call once at startup.
func (s *Service) RegisterMetrics() {
	obs.RegisterGauge("loadify_active_runs", "Currently running load runs.", func() float64 {
		s.mu.Lock()
		defer s.mu.Unlock()
		return float64(len(s.runs))
	})
	obs.RegisterGauge("loadify_queue_depth", "Runs waiting for admission.", func() float64 {
		s.mu.Lock()
		defer s.mu.Unlock()
		return float64(len(s.queue))
	})
	obs.RegisterGauge("loadify_workers_connected", "Connected workers.", func() float64 {
		return float64(len(s.reg.List()))
	})
}

// SetLimits configures admission control: at most maxConcurrent runs dispatch
// at once, and workers at or above cpuMaxPct are not eligible (0 disables the
// CPU gate). Runs that can't be admitted are queued.
func (s *Service) SetLimits(maxConcurrent int, cpuMaxPct float64) {
	s.mu.Lock()
	if maxConcurrent > 0 {
		s.maxRuns = maxConcurrent
	}
	s.cpuMax = cpuMaxPct
	s.mu.Unlock()
}

// --- WorkerService ---

// Connect handles a worker's long-lived bidirectional stream.
func (s *Service) Connect(stream loadifyv1.WorkerService_ConnectServer) error {
	ctx := stream.Context()
	send := make(chan *loadifyv1.CoordinatorMessage, 64)
	var workerID string

	// Sender goroutine owns Send.
	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-send:
				if !ok {
					return
				}
				if err := stream.Send(msg); err != nil {
					return
				}
			}
		}
	}()

	defer func() {
		// Only remove if THIS stream still owns the registry entry. If the worker
		// already reconnected on a newer stream, that stream's Add replaced the
		// handle; a stale teardown must not evict the live worker (see Remove).
		if workerID != "" && s.reg.Remove(workerID, send) {
			s.log.Info("worker disconnected", "worker", workerID)
		}
	}()

	for {
		msg, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		switch m := msg.Msg.(type) {
		case *loadifyv1.WorkerMessage_Register:
			workerID = m.Register.WorkerId
			s.reg.Add(m.Register, send)
			s.log.Info("worker registered", "worker", workerID, "region", m.Register.Region)
			// Rebuild state for any runs this worker is already executing (e.g.
			// after a coordinator restart), so their metrics aren't dropped.
			s.rehydrate(workerID, m.Register.ActiveRuns)
			send <- &loadifyv1.CoordinatorMessage{Msg: &loadifyv1.CoordinatorMessage_RegisterAck{
				RegisterAck: &loadifyv1.RegisterAck{LeaseId: workerID, HeartbeatIntervalMs: 2000},
			}}
			// A newly-connected worker is fresh capacity. Admit any queued runs
			// now instead of waiting for a slot to free — otherwise a run started
			// while no worker was connected (e.g. right after a coordinator
			// restart) sits queued until an unrelated run finishes.
			s.mu.Lock()
			s.drainLocked()
			s.mu.Unlock()
		case *loadifyv1.WorkerMessage_Heartbeat:
			s.reg.Touch(m.Heartbeat.WorkerId, registry.Stats{
				ActiveVUs:     m.Heartbeat.ActiveVus,
				CPUPct:        m.Heartbeat.CpuPct,
				MemBytes:      m.Heartbeat.MemBytes,
				MemTotalBytes: m.Heartbeat.MemTotalBytes,
				NetRxBps:      m.Heartbeat.NetRxBps,
				NetTxBps:      m.Heartbeat.NetTxBps,
				NetRxPps:      m.Heartbeat.NetRxPps,
				NetTxPps:      m.Heartbeat.NetTxPps,
			})
			s.recordPeakCPU(m.Heartbeat.WorkerId)
		case *loadifyv1.WorkerMessage_Metrics:
			s.ingest(m.Metrics)
		case *loadifyv1.WorkerMessage_Finished:
			s.workerFinished(m.Finished)
		}
	}
}

func (s *Service) ingest(b *loadifyv1.MetricBatch) {
	s.mu.Lock()
	rs := s.runs[b.RunId]
	s.mu.Unlock()
	if rs != nil {
		rs.agg.Ingest(b)
	}
}

// rehydrate rebuilds run state for runs a (re)connecting worker reports as
// active but the coordinator no longer knows about — the recovery path after a
// coordinator restart. Metrics for these runs then aggregate and persist again.
func (s *Service) rehydrate(workerID string, active []*loadifyv1.ActiveRun) {
	if len(active) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ar := range active {
		if rs := s.runs[ar.RunId]; rs != nil && rs.agg != nil {
			rs.assigned[workerID] = true
			continue
		}
		aggCtx, aggCancel := context.WithCancel(context.Background())
		agg := aggregator.New(ar.RunId, ar.Protocol, s.writer, s.log)
		go agg.Run(aggCtx)
		s.runs[ar.RunId] = &runState{
			runID:     ar.RunId,
			protocol:  ar.Protocol,
			agg:       agg,
			aggCancel: aggCancel,
			assigned:  map[string]bool{workerID: true},
			finished:  make(map[string]bool),
			status:    loadifyv1.RunStatus_RUN_STATUS_RUNNING,
			startedAt: time.Now(),
			slotHeld:  true,
		}
		s.running++
		s.log.Info("rehydrated run from worker", "run", ar.RunId, "worker", workerID)
	}
}

// recordPeakCPU folds a worker's current normalized CPU utilization into the
// peak of every run it's assigned to, so the run summary can flag results that
// may reflect the load generator's own saturation rather than the target's.
func (s *Service) recordPeakCPU(workerID string) {
	util, ok := s.reg.Utilization(workerID)
	if !ok {
		return
	}
	s.mu.Lock()
	for _, rs := range s.runs {
		if rs.status == loadifyv1.RunStatus_RUN_STATUS_RUNNING && rs.assigned[workerID] && util > rs.peakCPUPct {
			rs.peakCPUPct = util
		}
	}
	s.mu.Unlock()
}

func (s *Service) workerFinished(f *loadifyv1.RunFinished) {
	s.mu.Lock()
	rs := s.runs[f.RunId]
	if rs == nil {
		s.mu.Unlock()
		return
	}
	rs.finished[f.WorkerId] = true
	rs.droppedIterations += f.DroppedIterations
	rs.droppedMetrics += f.DroppedMetrics
	done := len(rs.finished) >= len(rs.assigned)
	var cancel context.CancelFunc
	if done {
		cancel = s.finalizeLocked(rs, loadifyv1.RunStatus_RUN_STATUS_COMPLETED)
	}
	terminalStatus := rs.status
	s.mu.Unlock()
	if done && cancel != nil {
		s.log.Info("run terminal", "run", f.RunId, "status", terminalStatus.String())
		s.stopAggregatorAfterGrace(cancel)
	}
}

// finalizeLocked runs the once-only terminal cleanup for a run: it stamps
// endedAt, frees the admission slot, drains the queue, and returns the
// aggregator's cancel to invoke after the lock is dropped (nil if the run had
// already finalized — making this idempotent against a duplicate RunFinished or
// a force-finalize racing the real one).
//
// A run still RUNNING adopts fallbackStatus (COMPLETED on the normal
// worker-finished path). A run already marked terminal — the auto-stop breaker
// and user-stop both set rs.status=ABORTED *before* workers report in — keeps
// its verdict and reason. The old inline code gated the whole cleanup on
// status==RUNNING, so an auto-stopped run leaked its slot and hung "running"
// forever; routing every terminal path through here fixes that. Caller holds
// s.mu.
func (s *Service) finalizeLocked(rs *runState, fallbackStatus loadifyv1.RunStatus) context.CancelFunc {
	if rs == nil || !rs.endedAt.IsZero() {
		return nil
	}
	if rs.status == loadifyv1.RunStatus_RUN_STATUS_RUNNING {
		rs.status = fallbackStatus
	}
	rs.endedAt = time.Now()
	if rs.slotHeld {
		rs.slotHeld = false
		s.running--
		s.drainLocked() // a slot freed: admit queued runs
	}
	return rs.aggCancel
}

// stopAggregatorAfterGrace tears down a run's aggregator after a short drain
// window, closing the live stream apisrv's watchRun blocks on so the run
// finalizes instead of hanging "running".
func (s *Service) stopAggregatorAfterGrace(cancel context.CancelFunc) {
	if cancel == nil {
		return
	}
	go func() {
		time.Sleep(aggDrainGrace)
		cancel()
	}()
}

// forceFinalize terminalizes a run the coordinator can no longer expect its
// workers to finish (they're stuck or gone), so a stop actually stops and a
// zombie run can't hang "running" forever. Idempotent: a no-op once the run has
// ended. Preserves an existing abort reason; otherwise stamps defaultReason.
func (s *Service) forceFinalize(runID, defaultReason string) {
	s.mu.Lock()
	rs := s.runs[runID]
	// Only force-finalize a dispatched run. A still-queued run holds no slot and
	// no aggregator; stopping it is a separate queue concern, not this watchdog's.
	if rs == nil || !rs.endedAt.IsZero() || !rs.slotHeld {
		s.mu.Unlock()
		return
	}
	if rs.reason == "" {
		rs.reason = defaultReason
	}
	cancel := s.finalizeLocked(rs, loadifyv1.RunStatus_RUN_STATUS_ABORTED)
	reason := rs.reason
	s.mu.Unlock()
	s.log.Warn("run force-finalized", "run", runID, "reason", reason)
	s.stopAggregatorAfterGrace(cancel)
}

// reapOnce force-aborts RUNNING runs whose assigned workers have ALL been
// missing from the registry for longer than orphanGrace — a worker that crashed
// or dropped its connection without reporting Finished would otherwise strand
// the run as "running" forever. Called periodically by Watchdog; exposed for
// tests to drive deterministically.
func (s *Service) reapOnce(now time.Time) {
	present := make(map[string]bool)
	for _, w := range s.reg.List() {
		present[w.WorkerId] = true
	}
	var orphaned []string
	s.mu.Lock()
	for id, rs := range s.runs {
		if rs.status != loadifyv1.RunStatus_RUN_STATUS_RUNNING || !rs.endedAt.IsZero() || len(rs.assigned) == 0 {
			rs.workersLostAt = time.Time{}
			continue
		}
		anyPresent := false
		for wid := range rs.assigned {
			if present[wid] {
				anyPresent = true
				break
			}
		}
		if anyPresent {
			rs.workersLostAt = time.Time{}
			continue
		}
		if rs.workersLostAt.IsZero() {
			rs.workersLostAt = now
			continue
		}
		if now.Sub(rs.workersLostAt) >= orphanGrace {
			orphaned = append(orphaned, id)
		}
	}
	s.mu.Unlock()
	for _, id := range orphaned {
		s.forceFinalize(id, "aborted: assigned workers disconnected")
	}
}

// Watchdog periodically reaps orphaned runs until ctx is done. Start it once at
// coordinator startup.
func (s *Service) Watchdog(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.reapOnce(now)
		}
	}
}

// --- CoordinatorService ---

// StartRun admits a run immediately when the cluster has capacity, otherwise
// queues it (and a freed slot later drains the queue). This keeps overloaded
// workers from being piled onto.
func (s *Service) StartRun(_ context.Context, req *loadifyv1.StartRunRequest) (*loadifyv1.StartRunResponse, error) {
	if req.RunId == "" {
		return nil, status.Error(codes.InvalidArgument, "run_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Idempotency: a run id already known to the coordinator (e.g. a duplicate
	// StartRun, or one racing a rehydrate after restart) must not overwrite the
	// live runState — that would orphan its aggregator goroutine. Report the
	// current state instead.
	if rs := s.runs[req.RunId]; rs != nil {
		st := "queued"
		if rs.status == loadifyv1.RunStatus_RUN_STATUS_RUNNING {
			st = "running"
		}
		return &loadifyv1.StartRunResponse{RunId: req.RunId, AssignedWorkers: int32(len(rs.assigned)), Status: st}, nil
	}

	if s.canDispatchLocked(req.Protocol) {
		assigned, err := s.dispatchLocked(req)
		if err != nil {
			return nil, err
		}
		return &loadifyv1.StartRunResponse{RunId: req.RunId, AssignedWorkers: int32(assigned), Status: "running"}, nil
	}
	// No capacity (slots full or no eligible worker): queue it.
	s.queue = append(s.queue, req)
	s.runs[req.RunId] = &runState{
		runID:     req.RunId,
		protocol:  req.Protocol,
		assigned:  make(map[string]bool),
		finished:  make(map[string]bool),
		status:    loadifyv1.RunStatus_RUN_STATUS_QUEUED,
		plannedMs: rampDurationMs(req.Ramp),
	}
	s.log.Info("run queued", "run", req.RunId, "queue_depth", len(s.queue))
	return &loadifyv1.StartRunResponse{RunId: req.RunId, Status: "queued", QueuePosition: int32(len(s.queue))}, nil
}

// canDispatchLocked reports whether a run for proto can start right now.
func (s *Service) canDispatchLocked(proto loadifyv1.Protocol) bool {
	return s.running < s.maxRuns && len(s.reg.Available(proto, s.cpuMax)) > 0
}

// rampDurationMs sums a ramp's stage durations (the run's planned wall-clock
// length), used to estimate when a slot will free for queued runs.
func rampDurationMs(ramp []*loadifyv1.RampStage) int64 {
	var total int64
	for _, st := range ramp {
		total += st.DurationMs
	}
	return total
}

// queueETALocked estimates, for the run at 1-based queue position pos, how long
// until a slot frees. A queued run waits for `pos` running runs to finish, so
// the estimate is the pos-th smallest remaining time among running runs. It's a
// rough hint (ignores worker-CPU gating and post-run drain), not a guarantee.
func (s *Service) queueETALocked(pos int) int64 {
	if pos <= 0 {
		return 0
	}
	now := time.Now()
	rem := make([]int64, 0, len(s.runs))
	for _, rs := range s.runs {
		if rs.status == loadifyv1.RunStatus_RUN_STATUS_RUNNING {
			rem = append(rem, rs.remainingMs(now))
		}
	}
	if len(rem) == 0 {
		return 0
	}
	sort.Slice(rem, func(i, j int) bool { return rem[i] < rem[j] })
	if pos > len(rem) {
		pos = len(rem)
	}
	return rem[pos-1]
}

// queuePositionLocked returns the 1-based position of runID in the queue, or 0
// if it isn't queued.
func (s *Service) queuePositionLocked(runID string) int {
	for i, req := range s.queue {
		if req.RunId == runID {
			return i + 1
		}
	}
	return 0
}

// GetCapacity reports cluster admission headroom so the UI can warn a user that
// starting a run now would queue it.
func (s *Service) GetCapacity(_ context.Context, _ *loadifyv1.CapacityRequest) (*loadifyv1.CapacitySnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := len(s.reg.Healthy(loadifyv1.Protocol_PROTOCOL_UNSPECIFIED))
	avail := len(s.reg.Available(loadifyv1.Protocol_PROTOCOL_UNSPECIFIED, s.cpuMax))
	return &loadifyv1.CapacitySnapshot{
		MaxRuns:          int32(s.maxRuns),
		Running:          int32(s.running),
		QueueDepth:       int32(len(s.queue)),
		WorkersTotal:     int32(total),
		WorkersAvailable: int32(avail),
		CpuMaxPct:        s.cpuMax,
		CanAccept:        s.running < s.maxRuns && avail > 0,
	}, nil
}

// dispatchLocked selects workers, slices the ramp and sends assignments. The
// caller holds s.mu. It returns the number of workers assigned.
func (s *Service) dispatchLocked(req *loadifyv1.StartRunRequest) (int, error) {
	candidates := s.reg.Available(req.Protocol, s.cpuMax)
	if len(candidates) == 0 {
		return 0, status.Error(codes.FailedPrecondition, "no healthy workers available")
	}
	workers := scheduler.PickWorkers(candidates, int(req.DesiredWorkers))
	slices := scheduler.SliceRamp(req.Ramp, len(workers))

	aggCtx, aggCancel := context.WithCancel(context.Background())
	agg := aggregator.New(req.RunId, req.Protocol, s.writer, s.log)
	// Arm the auto-stop circuit breaker from the plan (enabled by default).
	if p, perr := plan.Parse(req.PlanJson); perr == nil {
		agg.SetAutoStop(p.AutoStopOrDefault(), s.autoStopRun)
		// Size the stall breaker at 3× the request timeout (min 60s), so a
		// legitimately slow target (e.g. long non-streaming LLM calls) whose
		// requests still complete/time out within one timeout is never aborted;
		// only a target generating load with zero completions for far longer
		// trips it.
		if p.AutoStopOrDefault().AutoStopEnabled() {
			stallSec := int(3 * p.RequestTimeout().Seconds())
			if stallSec < 60 {
				stallSec = 60
			}
			agg.SetStallSec(stallSec)
		}
	}
	go agg.Run(aggCtx)

	rs := &runState{
		runID:     req.RunId,
		protocol:  req.Protocol,
		agg:       agg,
		aggCancel: aggCancel,
		assigned:  make(map[string]bool),
		finished:  make(map[string]bool),
		status:    loadifyv1.RunStatus_RUN_STATUS_RUNNING,
		startedAt: time.Now(),
		plannedMs: rampDurationMs(req.Ramp),
	}
	startAt := time.Now().Add(assignGrace).UnixMilli()
	for i, w := range workers {
		rs.assigned[w.ID] = true
		assignment := &loadifyv1.RunAssignment{
			RunId:         req.RunId,
			Protocol:      req.Protocol,
			PlanJson:      req.PlanJson,
			Ramp:          slices[i],
			Script:        req.Script,
			StartAtUnixMs: startAt,
			Seed:          int64(i),
			Env:           req.Env,
		}
		select {
		case w.Send <- &loadifyv1.CoordinatorMessage{Msg: &loadifyv1.CoordinatorMessage_Assignment{Assignment: assignment}}:
		default:
			s.log.Warn("worker send buffer full, skipping", "worker", w.ID)
			delete(rs.assigned, w.ID)
		}
	}
	if len(rs.assigned) == 0 {
		aggCancel()
		return 0, status.Error(codes.Unavailable, "failed to dispatch to any worker")
	}
	rs.slotHeld = true
	s.runs[req.RunId] = rs
	s.running++
	s.log.Info("run started", "run", req.RunId, "workers", len(rs.assigned))
	return len(rs.assigned), nil
}

// drainLocked admits queued runs while there is capacity and an eligible worker.
func (s *Service) drainLocked() {
	for len(s.queue) > 0 && s.running < s.maxRuns {
		req := s.queue[0]
		if len(s.reg.Available(req.Protocol, s.cpuMax)) == 0 {
			return // keep queued until a capable worker is free
		}
		s.queue = s.queue[1:]
		if _, err := s.dispatchLocked(req); err != nil {
			// A transient dispatch failure (e.g. a worker's send buffer was
			// momentarily full) must not strand the run: put it back at the
			// front and stop draining this round so it retries on the next
			// event rather than being silently lost.
			s.log.Warn("drain dispatch failed; re-queueing", "run", req.RunId, "err", err)
			s.queue = append([]*loadifyv1.StartRunRequest{req}, s.queue...)
			return
		}
	}
}

// StopRun signals all assigned workers to stop a run.
func (s *Service) StopRun(_ context.Context, req *loadifyv1.StopRunRequest) (*loadifyv1.StopRunResponse, error) {
	s.mu.Lock()
	rs := s.runs[req.RunId]
	// Record the verdict for a still-running run so it finalizes as "aborted"
	// with a clear reason, not a misleading "completed". A run already marked
	// terminal (e.g. by the auto-stop breaker) keeps its own reason.
	if rs != nil && rs.status == loadifyv1.RunStatus_RUN_STATUS_RUNNING {
		rs.status = loadifyv1.RunStatus_RUN_STATUS_ABORTED
		rs.reason = "stopped by user"
	}
	if rs != nil && rs.abortAt.IsZero() {
		rs.abortAt = time.Now()
	}
	s.mu.Unlock()
	if rs == nil {
		return nil, status.Error(codes.NotFound, "run not found")
	}
	for id := range rs.assigned {
		if w, ok := s.reg.Get(id); ok {
			select {
			case w.Send <- &loadifyv1.CoordinatorMessage{Msg: &loadifyv1.CoordinatorMessage_Stop{Stop: &loadifyv1.StopRequest{RunId: req.RunId, Graceful: req.Graceful}}}:
			default:
			}
		}
	}
	// Workers were signalled — but a stuck or already-gone worker may never
	// report Finished. Force-finalize after a grace period so Stop actually
	// stops: the run terminalizes to its aborted verdict, its slot frees and the
	// live stream closes, instead of hanging "running" forever.
	time.AfterFunc(stopGrace, func() {
		s.forceFinalize(req.RunId, "stopped by user (workers did not acknowledge)")
	})
	return &loadifyv1.StopRunResponse{RunId: req.RunId}, nil
}

// autoStopRun is the aggregator's circuit-breaker callback: it records the
// abort reason and signals all assigned workers to stop the run.
func (s *Service) autoStopRun(runID, reason string) {
	s.mu.Lock()
	if rs := s.runs[runID]; rs != nil {
		rs.status = loadifyv1.RunStatus_RUN_STATUS_ABORTED
		rs.reason = reason
	}
	s.mu.Unlock()
	_, _ = s.StopRun(context.Background(), &loadifyv1.StopRunRequest{RunId: runID, Graceful: true})
}

// GetRunState returns the current state of a run.
func (s *Service) GetRunState(_ context.Context, req *loadifyv1.RunStateRequest) (*loadifyv1.RunState, error) {
	s.mu.Lock()
	rs := s.runs[req.RunId]
	if rs == nil {
		s.mu.Unlock()
		return nil, status.Error(codes.NotFound, "run not found")
	}
	var pos int
	var eta int64
	if rs.status == loadifyv1.RunStatus_RUN_STATUS_QUEUED {
		pos = s.queuePositionLocked(req.RunId)
		eta = s.queueETALocked(pos)
	}
	s.mu.Unlock()

	var activeVUs int64
	for _, w := range s.reg.List() {
		if rs.assigned[w.WorkerId] {
			activeVUs += w.ActiveVus
		}
	}
	out := rs.toProto(activeVUs)
	out.QueuePosition = int32(pos)
	out.QueueEtaMs = eta
	return out, nil
}

// ListWorkers returns all connected workers.
func (s *Service) ListWorkers(_ context.Context, _ *loadifyv1.ListWorkersRequest) (*loadifyv1.ListWorkersResponse, error) {
	return &loadifyv1.ListWorkersResponse{Workers: s.reg.List()}, nil
}

// StreamLive forwards live ticks for a run to the caller (apisrv).
func (s *Service) StreamLive(req *loadifyv1.LiveRequest, stream loadifyv1.CoordinatorService_StreamLiveServer) error {
	s.mu.Lock()
	rs := s.runs[req.RunId]
	s.mu.Unlock()
	if rs == nil {
		return status.Error(codes.NotFound, "run not found")
	}
	if rs.agg == nil {
		return status.Error(codes.Unavailable, "run is queued")
	}
	ch := rs.agg.Subscribe()
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case tick, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(tick); err != nil {
				return err
			}
		}
	}
}
