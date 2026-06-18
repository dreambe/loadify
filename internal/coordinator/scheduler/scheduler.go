// Package scheduler shards a global ramp profile across N workers.
package scheduler

import (
	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
)

// SliceRamp divides a global ramp curve into n per-worker ramps. Each stage's
// target_vus and target_rps are split as evenly as possible; the remainder is
// distributed to the lowest-indexed workers so the global total is exact.
func SliceRamp(global []*loadifyv1.RampStage, n int) [][]*loadifyv1.RampStage {
	if n <= 0 {
		return nil
	}
	out := make([][]*loadifyv1.RampStage, n)
	for i := range out {
		out[i] = make([]*loadifyv1.RampStage, len(global))
	}
	for s, stage := range global {
		vus := splitEven(stage.TargetVus, n)
		rps := splitEven(stage.TargetRps, n)
		for i := 0; i < n; i++ {
			out[i][s] = &loadifyv1.RampStage{
				DurationMs: stage.DurationMs,
				TargetVus:  vus[i],
				TargetRps:  rps[i],
			}
		}
	}
	return out
}

// splitEven divides total into n shares, distributing the remainder to the
// first shares so the sum equals total.
func splitEven(total int64, n int) []int64 {
	res := make([]int64, n)
	if n == 0 {
		return res
	}
	base := total / int64(n)
	rem := total % int64(n)
	for i := 0; i < n; i++ {
		res[i] = base
		if int64(i) < rem {
			res[i]++
		}
	}
	return res
}

// PickWorkers selects up to desired workers from the candidates. If desired is
// <= 0 or exceeds the candidate count, all candidates are returned.
func PickWorkers[T any](candidates []T, desired int) []T {
	if desired <= 0 || desired >= len(candidates) {
		return candidates
	}
	return candidates[:desired]
}
