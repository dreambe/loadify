package httpd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/dreambe/loadify/internal/plan"
	"github.com/dreambe/loadify/internal/worker/protocols"
)

// W3C traceparent: 00-<32 hex>-<16 hex>-<2 hex flags>.
var traceparentRe = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`)

// TestTraceHeaderInjection: with TraceHeader on, every request carries a valid,
// unique W3C traceparent; off, none is sent.
func TestTraceHeaderInjection(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("traceparent"))
	}))
	defer srv.Close()

	// Off → no header.
	run(t, &plan.HTTPConfig{Method: "GET", URL: srv.URL}, &protocols.VU{ID: 1})
	if seen[0] != "" {
		t.Errorf("traceparent set without TraceHeader: %q", seen[0])
	}

	// On → valid traceparent, and a second request gets a different trace id.
	d := &Driver{cfg: &plan.HTTPConfig{Method: "GET", URL: srv.URL, TraceHeader: true}}
	if err := d.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.Exec(context.Background(), &protocols.VU{ID: 1})
	d.Exec(context.Background(), &protocols.VU{ID: 1})
	tp1, tp2 := seen[1], seen[2]
	if !traceparentRe.MatchString(tp1) {
		t.Errorf("traceparent %q not W3C-valid", tp1)
	}
	if tp1 == tp2 {
		t.Errorf("traceparent not unique per request: %q", tp1)
	}
}
