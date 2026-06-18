package apisrv

import (
	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/plan"
)

// alertEvaluator watches the live tick stream and reports the first moment the
// trailing-window error rate crosses the alert threshold, so apisrv can fire a
// one-shot mid-run notification. It fires at most once per run.
type alertEvaluator struct {
	enabled bool
	thresh  float64 // error-rate fraction
	window  int     // trailing ticks (~seconds)
	minReq  float64
	fired   bool
	buf     []tickSample
}

type tickSample struct{ reqs, errs float64 }

func newAlertEvaluator(c plan.AlertConfig) *alertEvaluator {
	w := c.WindowSec
	if w <= 0 {
		w = 10
	}
	return &alertEvaluator{
		enabled: c.AlertEnabled(),
		thresh:  c.ErrorRatePct / 100,
		window:  w,
		minReq:  float64(c.MinRequests),
	}
}

// observe folds one tick into the trailing window and returns (rate, true) the
// first time the windowed error rate crosses the threshold with enough volume.
func (e *alertEvaluator) observe(t *loadifyv1.LiveTick) (float64, bool) {
	if e == nil || !e.enabled || e.fired || t == nil {
		return 0, false
	}
	// Each tick is ~1s, so rps approximates that second's request count.
	reqs := t.GetRps()
	e.buf = append(e.buf, tickSample{reqs: reqs, errs: reqs * t.GetErrorRate()})
	if len(e.buf) > e.window {
		e.buf = e.buf[len(e.buf)-e.window:]
	}
	var sr, se float64
	for _, s := range e.buf {
		sr += s.reqs
		se += s.errs
	}
	if sr < e.minReq || sr == 0 {
		return 0, false
	}
	rate := se / sr
	if rate >= e.thresh && e.thresh > 0 {
		e.fired = true
		return rate, true
	}
	return 0, false
}
