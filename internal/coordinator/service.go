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

	mu   sync.Mutex
	runs map[string]*runState
}

// New creates a coordinator Service. writer may be nil (rollups discarded).
func New(writer store.RollupWriter, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		reg:    registry.New(6 * time.Second),
		writer: writer,
		log:    log,
		runs:   make(map[string]*runState),
	}
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
			send <- &loadifyv1.CoordinatorMessage{Msg: &loadifyv1.CoordinatorMessage_RegisterAck{
				RegisterAck: &loadifyv1.RegisterAck{LeaseId: workerID, HeartbeatIntervalMs: 2000},
			}}
		case *loadifyv1.WorkerMessage_Heartbeat:
			s.reg.Touch(m.Heartbeat.WorkerId, m.Heartbeat.ActiveVus)
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

func (s *Service) workerFinished(f *loadifyv1.RunFinished) {
	s.mu.Lock()
	rs := s.runs[f.RunId]
	if rs == nil {
		s.mu.Unlock()
		return
	}
	rs.finished[f.WorkerId] = true
	done := len(rs.finished) >= len(rs.assigned)
	if done && rs.status == loadifyv1.RunStatus_RUN_STATUS_RUNNING {
		rs.status = loadifyv1.RunStatus_RUN_STATUS_COMPLETED
		rs.endedAt = time.Now()
	}
	cancel := rs.aggCancel
	s.mu.Unlock()
	if done {
		s.log.Info("run completed", "run", f.RunId)
		// Give the aggregator a moment to drain late batches, then stop it.
		go func() {
			time.Sleep(3 * time.Second)
			cancel()
		}()
	}
}

// --- CoordinatorService ---

// StartRun selects workers, slices the ramp and dispatches assignments.
func (s *Service) StartRun(ctx context.Context, req *loadifyv1.StartRunRequest) (*loadifyv1.StartRunResponse, error) {
	if req.RunId == "" {
		return nil, status.Error(codes.InvalidArgument, "run_id required")
	}
	candidates := s.reg.Healthy(req.Protocol)
	if len(candidates) == 0 {
		return nil, status.Error(codes.FailedPrecondition, "no healthy workers available")
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
		return nil, status.Error(codes.Unavailable, "failed to dispatch to any worker")
	}

	s.mu.Lock()
	s.runs[req.RunId] = rs
	s.mu.Unlock()

	s.log.Info("run started", "run", req.RunId, "workers", len(rs.assigned))
	return &loadifyv1.StartRunResponse{RunId: req.RunId, AssignedWorkers: int32(len(rs.assigned))}, nil
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
