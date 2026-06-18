package executor

import (
	"math"
	"math/rand"
	"time"

	"github.com/dreambe/loadify/internal/plan"
)

// thinker yields per-iteration think-time durations. A nil ThinkTimeConfig
// falls back to a fixed pause (the legacy ThinkTimeMs behavior).
type thinker struct {
	cfg   *plan.ThinkTimeConfig
	fixed time.Duration
}

func newThinker(fixed time.Duration, cfg *plan.ThinkTimeConfig) *thinker {
	return &thinker{cfg: cfg, fixed: fixed}
}

// any reports whether a pause is ever applied (lets the hot loop skip the timer
// entirely when there's no think time).
func (t *thinker) any() bool {
	if t.cfg == nil {
		return t.fixed > 0
	}
	switch t.cfg.Distribution {
	case "uniform":
		return t.cfg.MaxMs > 0
	case "gaussian", "poisson":
		return t.cfg.MeanMs > 0
	default: // fixed
		return t.cfg.MinMs > 0
	}
}

// next samples the next think-time duration (never negative).
func (t *thinker) next(rng *rand.Rand) time.Duration {
	if t.cfg == nil {
		return t.fixed
	}
	ms := func(v int64) time.Duration { return time.Duration(v) * time.Millisecond }
	switch t.cfg.Distribution {
	case "uniform":
		lo, hi := t.cfg.MinMs, t.cfg.MaxMs
		if hi <= lo {
			return ms(lo)
		}
		return ms(lo + rng.Int63n(hi-lo+1))
	case "gaussian":
		v := float64(t.cfg.MeanMs) + rng.NormFloat64()*float64(t.cfg.StddevMs)
		if v < 0 {
			v = 0
		}
		return time.Duration(v) * time.Millisecond
	case "poisson":
		// Exponential inter-arrival with the given mean models a Poisson process.
		if t.cfg.MeanMs <= 0 {
			return 0
		}
		v := rng.ExpFloat64() * float64(t.cfg.MeanMs)
		return time.Duration(math.Min(v, float64(t.cfg.MeanMs)*20)) * time.Millisecond
	default: // fixed
		return ms(t.cfg.MinMs)
	}
}
