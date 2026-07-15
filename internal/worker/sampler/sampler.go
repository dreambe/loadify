// Package sampler collects per-iteration results into 1-second mergeable
// rollups and emits them as MetricBatch messages.
package sampler

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	loadifyv1 "github.com/dreambe/loadify/api/gen/go/loadify/v1"
	"github.com/dreambe/loadify/internal/metrics"
	"github.com/dreambe/loadify/internal/worker/protocols"
)

// validUTF8 makes a captured body snippet safe to place in a protobuf string
// field. Bodies are truncated to a byte cap upstream, which can split a
// multi-byte rune (e.g. a Chinese character in an HTTP request/response body)
// and leave invalid UTF-8; binary or compressed (gzip) responses are invalid
// too. protobuf REFUSES to marshal a string field that isn't valid UTF-8 —
// "string field contains invalid UTF-8" — which fails EVERY metric send and
// wedges the worker in an endless reconnect loop that delivers no data. Strip
// the offending bytes so the snippet is always marshalable.
func validUTF8(s string) string {
	if s == "" || utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}

// Per-flush caps on the number of raw samples retained for the live log, so the
// response-log stream stays bounded regardless of throughput. Errors are kept
// preferentially over successes.
const (
	errSampleCap = 40
	okSampleCap  = 20
)

// Sampler is safe for concurrent Record from many VUs and periodic Flush.
type Sampler struct {
	runID    string
	workerID string
	protocol loadifyv1.Protocol

	mu        sync.Mutex
	rec       *metrics.Recorder
	samples   []*loadifyv1.Sample
	okSamples int
	errSamps  int
	activeVUs atomic.Int64
}

// New creates a Sampler for a run.
func New(runID, workerID string, proto loadifyv1.Protocol) *Sampler {
	return &Sampler{runID: runID, workerID: workerID, protocol: proto, rec: metrics.NewRecorder()}
}

// Record ingests one iteration result.
func (s *Sampler) Record(r protocols.Result) {
	s.mu.Lock()
	s.rec.Record(r.Group, r.Status, r.OK, r.ErrorKind, r.LatencyUs, r.SentBytes, r.RecvBytes)
	s.maybeSampleLocked(r)
	s.mu.Unlock()
}

// maybeSampleLocked keeps a bounded, error-prioritized set of raw samples for
// the live response log. Caller holds s.mu.
func (s *Sampler) maybeSampleLocked(r protocols.Result) {
	if r.OK {
		if s.okSamples >= okSampleCap {
			return
		}
		s.okSamples++
	} else {
		if s.errSamps >= errSampleCap {
			return
		}
		s.errSamps++
	}
	s.samples = append(s.samples, &loadifyv1.Sample{
		TsUnixMs:  time.Now().UnixMilli(),
		Group:     r.Group,
		Protocol:  s.protocol,
		Status:    r.Status,
		Ok:        r.OK,
		LatencyUs: r.LatencyUs,
		DnsUs:     r.DNSUs,
		ConnectUs: r.ConnectUs,
		TlsUs:     r.TLSUs,
		TtfbUs:    r.TTFBUs,
		SentBytes: r.SentBytes,
		RecvBytes: r.RecvBytes,
		ErrorKind: r.ErrorKind,
		Method:    r.Method,
		Url:       validUTF8(r.URL),
		ReqBody:   validUTF8(r.ReqBody),
		RespBody:  validUTF8(r.RespBody),
	})
}

// SetActiveVUs records the current active VU count for the next batch.
func (s *Sampler) SetActiveVUs(n int64) { s.activeVUs.Store(n) }

// Flush swaps out the current window and returns it as a MetricBatch, or nil if
// no samples were recorded.
func (s *Sampler) Flush(bucket time.Time) *loadifyv1.MetricBatch {
	s.mu.Lock()
	prev := s.rec
	s.rec = metrics.NewRecorder()
	samples := s.samples
	s.samples = nil
	s.okSamples, s.errSamps = 0, 0
	s.mu.Unlock()

	buckets := prev.Buckets()
	if len(buckets) == 0 {
		// Still emit a heartbeat batch carrying active VU count and any samples.
		return &loadifyv1.MetricBatch{
			RunId:        s.runID,
			WorkerId:     s.workerID,
			BucketUnixMs: bucket.UnixMilli(),
			ActiveVus:    s.activeVUs.Load(),
			Samples:      samples,
		}
	}

	batch := &loadifyv1.MetricBatch{
		RunId:        s.runID,
		WorkerId:     s.workerID,
		BucketUnixMs: bucket.UnixMilli(),
		ActiveVus:    s.activeVUs.Load(),
		Samples:      samples,
	}
	for k, b := range buckets {
		batch.Agg = append(batch.Agg, &loadifyv1.AggSlice{
			Group:        k.Group,
			Protocol:     s.protocol,
			StatusClass:  k.StatusClass,
			Count:        b.Count,
			Errors:       b.Errors,
			SentBytes:    b.SentBytes,
			RecvBytes:    b.RecvBytes,
			HdrHistogram: metrics.EncodeHistogram(b.Hist),
		})
	}
	return batch
}
