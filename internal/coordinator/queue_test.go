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

// TestAdmissionQueuesWhenAtCapacity sets the concurrent-run cap to 1, starts two
// runs, and asserts the second is queued and only dispatched after the first
// completes — i.e. tasks queue instead of piling onto a full cluster.
func TestAdmissionQueuesWhenAtCapacity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in short mode")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	svc := coordinator.New(nil, nil)
	svc.SetLimits(1, 0, 0) // one run at a time

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gsrv := grpc.NewServer()
	loadifyv1.RegisterWorkerServiceServer(gsrv, svc)
	loadifyv1.RegisterCoordinatorServiceServer(gsrv, svc)
	go gsrv.Serve(lis)
	defer gsrv.Stop()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go worker.NewAgent("w1", "test", nil).Run(ctx, conn)

	cc := loadifyv1.NewCoordinatorServiceClient(conn)
	waitForWorker(t, ctx, cc)

	plan := []byte(`{"protocol":"http","http":{"url":"` + target.URL + `"}}`)
	ramp := []*loadifyv1.RampStage{{DurationMs: 1500, TargetVus: 3}}

	respA, err := cc.StartRun(ctx, &loadifyv1.StartRunRequest{RunId: "A", Protocol: loadifyv1.Protocol_PROTOCOL_HTTP, PlanJson: plan, Ramp: ramp, DesiredWorkers: 1})
	if err != nil {
		t.Fatalf("StartRun A: %v", err)
	}
	if respA.Status != "running" {
		t.Fatalf("run A status = %q, want running", respA.Status)
	}
	// Idempotency: re-issuing StartRun for a live run must report the current
	// state, not overwrite it (which would orphan its aggregator goroutine).
	respA2, err := cc.StartRun(ctx, &loadifyv1.StartRunRequest{RunId: "A", Protocol: loadifyv1.Protocol_PROTOCOL_HTTP, PlanJson: plan, Ramp: ramp, DesiredWorkers: 1})
	if err != nil {
		t.Fatalf("StartRun A (repeat): %v", err)
	}
	if respA2.Status != "running" {
		t.Fatalf("repeat StartRun A status = %q, want running (idempotent)", respA2.Status)
	}
	respB, err := cc.StartRun(ctx, &loadifyv1.StartRunRequest{RunId: "B", Protocol: loadifyv1.Protocol_PROTOCOL_HTTP, PlanJson: plan, Ramp: ramp, DesiredWorkers: 1})
	if err != nil {
		t.Fatalf("StartRun B: %v", err)
	}
	if respB.Status != "queued" {
		t.Fatalf("run B status = %q, want queued (cluster at capacity)", respB.Status)
	}

	// B should be dispatched only after A finishes. Poll B until it runs/completes.
	deadline := time.Now().Add(20 * time.Second)
	var bRan bool
	for time.Now().Before(deadline) {
		st, err := cc.GetRunState(ctx, &loadifyv1.RunStateRequest{RunId: "B"})
		if err == nil && (st.Status == loadifyv1.RunStatus_RUN_STATUS_RUNNING || st.Status == loadifyv1.RunStatus_RUN_STATUS_COMPLETED) {
			bRan = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !bRan {
		t.Fatal("queued run B was never dispatched after A freed the slot")
	}
}
