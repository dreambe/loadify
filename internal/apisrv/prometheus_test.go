package apisrv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dreambe/loadify/internal/auth"
)

// TestRunMetricsPrometheus checks the run metrics render as scrapeable
// Prometheus text with the expected metric names and the quantile labels.
func TestRunMetricsPrometheus(t *testing.T) {
	srv := newTestServer(newFakeMeta(), &fakeCoord{})
	req := httptest.NewRequest("GET", "/api/v1/runs/run-1/metrics?token="+token(t, auth.RoleViewer), nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("content-type = %q, want text/plain", ct)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"loadify_run_total_requests{run=\"run-1\"} 100", // fakeMetrics.Summary → total 100
		"# TYPE loadify_run_qps gauge",
		"loadify_run_latency_ms{run=\"run-1\",quantile=\"0.95\"} 50", // fakeMetrics P95ms=50
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q\n--- got ---\n%s", want, body)
		}
	}
}
