// Package registry tracks connected workers and their live send channels.
package registry

import (
	"sync"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
)

// Worker is a connected worker plus the channel used to push messages to it.
type Worker struct {
	ID         string
	Region     string
	CPUCores   int32
	Supported  []loadifyv1.Protocol
	Send       chan *loadifyv1.CoordinatorMessage
	ActiveVUs  int64
	CPUPct     float64
	MemBytes   int64
	LastSeen   time.Time
	Connected  time.Time
	healthyTTL time.Duration
}

// Healthy reports whether the worker has been seen recently.
func (w *Worker) Healthy(now time.Time) bool {
	return now.Sub(w.LastSeen) <= w.healthyTTL
}

// Registry is a concurrency-safe set of connected workers.
type Registry struct {
	mu      sync.RWMutex
	workers map[string]*Worker
	ttl     time.Duration
}

// New creates a Registry; ttl is how long since LastSeen a worker stays healthy.
func New(ttl time.Duration) *Registry {
	if ttl <= 0 {
		ttl = 6 * time.Second
	}
	return &Registry{workers: make(map[string]*Worker), ttl: ttl}
}

// Add registers (or replaces) a worker and returns its handle.
func (r *Registry) Add(reg *loadifyv1.RegisterRequest, send chan *loadifyv1.CoordinatorMessage) *Worker {
	now := time.Now()
	w := &Worker{
		ID:         reg.WorkerId,
		Region:     reg.Region,
		CPUCores:   reg.CpuCores,
		Supported:  reg.Supported,
		Send:       send,
		LastSeen:   now,
		Connected:  now,
		healthyTTL: r.ttl,
	}
	r.mu.Lock()
	r.workers[w.ID] = w
	r.mu.Unlock()
	return w
}

// Remove drops a worker.
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	delete(r.workers, id)
	r.mu.Unlock()
}

// Touch updates liveness and load from a heartbeat.
func (r *Registry) Touch(id string, activeVUs int64, cpuPct float64, memBytes int64) {
	r.mu.Lock()
	if w := r.workers[id]; w != nil {
		w.LastSeen = time.Now()
		w.ActiveVUs = activeVUs
		w.CPUPct = cpuPct
		w.MemBytes = memBytes
	}
	r.mu.Unlock()
}

// Get returns a worker by ID.
func (r *Registry) Get(id string) (*Worker, bool) {
	r.mu.RLock()
	w, ok := r.workers[id]
	r.mu.RUnlock()
	return w, ok
}

// Healthy returns the currently healthy workers that support the protocol.
func (r *Registry) Healthy(proto loadifyv1.Protocol) []*Worker {
	now := time.Now()
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Worker, 0, len(r.workers))
	for _, w := range r.workers {
		if !w.Healthy(now) {
			continue
		}
		if proto != loadifyv1.Protocol_PROTOCOL_UNSPECIFIED && !supports(w, proto) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// Available returns healthy workers supporting proto whose CPU is below
// cpuMaxPct. A cpuMaxPct of 0 disables the CPU gate.
func (r *Registry) Available(proto loadifyv1.Protocol, cpuMaxPct float64) []*Worker {
	now := time.Now()
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Worker, 0, len(r.workers))
	for _, w := range r.workers {
		if !w.Healthy(now) {
			continue
		}
		if proto != loadifyv1.Protocol_PROTOCOL_UNSPECIFIED && !supports(w, proto) {
			continue
		}
		if cpuMaxPct > 0 && w.CPUPct >= cpuMaxPct {
			continue
		}
		out = append(out, w)
	}
	return out
}

// List returns all workers as protobuf WorkerInfo.
func (r *Registry) List() []*loadifyv1.WorkerInfo {
	now := time.Now()
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*loadifyv1.WorkerInfo, 0, len(r.workers))
	for _, w := range r.workers {
		status := "healthy"
		if !w.Healthy(now) {
			status = "unhealthy"
		}
		out = append(out, &loadifyv1.WorkerInfo{
			WorkerId:       w.ID,
			Region:         w.Region,
			Status:         status,
			ActiveVus:      w.ActiveVUs,
			LastSeenUnixMs: w.LastSeen.UnixMilli(),
			CpuPct:         w.CPUPct,
			MemBytes:       w.MemBytes,
			CpuCores:       w.CPUCores,
		})
	}
	return out
}

func supports(w *Worker, proto loadifyv1.Protocol) bool {
	for _, p := range w.Supported {
		if p == proto {
			return true
		}
	}
	return false
}
