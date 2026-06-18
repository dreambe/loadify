package scheduler

import (
	"testing"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
)

func TestSliceRampPreservesTotals(t *testing.T) {
	global := []*loadifyv1.RampStage{
		{DurationMs: 1000, TargetVus: 10, TargetRps: 100},
		{DurationMs: 2000, TargetVus: 103, TargetRps: 7},
	}
	for _, n := range []int{1, 3, 4, 7} {
		slices := SliceRamp(global, n)
		if len(slices) != n {
			t.Fatalf("n=%d: got %d slices", n, len(slices))
		}
		for s := range global {
			var vus, rps int64
			for i := 0; i < n; i++ {
				vus += slices[i][s].TargetVus
				rps += slices[i][s].TargetRps
				if slices[i][s].DurationMs != global[s].DurationMs {
					t.Fatalf("duration mismatch")
				}
			}
			if vus != global[s].TargetVus {
				t.Errorf("n=%d stage=%d vus sum=%d want %d", n, s, vus, global[s].TargetVus)
			}
			if rps != global[s].TargetRps {
				t.Errorf("n=%d stage=%d rps sum=%d want %d", n, s, rps, global[s].TargetRps)
			}
		}
	}
}

func TestPickWorkers(t *testing.T) {
	c := []int{1, 2, 3, 4, 5}
	if got := PickWorkers(c, 0); len(got) != 5 {
		t.Errorf("0 means all, got %d", len(got))
	}
	if got := PickWorkers(c, 3); len(got) != 3 {
		t.Errorf("want 3, got %d", len(got))
	}
	if got := PickWorkers(c, 10); len(got) != 5 {
		t.Errorf("clamp to all, got %d", len(got))
	}
}
