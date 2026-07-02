// Package store defines the neutral data types shared between the metrics
// aggregator and the concrete metric/metadata stores (ClickHouse, Postgres).
package store

import (
	"context"
	"time"
)

// Rollup is a finalized per-second aggregate for one (run, group, status_class).
type Rollup struct {
	RunID       string
	TS          time.Time
	Group       string
	Protocol    string
	StatusClass string
	Count       int64
	Errors      int64
	SentBytes   int64
	RecvBytes   int64
	P50ms       float64
	P90ms       float64
	P95ms       float64
	P99ms       float64
	MaxMs       float64
	// Hist is the serialized merged HdrHistogram for exact re-aggregation.
	Hist []byte
}

// RollupWriter persists finalized rollups (implemented by the ClickHouse store).
type RollupWriter interface {
	WriteRollups(ctx context.Context, rows []Rollup) error
}

// SeriesPoint is one time bucket of a queried metric series.
type SeriesPoint struct {
	TS        time.Time `json:"ts"`
	RPS       float64   `json:"rps"`
	ErrorRate float64   `json:"error_rate"`
	P50ms     float64   `json:"p50_ms"`
	P90ms     float64   `json:"p90_ms"`
	P95ms     float64   `json:"p95_ms"`
	P99ms     float64   `json:"p99_ms"`
}

// SeriesReader queries historical rollups for charts.
type SeriesReader interface {
	QuerySeries(ctx context.Context, runID, group string, from, to time.Time, resSeconds int) ([]SeriesPoint, error)
}

// Sample is one persisted per-request observation for post-run error
// drill-down. Only the bounded, error-prioritized live samples are stored
// (not every request) and rows expire via TTL.
type Sample struct {
	RunID       string    `json:"-"`
	TS          time.Time `json:"ts"`
	Group       string    `json:"group"`
	Protocol    string    `json:"protocol"`
	StatusClass string    `json:"status_class"`
	Status      int32     `json:"status"`
	OK          bool      `json:"ok"`
	ErrorKind   string    `json:"error_kind"`
	Method      string    `json:"method"`
	URL         string    `json:"url"`
	LatencyUs   int64     `json:"latency_us"`
	RecvBytes   int64     `json:"recv_bytes"`
	ReqBody     string    `json:"req_body"`
	RespBody    string    `json:"resp_body"`
}

// SampleFilter narrows a sample query for drill-down.
type SampleFilter struct {
	Group       string
	StatusClass string
	ErrorKind   string
	Limit       int
	// From/To bound the sample time window (zero = unbounded), used to drill
	// into a single moment picked off a chart.
	From time.Time
	To   time.Time
}

// SampleStore persists and queries the sampled request detail.
type SampleStore interface {
	WriteSamples(ctx context.Context, rows []Sample) error
	QuerySamples(ctx context.Context, runID string, f SampleFilter) ([]Sample, error)
}
