package coordinator

import (
	"testing"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
)

// TestRehydrateRebuildsRunState verifies a (re)connecting worker's active runs
// rebuild coordinator state so a restart doesn't strand in-flight runs.
func TestRehydrateRebuildsRunState(t *testing.T) {
	svc := New(nil, nil)

	// Coordinator has no knowledge of run-x (as if freshly restarted).
	if _, err := svc.GetRunState(nil, &loadifyv1.RunStateRequest{RunId: "run-x"}); err == nil {
		t.Fatal("expected run-x to be unknown before rehydrate")
	}

	svc.rehydrate("worker-1", []*loadifyv1.ActiveRun{
		{RunId: "run-x", Protocol: loadifyv1.Protocol_PROTOCOL_HTTP},
	})

	st, err := svc.GetRunState(nil, &loadifyv1.RunStateRequest{RunId: "run-x"})
	if err != nil {
		t.Fatalf("run-x should exist after rehydrate: %v", err)
	}
	if st.Status != loadifyv1.RunStatus_RUN_STATUS_RUNNING {
		t.Errorf("status = %v, want RUNNING", st.Status)
	}

	// Metrics for the rehydrated run are now accepted (not dropped).
	svc.ingest(&loadifyv1.MetricBatch{
		RunId: "run-x", WorkerId: "worker-1",
		Agg: []*loadifyv1.AggSlice{{Group: "default", StatusClass: "2xx", Count: 5}},
	})

	// A second worker reporting the same run just joins it (no duplicate state).
	svc.rehydrate("worker-2", []*loadifyv1.ActiveRun{
		{RunId: "run-x", Protocol: loadifyv1.Protocol_PROTOCOL_HTTP},
	})
	svc.mu.Lock()
	assigned := len(svc.runs["run-x"].assigned)
	svc.mu.Unlock()
	if assigned != 2 {
		t.Errorf("assigned workers = %d, want 2", assigned)
	}
}
