package coordinator_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/coordinator"
	"github.com/dreambe/loadify/internal/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestBlackHoleTargetAutoStops wires a real worker to a real coordinator and
// points an arrival-rate run at a black-hole target (accepts the request, then
// hangs). It asserts the two behaviours the production incident violated:
//
//  1. Live ticks flow with active_vus>0 while requests are in flight — the run
//     is NOT stuck "awaiting data" with a blank active-VU count.
//  2. The run auto-stops on its own (here via the error-rate breaker once the
//     per-request timeout turns the hung requests into errors), instead of
//     running forever.
func TestBlackHoleTargetAutoStops(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in short mode")
	}

	// Black-hole: hang until the client (worker) cancels the request context.
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer target.Close()

	svc := coordinator.New(nil, nil)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gsrv := grpc.NewServer()
	loadifyv1.RegisterWorkerServiceServer(gsrv, svc)
	loadifyv1.RegisterCoordinatorServiceServer(gsrv, svc)
	go gsrv.Serve(lis)
	defer gsrv.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	agent := worker.NewAgent("worker-bh", "test", nil)
	go agent.Run(ctx, conn)

	cc := loadifyv1.NewCoordinatorServiceClient(conn)
	waitForWorker(t, ctx, cc)

	runID := "run-blackhole"
	// 1s per-request timeout so the hung requests surface as errors quickly; the
	// error-rate breaker (2%) then trips. Arrival model so VUs pile up in flight.
	planJSON := []byte(`{
		"protocol":"http",
		"max_vus":100,
		"auto_stop":{"enabled":true,"error_rate_pct":2},
		"http":{"url":"` + target.URL + `","method":"GET","timeout_ms":1000}
	}`)
	if _, err = cc.StartRun(ctx, &loadifyv1.StartRunRequest{
		RunId:          runID,
		Protocol:       loadifyv1.Protocol_PROTOCOL_HTTP,
		PlanJson:       planJSON,
		Ramp:           []*loadifyv1.RampStage{{TargetRps: 50, DurationMs: 20000}},
		DesiredWorkers: 1,
	}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Observe ticks: we must see active VUs > 0 (worker is executing; the UI's
	// "active VU" comes from these ticks).
	sawActiveVUs := make(chan struct{})
	go func() {
		var stream loadifyv1.CoordinatorService_StreamLiveClient
		for i := 0; i < 30 && stream == nil; i++ {
			stream, _ = cc.StreamLive(ctx, &loadifyv1.LiveRequest{RunId: runID})
			if stream == nil {
				time.Sleep(100 * time.Millisecond)
			}
		}
		if stream == nil {
			return
		}
		closed := false
		for {
			tk, rerr := stream.Recv()
			if rerr != nil {
				return
			}
			if tk.ActiveVus > 0 && !closed {
				close(sawActiveVUs)
				closed = true
			}
		}
	}()

	select {
	case <-sawActiveVUs:
	case <-time.After(12 * time.Second):
		t.Fatal("never observed active_vus>0 — worker not executing / active-VU pipeline broken")
	}

	// The run must terminate on its own (error-rate breaker), not hang forever.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		st, err := cc.GetRunState(ctx, &loadifyv1.RunStateRequest{RunId: runID})
		if err == nil && st.Status == loadifyv1.RunStatus_RUN_STATUS_ABORTED {
			t.Logf("auto-stopped as expected: %q", st.Reason)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("run did not auto-stop against a black-hole target (would hang \"running\" forever)")
}
