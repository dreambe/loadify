package executor

import (
	"context"
	"sync"
	"time"
)

// barrier is a per-worker rendezvous (sync point): callers block in wait()
// until `target` of them have gathered, then all are released together —
// modeling a burst of simultaneous requests. If the pool can't reach `target`
// within `timeout`, whoever is waiting is released anyway so the run never
// stalls. It auto-resets for the next gather.
type barrier struct {
	target  int
	timeout time.Duration

	mu      sync.Mutex
	cond    *sync.Cond
	waiting int
	gen     uint64 // generation; bumped on each release so late waiters re-gather
}

func newBarrier(target int, timeout time.Duration) *barrier {
	b := &barrier{target: target, timeout: timeout}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *barrier) wait(ctx context.Context) {
	b.mu.Lock()
	gen := b.gen
	b.waiting++
	if b.waiting >= b.target {
		b.release()
		b.mu.Unlock()
		return
	}
	// Wake on timeout via a watchdog so a partial gather still proceeds.
	timer := time.AfterFunc(b.timeout, func() {
		b.mu.Lock()
		if b.gen == gen {
			b.release()
		}
		b.mu.Unlock()
	})
	for b.gen == gen {
		// ctx cancellation: release everyone so VUs can exit promptly.
		if ctx.Err() != nil {
			b.release()
			break
		}
		b.cond.Wait()
	}
	timer.Stop()
	b.mu.Unlock()
}

// release opens the current generation and resets the count. Caller holds mu.
func (b *barrier) release() {
	b.waiting = 0
	b.gen++
	b.cond.Broadcast()
}
