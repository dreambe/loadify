package grpcd_test

import (
	"context"
	"net"
	"testing"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/worker/protocols"
	_ "github.com/dreambe/loadify/internal/worker/protocols/grpcd"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// startHealthServer runs a gRPC server exposing the standard health service,
// whose descriptors live in the global registry — so the driver can resolve the
// method dynamically without a user-supplied descriptor set.
func startHealthServer(t *testing.T) (addr string, stop func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	hs := health.NewServer()
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, hs)
	go func() { _ = srv.Serve(lis) }()
	return lis.Addr().String(), srv.Stop
}

func TestGRPCDriverHealthCheck(t *testing.T) {
	addr, stop := startHealthServer(t)
	defer stop()

	p, err := plan.Parse([]byte(`{"protocol":"grpc","grpc":{` +
		`"target":"` + addr + `",` +
		`"full_method":"/grpc.health.v1.Health/Check",` +
		`"request_json":"{}",` +
		`"plaintext":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	drv, err := protocols.New(loadifyv1.Protocol_PROTOCOL_GRPC, p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := drv.Prepare(ctx); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer drv.Teardown(context.Background())

	for i := 0; i < 3; i++ {
		res := drv.Exec(ctx, &protocols.VU{ID: 1, Iteration: int64(i)})
		if !res.OK {
			t.Fatalf("iter %d not ok: kind=%q status=%d", i, res.ErrorKind, res.Status)
		}
		if res.LatencyUs <= 0 {
			t.Errorf("iter %d: expected positive latency", i)
		}
	}
}

func TestGRPCDriverUnknownMethod(t *testing.T) {
	p, err := plan.Parse([]byte(`{"protocol":"grpc","grpc":{` +
		`"target":"127.0.0.1:1","full_method":"/no.Such/Method","plaintext":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	drv, err := protocols.New(loadifyv1.Protocol_PROTOCOL_GRPC, p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := drv.Prepare(ctx); err == nil {
		drv.Teardown(context.Background())
		t.Fatal("expected Prepare to fail resolving an unknown method")
	}
}
