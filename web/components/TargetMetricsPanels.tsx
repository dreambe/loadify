"use client";

import { useEffect, useState } from "react";
import LineChart, { formatElapsed } from "@/components/LineChart";
import { api } from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import { chartColor } from "@/lib/colors";
import type { TargetMetrics, TargetMetricPanel } from "@/lib/types";

// Localized panel titles by metric key; unknown keys fall back to the key.
const TITLE_KEY: Record<string, string> = {
  cpu: "target.cpu",
  load: "target.load",
  mem: "target.mem",
  disk: "target.disk",
  net: "target.net",
  diskio: "target.diskio",
  conns: "target.conns",
  fds: "target.fds",
};

// Shared crosshair/zoom + x-grid so the target charts line up with (and pan/zoom
// together with) loadify's load charts above. Optional — omitted for the live
// view, where each chart keeps its own axis.
interface SyncProps {
  gridTs?: number[]; // per-point timestamps of the load charts' x-axis (ms)
  xLabels?: string[];
  hover?: number | null;
  onHover?: (i: number | null) => void;
  zoom?: { lo: number; hi: number } | null;
  onZoom?: (z: { lo: number; hi: number } | null) => void;
  onSelect?: (i: number) => void;
}

// TargetMetricsPanels shows the system-under-test's own resource metrics
// (CPU/mem/disk/net/…), pulled from the operator's Prometheus for this run's
// window and drawn on loadify's native charts — the target's vitals beside the
// applied load, on ONE shared timeline. Renders nothing when the deployment has
// no Prometheus or the test didn't opt into target monitoring.
export default function TargetMetricsPanels({
  runId,
  startMs,
  live,
  sync,
}: {
  runId: string;
  startMs: number;
  live: boolean;
  sync?: SyncProps;
}) {
  const { t } = useI18n();
  const [data, setData] = useState<TargetMetrics | null>(null);

  useEffect(() => {
    let alive = true;
    const load = () => api.targetMetrics(runId).then((d) => alive && setData(d)).catch(() => {});
    load();
    if (!live) return () => { alive = false; };
    const id = setInterval(load, 5000); // refresh while the run is in flight
    return () => { alive = false; clearInterval(id); };
  }, [runId, live]);

  if (!data || !data.enabled || data.panels.length === 0) return null;

  return (
    <div className="panel" style={{ marginTop: 16 }}>
      <h2 style={{ marginBottom: 12 }}>
        {t("target.title")}
        {data.instance && <span className="muted" style={{ fontWeight: 400, fontSize: 13 }}> · {data.instance}</span>}
      </h2>
      <div className="target-grid">
        {data.panels.map((p) => (
          <TargetPanel key={p.key} p={p} startMs={startMs} title={t(TITLE_KEY[p.key] || p.key)} sync={sync} />
        ))}
      </div>
    </div>
  );
}

// resampleStep aligns a target series (its own sparse Prometheus timestamps)
// onto the load charts' x-grid via step-fill: each grid slot takes the latest
// sample at or before it, NaN before the first sample (so the line just starts
// when data does). This lets one shared hover index / zoom window mean the same
// instant on every chart despite different native resolutions.
function resampleStep(points: { ts: number; v: number }[], grid: number[]): number[] {
  const out = new Array(grid.length).fill(NaN);
  let j = 0;
  for (let i = 0; i < grid.length; i++) {
    while (j < points.length && points[j].ts <= grid[i]) j++;
    if (j > 0) out[i] = points[j - 1].v;
  }
  return out;
}

function TargetPanel({ p, startMs, title, sync }: { p: TargetMetricPanel; startMs: number; title: string; sync?: SyncProps }) {
  const palette = [chartColor.accent, chartColor.yellow, chartColor.violet, chartColor.green];
  const dash = ["", "6 4", "2 3", "10 4 2 4"];

  const aligned = sync?.gridTs && sync.gridTs.length > 0;
  let xLabels: string[];
  let series: { label: string; color: string; dash: string; data: number[] }[];

  if (aligned) {
    // Same x-grid as the load charts → shared crosshair/zoom line up in time.
    xLabels = sync!.xLabels ?? sync!.gridTs!.map((tms) => formatElapsed((tms - startMs) / 1000));
    series = p.series.map((s, i) => ({
      label: s.label,
      color: palette[i % palette.length],
      dash: dash[i % dash.length],
      data: resampleStep(s.points, sync!.gridTs!),
    }));
  } else {
    // Standalone (live view): the series' own timestamps.
    const ts = p.series[0]?.points.map((pt) => pt.ts) ?? [];
    xLabels = ts.map((x) => formatElapsed((x - startMs) / 1000));
    series = p.series.map((s, i) => ({
      label: s.label,
      color: palette[i % palette.length],
      dash: dash[i % dash.length],
      data: s.points.map((pt) => pt.v),
    }));
  }

  return (
    <div>
      <div className="target-panel-title">{title}</div>
      <LineChart
        series={series}
        xLabels={xLabels}
        unit={p.unit}
        height={160}
        hoverIndex={aligned ? sync!.hover : undefined}
        onHover={aligned ? sync!.onHover : undefined}
        zoom={aligned ? sync!.zoom : undefined}
        onZoom={aligned ? sync!.onZoom : undefined}
        onSelect={aligned ? sync!.onSelect : undefined}
      />
    </div>
  );
}
