package executor

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dreambe/loadify/internal/worker/protocols"
	"github.com/dreambe/loadify/internal/worker/sampler"
)

// pacerInterval is how often the arrival executor releases scheduled iterations.
const pacerInterval = 50 * time.Millisecond

// ArrivalExecutor drives the open (arrival-rate) model: it starts iterations at
// a target request rate independent of how long each takes, growing a worker
// pool on demand up to a cap. When the cap can't sustain the rate, excess
// iterations are dropped and counted (like k6's dropped_iterations).
type ArrivalExecutor struct {
	driver  protocols.Driver
	ramp    *Ramp
	sampler *sampler.Sampler
	maxVUs  int
	log     *slog.Logger

	busy    atomic.Int64
	dropped atomic.Int64
}

// ArrivalConfig configures an ArrivalExecutor.
type ArrivalConfig struct {
	Driver  protocols.Driver
	Ramp    *Ramp
	Sampler *sampler.Sampler
	MaxVUs  int // 0 = derive from peak rate
	Logger  *slog.Logger
}

// NewArrival creates an ArrivalExecutor.
func NewArrival(c ArrivalConfig) *ArrivalExecutor {
	log := c.Logger
	if log == nil {
		log = slog.Default()
	}
	maxVUs := c.MaxVUs
	if maxVUs <= 0 {
		// Allow up to ~5s of in-flight requests at peak rate, with a floor.
		maxVUs = int(c.Ramp.PeakRPS())*5 + 100
	}
	// Never let the derived (or requested) pool exceed the per-worker ceiling.
	if capped := clampVUs(maxVUs, maxVUsPerWorker()); capped != maxVUs {
		log.Warn("arrival VU pool capped", "requested", maxVUs, "max_vus_per_worker", capped)
		maxVUs = capped
	}
	return &ArrivalExecutor{driver: c.Driver, ramp: c.Ramp, sampler: c.Sampler, maxVUs: maxVUs, log: log}
}

// Run paces iterations to follow the rate ramp until it elapses or ctx is done.
func (e *ArrivalExecutor) Run(ctx context.Context) error {
	if err := e.driver.Prepare(ctx); err != nil {
		return err
	}
	defer func() { _ = e.driver.Teardown(context.Background()) }()

	jobs := make(chan struct{}, e.maxVUs)
	var wg sync.WaitGroup
	pool := 0
	spawn := func() {
		pool++
		wg.Add(1)
		go e.worker(ctx, jobs, &wg)
	}
	spawn() // start with one worker

	start := time.Now()
	deadline := start.Add(e.ramp.Total())
	ticker := time.NewTicker(pacerInterval)
	defer ticker.Stop()

	var acc float64
	for {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		case now := <-ticker.C:
			if now.After(deadline) {
				close(jobs)
				wg.Wait()
				e.sampler.SetActiveVUs(0)
				return nil
			}
			elapsed := now.Sub(start)
			acc += e.ramp.RateAt(elapsed) * pacerInterval.Seconds()
			n := int(acc)
			acc -= float64(n)
			for i := 0; i < n; i++ {
				select {
				case jobs <- struct{}{}:
				default:
					e.dropped.Add(1)
				}
			}
			// Grow the pool toward the current in-flight + backlog demand.
			need := int(e.busy.Load()) + len(jobs) + 1
			if need > e.maxVUs {
				need = e.maxVUs
			}
			for pool < need {
				spawn()
			}
			e.sampler.SetActiveVUs(e.busy.Load())
		}
	}
}

func (e *ArrivalExecutor) worker(ctx context.Context, jobs <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	vu := &protocols.VU{}
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-jobs:
			if !ok {
				return
			}
			e.busy.Add(1)
			if md, ok := e.driver.(protocols.MultiDriver); ok {
				for _, res := range md.ExecMulti(ctx, vu) {
					if ctx.Err() == nil {
						e.sampler.Record(res)
					}
				}
			} else {
				res := e.driver.Exec(ctx, vu)
				if ctx.Err() == nil {
					e.sampler.Record(res)
				}
			}
			vu.Iteration++
			e.busy.Add(-1)
		}
	}
}

// Dropped returns the number of iterations dropped because the worker pool was
// saturated at its cap.
func (e *ArrivalExecutor) Dropped() int64 { return e.dropped.Load() }
