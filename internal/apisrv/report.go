package apisrv

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dreambe/loadify/internal/store"
	"github.com/go-chi/chi/v5"
)

// handleRunReport renders a self-contained, print-friendly HTML report for a
// run (the browser can save it as PDF). It reuses the stored summary, the test
// snapshot and the per-second series — no external assets or PDF dependency.
func (s *Server) handleRunReport(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	// Authorize via a normal session OR a public share token scoped to this run
	// (this route isn't behind the viewer middleware, so share links work).
	if !s.reportAuthorized(r, runID) {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx, cancel := withTimeout(r.Context())
	defer cancel()

	run, err := s.pg.GetRun(ctx, runID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	from := run.CreatedAt.Add(-time.Minute)
	to := time.Now()
	if run.EndedAt != nil {
		to = run.EndedAt.Add(time.Minute)
	}
	series, _ := s.ch.QuerySeries(ctx, runID, "", from, to, 1)

	var summary struct {
		Total   int64 `json:"total_requests"`
		Summary struct {
			ErrorRate float64 `json:"error_rate"`
			P50ms     float64 `json:"p50_ms"`
			P90ms     float64 `json:"p90_ms"`
			P95ms     float64 `json:"p95_ms"`
			P99ms     float64 `json:"p99_ms"`
		} `json:"summary"`
		Passed    *bool           `json:"passed"`
		Reason    string          `json:"reason"`
		Stopped   bool            `json:"auto_stopped"`
		RawChecks json.RawMessage `json:"checks"`
	}
	_ = json.Unmarshal(run.Summary, &summary)

	var checks []struct {
		Metric string  `json:"metric"`
		Op     string  `json:"op"`
		Value  float64 `json:"value"`
		Actual float64 `json:"actual"`
		OK     bool    `json:"ok"`
	}
	_ = json.Unmarshal(summary.RawChecks, &checks)

	durationS := 0.0
	if run.StartedAt != nil && run.EndedAt != nil {
		durationS = run.EndedAt.Sub(*run.StartedAt).Seconds()
	}
	avgQPS := 0.0
	if durationS > 0 {
		avgQPS = float64(summary.Total) / durationS
	}

	shortID := runID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	printLabel := "打印 / 存为 PDF"
	if r.URL.Query().Get("lang") == "en" {
		printLabel = "Print / Save PDF"
	}
	data := reportData{
		PrintLabel:  printLabel,
		Name:        orDefault(run.Name, shortID),
		RunID:       runID,
		Status:      run.Status,
		Creator:     run.CreatorName,
		Started:     fmtTime(run.StartedAt),
		Ended:       fmtTime(run.EndedAt),
		DurationS:   durationS,
		Total:       summary.Total,
		AvgQPS:      avgQPS,
		ErrorPct:    summary.Summary.ErrorRate * 100,
		P50:         summary.Summary.P50ms,
		P90:         summary.Summary.P90ms,
		P95:         summary.Summary.P95ms,
		P99:         summary.Summary.P99ms,
		AutoStopped: summary.Stopped,
		Reason:      summary.Reason,
		QPSPath:     svgPath(series, func(p store.SeriesPoint) float64 { return p.RPS }),
		P95Path:     svgPath(series, func(p store.SeriesPoint) float64 { return p.P95ms }),
	}
	for _, c := range checks {
		data.Checks = append(data.Checks, reportCheck{c.Metric, c.Op, c.Value, c.Actual, c.OK})
	}
	if run.Snapshot != nil {
		if b, e := json.MarshalIndent(json.RawMessage(run.Snapshot), "", "  "); e == nil {
			data.Snapshot = string(b)
		}
	}

	// The print button needs an inline handler; relax the (otherwise script-less)
	// CSP for this self-contained, asset-free page so window.print() can run.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := reportTmpl.Execute(w, data); err != nil {
		s.log.Warn("report render failed", "run", runID, "err", err)
	}
}

type reportCheck struct {
	Metric string
	Op     string
	Value  float64
	Actual float64
	OK     bool
}

type reportData struct {
	Name, RunID, Status, Creator, Started, Ended, PrintLabel string
	DurationS, AvgQPS, ErrorPct                              float64
	P50, P90, P95, P99                                       float64
	Total                                                    int64
	AutoStopped                                              bool
	Reason, Snapshot                                         string
	QPSPath, P95Path                                         template.HTML
	Checks                                                   []reportCheck
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return "–"
	}
	return t.Format("2006-01-02 15:04:05")
}

// svgPath maps a series to an SVG polyline path scaled into a 600x120 box.
func svgPath(series []store.SeriesPoint, pick func(store.SeriesPoint) float64) template.HTML {
	if len(series) == 0 {
		return ""
	}
	const w, h, pad = 600.0, 120.0, 8.0
	maxV := 1.0
	for _, p := range series {
		if v := pick(p); v > maxV {
			maxV = v
		}
	}
	var b strings.Builder
	for i, p := range series {
		x := pad
		if len(series) > 1 {
			x = pad + (float64(i)/float64(len(series)-1))*(w-2*pad)
		}
		y := pad + (h - 2*pad) - (pick(p)/maxV)*(h-2*pad)
		if i == 0 {
			b.WriteString("M")
		} else {
			b.WriteString(" L")
		}
		b.WriteString(ftoa(x) + "," + ftoa(y))
	}
	return template.HTML(b.String()) //nolint:gosec // numbers only
}

func ftoa(f float64) string {
	return strconv.FormatFloat(f, 'f', 1, 64)
}

// reportTmpl is the self-contained report. Dark, print-friendly.
var reportTmpl = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<title>Loadify report — {{.Name}}</title>
<style>
  :root{--bg:#0b0f17;--panel:#121826;--bd:#26334a;--tx:#e9eff8;--mut:#8b99b3;--ac:#36d6e7;--gr:#3ddc97;--rd:#ff5d73}
  *{box-sizing:border-box} body{margin:0;background:var(--bg);color:var(--tx);font:14px/1.5 system-ui,-apple-system,"PingFang SC",sans-serif;padding:32px}
  .wrap{max-width:760px;margin:0 auto}
  h1{font-size:22px;margin:0 0 4px} .mut{color:var(--mut)}
  .panel{background:var(--panel);border:1px solid var(--bd);border-radius:10px;padding:16px;margin:16px 0}
  .grid{display:grid;grid-template-columns:repeat(4,1fr);gap:12px}
  .cell{background:#0b1220;border:1px solid var(--bd);border-radius:8px;padding:10px}
  .cell .l{color:var(--mut);font-size:11px;text-transform:uppercase;letter-spacing:.08em}
  .cell .v{font-size:20px;font-weight:700;margin-top:4px;font-variant-numeric:tabular-nums}
  table{width:100%;border-collapse:collapse} th,td{text-align:left;padding:7px 9px;border-bottom:1px solid var(--bd)}
  th{color:var(--mut);font-size:12px} .ok{color:var(--gr)} .bad{color:var(--rd)}
  .badge{display:inline-block;padding:2px 10px;border-radius:999px;border:1px solid var(--bd);font-size:12px}
  pre{background:#0b1220;border:1px solid var(--bd);border-radius:8px;padding:12px;overflow:auto;font-size:12px}
  svg{width:100%;height:120px}
  .warn{background:rgba(255,93,115,.12);border:1px solid var(--rd);color:var(--rd);border-radius:8px;padding:10px 12px}
  .btn{display:inline-block;cursor:pointer;background:var(--ac);color:#04222a;border:none;border-radius:8px;padding:8px 14px;font:inherit;font-weight:600;margin-top:12px}
  @media print{body{background:#fff;color:#000}.panel,.cell{background:#fff;border-color:#ccc}.mut{color:#555}.no-print{display:none}}
</style></head>
<body><div class="wrap">
  <h1>{{.Name}}</h1>
  <div class="mut">{{.RunID}} · <span class="badge">{{.Status}}</span> · {{.Creator}} · {{.Started}} → {{.Ended}}</div>
  <button class="btn no-print" onclick="window.print()">{{.PrintLabel}}</button>
  {{if .AutoStopped}}<div class="warn" style="margin-top:12px">⚠ {{.Reason}}</div>{{end}}

  <div class="panel"><div class="grid">
    <div class="cell"><div class="l">Total requests</div><div class="v">{{.Total}}</div></div>
    <div class="cell"><div class="l">Duration</div><div class="v">{{printf "%.0f" .DurationS}}s</div></div>
    <div class="cell"><div class="l">Avg QPS</div><div class="v">{{printf "%.1f" .AvgQPS}}</div></div>
    <div class="cell"><div class="l">Error rate</div><div class="v">{{printf "%.2f" .ErrorPct}}%</div></div>
    <div class="cell"><div class="l">p50</div><div class="v">{{printf "%.1f" .P50}} ms</div></div>
    <div class="cell"><div class="l">p90</div><div class="v">{{printf "%.1f" .P90}} ms</div></div>
    <div class="cell"><div class="l">p95</div><div class="v">{{printf "%.1f" .P95}} ms</div></div>
    <div class="cell"><div class="l">p99</div><div class="v">{{printf "%.1f" .P99}} ms</div></div>
  </div></div>

  {{if .QPSPath}}<div class="panel"><div class="mut">Throughput (QPS)</div>
    <svg viewBox="0 0 600 120" preserveAspectRatio="none"><path d="{{.QPSPath}}" fill="none" stroke="#36d6e7" stroke-width="2"/></svg></div>
  <div class="panel"><div class="mut">Latency p95 (ms)</div>
    <svg viewBox="0 0 600 120" preserveAspectRatio="none"><path d="{{.P95Path}}" fill="none" stroke="#ffc857" stroke-width="2"/></svg></div>{{end}}

  {{if .Checks}}<div class="panel"><div class="mut" style="margin-bottom:8px">SLA thresholds</div>
    <table><thead><tr><th>Metric</th><th>Op</th><th>Threshold</th><th>Actual</th><th>Result</th></tr></thead><tbody>
    {{range .Checks}}<tr class="{{if .OK}}ok{{else}}bad{{end}}"><td>{{.Metric}}</td><td>{{.Op}}</td><td>{{.Value}}</td><td>{{printf "%.2f" .Actual}}</td><td>{{if .OK}}✓{{else}}✗{{end}}</td></tr>{{end}}
    </tbody></table></div>{{end}}

  {{if .Snapshot}}<div class="panel"><div class="mut" style="margin-bottom:8px">Test snapshot (at run time)</div><pre>{{.Snapshot}}</pre></div>{{end}}

  <p class="mut" style="font-size:12px">Generated by Loadify · response-level detail is sampled, not exhaustive.</p>
</div></body></html>`))
