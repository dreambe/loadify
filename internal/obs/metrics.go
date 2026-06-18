package obs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Shared Prometheus collectors, registered on the default registry that
// HealthServer exposes at /metrics. These turn the endpoint from "Go runtime
// only" into something an operator can actually alert on.
var (
	// HTTPRequests counts REST API requests by route pattern, method and status.
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "loadify_http_requests_total",
		Help: "Total REST API requests by route, method and status class.",
	}, []string{"route", "method", "status"})

	// HTTPDuration observes REST API request latency by route.
	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "loadify_http_request_duration_seconds",
		Help:    "REST API request duration in seconds by route and method.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "method"})

	// ClickHouseWriteErrors counts failed writes to the metrics store. A
	// non-zero rate means run charts/summaries may be silently incomplete.
	ClickHouseWriteErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "loadify_clickhouse_write_errors_total",
		Help: "Total failed writes (rollups or samples) to ClickHouse.",
	})
)

// RegisterGauge registers a gauge whose value is read from fn on each scrape.
// Used by the coordinator to expose live state (active runs, queue depth,
// connected workers) without threading metric updates through every mutation.
func RegisterGauge(name, help string, fn func() float64) {
	promauto.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help}, fn)
}
