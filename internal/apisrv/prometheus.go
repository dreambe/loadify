package apisrv

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// handleRunMetrics exposes a run's headline metrics in Prometheus text format,
// so loadify results can be scraped into Prometheus/Grafana alongside the
// target's own metrics. Scraping a running run yields near-live values (the
// per-second rollups are persisted continuously); a finished run yields its
// final summary. The scraper authenticates with ?token= or a Bearer header.
func (s *Server) handleRunMetrics(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	ctx, cancel := withTimeout(r.Context())
	defer cancel()

	pt, total, err := s.ch.Summary(ctx, runID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	g := func(name, help string, val float64, extraLabels string) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s{run=%q%s} %g\n", name, help, name, name, runID, extraLabels, val)
	}
	g("loadify_run_total_requests", "Total requests recorded for the run.", float64(total), "")
	g("loadify_run_qps", "Requests per second.", pt.RPS, "")
	g("loadify_run_error_rate", "Error ratio in [0,1].", pt.ErrorRate, "")
	// Latency percentiles share one metric with a quantile label, Prometheus-style.
	fmt.Fprintf(w, "# HELP loadify_run_latency_ms Response latency percentiles (ms).\n# TYPE loadify_run_latency_ms gauge\n")
	fmt.Fprintf(w, "loadify_run_latency_ms{run=%q,quantile=\"0.5\"} %g\n", runID, pt.P50ms)
	fmt.Fprintf(w, "loadify_run_latency_ms{run=%q,quantile=\"0.9\"} %g\n", runID, pt.P90ms)
	fmt.Fprintf(w, "loadify_run_latency_ms{run=%q,quantile=\"0.95\"} %g\n", runID, pt.P95ms)
	fmt.Fprintf(w, "loadify_run_latency_ms{run=%q,quantile=\"0.99\"} %g\n", runID, pt.P99ms)
}
