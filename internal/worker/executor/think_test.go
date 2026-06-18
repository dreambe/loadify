package executor

import (
	"math/rand"
	"testing"
	"time"

	"github.com/dreambe/loadify/internal/plan"
)

func TestThinkerDistributions(t *testing.T) {
	rng := rand.New(rand.NewSource(1))

	// Fixed fallback (no config).
	if d := newThinker(250*time.Millisecond, nil).next(rng); d != 250*time.Millisecond {
		t.Errorf("fixed fallback = %v, want 250ms", d)
	}

	// Uniform stays within [min,max].
	u := newThinker(0, &plan.ThinkTimeConfig{Distribution: "uniform", MinMs: 100, MaxMs: 300})
	for i := 0; i < 1000; i++ {
		d := u.next(rng)
		if d < 100*time.Millisecond || d > 300*time.Millisecond {
			t.Fatalf("uniform out of range: %v", d)
		}
	}

	// Gaussian is never negative and averages near the mean.
	g := newThinker(0, &plan.ThinkTimeConfig{Distribution: "gaussian", MeanMs: 200, StddevMs: 50})
	var sum time.Duration
	const n = 5000
	for i := 0; i < n; i++ {
		d := g.next(rng)
		if d < 0 {
			t.Fatalf("gaussian negative: %v", d)
		}
		sum += d
	}
	avg := sum / n
	if avg < 150*time.Millisecond || avg > 250*time.Millisecond {
		t.Errorf("gaussian mean = %v, want ~200ms", avg)
	}

	// any() reflects whether a pause ever applies.
	if newThinker(0, nil).any() {
		t.Error("no think time should report any()=false")
	}
	if !u.any() {
		t.Error("uniform should report any()=true")
	}
}
