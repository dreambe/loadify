package coordinator

import (
	"context"
	"testing"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
)

// addWorker registers a fake HTTP-capable worker with a buffered send channel so
// dispatch never blocks in tests.
func addWorker(s *Service, id string) {
	s.reg.Add(&loadifyv1.RegisterRequest{
		WorkerId:  id,
		Supported: []loadifyv1.Protocol{loadifyv1.Protocol_PROTOCOL_HTTP},
		CpuCores:  4,
	}, make(chan *loadifyv1.CoordinatorMessage, 8))
}

func TestRampDurationMs(t *testing.T) {
	got := rampDurationMs([]*loadifyv1.RampStage{{DurationMs: 1000}, {DurationMs: 2500}})
	if got != 3500 {
		t.Fatalf("rampDurationMs = %d, want 3500", got)
	}
	if rampDurationMs(nil) != 0 {
		t.Fatal("nil ramp should be 0")
	}
}

// TestCapacityAndQueueETA drives admission directly: one slot, one worker. The
// first run dispatches and the cluster reports full; the second queues with
// position 1 and a non-zero ETA derived from the first run's planned duration.
func TestCapacityAndQueueETA(t *testing.T) {
	s := New(nil, nil)
	s.SetLimits(1, 0) // one concurrent run, CPU gate off
	addWorker(s, "w1")

	ctx := context.Background()
	ramp := []*loadifyv1.RampStage{{DurationMs: 60000, TargetVus: 5}}
	plan := []byte(`{"protocol":"http","http":{"url":"http://x"}}`)

	cap0, _ := s.GetCapacity(ctx, &loadifyv1.CapacityRequest{})
	if !cap0.CanAccept || cap0.WorkersAvailable != 1 || cap0.WorkersTotal != 1 {
		t.Fatalf("empty cluster should accept: %+v", cap0)
	}

	respA, err := s.StartRun(ctx, &loadifyv1.StartRunRequest{RunId: "A", Protocol: loadifyv1.Protocol_PROTOCOL_HTTP, PlanJson: plan, Ramp: ramp, DesiredWorkers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if respA.Status != "running" {
		t.Fatalf("A status = %q, want running", respA.Status)
	}

	cap1, _ := s.GetCapacity(ctx, &loadifyv1.CapacityRequest{})
	if cap1.CanAccept || cap1.Running != 1 {
		t.Fatalf("cluster should be full: %+v", cap1)
	}

	respB, err := s.StartRun(ctx, &loadifyv1.StartRunRequest{RunId: "B", Protocol: loadifyv1.Protocol_PROTOCOL_HTTP, PlanJson: plan, Ramp: ramp, DesiredWorkers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if respB.Status != "queued" || respB.QueuePosition != 1 {
		t.Fatalf("B = %q pos %d, want queued pos 1", respB.Status, respB.QueuePosition)
	}

	st, err := s.GetRunState(ctx, &loadifyv1.RunStateRequest{RunId: "B"})
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != loadifyv1.RunStatus_RUN_STATUS_QUEUED || st.QueuePosition != 1 {
		t.Fatalf("B state pos = %d status %v", st.QueuePosition, st.Status)
	}
	if st.QueueEtaMs <= 0 || st.QueueEtaMs > 60000 {
		t.Fatalf("B ETA = %d ms, want (0, 60000]", st.QueueEtaMs)
	}
}
