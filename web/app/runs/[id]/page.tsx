"use client";

import { useEffect, useRef, useState } from "react";
import Nav from "@/components/Nav";
import LiveRunChart from "@/components/LiveRunChart";
import LineChart, { formatElapsed } from "@/components/LineChart";
import { api, exportCSVURL, reportURL } from "@/lib/api";
import ErrorDrilldown from "@/components/ErrorDrilldown";
import Help from "@/components/Help";
import { useToast } from "@/components/Toast";
import Icon from "@/components/Icon";
import { useAuth, roleAtLeast, ownsOrAdmin } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import { chartColor, latencyColors } from "@/lib/colors";
import type { Run, SeriesPoint, TrendPoint } from "@/lib/types";

export default function RunDetailPage({ params }: { params: { id: string } }) {
  const { t } = useI18n();
  const { user, ready } = useAuth();
  const toast = useToast();
  const [run, setRun] = useState<Run | null>(null);
  const [series, setSeries] = useState<SeriesPoint[]>([]);
  const [trend, setTrend] = useState<TrendPoint[]>([]);
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

  useEffect(() => {
    if (terminal && run?.test_def_id) {
      api.testTrend(run.test_def_id, 20).then(setTrend).catch(() => {});
    }
  }, [terminal, run?.test_def_id]);

  async function setAsBaseline() {
    if (!run) return;
    try {
      await api.setBaseline(run.test_def_id, run.id);
      toast.success(t("run.baselineSet"));
    } catch (e: any) {
      toast.error(e.message);
    }
  }

  async function clearAsBaseline() {
    if (!run) return;
    try {
      await api.clearBaseline(run.test_def_id);
      toast.success(t("run.baselineCleared"));
      api.getRun(runId).then(setRun).catch(() => {});
    } catch (e: any) {
      toast.error(e.message);
    }
  }

  if (!ready) return null;
  const canStop = roleAtLeast(user?.role, "operator");
  const baseline = run?.summary?.baseline;
  // The closed (VU) model's latency is optimistic under saturation (coordinated
  // omission); flag it so the curve discloses its own caveat. QPS/arrival-rate
  // runs are not affected.
  const vuMode = isVuMode(run?.test_snapshot);

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
              <>
                <button
                  className="secondary"
                  onClick={() =>
                    api
                      .startRun(run.test_def_id, Math.max(1, run.desired_workers), "")
                      .then((res) => (window.location.href = `/runs/${res.run_id}`))
                      .catch((e: any) => toast.error(e.message))
                  }
                >
                  <Icon name="rerun" /> {t("runs.rerun")}
                </button>
                <button className="ghost" onClick={setAsBaseline}>
                  <Icon name="star" /> {t("run.setBaseline")}
                </button>
              </>
            )}
            {terminal && (
              <a className="badge" href={reportURL(runId)} target="_blank" rel="noreferrer">
                <Icon name="report" /> {t("run.report")}
              </a>
            )}
            {terminal && (
              <a className="badge" href={exportCSVURL(runId)} download>
                <Icon name="download" /> {t("run.exportCsv")}
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
            <Icon name="warn" /> {t("run.autoStopped")}: {run.summary.reason}
          </div>
        )}
        {run?.summary?.metrics_degraded && (
          <div
            className="error"
            style={{
              background: "rgba(255,200,87,.12)",
              border: "1px solid var(--yellow)",
              color: "var(--yellow)",
              borderRadius: 8,
              padding: "10px 12px",
              marginBottom: 12,
            }}
          >
            <Icon name="warn" /> {t("run.metricsDegraded")}
          </div>
        )}

        {run?.status === "running" && canStop && (
          <button
            className="secondary"
            disabled={!ownsOrAdmin(user, run?.created_by)}
            title={ownsOrAdmin(user, run?.created_by) ? undefined : t("common.ownerOnly")}
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
                series={[{ label: "qps", color: chartColor.accent, data: series.map((p) => p.rps) }]}
                xLabels={xLabels}
                hoverIndex={hover}
                onHover={setHover}
              />
            </div>
            <div className="panel">
              <div className="row" style={{ justifyContent: "space-between", alignItems: "center" }}>
                <h2 style={{ margin: 0 }}>
                  {t("run.latency")}
                  {vuMode && <Help tip={t("run.coOmissionNote")} />}
                </h2>
                {vuMode && <span className="badge queued">{t("run.coOmissionBadge")}</span>}
              </div>
              <div style={{ height: 12 }} />
              <LineChart
                unit="ms"
                series={[
                  { label: "p50", color: latencyColors.p50, data: series.map((p) => p.p50_ms) },
                  { label: "p90", color: latencyColors.p90, data: series.map((p) => p.p90_ms) },
                  { label: "p95", color: latencyColors.p95, data: series.map((p) => p.p95_ms) },
                  { label: "p99", color: latencyColors.p99, data: series.map((p) => p.p99_ms) },
                ]}
                xLabels={xLabels}
                hoverIndex={hover}
                onHover={setHover}
              />
            </div>
            <div className="panel">
              <h2>{t("run.errorRate")}</h2>
              <LineChart
                unit="%"
                series={[
                  { label: "errors", color: chartColor.red, data: series.map((p) => p.error_rate * 100) },
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
            {baseline && (
              <div className="panel">
                <div className="row" style={{ justifyContent: "space-between", alignItems: "center" }}>
                  <h2 style={{ margin: 0 }}>{t("run.vsBaseline")}</h2>
                  <div className="row" style={{ alignItems: "center" }}>
                    <span className={`badge ${run?.summary?.regressed ? "failed" : "completed"}`}>
                      {run?.summary?.regressed ? t("run.regressed") : t("run.noRegress")}
                    </span>
                    {canStop && (
                      <button className="ghost sm" onClick={clearAsBaseline}>
                        {t("run.clearBaseline")}
                      </button>
                    )}
                  </div>
                </div>
                <div className="metrics-grid" style={{ marginTop: 12 }}>
                  <div className="metric">
                    <div className="label">p95 {t("run.baselineWas")}</div>
                    <div className="value">{baseline.p95_ms.toFixed(1)} ms</div>
                  </div>
                  <div className="metric">
                    <div className="label">p95 {t("run.delta")}</div>
                    <div className="value" style={{ color: baseline.p95_delta_pct > 0 ? "var(--red)" : "var(--green)" }}>
                      {baseline.p95_delta_pct > 0 ? "+" : ""}
                      {baseline.p95_delta_pct.toFixed(1)}%
                    </div>
                  </div>
                </div>
              </div>
            )}
            {trend.length > 1 && (
              <div className="panel">
                <h2>{t("run.trend")}</h2>
                <LineChart
                  unit="ms"
                  series={[{ label: "p95", color: chartColor.accent, data: trend.map((p) => p.metrics.p95_ms) }]}
                  xLabels={trend.map((p) => (p.ended_at ? new Date(p.ended_at).toLocaleDateString() : ""))}
                />
                <p className="muted" style={{ fontSize: 12 }}>{t("run.trendHint")}</p>
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
// isVuMode reports whether a run used the closed (VU) load model. The open
// (QPS/arrival-rate) model is indicated by any ramp stage carrying target_rps;
// absent a snapshot we default to true so the honest caveat is shown.
function isVuMode(snapshot: any): boolean {
  const stages = snapshot?.ramp;
  if (!Array.isArray(stages)) return true;
  return !stages.some((s: any) => (s?.target_rps ?? 0) > 0);
}

// maskSnapshot returns a copy with environment variable values masked, so the
// raw dump stays useful without exposing target credentials in the UI (full
// secret handling is tracked separately).
function maskSnapshot(snapshot: any): any {
  if (!snapshot?.environment?.vars) return snapshot;
  const masked = { ...snapshot, environment: { ...snapshot.environment, vars: {} as Record<string, string> } };
  for (const k of Object.keys(snapshot.environment.vars)) masked.environment.vars[k] = "••••••";
  return masked;
}

function SnapshotPanel({ snapshot, t }: { snapshot: any; t: (k: string) => string }) {
  const [open, setOpen] = useState(false);
  const plan = snapshot?.plan ?? {};
  const http = plan?.http;
  const scenario = plan?.scenario;
  const env = snapshot?.environment;
  const envKeys = env?.vars ? Object.keys(env.vars) : [];
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
      {env?.name && (
        <div className="muted" style={{ marginTop: 4, fontSize: 12.5 }}>
          {t("run.snapshotEnv")}: <b style={{ color: "var(--text-2)" }}>{env.name}</b>
          {envKeys.length > 0 && (
            <span style={{ fontFamily: "var(--font-mono)" }}> · {envKeys.join(", ")}</span>
          )}
        </div>
      )}
      {open && (
        <pre style={{ marginTop: 10, maxHeight: 360, overflow: "auto", fontSize: 12 }}>
          {JSON.stringify(maskSnapshot(snapshot), null, 2)}
        </pre>
      )}
    </div>
  );
}
