// Package executor runs a protocol Driver under a virtual-user pool that tracks
// a ramp curve, feeding results into a sampler.
package executor

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/dreambe/loadify/internal/worker/protocols"
	"github.com/dreambe/loadify/internal/worker/sampler"
)

// controlInterval is how often the pool resizes toward the ramp target.
const controlInterval = 200 * time.Millisecond

// Executor scales a VU pool to follow a Ramp and drives a Driver.
type Executor struct {
	driver    protocols.Driver
	ramp      *Ramp
	sampler   *sampler.Sampler
	thinkTime time.Duration
	log       *slog.Logger

	mu      sync.Mutex
	vus     []*vuHandle
	nextID  int
	stopped bool
}

type vuHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Config configures an Executor.
type Config struct {
	Driver    protocols.Driver
	Ramp      *Ramp
	Sampler   *sampler.Sampler
	ThinkTime time.Duration
	Logger    *slog.Logger
}

// New creates an Executor.
func New(c Config) *Executor {
	log := c.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Executor{driver: c.Driver, ramp: c.Ramp, sampler: c.Sampler, thinkTime: c.ThinkTime, log: log}
}

// Run prepares the driver, follows the ramp until it elapses or ctx is
// cancelled, then tears everything down.
func (e *Executor) Run(ctx context.Context) error {
	if err := e.driver.Prepare(ctx); err != nil {
		return err
	}
	defer func() { _ = e.driver.Teardown(context.Background()) }()

	start := time.Now()
	deadline := start.Add(e.ramp.Total())
	ticker := time.NewTicker(controlInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.scaleTo(ctx, 0)
			return ctx.Err()
		case now := <-ticker.C:
			if now.After(deadline) {
				e.scaleTo(ctx, 0)
				e.sampler.SetActiveVUs(0)
				return nil
			}
			target := e.ramp.TargetAt(now.Sub(start))
			e.scaleTo(ctx, target)
			e.sampler.SetActiveVUs(int64(e.count()))
		}
	}
}

func (e *Executor) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.vus)
}

// scaleTo adjusts the live VU count toward target.
func (e *Executor) scaleTo(ctx context.Context, target int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopped {
		target = 0
	}
	for len(e.vus) < target {
		e.spawnLocked(ctx)
	}
	for len(e.vus) > target {
		last := e.vus[len(e.vus)-1]
		e.vus = e.vus[:len(e.vus)-1]
		last.cancel()
	}
}

func (e *Executor) spawnLocked(parent context.Context) {
	e.nextID++
	id := e.nextID
	vctx, cancel := context.WithCancel(parent)
	h := &vuHandle{cancel: cancel, done: make(chan struct{})}
	e.vus = append(e.vus, h)
	go e.runVU(vctx, id, h)
}

func (e *Executor) runVU(ctx context.Context, id int, h *vuHandle) {
	defer close(h.done)
	vu := &protocols.VU{ID: id}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		res := e.driver.Exec(ctx, vu)
		// A cancelled context produces a spurious transport error; drop it.
		if ctx.Err() != nil {
			return
		}
		e.sampler.Record(res)
		vu.Iteration++
		if e.thinkTime > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(e.thinkTime):
			}
		}
	}
}

// Stop signals all VUs to drain.
func (e *Executor) Stop() {
	e.mu.Lock()
	e.stopped = true
	e.mu.Unlock()
	e.scaleTo(context.Background(), 0)
}
