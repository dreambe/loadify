// Package metrics defines the shared sample/aggregate types used by workers,
// the coordinator and the metrics store. Percentiles are computed with
// HdrHistogram so partial rollups merge exactly across workers.
package metrics

import (
	"bytes"
	"encoding/gob"

	hdr "github.com/HdrHistogram/hdrhistogram-go"
)

// Latency histogram bounds: 1µs .. 600s, 3 significant figures.
const (
	latencyMinUs = 1
	latencyMaxUs = 600_000_000
	sigFigures   = 3
)

// NewHistogram returns a histogram with the canonical latency bounds. The
// recorder and the coordinator's aggregator must use identical bounds so
// per-worker histograms merge exactly.
func NewHistogram() *hdr.Histogram {
	return hdr.New(latencyMinUs, latencyMaxUs, sigFigures)
}

// StatusClass buckets a result into a coarse class for rollups.
func StatusClass(status int32, ok bool, errKind string) string {
	if errKind != "" || !ok {
		if status >= 500 {
			return "5xx"
		}
		if status >= 400 {
			return "4xx"
		}
		return "err"
	}
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 200 && status < 300:
		return "2xx"
	default:
		return "2xx"
	}
}

// Key identifies an aggregation bucket within a 1s window.
type Key struct {
	Group       string
	StatusClass string
}

// Bucket accumulates counters plus a latency histogram for one Key.
type Bucket struct {
	Count     int64
	Errors    int64
	SentBytes int64
	RecvBytes int64
	Hist      *hdr.Histogram
}

func newBucket() *Bucket {
	return &Bucket{Hist: NewHistogram()}
}

// Recorder accumulates samples for the current 1-second window.
type Recorder struct {
	buckets map[Key]*Bucket
}

// NewRecorder returns an empty Recorder.
func NewRecorder() *Recorder {
	return &Recorder{buckets: make(map[Key]*Bucket)}
}

// Record adds one observation.
func (r *Recorder) Record(group string, status int32, ok bool, errKind string, latencyUs, sentBytes, recvBytes int64) {
	k := Key{Group: group, StatusClass: StatusClass(status, ok, errKind)}
	b := r.buckets[k]
	if b == nil {
		b = newBucket()
		r.buckets[k] = b
	}
	b.Count++
	if !ok || errKind != "" {
		b.Errors++
	}
	b.SentBytes += sentBytes
	b.RecvBytes += recvBytes
	if latencyUs < latencyMinUs {
		latencyUs = latencyMinUs
	}
	if latencyUs > latencyMaxUs {
		latencyUs = latencyMaxUs
	}
	_ = b.Hist.RecordValue(latencyUs)
}

// Buckets returns the current accumulated buckets keyed by Key.
func (r *Recorder) Buckets() map[Key]*Bucket {
	return r.buckets
}

// Reset clears all buckets, ready for the next window.
func (r *Recorder) Reset() {
	r.buckets = make(map[Key]*Bucket)
}

// EncodeHistogram serializes an HdrHistogram for transport.
func EncodeHistogram(h *hdr.Histogram) []byte {
	if h == nil {
		return nil
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(h.Export()); err != nil {
		return nil
	}
	return buf.Bytes()
}

// DecodeHistogram restores an HdrHistogram from EncodeHistogram bytes.
func DecodeHistogram(data []byte) *hdr.Histogram {
	if len(data) == 0 {
		return nil
	}
	var snap hdr.Snapshot
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&snap); err != nil {
		return nil
	}
	return hdr.Import(&snap)
}

// Percentiles holds the standard latency percentiles in milliseconds.
type Percentiles struct {
	P50, P90, P95, P99 float64
}

// PercentilesOf computes percentiles (ms) from a histogram of microseconds.
func PercentilesOf(h *hdr.Histogram) Percentiles {
	if h == nil || h.TotalCount() == 0 {
		return Percentiles{}
	}
	return Percentiles{
		P50: float64(h.ValueAtQuantile(50)) / 1000.0,
		P90: float64(h.ValueAtQuantile(90)) / 1000.0,
		P95: float64(h.ValueAtQuantile(95)) / 1000.0,
		P99: float64(h.ValueAtQuantile(99)) / 1000.0,
	}
}
