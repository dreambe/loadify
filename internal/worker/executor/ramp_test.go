package executor

import (
	"testing"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
)

func TestRampInterpolation(t *testing.T) {
	r := NewRamp([]*loadifyv1.RampStage{
		{DurationMs: 1000, TargetVus: 100}, // ramp 0 -> 100 over 1s
		{DurationMs: 1000, TargetVus: 100}, // steady
		{DurationMs: 1000, TargetVus: 0},   // ramp 100 -> 0
	})
	if r.Total() != 3*time.Second {
		t.Fatalf("total = %v", r.Total())
	}
	cases := []struct {
		at   time.Duration
		want int
	}{
		{0, 0},
		{500 * time.Millisecond, 50},
		{1 * time.Second, 100},
		{1500 * time.Millisecond, 100},
		{2500 * time.Millisecond, 50},
		{3 * time.Second, 0},
		{10 * time.Second, 0}, // past end holds last
	}
	for _, c := range cases {
		if got := r.TargetAt(c.at); got != c.want {
			t.Errorf("TargetAt(%v) = %d, want %d", c.at, got, c.want)
		}
	}
}

func TestRampEmpty(t *testing.T) {
	r := NewRamp(nil)
	if r.TargetAt(time.Second) != 0 {
		t.Error("empty ramp should be 0")
	}
}
