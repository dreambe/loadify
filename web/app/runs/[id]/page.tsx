"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import Nav from "@/components/Nav";
import LiveRunChart from "@/components/LiveRunChart";
import LineChart, { formatElapsed } from "@/components/LineChart";
import { api, exportCSVURL, reportURL, shareRunURL, setShareToken } from "@/lib/api";
import ErrorDrilldown from "@/components/ErrorDrilldown";
import Modal from "@/components/Modal";
import InspectDrawer from "@/components/InspectDrawer";
import Help from "@/components/Help";
import { useToast } from "@/components/Toast";
import { useConfirm } from "@/components/Confirm";
import Icon from "@/components/Icon";
import { useAuth, roleAtLeast, ownsOrAdmin, getToken } from "@/lib/auth";
import { useI18n, statusLabel } from "@/lib/i18n";
import { fmtMs } from "@/lib/format";
import { chartColor, latencyColors, latencyBandColor } from "@/lib/colors";
import type { Run, SeriesPoint, TrendPoint } from "@/lib/types";

export default function RunDetailPage({ params }: { params: { id: string } }) {
  const { t } = useI18n();
  // Public share mode: a ?share= token in the URL authorizes the read API and
  // lets this real page render with no login (operator actions stay hidden).
  const share = typeof window !== "undefined" ? new URLSearchParams(window.location.search).get("share") : null;
  if (share) setShareToken(share);
  const { user, ready } = useAuth(!share);
  const toast = useToast();
  const confirm = useConfirm();
  const [rerunning, setRerunning] = useState(false);
  const [run, setRun] = useState<Run | null>(null);
  const [series, setSeries] = useState<SeriesPoint[]>([]);
  const [trend, setTrend] = useState<TrendPoint[]>([]);
  const [baselineRunId, setBaselineRunId] = useState<string | null>(null);
  const [exportOpen, setExportOpen] = useState(false);
  const exportRef = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<number | null>(null);
  // Shared across all three charts so they zoom to the same x-window together.
  const [zoom, setZoom] = useState<{ lo: number; hi: number } | null>(null);
  // A clicked moment (opens the inspect drawer) and the chart expanded fullscreen.
  const [selected, setSelected] = useState<number | null>(null);
  const [expandedChart, setExpandedChart] = useState<string | null>(null);
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

  const terminal = run && run.status !== "running" && run.status !== "pending" && run.status !== "queued";

  useEffect(() => {
    if (terminal) {
      api.runSeries(runId).then(setSeries).catch(() => {});
    }
  }, [terminal, runId]);

  useEffect(() => {
    // Trend is a test-level endpoint not covered by a run share token; skip it
    // in share mode (and breadcrumb back-to-list is hidden there too).
    if (terminal && run?.test_def_id && !share) {
      api.testTrend(run.test_def_id, 20).then(setTrend).catch(() => {});
    }
  }, [terminal, run?.test_def_id, share]);

  useEffect(() => {
    // Whether THIS run is its test's current baseline. The run payload can't tell
    // us (the server skips self-comparison, so run.summary.baseline is empty on
    // the baseline run itself) — read it from the test definition. Test-level
    // endpoint, so skip in share mode like the trend above.
    if (terminal && run?.test_def_id && !share) {
      api
        .getTest(run.test_def_id)
        .then((td) => setBaselineRunId(td.baseline_run_id ?? null))
        .catch(() => {});
    }
  }, [terminal, run?.test_def_id, share]);

  // Close the export menu on outside click / Escape (the canonical menu pattern).
  useEffect(() => {
    if (!exportOpen) return;
    const onDoc = (e: MouseEvent) => {
      if (exportRef.current && !exportRef.current.contains(e.target as Node)) setExportOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setExportOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
    };
  }, [exportOpen]);

  async function shareLink() {
    try {
      const { token } = await api.shareRun(runId);
      await navigator.clipboard.writeText(shareRunURL(runId, token));
      toast.success(t("run.shareCopied"));
    } catch {
      toast.error(t("run.shareFailed"));
    }
  }

  async function setAsBaseline() {
    if (!run) return;
    try {
      await api.setBaseline(run.test_def_id, run.id);
      setBaselineRunId(run.id);
      toast.success(t("run.baselineSet"));
    } catch (e: any) {
      toast.error(e.message);
    }
  }

  async function clearAsBaseline() {
    if (!run) return;
    try {
      await api.clearBaseline(run.test_def_id);
      setBaselineRunId(null);
      toast.success(t("run.baselineCleared"));
      api.getRun(runId).then(setRun).catch(() => {});
    } catch (e: any) {
      toast.error(e.message);
    }
  }

  if (!ready) return null;
  const canStop = roleAtLeast(user?.role, "operator");
  const baseline = run?.summary?.baseline;
  const isBaseline = !!run && baselineRunId === run.id;
  // Headline numbers for the KPI strip (lead with the verdict, then the charts).
  const sum = run?.summary?.summary;
  const elapsedMs =
    run?.started_at && run?.ended_at ? new Date(run.ended_at).getTime() - new Date(run.started_at).getTime() : 0;
  const avgRps =
    run?.summary?.total_requests && elapsedMs > 0 ? run.summary.total_requests / (elapsedMs / 1000) : null;
  // The closed (VU) model's latency is optimistic under saturation (coordinated
  // omission); flag it so the curve discloses its own caveat. QPS/arrival-rate
  // runs are not affected.
  const vuMode = isVuMode(run?.test_snapshot);

  // X-axis: elapsed test time from the first series point.
  const seriesBase = series.length > 0 ? new Date(series[0].ts).getTime() : 0;
  const xLabels = series.map((p) => formatElapsed((new Date(p.ts).getTime() - seriesBase) / 1000));

  // Seconds that had errors — flagged with a dot on the error chart so it's
  // obvious where to click to drill in.
  const errorIdx = series.reduce<number[]>((a, p, i) => {
    if (p.error_rate > 0) a.push(i);
    return a;
  }, []);

  // One source of truth for the three charts, reused by the inline panels and
  // the fullscreen modal.
  const chartDefs: {
    key: string;
    title: string;
    unit?: string;
    help?: string;
    header?: React.ReactNode;
    series: { label: string; color: string; data: number[] }[];
    band?: { lower: number[]; upper: number[]; color: string };
    markIndices?: number[];
    markColor?: string;
  }[] = [
    {
      key: "qps",
      title: t("run.throughput"),
      series: [{ label: "qps", color: chartColor.accent, data: series.map((p) => p.rps) }],
    },
    {
      key: "latency",
      title: t("run.latency"),
      unit: "ms",
      help: vuMode ? t("run.coOmissionNote") : undefined,
      header: vuMode ? <span className="badge queued">{t("run.coOmissionBadge")}</span> : undefined,
      series: [
        { label: "p50", color: latencyColors.p50, data: series.map((p) => p.p50_ms) },
        { label: "p90", color: latencyColors.p90, data: series.map((p) => p.p90_ms) },
        { label: "p95", color: latencyColors.p95, data: series.map((p) => p.p95_ms) },
        { label: "p99", color: latencyColors.p99, data: series.map((p) => p.p99_ms) },
      ],
      band: { lower: series.map((p) => p.p50_ms), upper: series.map((p) => p.p99_ms), color: latencyBandColor },
    },
    {
      key: "error",
      title: t("run.errorRate"),
      unit: "%",
      series: [{ label: "errors", color: chartColor.red, data: series.map((p) => p.error_rate * 100) }],
      markIndices: errorIdx,
      markColor: chartColor.red,
    },
  ];

  const renderLineChart = (d: (typeof chartDefs)[number], height?: number, panZoom?: boolean) => (
    <LineChart
      series={d.series}
      unit={d.unit}
      band={d.band}
      markIndices={d.markIndices}
      markColor={d.markColor}
      height={height}
      xLabels={xLabels}
      hoverIndex={hover}
      onHover={setHover}
      zoom={zoom}
      onZoom={setZoom}
      onSelect={setSelected}
      panZoom={panZoom}
      fileName={[run?.name || `run-${runId.slice(0, 8)}`, d.title].join(" - ")}
    />
  );

  return (
    <>
      {/* An anonymous share viewer gets brand-only chrome: the tabs and account
          menu would all dead-end at the login page. */}
      <Nav brandOnly={!!share && !getToken()} />
      <div className="container">
        {!share && (
          <Link href="/runs" className="muted" style={{ fontSize: 13, display: "inline-block", marginBottom: 4 }}>
            ← {t("run.backToRuns")}
          </Link>
        )}
        <div className="row" style={{ justifyContent: "space-between", alignItems: "center" }}>
          {/* Title + run status. Status is information, not an action, so it lives
              by the name — never mixed into the action row. */}
          <div className="row" style={{ alignItems: "center", gap: 12 }}>
            <h1 style={{ margin: 0 }}>{run?.name || `${t("run.title")} ${runId.slice(0, 8)}`}</h1>
            {run && <span className={`badge ${run.status}`}>{statusLabel(t, run.status)}</span>}
          </div>
          {/* Actions: rerun (primary) · baseline toggle (state+action in one
              control) · export/share grouped into one menu to keep the bar calm. */}
          <div className="row" style={{ alignItems: "center" }}>
            {terminal && run && canStop && (
              <>
                <button
                  className="secondary"
                  disabled={rerunning}
                  onClick={() => {
                    if (rerunning) return;
                    setRerunning(true);
                    api
                      .startRun(run.test_def_id, Math.max(1, run.desired_workers), "")
                      .then((res) => (window.location.href = `/runs/${res.run_id}`))
                      .catch((e: any) => {
                        toast.error(e.message);
                        setRerunning(false);
                      });
                  }}
                >
                  <Icon name="rerun" /> {t("runs.rerun")}
                </button>
                <button
                  className={"ghost" + (isBaseline ? " on" : "")}
                  aria-pressed={isBaseline}
                  title={isBaseline ? t("run.clearBaseline") : undefined}
                  onClick={isBaseline ? clearAsBaseline : setAsBaseline}
                >
                  <Icon name="star" /> {isBaseline ? t("run.currentBaseline") : t("run.setBaseline")}
                </button>
              </>
            )}
            {terminal && (
              <div style={{ position: "relative" }} ref={exportRef}>
                <button
                  className="ghost"
                  aria-haspopup="menu"
                  aria-expanded={exportOpen}
                  onClick={() => setExportOpen((v) => !v)}
                >
                  <Icon name="download" /> {t("run.exportMenu")}
                  <Icon name="chevron" size={13} className={"nav-caret" + (exportOpen ? " up" : "")} />
                </button>
                {exportOpen && (
                  <div className="menu" role="menu">
                    <Link
                      className="menu-item"
                      role="menuitem"
                      href={`/compare?a=${runId}${baselineRunId && baselineRunId !== runId ? `&b=${baselineRunId}` : ""}`}
                      onClick={() => setExportOpen(false)}
                    >
                      <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
                        <Icon name="compare" /> {t("run.compare")}
                      </span>
                    </Link>
                    <a
                      className="menu-item"
                      role="menuitem"
                      href={reportURL(runId)}
                      target="_blank"
                      rel="noreferrer"
                      onClick={() => setExportOpen(false)}
                    >
                      <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
                        <Icon name="report" /> {t("run.report")}
                      </span>
                    </a>
                    <a
                      className="menu-item"
                      role="menuitem"
                      href={exportCSVURL(runId)}
                      download
                      onClick={() => setExportOpen(false)}
                    >
                      <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
                        <Icon name="download" /> {t("run.exportCsv")}
                      </span>
                    </a>
                    {canStop && (
                      <button
                        className="menu-item"
                        role="menuitem"
                        onClick={() => {
                          setExportOpen(false);
                          shareLink();
                        }}
                      >
                        <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
                          <Icon name="upload" /> {t("run.share")}
                        </span>
                      </button>
                    )}
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
        {run && (
          <div className="muted" style={{ marginBottom: 12 }}>
            {t("run.creator")}: {run.creator_name || t("run.creatorSystem")}
            {" · "}
            {t("runs.colStarted")}: {run.started_at ? new Date(run.started_at).toLocaleString() : "–"}
            {" · "}
            <span title={runId}>ID: </span>
            <button
              type="button"
              className="id-chip"
              title={t("run.copyId")}
              onClick={async () => {
                try {
                  await navigator.clipboard.writeText(runId);
                  toast.success(t("run.idCopied"));
                } catch {
                  toast.error(t("run.shareFailed"));
                }
              }}
            >
              <code>{runId.slice(0, 8)}</code>
              <Icon name="copy" size={12} />
            </button>
          </div>
        )}
        {run?.summary?.auto_stopped && (
          <div
            className="error"
            style={{
              background: "color-mix(in srgb, var(--red) 12%, transparent)",
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
              background: "color-mix(in srgb, var(--yellow) 12%, transparent)",
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
        {run?.summary?.generator_saturated && (
          <div
            className="error"
            style={{
              background: "color-mix(in srgb, var(--yellow) 12%, transparent)",
              border: "1px solid var(--yellow)",
              color: "var(--yellow)",
              borderRadius: 8,
              padding: "10px 12px",
              marginBottom: 12,
            }}
          >
            <Icon name="warn" /> {t("run.generatorSaturated")}
            <div className="caption" style={{ color: "var(--yellow)", marginTop: 4 }}>
              {run.summary.peak_cpu_pct ? t("run.genPeakCpu").replace("{pct}", run.summary.peak_cpu_pct.toFixed(0)) : ""}
              {run.summary.dropped_iterations ? " · " + t("run.genDroppedIters").replace("{n}", String(run.summary.dropped_iterations)) : ""}
              {run.summary.dropped_metrics ? " · " + t("run.genDroppedMetrics").replace("{n}", String(run.summary.dropped_metrics)) : ""}
            </div>
          </div>
        )}

        {run?.status === "running" && canStop && (
          <button
            className="secondary"
            disabled={!ownsOrAdmin(user, run?.created_by)}
            title={ownsOrAdmin(user, run?.created_by) ? undefined : t("common.ownerOnly")}
            onClick={async () => {
              if (!(await confirm({ title: t("run.stop"), danger: true, confirmLabel: t("run.stop") }))) return;
              await api.stopRun(runId);
              api.getRun(runId).then(setRun);
            }}
          >
            {t("run.stop")}
          </button>
        )}

        {run?.status === "queued" && (
          <div
            className="panel"
            style={{
              background: "color-mix(in srgb, var(--yellow) 10%, transparent)",
              border: "1px solid var(--yellow)",
              display: "flex",
              alignItems: "center",
              gap: 10,
            }}
          >
            <span className="spinner" aria-hidden />
            <div>
              <strong style={{ color: "var(--yellow)" }}>{t("run.queuedTitle")}</strong>
              <div className="muted" style={{ fontSize: 13, marginTop: 2 }}>
                {run.queue_position ? t("run.queuedPosition").replace("{n}", String(run.queue_position)) : t("run.queuedWaiting")}
                {run.queue_eta_ms && run.queue_eta_ms > 0
                  ? " · " + t("run.queuedEta").replace("{eta}", formatElapsed(run.queue_eta_ms / 1000))
                  : ""}
              </div>
            </div>
          </div>
        )}

        {!terminal && run?.status !== "queued" && (
          <LiveRunChart runId={runId} runName={run?.name || `run-${runId.slice(0, 8)}`} />
        )}

        {terminal && (
          <div>
            {sum && (
              <div className="panel">
                <div className="metrics-grid">
                  <div className="metric">
                    <div className="label">{t("run.kpiTotal")}</div>
                    <div className="value">{(run?.summary?.total_requests ?? 0).toLocaleString()}</div>
                  </div>
                  {avgRps !== null && (
                    <div className="metric">
                      <div className="label">{t("run.kpiAvgRps")}</div>
                      <div className="value">{avgRps.toFixed(avgRps < 100 ? 1 : 0)}</div>
                    </div>
                  )}
                  <div className="metric">
                    <div className="label">p50</div>
                    <div className="value">{fmtMs(sum.p50_ms ?? 0)}</div>
                  </div>
                  <div className="metric">
                    <div className="label">p95</div>
                    <div className="value">{fmtMs(sum.p95_ms ?? 0)}</div>
                  </div>
                  <div className="metric">
                    <div className="label">p99</div>
                    <div className="value">{fmtMs(sum.p99_ms ?? 0)}</div>
                  </div>
                  <div className="metric">
                    <div className="label">{t("run.errorRate")}</div>
                    <div className="value">{((sum.error_rate ?? 0) * 100).toFixed(2)}%</div>
                  </div>
                  {(run?.summary?.checks?.length ?? 0) > 0 && (
                    <div className="metric">
                      <div className="label">SLA</div>
                      <div className="value">
                        <span className={`badge ${run?.summary?.passed ? "ok" : "failed"}`}>
                          {run?.summary?.passed ? t("run.passed") : t("run.failed")}
                        </span>
                      </div>
                    </div>
                  )}
                </div>
              </div>
            )}
            {chartDefs.map((d) => (
              <div className="panel" key={d.key}>
                <div className="row" style={{ justifyContent: "space-between", alignItems: "center" }}>
                  <h2 style={{ margin: 0 }}>
                    {d.title}
                    {d.help && <Help tip={d.help} />}
                  </h2>
                  <div className="row" style={{ alignItems: "center", gap: 8 }}>
                    {d.header}
                    <button
                      className="ghost sm"
                      title={t("chart.expand")}
                      aria-label={t("chart.expand")}
                      onClick={() => setExpandedChart(d.key)}
                    >
                      ⤢
                    </button>
                  </div>
                </div>
                <div style={{ height: 10 }} />
                {renderLineChart(d)}
              </div>
            ))}
            {run?.summary?.checks && run.summary.checks.length > 0 && (
              <div className="panel">
                <div className="row" style={{ justifyContent: "space-between", alignItems: "center" }}>
                  <h2 style={{ margin: 0 }}>{t("run.sla")}</h2>
                  <span className={`badge ${run.summary.passed ? "ok" : "failed"}`}>
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
                    <span className={`badge ${run?.summary?.regressed ? "failed" : "ok"}`}>
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
                  series={[{ label: "p95", color: latencyColors.p95, data: trend.map((p) => p.metrics.p95_ms) }]}
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

      {expandedChart &&
        (() => {
          const d = chartDefs.find((c) => c.key === expandedChart);
          if (!d) return null;
          return (
            <Modal
              wide
              title={
                <span style={{ display: "inline-flex", alignItems: "center", gap: 10 }}>
                  {d.title}
                  {d.header}
                </span>
              }
              onClose={() => setExpandedChart(null)}
            >
              {renderLineChart(
                d,
                typeof window !== "undefined" ? Math.max(380, Math.round(window.innerHeight * 0.66)) : 560,
                true
              )}
            </Modal>
          );
        })()}

      {selected !== null && series[selected] && (
        <InspectDrawer
          runId={runId}
          series={series}
          index={selected}
          label={xLabels[selected] ?? `#${selected + 1}`}
          onClose={() => setSelected(null)}
        />
      )}
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
      </div>
      <div className="metrics-grid" style={{ marginTop: 12 }}>
        {cell("p50", s?.p50_ms !== undefined ? fmtMs(s.p50_ms) : "–")}
        {cell("p90", s?.p90_ms !== undefined ? fmtMs(s.p90_ms) : "–")}
        {cell("p95", s?.p95_ms !== undefined ? fmtMs(s.p95_ms) : "–")}
        {cell("p99", s?.p99_ms !== undefined ? fmtMs(s.p99_ms) : "–")}
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

const snapMono: React.CSSProperties = { fontFamily: "var(--font-mono)", wordBreak: "break-all" };

// safeDecode + splitURL break a baked request URL into a readable base path and
// a key/value list of query params, so the snapshot reads like the test builder
// instead of one unreadable query-string blob. Works on templated URLs too.
function safeDecode(s: string): string {
  try {
    return decodeURIComponent(s.replace(/\+/g, " "));
  } catch {
    return s;
  }
}
function splitURL(u: string): { base: string; params: [string, string][] } {
  if (!u) return { base: "", params: [] };
  const qi = u.indexOf("?");
  if (qi < 0) return { base: u, params: [] };
  const params = u
    .slice(qi + 1)
    .split("&")
    .filter(Boolean)
    .map((kv): [string, string] => {
      const eq = kv.indexOf("=");
      return eq < 0 ? [safeDecode(kv), ""] : [safeDecode(kv.slice(0, eq)), safeDecode(kv.slice(eq + 1))];
    });
  return { base: u.slice(0, qi), params };
}

// KVList renders a compact, readable key/value block (query params, headers).
function KVList({ label, rows }: { label: string; rows: [string, string][] }) {
  if (rows.length === 0) return null;
  return (
    <div>
      <div className="muted" style={{ fontSize: 12, marginBottom: 3 }}>{label}</div>
      <div style={{ display: "grid", gap: 2 }}>
        {rows.map(([k, v], i) => (
          <div key={i} style={snapMono}>
            <span style={{ color: "var(--muted)" }}>{k}</span>
            <span style={{ color: "var(--border-strong)" }}> = </span>
            {v || "—"}
          </div>
        ))}
      </div>
    </div>
  );
}

function SnapField({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: "flex", gap: 10 }}>
      <div className="muted" style={{ width: 84, flex: "none", fontSize: 12.5 }}>{label}</div>
      <div style={{ flex: 1, minWidth: 0 }}>{children}</div>
    </div>
  );
}

function SnapshotPanel({ snapshot, t }: { snapshot: any; t: (k: string) => string }) {
  const [open, setOpen] = useState(false);
  const [raw, setRaw] = useState(false);
  const plan = snapshot?.plan ?? {};
  const http = plan?.http;
  const scenario = plan?.scenario;
  const ramp: any[] = Array.isArray(snapshot?.ramp) ? snapshot.ramp : [];
  const thresholds: any[] = Array.isArray(snapshot?.thresholds) ? snapshot.thresholds : [];
  const env = snapshot?.environment;
  const envKeys = env?.vars ? Object.keys(env.vars) : [];
  const req = http ? splitURL(http.url || "") : null;
  const httpHeaders: [string, string][] = http?.headers ? Object.entries(http.headers).map(([k, v]) => [k, String(v)]) : [];
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
        {req ? ` · ${http.method} ${req.base}` : ""}
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
        <div style={{ marginTop: 12, display: "grid", gap: 10, fontSize: 13 }}>
          <SnapField label={t("run.snapTarget")}>
            {req ? (
              <div style={{ display: "grid", gap: 8 }}>
                <div style={snapMono}>
                  <b>{http.method}</b> {req.base}
                </div>
                <KVList label={t("run.snapParams")} rows={req.params} />
                <KVList label={t("run.snapHeaders")} rows={httpHeaders} />
                {http.body ? (
                  <div>
                    <div className="muted" style={{ fontSize: 12, marginBottom: 3 }}>{t("run.snapReqBody")}</div>
                    <pre style={{ margin: 0, maxHeight: 160, overflow: "auto", whiteSpace: "pre-wrap", wordBreak: "break-all", fontSize: 12 }}>
                      {http.body}
                    </pre>
                  </div>
                ) : null}
              </div>
            ) : scenario ? (
              <span>
                {scenario.mode} · {scenario.steps?.length ?? 0} {t("run.steps")}
              </span>
            ) : (
              <span style={snapMono}>{snapshot?.protocol}</span>
            )}
          </SnapField>

          {ramp.length > 0 && (
            <SnapField label={t("run.snapRamp")}>
              <div style={{ display: "grid", gap: 2 }}>
                {ramp.map((st, i) => {
                  const isRps = (st.target_rps ?? 0) > 0;
                  const tgt = isRps ? `${st.target_rps} QPS` : `${st.target_vus ?? 0} VU`;
                  return (
                    <div key={i} style={snapMono}>
                      → {tgt} · {t("run.snapForS")} {Math.round((st.duration_ms || 0) / 1000)}s
                    </div>
                  );
                })}
              </div>
            </SnapField>
          )}

          {thresholds.length > 0 && (
            <SnapField label={t("run.snapThresholds")}>
              <div style={{ display: "grid", gap: 2 }}>
                {thresholds.map((th, i) => (
                  <div key={i} style={snapMono}>
                    {th.metric} {th.op} {th.value}
                  </div>
                ))}
              </div>
            </SnapField>
          )}

          {(plan.think_time_ms || plan.think_time) && (
            <SnapField label={t("run.snapThink")}>
              <span style={snapMono}>
                {plan.think_time_ms ? `${plan.think_time_ms} ms` : plan.think_time?.distribution}
              </span>
            </SnapField>
          )}
          {plan.max_vus ? (
            <SnapField label={t("run.snapMaxVus")}>
              <span style={snapMono}>{plan.max_vus}</span>
            </SnapField>
          ) : null}

          <div>
            <button className="ghost sm" onClick={() => setRaw((v) => !v)}>
              {raw ? t("run.snapRawHide") : t("run.snapRaw")}
            </button>
            {raw && (
              <pre style={{ marginTop: 8, maxHeight: 360, overflow: "auto", fontSize: 12 }}>
                {JSON.stringify(maskSnapshot(snapshot), null, 2)}
              </pre>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
