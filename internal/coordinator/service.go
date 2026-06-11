// Package coordinator implements the WorkerService (worker connections) and the
// CoordinatorService (control API consumed by apisrv).
package coordinator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/coordinator/aggregator"
	"github.com/dreambe/loadify/internal/coordinator/registry"
	"github.com/dreambe/loadify/internal/coordinator/scheduler"
	"github.com/dreambe/loadify/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// assignGrace is the lead time given to workers so they start in sync.
const assignGrace = 2 * time.Second

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
	}
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
		if workerID != "" {
			s.reg.Remove(workerID)
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
		case *loadifyv1.WorkerMessage_Heartbeat:
			s.reg.Touch(m.Heartbeat.WorkerId, m.Heartbeat.ActiveVus, m.Heartbeat.CpuPct, m.Heartbeat.MemBytes)
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
		}
		s.running++
		s.log.Info("rehydrated run from worker", "run", ar.RunId, "worker", workerID)
	}
}

func (s *Service) workerFinished(f *loadifyv1.RunFinished) {
	s.mu.Lock()
	rs := s.runs[f.RunId]
	if rs == nil {
		s.mu.Unlock()
		return
	}
	rs.finished[f.WorkerId] = true
	done := len(rs.finished) >= len(rs.assigned)
	var cancel context.CancelFunc
	if done && rs.status == loadifyv1.RunStatus_RUN_STATUS_RUNNING {
		rs.status = loadifyv1.RunStatus_RUN_STATUS_COMPLETED
		rs.endedAt = time.Now()
		s.running--
		cancel = rs.aggCancel
		s.drainLocked() // a slot freed: admit queued runs
	}
	s.mu.Unlock()
	if done && cancel != nil {
		s.log.Info("run completed", "run", f.RunId)
		// Give the aggregator a moment to drain late batches, then stop it.
		go func() {
			time.Sleep(3 * time.Second)
			cancel()
		}()
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
		runID:    req.RunId,
		protocol: req.Protocol,
		assigned: make(map[string]bool),
		finished: make(map[string]bool),
		status:   loadifyv1.RunStatus_RUN_STATUS_QUEUED,
	}
	s.log.Info("run queued", "run", req.RunId, "queue_depth", len(s.queue))
	return &loadifyv1.StartRunResponse{RunId: req.RunId, Status: "queued", QueuePosition: int32(len(s.queue))}, nil
}

// canDispatchLocked reports whether a run for proto can start right now.
func (s *Service) canDispatchLocked(proto loadifyv1.Protocol) bool {
	return s.running < s.maxRuns && len(s.reg.Available(proto, s.cpuMax)) > 0
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
			s.log.Warn("drain dispatch failed", "run", req.RunId, "err", err)
			if rs := s.runs[req.RunId]; rs != nil {
				rs.status = loadifyv1.RunStatus_RUN_STATUS_FAILED
			}
		}
	}
}

// StopRun signals all assigned workers to stop a run.
func (s *Service) StopRun(_ context.Context, req *loadifyv1.StopRunRequest) (*loadifyv1.StopRunResponse, error) {
	s.mu.Lock()
	rs := s.runs[req.RunId]
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
	return &loadifyv1.StopRunResponse{RunId: req.RunId}, nil
}

// GetRunState returns the current state of a run.
func (s *Service) GetRunState(_ context.Context, req *loadifyv1.RunStateRequest) (*loadifyv1.RunState, error) {
	s.mu.Lock()
	rs := s.runs[req.RunId]
	s.mu.Unlock()
	if rs == nil {
		return nil, status.Error(codes.NotFound, "run not found")
	}
	var activeVUs int64
	for _, w := range s.reg.List() {
		if rs.assigned[w.WorkerId] {
			activeVUs += w.ActiveVus
		}
	}
	return rs.toProto(activeVUs), nil
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
