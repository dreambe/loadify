// Package aggregator merges per-worker MetricBatches into per-second rollups,
// emits live ticks, and persists finalized rollups to the metric store.
package aggregator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/metrics"
	"github.com/dreambe/loadify/internal/obs"
	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/store"
)

// finalizeGrace is how long a 1s bucket waits for late batches before it is
// finalized and written.
const finalizeGrace = 2 * time.Second

// Aggregator merges batches for a single run.
type Aggregator struct {
	runID    string
	protocol string
	writer   store.RollupWriter
	log      *slog.Logger

	mu          sync.Mutex
	buckets     map[int64]map[metrics.Key]*metrics.Bucket // bucketSec -> key -> agg
	samples     map[int64][]*loadifyv1.Sample             // bucketSec -> raw samples
	vusBySec    map[int64]map[string]int64                // bucketSec -> worker -> active VUs
	listeners   []chan *loadifyv1.LiveTick
	lastEmitted int64
	totalCount  int64
	lateBatches int64 // batches that arrived after their second was finalized

	// Auto-stop circuit breaker (cross-worker error-rate over a window).
	autoStop  plan.AutoStopConfig
	onStop    func(runID, reason string)
	autoWin   []secStat
	autoFired bool
}

type secStat struct{ count, errors int64 }

// sampleToStore converts a wire Sample into a persisted store.Sample, deriving
// the coarse status_class the drill-down filters on.
func sampleToStore(runID string, s *loadifyv1.Sample) store.Sample {
	return store.Sample{
		RunID:       runID,
		TS:          time.UnixMilli(s.TsUnixMs),
		Group:       s.Group,
		Protocol:    s.Protocol.String(),
		StatusClass: metrics.StatusClass(s.Status, s.Ok, s.ErrorKind),
		Status:      s.Status,
		OK:          s.Ok,
		ErrorKind:   s.ErrorKind,
		Method:      s.Method,
		URL:         s.Url,
		LatencyUs:   s.LatencyUs,
		RecvBytes:   s.RecvBytes,
		ReqBody:     s.ReqBody,
		RespBody:    s.RespBody,
	}
}

// SetAutoStop arms the circuit breaker with a config and a stop callback. The
// callback is invoked at most once, off the aggregator lock.
func (a *Aggregator) SetAutoStop(cfg plan.AutoStopConfig, onStop func(runID, reason string)) {
	a.mu.Lock()
	a.autoStop = cfg
	a.onStop = onStop
	a.mu.Unlock()
}

// liveSampleCap bounds how many raw samples ride along on a single LiveTick.
const liveSampleCap = 80

// New creates an Aggregator for a run.
func New(runID string, proto loadifyv1.Protocol, writer store.RollupWriter, log *slog.Logger) *Aggregator {
	if log == nil {
		log = slog.Default()
	}
	return &Aggregator{
		runID:     runID,
		protocol:  proto.String(),
		writer:    writer,
		log:       log,
		buckets:   make(map[int64]map[metrics.Key]*metrics.Bucket),
		samples:   make(map[int64][]*loadifyv1.Sample),
		vusBySec:  make(map[int64]map[string]int64),
	}
}

// Ingest merges one worker batch.
func (a *Aggregator) Ingest(b *loadifyv1.MetricBatch) {
	sec := b.BucketUnixMs / 1000
	a.mu.Lock()
	// A batch for an already-finalized second can never be emitted (its bucket
	// is gone). Don't resurrect a zombie bucket that would be silently dropped
	// at the next finalize — count it instead so the loss is observable.
	if a.lastEmitted != 0 && sec <= a.lastEmitted {
		a.lateBatches++
		a.mu.Unlock()
		return
	}
	if a.vusBySec[sec] == nil {
		a.vusBySec[sec] = make(map[string]int64)
	}
	a.vusBySec[sec][b.WorkerId] = b.ActiveVus
	bk := a.buckets[sec]
	if bk == nil {
		bk = make(map[metrics.Key]*metrics.Bucket)
		a.buckets[sec] = bk
	}
	for _, agg := range b.Agg {
		k := metrics.Key{Group: agg.Group, StatusClass: agg.StatusClass}
		dst := bk[k]
		if dst == nil {
			dst = &metrics.Bucket{Hist: metrics.NewHistogram()}
			bk[k] = dst
		}
		dst.Count += agg.Count
		dst.Errors += agg.Errors
		dst.SentBytes += agg.SentBytes
		dst.RecvBytes += agg.RecvBytes
		if h := metrics.DecodeHistogram(agg.HdrHistogram); h != nil {
			dst.Hist.Merge(h)
		}
		a.totalCount += agg.Count
	}
	if len(b.Samples) > 0 {
		if cur := a.samples[sec]; len(cur) < liveSampleCap {
			room := liveSampleCap - len(cur)
			add := b.Samples
			if len(add) > room {
				add = add[:room]
			}
			a.samples[sec] = append(cur, add...)
		}
	}
	a.mu.Unlock()
}

// Subscribe returns a channel of live ticks; close happens on Run exit.
func (a *Aggregator) Subscribe() chan *loadifyv1.LiveTick {
	ch := make(chan *loadifyv1.LiveTick, 64)
	a.mu.Lock()
	a.listeners = append(a.listeners, ch)
	a.mu.Unlock()
	return ch
}

// Run finalizes buckets every second until ctx is done, then drains remaining.
func (a *Aggregator) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			a.finalize(time.Now().Add(time.Hour)) // drain everything
			a.mu.Lock()
			late := a.lateBatches
			a.mu.Unlock()
			if late > 0 {
				a.log.Warn("dropped late metric batches; rollups for those seconds are incomplete",
					"run", a.runID, "late_batches", late, "grace", finalizeGrace)
			}
			a.closeListeners()
			return
		case now := <-t.C:
			a.finalize(now)
		}
	}
}

// finalize writes and emits any bucket older than the grace window.
func (a *Aggregator) finalize(now time.Time) {
	cutoff := now.Add(-finalizeGrace).Unix()
	a.mu.Lock()
	var ready []int64
	for sec := range a.buckets {
		if sec <= cutoff {
			ready = append(ready, sec)
		}
	}
	// Emit in chronological order: the lastEmitted guard below treats any second
	// <= lastEmitted as already-past, so processing a higher second before a
	// lower one (map iteration is unordered) would wrongly drop the lower one's
	// tick. This only bites when several buckets finalize at once (drain, catch-up).
	sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] })
	var rows []store.Rollup
	var ticks []*loadifyv1.LiveTick
	var sampleRows []store.Sample
	for _, sec := range ready {
		bk := a.buckets[sec]
		delete(a.buckets, sec)
		secSamples := a.samples[sec]
		delete(a.samples, sec)
		// Use the VU count reported for THIS second, not the current pool size,
		// so a ramping/draining run's per-second ActiveVUs reflects history.
		activeVUs := int64(0)
		for _, v := range a.vusBySec[sec] {
			activeVUs += v
		}
		delete(a.vusBySec, sec)
		if a.lastEmitted != 0 && sec <= a.lastEmitted {
			continue
		}
		a.lastEmitted = sec
		ts := time.Unix(sec, 0)
		rows, ticks = a.collect(sec, ts, bk, activeVUs, secSamples, rows, ticks)
		for _, sm := range secSamples {
			sampleRows = append(sampleRows, sampleToStore(a.runID, sm))
		}
	}
	listeners := append([]chan *loadifyv1.LiveTick(nil), a.listeners...)
	writer := a.writer
	a.mu.Unlock()

	if writer != nil && len(rows) > 0 {
		if err := writer.WriteRollups(context.Background(), rows); err != nil {
			a.log.Warn("write rollups failed", "err", err, "run", a.runID)
			obs.ClickHouseWriteErrors.Inc()
		}
	}
	// Persist the bounded sampled detail for post-run error drill-down (only
	// the sampled set, never every request).
	if sw, ok := writer.(store.SampleStore); ok && len(sampleRows) > 0 {
		if err := sw.WriteSamples(context.Background(), sampleRows); err != nil {
			a.log.Warn("write samples failed", "err", err, "run", a.runID)
			obs.ClickHouseWriteErrors.Inc()
		}
	}
	for _, tick := range ticks {
		for _, ch := range listeners {
			select {
			case ch <- tick:
			default:
			}
		}
	}
	a.evalAutoStop(ticks)
}

// evalAutoStop feeds finalized per-second ticks into a trailing window and
// fires the stop callback once if the windowed error rate crosses the
// threshold (and enough requests have accumulated to avoid startup jitter).
// Transaction pseudo-groups don't add to the count here — ticks are the
// aggregate across real request groups already.
func (a *Aggregator) evalAutoStop(ticks []*loadifyv1.LiveTick) {
	a.mu.Lock()
	if !a.autoStop.AutoStopEnabled() || a.onStop == nil || a.autoFired || len(ticks) == 0 {
		a.mu.Unlock()
		return
	}
	win := a.autoStop.WindowSec
	if win <= 0 {
		win = 10
	}
	for _, tk := range ticks {
		cnt := int64(tk.Rps + 0.5)
		errs := int64(tk.ErrorRate*tk.Rps + 0.5)
		a.autoWin = append(a.autoWin, secStat{count: cnt, errors: errs})
	}
	if len(a.autoWin) > win {
		a.autoWin = a.autoWin[len(a.autoWin)-win:]
	}
	var c, e int64
	for _, s := range a.autoWin {
		c += s.count
		e += s.errors
	}
	if c < int64(a.autoStop.MinRequests) || c == 0 {
		a.mu.Unlock()
		return
	}
	rate := float64(e) / float64(c) * 100
	if rate <= a.autoStop.ErrorRatePct {
		a.mu.Unlock()
		return
	}
	a.autoFired = true
	onStop := a.onStop
	reason := fmt.Sprintf("auto-stopped: error rate %.0f%% > %.0f%% over %ds", rate, a.autoStop.ErrorRatePct, win)
	a.mu.Unlock()
	a.log.Warn("auto-stop triggered", "run", a.runID, "reason", reason)
	onStop(a.runID, reason)
}

// collect turns one second's buckets into a rollup row set plus an aggregate
// LiveTick across all groups.
func (a *Aggregator) collect(sec int64, ts time.Time, bk map[metrics.Key]*metrics.Bucket, activeVUs int64, samples []*loadifyv1.Sample, rows []store.Rollup, ticks []*loadifyv1.LiveTick) ([]store.Rollup, []*loadifyv1.LiveTick) {
	total := metrics.NewHistogram()
	var count, errors int64
	groups := make(map[string]*loadifyv1.GroupTick)
	for k, b := range bk {
		pct := metrics.PercentilesOf(b.Hist)
		rows = append(rows, store.Rollup{
			RunID:       a.runID,
			TS:          ts,
			Group:       k.Group,
			Protocol:    a.protocol,
			StatusClass: k.StatusClass,
			Count:       b.Count,
			Errors:      b.Errors,
			SentBytes:   b.SentBytes,
			RecvBytes:   b.RecvBytes,
			P50ms:       pct.P50,
			P90ms:       pct.P90,
			P95ms:       pct.P95,
			P99ms:       pct.P99,
			MaxMs:       float64(b.Hist.Max()) / 1000.0,
			Hist:        metrics.EncodeHistogram(b.Hist),
		})
		// The scenario transaction pseudo-group (latency = sum of its steps) is a
		// per-group breakdown row only; it must never enter the request-level
		// top-line, or RPS is inflated and the aggregate latency is polluted.
		if !strings.HasPrefix(k.Group, "txn:") {
			count += b.Count
			errors += b.Errors
			total.Merge(b.Hist)
		}
		g := groups[k.Group]
		if g == nil {
			g = &loadifyv1.GroupTick{}
			groups[k.Group] = g
		}
		g.Rps += float64(b.Count)
		g.ErrorRate += float64(b.Errors)
	}
	for _, g := range groups {
		if g.Rps > 0 {
			g.ErrorRate = g.ErrorRate / g.Rps
		}
	}
	tp := metrics.PercentilesOf(total)
	errRate := 0.0
	if count > 0 {
		errRate = float64(errors) / float64(count)
	}
	ticks = append(ticks, &loadifyv1.LiveTick{
		RunId:     a.runID,
		TsUnixMs:  ts.UnixMilli(),
		Rps:       float64(count),
		ErrorRate: errRate,
		ActiveVus: activeVUs,
		P50Ms:     tp.P50,
		P90Ms:     tp.P90,
		P95Ms:     tp.P95,
		P99Ms:     tp.P99,
		Groups:    groups,
		Samples:   samples,
	})
	return rows, ticks
}

func (a *Aggregator) closeListeners() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, ch := range a.listeners {
		close(ch)
	}
	a.listeners = nil
}

// TotalCount returns the number of requests aggregated so far.
func (a *Aggregator) TotalCount() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.totalCount
}
