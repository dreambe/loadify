package coordinator_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/coordinator"
	"github.com/dreambe/loadify/internal/store"
	"github.com/dreambe/loadify/internal/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// memWriter captures finalized rollups in memory.
type memWriter struct {
	mu   sync.Mutex
	rows []store.Rollup
}

func (m *memWriter) WriteRollups(_ context.Context, rows []store.Rollup) error {
	m.mu.Lock()
	m.rows = append(m.rows, rows...)
	m.mu.Unlock()
	return nil
}

func (m *memWriter) total() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, r := range m.rows {
		n += r.Count
	}
	return n
}

// TestDistributedRunEndToEnd wires a real coordinator + worker over gRPC and
// drives load against a local HTTP server, asserting metrics flow through.
func TestDistributedRunEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in short mode")
	}

	// Target server.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	// Coordinator gRPC server.
	writer := &memWriter{}
	svc := coordinator.New(writer, nil)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gsrv := grpc.NewServer()
	loadifyv1.RegisterWorkerServiceServer(gsrv, svc)
	loadifyv1.RegisterCoordinatorServiceServer(gsrv, svc)
	go gsrv.Serve(lis)
	defer gsrv.Stop()

	// Worker dials the coordinator.
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	agent := worker.NewAgent("worker-e2e", "test", nil)
	go agent.Run(ctx, conn)

	// Control-plane client.
	cc := loadifyv1.NewCoordinatorServiceClient(conn)
	waitForWorker(t, ctx, cc)

	runID := "run-e2e"
	planJSON := []byte(`{"protocol":"http","http":{"url":"` + target.URL + `"}}`)
	_, err = cc.StartRun(ctx, &loadifyv1.StartRunRequest{
		RunId:          runID,
		Protocol:       loadifyv1.Protocol_PROTOCOL_HTTP,
		PlanJson:       planJSON,
		Ramp:           []*loadifyv1.RampStage{{DurationMs: 1500, TargetVus: 5}},
		DesiredWorkers: 1,
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Consume live ticks until the stream closes (run finished).
	stream, err := cc.StreamLive(ctx, &loadifyv1.LiveRequest{RunId: runID})
	if err != nil {
		t.Fatalf("StreamLive: %v", err)
	}
	var maxRPS float64
	var ticks int
	for {
		tick, rerr := stream.Recv()
		if rerr != nil {
			break
		}
		ticks++
		if tick.Rps > maxRPS {
			maxRPS = tick.Rps
		}
	}

	if ticks == 0 {
		t.Fatal("received no live ticks")
	}
	if maxRPS <= 0 {
		t.Errorf("max RPS = %.1f, want > 0", maxRPS)
	}
	if total := writer.total(); total == 0 {
		t.Errorf("no rollups persisted")
	} else {
		t.Logf("ticks=%d maxRPS=%.0f persisted_requests=%d", ticks, maxRPS, total)
	}

	// Run should report completed.
	st, err := cc.GetRunState(ctx, &loadifyv1.RunStateRequest{RunId: runID})
	if err != nil {
		t.Fatalf("GetRunState: %v", err)
	}
	if st.Status != loadifyv1.RunStatus_RUN_STATUS_COMPLETED {
		t.Errorf("status = %v, want COMPLETED", st.Status)
	}
}

func waitForWorker(t *testing.T, ctx context.Context, cc loadifyv1.CoordinatorServiceClient) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := cc.ListWorkers(ctx, &loadifyv1.ListWorkersRequest{})
		if err == nil && len(resp.Workers) > 0 && resp.Workers[0].Status == "healthy" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("worker did not register in time")
}

// TestMultiWorkerDistribution wires several workers to one coordinator and
// asserts the run is sharded across all of them: the coordinator assigns the
// run to every worker, multiple workers generate VU load concurrently (observed
// via the registry's per-worker active-VU counts), and the merged metrics flow
// through to completion.
func TestMultiWorkerDistribution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in short mode")
	}
	const numWorkers = 3

	var hits int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	writer := &memWriter{}
	svc := coordinator.New(writer, nil)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gsrv := grpc.NewServer()
	loadifyv1.RegisterWorkerServiceServer(gsrv, svc)
	loadifyv1.RegisterCoordinatorServiceServer(gsrv, svc)
	go gsrv.Serve(lis)
	defer gsrv.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	// Each worker runs over its own client connection, like separate nodes.
	for i := 0; i < numWorkers; i++ {
		conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		ag := worker.NewAgent(fmt.Sprintf("worker-%d", i), "test", nil)
		go ag.Run(ctx, conn)
	}

	ctrl, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()
	cc := loadifyv1.NewCoordinatorServiceClient(ctrl)
	waitForNWorkers(t, ctx, cc, numWorkers)

	runID := "run-multi"
	planJSON := []byte(`{"protocol":"http","http":{"url":"` + target.URL + `"}}`)
	resp, err := cc.StartRun(ctx, &loadifyv1.StartRunRequest{
		RunId:    runID,
		Protocol: loadifyv1.Protocol_PROTOCOL_HTTP,
		PlanJson: planJSON,
		// 12 global VUs over 3 workers = 4 each; ramp up then hold steady so the
		// run is comfortably long enough for per-worker heartbeats to land.
		Ramp: []*loadifyv1.RampStage{
			{DurationMs: 800, TargetVus: 12},
			{DurationMs: 5000, TargetVus: 12},
		},
		DesiredWorkers: numWorkers,
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if resp.AssignedWorkers != numWorkers {
		t.Fatalf("assigned workers = %d, want %d", resp.AssignedWorkers, numWorkers)
	}

	// While the run is in flight, poll the registry and record the most workers
	// seen actively running VUs at the same time.
	var maxConcurrent int64
	pollDone := make(chan struct{})
	var pollWG sync.WaitGroup
	pollWG.Add(1)
	go func() {
		defer pollWG.Done()
		tk := time.NewTicker(100 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-pollDone:
				return
			case <-tk.C:
				wr, err := cc.ListWorkers(ctx, &loadifyv1.ListWorkersRequest{})
				if err != nil {
					continue
				}
				var active int64
				for _, w := range wr.Workers {
					if w.ActiveVus > 0 {
						active++
					}
				}
				for {
					cur := atomic.LoadInt64(&maxConcurrent)
					if active <= cur || atomic.CompareAndSwapInt64(&maxConcurrent, cur, active) {
						break
					}
				}
			}
		}
	}()

	// Consume live ticks until the run finishes.
	stream, err := cc.StreamLive(ctx, &loadifyv1.LiveRequest{RunId: runID})
	if err != nil {
		t.Fatalf("StreamLive: %v", err)
	}
	var ticks int
	for {
		if _, rerr := stream.Recv(); rerr != nil {
			break
		}
		ticks++
	}
	close(pollDone)
	pollWG.Wait()

	mc := atomic.LoadInt64(&maxConcurrent)
	if mc < 2 {
		t.Errorf("max concurrently-active workers = %d, want >= 2 (load not distributed)", mc)
	}
	if writer.total() == 0 {
		t.Error("no rollups persisted")
	}
	if atomic.LoadInt64(&hits) == 0 {
		t.Error("target received no requests")
	}
	st, err := cc.GetRunState(ctx, &loadifyv1.RunStateRequest{RunId: runID})
	if err != nil {
		t.Fatalf("GetRunState: %v", err)
	}
	if st.Status != loadifyv1.RunStatus_RUN_STATUS_COMPLETED {
		t.Errorf("status = %v, want COMPLETED", st.Status)
	}
	t.Logf("workers=%d maxConcurrentActive=%d ticks=%d hits=%d persisted=%d",
		numWorkers, mc, ticks, atomic.LoadInt64(&hits), writer.total())
}

// waitForNWorkers blocks until at least n healthy workers are registered.
func waitForNWorkers(t *testing.T, ctx context.Context, cc loadifyv1.CoordinatorServiceClient, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := cc.ListWorkers(ctx, &loadifyv1.ListWorkersRequest{})
		if err == nil {
			healthy := 0
			for _, w := range resp.Workers {
				if w.Status == "healthy" {
					healthy++
				}
			}
			if healthy >= n {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("fewer than %d workers registered in time", n)
}
