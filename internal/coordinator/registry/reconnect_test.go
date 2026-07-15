package registry

import (
	"testing"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
)

// TestRemoveIsIdentityChecked guards the reconnect race: a worker whose stream
// broke and immediately reconnected (a new Add) must NOT be evicted when the
// stale stream's teardown fires afterwards. Before the fix, Remove deleted
// unconditionally, so the live reconnected worker vanished from the cluster
// while still connected and heartbeating — orphaning any run assigned to it.
func TestRemoveIsIdentityChecked(t *testing.T) {
	r := New(time.Hour)
	reg := &loadifyv1.RegisterRequest{
		WorkerId:  "w1",
		Supported: []loadifyv1.Protocol{loadifyv1.Protocol_PROTOCOL_HTTP},
	}
	sendA := make(chan *loadifyv1.CoordinatorMessage, 1) // stream A
	sendB := make(chan *loadifyv1.CoordinatorMessage, 1) // stream B (reconnect)

	r.Add(reg, sendA)
	r.Add(reg, sendB) // reconnect replaces the handle with stream B

	// Stale stream A tears down and tries to remove — must be a no-op because it
	// no longer owns the registry entry.
	if r.Remove("w1", sendA) {
		t.Fatal("stale stream A removed the live worker (reconnect race)")
	}
	if _, ok := r.Get("w1"); !ok {
		t.Fatal("worker vanished after a stale stream's teardown")
	}

	// The live stream B's own teardown removes it.
	if !r.Remove("w1", sendB) {
		t.Fatal("live stream B failed to remove its own entry")
	}
	if _, ok := r.Get("w1"); ok {
		t.Fatal("worker still present after its owning stream's teardown")
	}
}
