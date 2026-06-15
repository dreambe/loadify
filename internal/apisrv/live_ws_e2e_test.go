package apisrv

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/auth"
	"github.com/dreambe/loadify/internal/coordinator"
	"github.com/dreambe/loadify/internal/store"
	"github.com/dreambe/loadify/internal/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type noopWriter struct{}

func (noopWriter) WriteRollups(context.Context, []store.Rollup) error { return nil }

// TestLiveWebSocketE2E drives the exact path the browser uses: a real
// coordinator + worker over gRPC, an apisrv in front, and a WebSocket client
// hitting /api/v1/runs/{id}/live. It asserts live ticks actually reach the
// socket — catching any apisrv-side WS/auth/stream-forwarding regression that
// the coordinator-only e2e test would miss.
func TestLiveWebSocketE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in short mode")
	}

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	// Coordinator gRPC server + a worker.
	svc := coordinator.New(noopWriter{}, nil)
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

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	agent := worker.NewAgent("worker-ws", "test", nil)
	go agent.Run(ctx, conn)

	cc := loadifyv1.NewCoordinatorServiceClient(conn)
	// Wait for the worker to register healthy.
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		resp, lerr := cc.ListWorkers(ctx, &loadifyv1.ListWorkersRequest{})
		if lerr == nil && len(resp.Workers) > 0 && resp.Workers[0].Status == "healthy" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// apisrv in front of the real coordinator client.
	srv := newTestServer(newFakeMeta(), cc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Start a run directly on the coordinator (the run id is all the WS needs).
	runID := "run-ws-e2e"
	planJSON := []byte(`{"protocol":"http","http":{"url":"` + target.URL + `"}}`)
	if _, err = cc.StartRun(ctx, &loadifyv1.StartRunRequest{
		RunId:          runID,
		Protocol:       loadifyv1.Protocol_PROTOCOL_HTTP,
		PlanJson:       planJSON,
		Ramp:           []*loadifyv1.RampStage{{DurationMs: 2000, TargetVus: 5}},
		DesiredWorkers: 1,
	}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Connect a WebSocket through apisrv, exactly like the browser.
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") +
		"/api/v1/runs/" + runID + "/live?token=" + token(t, auth.RoleOperator)
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	var ticks int
	var maxRPS float64
	for {
		var tk liveTick
		if err := wsjson.Read(ctx, c, &tk); err != nil {
			break // stream closed (run finished) or timeout
		}
		ticks++
		if tk.RPS > maxRPS {
			maxRPS = tk.RPS
		}
	}

	if ticks == 0 {
		t.Fatal("received no live ticks over the apisrv WebSocket")
	}
	if maxRPS <= 0 {
		t.Errorf("max RPS over WS = %.1f, want > 0", maxRPS)
	}
	t.Logf("live WS e2e: ticks=%d maxRPS=%.0f", ticks, maxRPS)
}
