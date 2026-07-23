package targetmetrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestCollectParsesAndGroups feeds a canned Prometheus query_range response and
// asserts the default queries become 4 panels (cpu/mem/disk/net), that net
// carries both rx+tx series, and that values/timestamps parse.
func TestCollectParsesAndGroups(t *testing.T) {
	var gotQueries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQueries = append(gotQueries, r.URL.Query().Get("query"))
		w.Header().Set("Content-Type", "application/json")
		// One series, two points. Value is a string per the Prometheus wire format.
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"instance":"t:9100"},"values":[[1700000000,"42.5"],[1700000005,"43.0"]]}
		]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	start := time.Unix(1700000000, 0)
	panels, err := c.Collect(context.Background(), "t:9100", start, start.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	keys := map[string]*Panel{}
	for i := range panels {
		keys[panels[i].Key] = &panels[i]
	}
	for _, k := range []string{"cpu", "mem", "disk", "net"} {
		if keys[k] == nil {
			t.Errorf("missing panel %q", k)
		}
	}
	if net := keys["net"]; net != nil {
		if len(net.Series) != 2 {
			t.Errorf("net panel has %d series, want 2 (rx+tx)", len(net.Series))
		}
		if net.Unit != "B/s" {
			t.Errorf("net unit = %q, want B/s", net.Unit)
		}
	}
	if cpu := keys["cpu"]; cpu != nil {
		// cpu carries used + iowait lines.
		if len(cpu.Series) < 1 || len(cpu.Series[0].Points) != 2 {
			t.Fatalf("cpu series/points = %d/%v", len(cpu.Series), cpu.Series)
		}
		if cpu.Series[0].Label != "used" {
			t.Errorf("cpu first series = %q, want used", cpu.Series[0].Label)
		}
		p0 := cpu.Series[0].Points[0]
		if p0.TS != 1700000000000 || p0.V != 42.5 {
			t.Errorf("cpu point0 = %+v, want ts=1700000000000 v=42.5", p0)
		}
	}

	// The target instance must be substituted into the PromQL.
	if len(gotQueries) == 0 || !strings.Contains(gotQueries[0], `instance="t:9100"`) {
		t.Errorf("instance not substituted into query: %v", gotQueries)
	}
}

func TestStepFor(t *testing.T) {
	s := time.Unix(0, 0)
	if got := stepFor(s, s.Add(2*time.Minute)); got != 5*time.Second { // 120s/120=1s -> floor 5s
		t.Errorf("short window step = %v, want 5s", got)
	}
	if got := stepFor(s, s.Add(4*time.Hour)); got != 60*time.Second { // clamps to 60s
		t.Errorf("long window step = %v, want 60s", got)
	}
}
