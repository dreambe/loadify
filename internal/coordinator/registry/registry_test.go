package registry

import (
	"testing"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
)

// TestAvailableCPUNormalized verifies the CPU gate compares against total-
// capacity utilization, not the raw per-core CPUPct (which can exceed 100 on a
// multi-core box). A worker at 200% of one core on 8 cores is only 25% utilized
// and must stay eligible under an 85% threshold.
func TestAvailableCPUNormalized(t *testing.T) {
	r := New(time.Hour)
	add := func(id string, cores int32, cpu float64) {
		w := r.Add(&loadifyv1.RegisterRequest{
			WorkerId:  id,
			CpuCores:  cores,
			Supported: []loadifyv1.Protocol{loadifyv1.Protocol_PROTOCOL_HTTP},
		}, make(chan *loadifyv1.CoordinatorMessage, 1))
		w.CPUPct = cpu
	}
	add("busy-1core", 1, 90)   // 90% of its one core -> over 85
	add("light-8core", 8, 200) // 200%/8 = 25% -> under 85
	add("hot-8core", 8, 720)   // 720%/8 = 90% -> over 85

	avail := r.Available(loadifyv1.Protocol_PROTOCOL_HTTP, 85, 0)
	got := map[string]bool{}
	for _, w := range avail {
		got[w.ID] = true
	}
	if got["busy-1core"] {
		t.Error("busy-1core (90%) should be excluded")
	}
	if !got["light-8core"] {
		t.Error("light-8core (25% utilized) should be available")
	}
	if got["hot-8core"] {
		t.Error("hot-8core (90% utilized) should be excluded")
	}

	// A zero threshold disables the gate: all healthy workers are available.
	if len(r.Available(loadifyv1.Protocol_PROTOCOL_HTTP, 0, 0)) != 3 {
		t.Error("cpuMax=0 should disable the CPU gate")
	}
}

// TestAvailableMemGate verifies a worker at/above the memory threshold takes no
// new runs, a worker not reporting memory (MemTotalBytes==0) is never excluded,
// and memMax=0 disables the gate.
func TestAvailableMemGate(t *testing.T) {
	r := New(time.Hour)
	add := func(id string, used, total int64) {
		w := r.Add(&loadifyv1.RegisterRequest{
			WorkerId:  id,
			Supported: []loadifyv1.Protocol{loadifyv1.Protocol_PROTOCOL_HTTP},
		}, make(chan *loadifyv1.CoordinatorMessage, 1))
		w.MemBytes = used
		w.MemTotalBytes = total
	}
	add("stressed", 90, 100) // 90% -> over 85, excluded
	add("roomy", 40, 100)    // 40% -> under 85, available
	add("no-report", 0, 0)   // no mem data -> never excluded

	avail := map[string]bool{}
	for _, w := range r.Available(loadifyv1.Protocol_PROTOCOL_HTTP, 0 /*cpu off*/, 85) {
		avail[w.ID] = true
	}
	if avail["stressed"] {
		t.Error("stressed (90% mem) should be excluded")
	}
	if !avail["roomy"] {
		t.Error("roomy (40% mem) should be available")
	}
	if !avail["no-report"] {
		t.Error("a worker not reporting memory must not be excluded by the mem gate")
	}
	if len(r.Available(loadifyv1.Protocol_PROTOCOL_HTTP, 0, 0)) != 3 {
		t.Error("memMax=0 should disable the memory gate")
	}
}
