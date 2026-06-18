package apisrv

import (
	"testing"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
)

func tick(rps, errRate float64) *loadifyv1.LiveTick {
	return &loadifyv1.LiveTick{Rps: rps, ErrorRate: errRate}
}

func TestAlertEvaluator(t *testing.T) {
	cfg := plan.AlertConfig{ErrorRatePct: 30, WindowSec: 5, MinRequests: 20}

	t.Run("fires once when windowed rate crosses threshold", func(t *testing.T) {
		e := newAlertEvaluator((&plan.Plan{Alert: &cfg}).AlertOrDefault())
		// Below volume: no fire even at high error rate.
		if _, fire := e.observe(tick(5, 0.9)); fire {
			t.Fatal("fired below min_requests")
		}
		// Enough volume, error rate well over 30% → fires.
		var fires int
		for i := 0; i < 5; i++ {
			if _, f := e.observe(tick(50, 0.5)); f {
				fires++
			}
		}
		if fires != 1 {
			t.Fatalf("expected exactly one fire, got %d", fires)
		}
	})

	t.Run("does not fire below threshold", func(t *testing.T) {
		e := newAlertEvaluator((&plan.Plan{Alert: &cfg}).AlertOrDefault())
		for i := 0; i < 10; i++ {
			if _, f := e.observe(tick(100, 0.05)); f {
				t.Fatal("fired below threshold")
			}
		}
	})

	t.Run("disabled never fires", func(t *testing.T) {
		off := false
		e := newAlertEvaluator((&plan.Plan{Alert: &plan.AlertConfig{Enabled: &off}}).AlertOrDefault())
		for i := 0; i < 10; i++ {
			if _, f := e.observe(tick(100, 0.99)); f {
				t.Fatal("disabled alert fired")
			}
		}
	})
}
