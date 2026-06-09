// Package aggregator merges per-worker MetricBatches into per-second rollups,
// emits live ticks, and persists finalized rollups to the metric store.
package aggregator

import (
	"context"
	"log/slog"
	"sync"
	"time"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/metrics"
	"github.com/dreambe/loadify/internal/store"
	hdr "github.com/HdrHistogram/hdrhistogram-go"
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
	workerVUs   map[string]int64
	listeners   []chan *loadifyv1.LiveTick
	lastEmitted int64
	totalCount  int64
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
		workerVUs: make(map[string]int64),
	}
}

// Ingest merges one worker batch.
func (a *Aggregator) Ingest(b *loadifyv1.MetricBatch) {
	sec := b.BucketUnixMs / 1000
	a.mu.Lock()
	a.workerVUs[b.WorkerId] = b.ActiveVus
	bk := a.buckets[sec]
	if bk == nil {
		bk = make(map[metrics.Key]*metrics.Bucket)
		a.buckets[sec] = bk
	}
	for _, agg := range b.Agg {
		k := metrics.Key{Group: agg.Group, StatusClass: agg.StatusClass}
		dst := bk[k]
		if dst == nil {
			dst = &metrics.Bucket{Hist: hdr.New(1, 120_000_000, 3)}
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
	var rows []store.Rollup
	var ticks []*loadifyv1.LiveTick
	activeVUs := int64(0)
	for _, v := range a.workerVUs {
		activeVUs += v
	}
	for _, sec := range ready {
		bk := a.buckets[sec]
		delete(a.buckets, sec)
		secSamples := a.samples[sec]
		delete(a.samples, sec)
		if a.lastEmitted != 0 && sec <= a.lastEmitted {
			continue
		}
		a.lastEmitted = sec
		ts := time.Unix(sec, 0)
		rows, ticks = a.collect(sec, ts, bk, activeVUs, secSamples, rows, ticks)
	}
	listeners := append([]chan *loadifyv1.LiveTick(nil), a.listeners...)
	writer := a.writer
	a.mu.Unlock()

	if writer != nil && len(rows) > 0 {
		if err := writer.WriteRollups(context.Background(), rows); err != nil {
			a.log.Warn("write rollups failed", "err", err, "run", a.runID)
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
}

// collect turns one second's buckets into a rollup row set plus an aggregate
// LiveTick across all groups.
func (a *Aggregator) collect(sec int64, ts time.Time, bk map[metrics.Key]*metrics.Bucket, activeVUs int64, samples []*loadifyv1.Sample, rows []store.Rollup, ticks []*loadifyv1.LiveTick) ([]store.Rollup, []*loadifyv1.LiveTick) {
	total := hdr.New(1, 120_000_000, 3)
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
		count += b.Count
		errors += b.Errors
		total.Merge(b.Hist)
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
