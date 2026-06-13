package executor

import (
	"os"
	"strconv"
)

// defaultMaxVUsPerWorker is the hard ceiling on a single worker's VU pool when
// LOADIFY_MAX_VUS_PER_WORKER is unset. It exists so a runaway plan (e.g. a ramp
// to a million VUs) clamps to a survivable pool instead of OOM-ing the worker.
const defaultMaxVUsPerWorker = 5000

// maxVUsPerWorker reads the per-worker VU ceiling from the environment, falling
// back to defaultMaxVUsPerWorker. A non-positive override disables the cap.
func maxVUsPerWorker() int {
	v := os.Getenv("LOADIFY_MAX_VUS_PER_WORKER")
	if v == "" {
		return defaultMaxVUsPerWorker
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultMaxVUsPerWorker
	}
	return n // <=0 means unlimited
}

// clampVUs applies the ceiling (cap <= 0 means unlimited).
func clampVUs(target, cap int) int {
	if cap > 0 && target > cap {
		return cap
	}
	return target
}
