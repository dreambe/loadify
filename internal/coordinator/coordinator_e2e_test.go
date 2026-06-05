package coordinator_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
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
