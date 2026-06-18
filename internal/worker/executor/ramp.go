package executor

import (
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
)

// Ramp computes the target VU count over time from a sequence of stages.
// Each stage linearly interpolates from the previous stage's target to its own
// target_vus over its duration (a classic ramp-up/steady/ramp-down profile).
type Ramp struct {
	stages []*loadifyv1.RampStage
	total  time.Duration
}

// NewRamp builds a Ramp from stages.
func NewRamp(stages []*loadifyv1.RampStage) *Ramp {
	var total time.Duration
	for _, s := range stages {
		total += time.Duration(s.DurationMs) * time.Millisecond
	}
	return &Ramp{stages: stages, total: total}
}

// Total returns the full ramp duration.
func (r *Ramp) Total() time.Duration { return r.total }

// IsArrival reports whether this ramp drives an open (arrival-rate) model:
// any stage specifies a target request rate.
func (r *Ramp) IsArrival() bool {
	for _, s := range r.stages {
		if s.TargetRps > 0 {
			return true
		}
	}
	return false
}

// PeakRPS returns the highest target rate across stages.
func (r *Ramp) PeakRPS() int64 {
	var peak int64
	for _, s := range r.stages {
		if s.TargetRps > peak {
			peak = s.TargetRps
		}
	}
	return peak
}

// RateAt returns the desired arrival rate (req/s) at elapsed time t, linearly
// interpolating from the previous stage's target_rps (the open-model analogue
// of TargetAt).
func (r *Ramp) RateAt(t time.Duration) float64 {
	if len(r.stages) == 0 {
		return 0
	}
	var prev float64
	var acc time.Duration
	for _, s := range r.stages {
		dur := time.Duration(s.DurationMs) * time.Millisecond
		target := float64(s.TargetRps)
		if t < acc+dur || dur == 0 {
			if dur == 0 {
				return target
			}
			frac := float64(t-acc) / float64(dur)
			if frac < 0 {
				frac = 0
			}
			return prev + frac*(target-prev)
		}
		acc += dur
		prev = target
	}
	return prev
}

// TargetAt returns the desired active VU count at elapsed time t.
func (r *Ramp) TargetAt(t time.Duration) int {
	if len(r.stages) == 0 {
		return 0
	}
	var prevTarget int64
	var acc time.Duration
	for _, s := range r.stages {
		dur := time.Duration(s.DurationMs) * time.Millisecond
		if t < acc+dur || dur == 0 {
			// Interpolate within this stage.
			if dur == 0 {
				return int(s.TargetVus)
			}
			frac := float64(t-acc) / float64(dur)
			if frac < 0 {
				frac = 0
			}
			val := float64(prevTarget) + frac*float64(s.TargetVus-prevTarget)
			return int(val + 0.5)
		}
		acc += dur
		prevTarget = s.TargetVus
	}
	// Past the end: hold the last target.
	return int(prevTarget)
}
