// Package targetmetrics queries the operator's Prometheus for the
// system-under-test's own resource metrics (CPU / memory / disk / network) over
// a run's time window, so the run page can render the TARGET's vitals natively
// — on loadify's own charts, on the same timeline as the applied load — instead
// of embedding Grafana. loadify measures the pressure; this shows the target's
// response to it.
package targetmetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Point is one (timestamp, value) sample; TS is unix milliseconds so it maps
// straight onto the frontend chart's time axis.
type Point struct {
	TS int64   `json:"ts"`
	V  float64 `json:"v"`
}

// Series is one line on a panel.
type Series struct {
	Label  string  `json:"label"`
	Points []Point `json:"points"`
}

// Panel is one chart (e.g. CPU%) with one or more series.
type Panel struct {
	Key    string   `json:"key"`
	Unit   string   `json:"unit"`
	Series []Series `json:"series"`
}

// Client queries a single Prometheus base URL.
type Client struct {
	base string
	http *http.Client
}

// New returns a client for the Prometheus at base (e.g. http://prom:9090).
func New(base string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 8 * time.Second}}
}

// queryDef is one series to fetch. `sum by (instance)` collapses multi-device /
// multi-cpu results to a single line for the target instance.
type queryDef struct{ panel, unit, label, promql string }

// defaultQueries are the standard node_exporter expressions, parameterized by
// the target's instance label. `$I` (which may appear several times per query)
// is replaced with the escaped instance. Disk is aggregated across real
// filesystems (no mountpoint assumption) so any target reports overall usage.
func defaultQueries(inst string) []queryDef {
	q := func(s string) string { return strings.ReplaceAll(s, "$I", inst) }
	const realFS = `fstype!~"tmpfs|overlay|squashfs|ramfs|devtmpfs|autofs|fuse.*"`
	// Ordered for a 2-column layout, pairing related signals. Each panel skips
	// itself if the target doesn't expose that metric (graceful on partial
	// node_exporter setups). iowait is folded into CPU as a second line since a
	// disk-bound target reads very differently from a CPU-bound one.
	return []queryDef{
		{"cpu", "%", "used", q(`100 * (1 - avg by (instance) (rate(node_cpu_seconds_total{mode="idle",instance="$I"}[1m])))`)},
		{"cpu", "%", "iowait", q(`100 * avg by (instance) (rate(node_cpu_seconds_total{mode="iowait",instance="$I"}[1m]))`)},
		{"load", "", "1m", q(`node_load1{instance="$I"}`)},
		{"mem", "%", "used", q(`100 * (1 - node_memory_MemAvailable_bytes{instance="$I"} / node_memory_MemTotal_bytes{instance="$I"})`)},
		{"disk", "%", "used", q(`100 * (1 - sum(node_filesystem_avail_bytes{instance="$I",` + realFS + `}) / sum(node_filesystem_size_bytes{instance="$I",` + realFS + `}))`)},
		{"net", "B/s", "rx", q(`sum by (instance) (rate(node_network_receive_bytes_total{instance="$I",device!="lo"}[1m]))`)},
		{"net", "B/s", "tx", q(`sum by (instance) (rate(node_network_transmit_bytes_total{instance="$I",device!="lo"}[1m]))`)},
		{"diskio", "B/s", "read", q(`sum by (instance) (rate(node_disk_read_bytes_total{instance="$I"}[1m]))`)},
		{"diskio", "B/s", "write", q(`sum by (instance) (rate(node_disk_written_bytes_total{instance="$I"}[1m]))`)},
		{"conns", "", "established", q(`node_netstat_Tcp_CurrEstab{instance="$I"}`)},
		{"fds", "", "open", q(`node_filefd_allocated{instance="$I"}`)},
	}
}

// Collect runs the default node_exporter panels for `instance` over [start,end]
// and returns them grouped by panel. A query that errors or returns no data is
// skipped (its series is omitted) rather than failing the whole request, so a
// partially-instrumented target still shows what it has.
func (c *Client) Collect(ctx context.Context, instance string, start, end time.Time) ([]Panel, error) {
	// Substitute a PromQL-safe instance (escape " and \) into the templates.
	inst := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(instance)
	step := stepFor(start, end)

	byPanel := map[string]*Panel{}
	order := []string{}
	for _, qd := range defaultQueries(inst) {
		pts, err := c.queryRange(ctx, qd.promql, start, end, step)
		if err != nil || len(pts) == 0 {
			continue
		}
		p := byPanel[qd.panel]
		if p == nil {
			p = &Panel{Key: qd.panel, Unit: qd.unit}
			byPanel[qd.panel] = p
			order = append(order, qd.panel)
		}
		p.Series = append(p.Series, Series{Label: qd.label, Points: pts})
	}
	out := make([]Panel, 0, len(order))
	for _, k := range order {
		out = append(out, *byPanel[k])
	}
	return out, nil
}

// stepFor picks a resolution: ~120 points across the window, clamped to [5s,60s].
func stepFor(start, end time.Time) time.Duration {
	w := end.Sub(start)
	if w <= 0 {
		return 5 * time.Second
	}
	step := w / 120
	if step < 5*time.Second {
		step = 5 * time.Second
	}
	if step > 60*time.Second {
		step = 60 * time.Second
	}
	return step
}

// promRangeResp is the subset of Prometheus /query_range we read.
type promRangeResp struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Values [][2]json.RawMessage `json:"values"` // [ [<unixSec:number>, "<value:string>"], ... ]
		} `json:"result"`
	} `json:"data"`
}

// queryRange runs an instant range query and returns the FIRST series' points
// (queries are written to collapse to one series per target instance).
func (c *Client) queryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]Point, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("start", strconv.FormatFloat(float64(start.UnixMilli())/1000, 'f', 3, 64))
	q.Set("end", strconv.FormatFloat(float64(end.UnixMilli())/1000, 'f', 3, 64))
	q.Set("step", strconv.Itoa(int(step.Seconds())))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/query_range?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus %d", resp.StatusCode)
	}
	var pr promRangeResp
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}
	if pr.Status != "success" || len(pr.Data.Result) == 0 {
		return nil, nil
	}
	raw := pr.Data.Result[0].Values
	pts := make([]Point, 0, len(raw))
	for _, v := range raw {
		var sec float64
		var valStr string
		if json.Unmarshal(v[0], &sec) != nil || json.Unmarshal(v[1], &valStr) != nil {
			continue
		}
		f, err := strconv.ParseFloat(valStr, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		pts = append(pts, Point{TS: int64(sec * 1000), V: f})
	}
	return pts, nil
}
