"use client";

import { useEffect, useRef, useState } from "react";
import Nav from "@/components/Nav";
import LiveRunChart from "@/components/LiveRunChart";
import LineChart, { formatElapsed } from "@/components/LineChart";
import { api, exportCSVURL, reportURL } from "@/lib/api";
import ErrorDrilldown from "@/components/ErrorDrilldown";
import { useAuth, roleAtLeast } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import type { Run, SeriesPoint } from "@/lib/types";

export default function RunDetailPage({ params }: { params: { id: string } }) {
  const { t } = useI18n();
  const { user, ready } = useAuth();
  const [run, setRun] = useState<Run | null>(null);
  const [series, setSeries] = useState<SeriesPoint[]>([]);
  const [hover, setHover] = useState<number | null>(null);
  const runId = params.id;
  const stopped = useRef(false);

  useEffect(() => {
    if (!ready) return;
    stopped.current = false;
    const load = () =>
      api
        .getRun(runId)
        .then((r) => {
          setRun(r);
          // Stop polling once terminal — otherwise the 4s refresh keeps
          // replacing state and the finished view flickers.
          if (r.status !== "running" && r.status !== "pending" && r.status !== "queued") {
            stopped.current = true;
            clearInterval(timer);
          }
        })
        .catch(() => {});
    load();
    const timer = setInterval(() => {
      if (!stopped.current) load();
    }, 4000);
    return () => clearInterval(timer);
  }, [ready, runId]);

  const terminal = run && run.status !== "running" && run.status !== "pending";

  useEffect(() => {
    if (terminal) {
      api.runSeries(runId).then(setSeries).catch(() => {});
    }
  }, [terminal, runId]);

  if (!ready) return null;
  const canStop = roleAtLeast(user?.role, "operator");

  // X-axis: elapsed test time from the first series point.
  const seriesBase = series.length > 0 ? new Date(series[0].ts).getTime() : 0;
  const xLabels = series.map((p) => formatElapsed((new Date(p.ts).getTime() - seriesBase) / 1000));

  return (
    <>
      <Nav />
      <div className="container">
        <div className="row" style={{ justifyContent: "space-between" }}>
          <h1>{run?.name || `${t("run.title")} ${runId.slice(0, 8)}`}</h1>
          <div className="row" style={{ alignItems: "center" }}>
            {terminal && run && canStop && (
              <button
                className="secondary"
                onClick={() =>
                  api
                    .startRun(run.test_def_id, Math.max(1, run.desired_workers), "")
                    .then((res) => (window.location.href = `/runs/${res.run_id}`))
                }
              >
                ↻ {t("runs.rerun")}
              </button>
            )}
            {terminal && (
              <a className="badge" href={reportURL(runId)} target="_blank" rel="noreferrer">
                📄 {t("run.report")}
              </a>
            )}
            {terminal && (
              <a className="badge" href={exportCSVURL(runId)} download>
                ⬇ {t("run.exportCsv")}
              </a>
            )}
            {run && <span className={`badge ${run.status}`}>{run.status}</span>}
          </div>
        </div>
        {run && (
          <div className="muted" style={{ marginBottom: 12 }}>
            {t("run.creator")}: {run.creator_name || t("run.creatorSystem")}
            {" · "}
            {t("runs.colStarted")}: {run.started_at ? new Date(run.started_at).toLocaleString() : "–"}
          </div>
        )}
        {run?.summary?.auto_stopped && (
          <div
            className="error"
            style={{
              background: "rgba(255,93,115,.12)",
              border: "1px solid var(--red)",
              borderRadius: 8,
              padding: "10px 12px",
              marginBottom: 12,
            }}
          >
            🛑 {t("run.autoStopped")}: {run.summary.reason}
          </div>
        )}

        {run?.status === "running" && canStop && (
          <button
            className="secondary"
            onClick={() => api.stopRun(runId).then(() => api.getRun(runId).then(setRun))}
          >
            {t("run.stop")}
          </button>
        )}

        {!terminal && <LiveRunChart runId={runId} />}

        {terminal && (
          <div>
            <div className="panel">
              <h2>{t("run.throughput")}</h2>
              <LineChart
                series={[{ label: "qps", color: "#36d6e7", data: series.map((p) => p.rps) }]}
                xLabels={xLabels}
                hoverIndex={hover}
                onHover={setHover}
              />
            </div>
            <div className="panel">
              <h2>{t("run.latency")}</h2>
              <LineChart
                unit="ms"
                series={[
                  { label: "p50", color: "#3ddc97", data: series.map((p) => p.p50_ms) },
                  { label: "p90", color: "#7c8cf8", data: series.map((p) => p.p90_ms) },
                  { label: "p95", color: "#ffc857", data: series.map((p) => p.p95_ms) },
                  { label: "p99", color: "#ff5d73", data: series.map((p) => p.p99_ms) },
                ]}
                xLabels={xLabels}
                hoverIndex={hover}
                onHover={setHover}
              />
            </div>
            <div className="panel">
              <h2>{t("run.errorRate")}</h2>
              <LineChart
                series={[
                  { label: "errors", color: "#ff5d73", data: series.map((p) => p.error_rate * 100) },
                ]}
                xLabels={xLabels}
                hoverIndex={hover}
                onHover={setHover}
              />
            </div>
            {run?.summary?.checks && run.summary.checks.length > 0 && (
              <div className="panel">
                <div className="row" style={{ justifyContent: "space-between", alignItems: "center" }}>
                  <h2 style={{ margin: 0 }}>{t("run.sla")}</h2>
                  <span className={`badge ${run.summary.passed ? "completed" : "failed"}`}>
                    {run.summary.passed ? t("run.passed") : t("run.failed")}
                  </span>
                </div>
                <table style={{ marginTop: 12 }}>
                  <thead>
                    <tr>
                      <th>{t("sla.metric")}</th>
                      <th>{t("sla.op")}</th>
                      <th>{t("sla.value")}</th>
                      <th>{t("sla.actual")}</th>
                      <th>{t("sla.ok")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {run.summary.checks.map((c, i) => (
                      <tr key={i} style={{ color: c.ok ? "var(--green)" : "var(--red)" }}>
                        <td>{c.metric}</td>
                        <td>{c.op}</td>
                        <td>{c.value}</td>
                        <td>{c.actual.toFixed(2)}</td>
                        <td>{c.ok ? "✓" : "✗"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {run?.summary != null && <SummaryReport run={run} t={t} />}
            <ErrorDrilldown runId={runId} series={series} />
          </div>
        )}

        {run?.test_snapshot != null && <SnapshotPanel snapshot={run.test_snapshot} t={t} />}
      </div>
    </>
  );
}

// SummaryReport renders a finished run as a readable report (not a JSON dump).
function SummaryReport({ run, t }: { run: Run; t: (k: string) => string }) {
  const s = run.summary?.summary;
  const total = run.summary?.total_requests ?? 0;
  const durationS =
    run.started_at && run.ended_at
      ? Math.max(1, (new Date(run.ended_at).getTime() - new Date(run.started_at).getTime()) / 1000)
      : 0;
  const avgQps = durationS > 0 ? total / durationS : 0;
  const cell = (label: string, value: string) => (
    <div className="metric">
      <div className="label">{label}</div>
      <div className="value">{value}</div>
    </div>
  );
  return (
    <div className="panel">
      <h2>{t("run.summary")}</h2>
      <div className="metrics-grid">
        {cell(t("report.total"), total.toLocaleString())}
        {cell(t("report.duration"), durationS ? formatElapsed(durationS) : "–")}
        {cell(t("report.avgQps"), avgQps ? avgQps.toFixed(1) : "–")}
        {cell(t("report.errorRate"), s?.error_rate !== undefined ? (s.error_rate * 100).toFixed(2) + "%" : "–")}
        {cell("p50", s?.p50_ms !== undefined ? s.p50_ms.toFixed(1) + " ms" : "–")}
        {cell("p90", s?.p90_ms !== undefined ? s.p90_ms.toFixed(1) + " ms" : "–")}
        {cell("p95", s?.p95_ms !== undefined ? s.p95_ms.toFixed(1) + " ms" : "–")}
        {cell("p99", s?.p99_ms !== undefined ? s.p99_ms.toFixed(1) + " ms" : "–")}
      </div>
    </div>
  );
}

// SnapshotPanel shows the test definition as it was when the run started, so a
// run stays self-describing even after the test is edited or deleted.
function SnapshotPanel({ snapshot, t }: { snapshot: any; t: (k: string) => string }) {
  const [open, setOpen] = useState(false);
  const plan = snapshot?.plan ?? {};
  const http = plan?.http;
  const scenario = plan?.scenario;
  return (
    <div className="panel">
      <div className="row" style={{ justifyContent: "space-between", alignItems: "center" }}>
        <h2 style={{ margin: 0 }}>{t("run.snapshot")}</h2>
        <button className="secondary" onClick={() => setOpen((v) => !v)}>
          {open ? t("run.snapshotHide") : t("run.snapshotShow")}
        </button>
      </div>
      <div className="muted" style={{ marginTop: 8, fontSize: 13 }}>
        {snapshot?.name} · {snapshot?.protocol}
        {http ? ` · ${http.method} ${http.url}` : ""}
        {scenario ? ` · ${scenario.mode} · ${scenario.steps?.length ?? 0} ${t("run.steps")}` : ""}
      </div>
      {open && (
        <pre style={{ marginTop: 10, maxHeight: 360, overflow: "auto", fontSize: 12 }}>
          {JSON.stringify(snapshot, null, 2)}
        </pre>
      )}
    </div>
  );
}
