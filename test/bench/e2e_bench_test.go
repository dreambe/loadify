// Package bench measures end-to-end throughput of the full pipeline
// (coordinator + workers over real gRPC + HTTP driver + metrics aggregation)
// against an in-process echo server. It is gated behind LOADIFY_BENCH=1 so CI
// stays fast; numbers depend entirely on the host and are a floor, not a claim.
//
//	LOADIFY_BENCH=1 go test -run TestE2EThroughput -v ./test/bench/
package bench

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
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

type countWriter struct {
	mu    sync.Mutex
	total int64
}

func (m *countWriter) WriteRollups(_ context.Context, rows []store.Rollup) error {
	m.mu.Lock()
	for _, r := range rows {
		m.total += r.Count
	}
	m.mu.Unlock()
	return nil
}

func TestE2EThroughput(t *testing.T) {
	if os.Getenv("LOADIFY_BENCH") != "1" {
		t.Skip("set LOADIFY_BENCH=1 to run the throughput benchmark")
	}

	var hits int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	writer := &countWriter{}
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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const workers = 2
	for i := 0; i < workers; i++ {
		conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		ag := worker.NewAgent("bench-"+string(rune('a'+i)), "bench", nil)
		go ag.Run(ctx, conn)
	}

	ctrl, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()
	cc := loadifyv1.NewCoordinatorServiceClient(ctrl)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := cc.ListWorkers(ctx, &loadifyv1.ListWorkersRequest{}); err == nil && len(resp.Workers) >= workers {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Closed model: 256 VUs hammering for 10s measures the pipeline's ceiling
	// on this host (no think time, keep-alive on).
	const durMs = 10_000
	start := time.Now()
	_, err = cc.StartRun(ctx, &loadifyv1.StartRunRequest{
		RunId:    "bench",
		Protocol: loadifyv1.Protocol_PROTOCOL_HTTP,
		PlanJson: []byte(`{"protocol":"http","http":{"url":"` + target.URL + `"}}`),
		Ramp: []*loadifyv1.RampStage{
			{DurationMs: 1000, TargetVus: 256},
			{DurationMs: durMs - 1000, TargetVus: 256},
		},
		DesiredWorkers: workers,
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	stream, err := cc.StreamLive(ctx, &loadifyv1.LiveRequest{RunId: "bench"})
	if err != nil {
		t.Fatal(err)
	}
	var peak float64
	for {
		tick, rerr := stream.Recv()
		if rerr != nil {
			break
		}
		if tick.Rps > peak {
			peak = tick.Rps
		}
	}
	elapsed := time.Since(start).Seconds()
	total := atomic.LoadInt64(&hits)
	t.Logf("workers=%d vus=256 duration=%.1fs total_requests=%d avg_qps=%.0f peak_qps=%.0f",
		workers, elapsed, total, float64(total)/elapsed, peak)
}
