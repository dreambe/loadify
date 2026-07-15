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

// TestStopDoesNotDropWorker proves that manually stopping a run does NOT take
// the executing worker offline: after StopRun the run terminalizes to ABORTED
// while the worker stays registered and healthy (its stream and heartbeats are
// untouched — Stop only cancels the run). The "node went offline when I stopped"
// symptom came from the reconnect race in the registry, not the stop path.
func TestStopDoesNotDropWorker(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in short mode")
	}

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
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
	agent := worker.NewAgent("worker-stop", "test", nil)
	go agent.Run(ctx, conn)

	cc := loadifyv1.NewCoordinatorServiceClient(conn)
	waitForWorker(t, ctx, cc)

	runID := "run-stop"
	// A long-ish arrival run so it's mid-flight when we stop it.
	planJSON := []byte(`{"protocol":"http","http":{"url":"` + target.URL + `"}}`)
	if _, err = cc.StartRun(ctx, &loadifyv1.StartRunRequest{
		RunId:          runID,
		Protocol:       loadifyv1.Protocol_PROTOCOL_HTTP,
		PlanJson:       planJSON,
		Ramp:           []*loadifyv1.RampStage{{TargetRps: 20, DurationMs: 20000}},
		DesiredWorkers: 1,
	}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Let the run get going.
	time.Sleep(3 * time.Second)
	if _, err = cc.StopRun(ctx, &loadifyv1.StopRunRequest{RunId: runID, Graceful: true}); err != nil {
		t.Fatalf("StopRun: %v", err)
	}

	// After the stop: the run must terminalize, and the worker must remain
	// registered and healthy the whole time (never flip offline).
	var aborted bool
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		wr, err := cc.ListWorkers(ctx, &loadifyv1.ListWorkersRequest{})
		if err != nil {
			t.Fatalf("ListWorkers: %v", err)
		}
		if len(wr.Workers) != 1 {
			t.Fatalf("worker count = %d, want 1 (worker dropped after stop)", len(wr.Workers))
		}
		if wr.Workers[0].Status != "healthy" {
			t.Fatalf("worker status = %q after stop, want healthy (node went offline on stop)", wr.Workers[0].Status)
		}
		st, err := cc.GetRunState(ctx, &loadifyv1.RunStateRequest{RunId: runID})
		if err == nil && st.Status == loadifyv1.RunStatus_RUN_STATUS_ABORTED {
			aborted = true
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if !aborted {
		t.Fatal("run did not become ABORTED after stop")
	}
	// One final check: worker still healthy after the run finalized.
	wr, _ := cc.ListWorkers(ctx, &loadifyv1.ListWorkersRequest{})
	if len(wr.Workers) != 1 || wr.Workers[0].Status != "healthy" {
		t.Fatalf("worker not healthy after run finalized: %+v", wr.Workers)
	}
}
