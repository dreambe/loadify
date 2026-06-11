// Package worker implements the load-generation agent: it dials the
// coordinator, registers, accepts run assignments and streams metrics back.
package worker

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/script"
	"github.com/dreambe/loadify/internal/sysstat"
	"github.com/dreambe/loadify/internal/worker/executor"
	"github.com/dreambe/loadify/internal/worker/protocols"
	_ "github.com/dreambe/loadify/internal/worker/protocols/grpcd" // register gRPC driver
	_ "github.com/dreambe/loadify/internal/worker/protocols/httpd" // register HTTP/HTTPS drivers
	_ "github.com/dreambe/loadify/internal/worker/protocols/ssed"  // register SSE driver
	_ "github.com/dreambe/loadify/internal/worker/protocols/wsd"   // register WebSocket driver
	"github.com/dreambe/loadify/internal/worker/sampler"
	"google.golang.org/grpc"
)

// Agent is a load-generation worker.
type Agent struct {
	workerID string
	region   string
	log      *slog.Logger

	sendCh chan *loadifyv1.WorkerMessage

	mu        sync.Mutex
	runs      map[string]context.CancelFunc
	runProtos map[string]loadifyv1.Protocol
	active    int64
}

// NewAgent creates an Agent.
func NewAgent(workerID, region string, log *slog.Logger) *Agent {
	if log == nil {
		log = slog.Default()
	}
	return &Agent{
		workerID: workerID,
		region:   region,
		log:      log,
		sendCh:    make(chan *loadifyv1.WorkerMessage, 256),
		runs:      make(map[string]context.CancelFunc),
		runProtos: make(map[string]loadifyv1.Protocol),
	}
}

// Run connects to the coordinator and serves assignments until ctx is done.
// It reconnects with backoff on stream errors.
func (a *Agent) Run(ctx context.Context, conn *grpc.ClientConn) error {
	client := loadifyv1.NewWorkerServiceClient(conn)
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := a.session(ctx, client)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.log.Warn("coordinator session ended, reconnecting", "err", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

// session runs one Connect stream lifecycle.
func (a *Agent) session(ctx context.Context, client loadifyv1.WorkerServiceClient) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := client.Connect(ctx)
	if err != nil {
		return err
	}

	// Single sender goroutine owns Send (gRPC streams allow one concurrent Send).
	var senderWG sync.WaitGroup
	senderWG.Add(1)
	go func() {
		defer senderWG.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-a.sendCh:
				if err := stream.Send(msg); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	a.enqueue(&loadifyv1.WorkerMessage{Msg: &loadifyv1.WorkerMessage_Register{Register: &loadifyv1.RegisterRequest{
		WorkerId:  a.workerID,
		Region:    a.region,
		CpuCores:  int32(runtime.NumCPU()),
		Supported: []loadifyv1.Protocol{
			loadifyv1.Protocol_PROTOCOL_HTTP,
			loadifyv1.Protocol_PROTOCOL_HTTPS,
			loadifyv1.Protocol_PROTOCOL_GRPC,
			loadifyv1.Protocol_PROTOCOL_WEBSOCKET,
			loadifyv1.Protocol_PROTOCOL_SSE,
		},
		ActiveRuns: a.activeRuns(),
	}}})

	// Heartbeats.
	go a.heartbeatLoop(ctx)

	for {
		msg, err := stream.Recv()
		if err != nil {
			cancel()
			senderWG.Wait()
			return err
		}
		a.handle(ctx, msg)
	}
}

func (a *Agent) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	cpu := sysstat.NewCPUSampler()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.enqueue(&loadifyv1.WorkerMessage{Msg: &loadifyv1.WorkerMessage_Heartbeat{Heartbeat: &loadifyv1.HeartbeatRequest{
				WorkerId:  a.workerID,
				ActiveVus: a.activeVUs(),
				CpuPct:    cpu.Sample(),
				MemBytes:  sysstat.MemBytes(),
			}}})
		}
	}
}

func (a *Agent) handle(ctx context.Context, msg *loadifyv1.CoordinatorMessage) {
	switch m := msg.Msg.(type) {
	case *loadifyv1.CoordinatorMessage_RegisterAck:
		a.log.Info("registered with coordinator", "lease", m.RegisterAck.LeaseId)
	case *loadifyv1.CoordinatorMessage_Assignment:
		go a.startRun(ctx, m.Assignment)
	case *loadifyv1.CoordinatorMessage_Stop:
		a.stopRun(m.Stop.RunId)
	}
}

func (a *Agent) startRun(parent context.Context, asg *loadifyv1.RunAssignment) {
	log := a.log.With("run", asg.RunId)
	p, err := plan.Parse(asg.PlanJson)
	if err != nil {
		log.Error("invalid plan", "err", err)
		return
	}
	var drv protocols.Driver
	if asg.Script != nil && asg.Script.MainJs != "" {
		drv, err = script.New(asg.Script, p, asg.Protocol)
	} else {
		drv, err = protocols.New(asg.Protocol, p)
	}
	if err != nil {
		log.Error("driver init failed", "err", err)
		return
	}

	smp := sampler.New(asg.RunId, a.workerID, asg.Protocol)
	ramp := executor.NewRamp(asg.Ramp)
	// An open (arrival-rate) ramp drives a target req/s; otherwise scale VUs.
	var exec interface {
		Run(context.Context) error
	}
	if ramp.IsArrival() {
		exec = executor.NewArrival(executor.ArrivalConfig{
			Driver:  drv,
			Ramp:    ramp,
			Sampler: smp,
			MaxVUs:  p.MaxVUs,
			Logger:  log,
		})
	} else {
		exec = executor.New(executor.Config{
			Driver:    drv,
			Ramp:      ramp,
			Sampler:   smp,
			ThinkTime: p.ThinkTime(),
			Logger:    log,
		})
	}

	runCtx, cancel := context.WithCancel(parent)
	a.mu.Lock()
	a.runs[asg.RunId] = cancel
	a.runProtos[asg.RunId] = asg.Protocol
	a.mu.Unlock()

	// Honor a synchronized start time across workers.
	if asg.StartAtUnixMs > 0 {
		wait := time.Until(time.UnixMilli(asg.StartAtUnixMs))
		if wait > 0 {
			select {
			case <-runCtx.Done():
				a.finishRun(asg.RunId)
				return
			case <-time.After(wait):
			}
		}
	}

	// Flush metric batches every second.
	flushDone := make(chan struct{})
	go a.flushLoop(runCtx, smp, flushDone)

	log.Info("run started", "protocol", asg.Protocol)
	if err := exec.Run(runCtx); err != nil && runCtx.Err() == nil {
		log.Error("executor error", "err", err)
	}
	close(flushDone)
	// Final flush.
	a.enqueue(&loadifyv1.WorkerMessage{Msg: &loadifyv1.WorkerMessage_Metrics{Metrics: smp.Flush(time.Now())}})
	a.enqueue(&loadifyv1.WorkerMessage{Msg: &loadifyv1.WorkerMessage_Finished{Finished: &loadifyv1.RunFinished{
		RunId:    asg.RunId,
		WorkerId: a.workerID,
	}}})
	a.finishRun(asg.RunId)
	log.Info("run finished")
}

func (a *Agent) flushLoop(ctx context.Context, smp *sampler.Sampler, done chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case now := <-t.C:
			if b := smp.Flush(now); b != nil {
				a.setActive(b.ActiveVus)
				a.enqueue(&loadifyv1.WorkerMessage{Msg: &loadifyv1.WorkerMessage_Metrics{Metrics: b}})
			}
		}
	}
}

func (a *Agent) stopRun(runID string) {
	a.mu.Lock()
	cancel := a.runs[runID]
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// activeRuns snapshots the runs this worker is currently executing, for
// reporting on (re)register so a restarted coordinator can rehydrate.
func (a *Agent) activeRuns() []*loadifyv1.ActiveRun {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*loadifyv1.ActiveRun, 0, len(a.runProtos))
	for id, proto := range a.runProtos {
		out = append(out, &loadifyv1.ActiveRun{RunId: id, Protocol: proto})
	}
	return out
}

func (a *Agent) finishRun(runID string) {
	a.mu.Lock()
	delete(a.runs, runID)
	delete(a.runProtos, runID)
	a.mu.Unlock()
}

func (a *Agent) enqueue(msg *loadifyv1.WorkerMessage) {
	select {
	case a.sendCh <- msg:
	default:
		a.log.Warn("send buffer full, dropping message")
	}
}

func (a *Agent) setActive(n int64) {
	a.mu.Lock()
	a.active = n
	a.mu.Unlock()
}

func (a *Agent) activeVUs() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.active
}
