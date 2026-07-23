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

// TargetMetricsPanels shows the system-under-test's own resource metrics
// (CPU/mem/disk/net), pulled from the operator's Prometheus for this run's
// window and drawn on loadify's native charts — so the target's vitals sit next
// to the applied load on the same page. Renders nothing when the deployment has
// no Prometheus or the test didn't opt into target monitoring.
export default function TargetMetricsPanels({ runId, startMs, live }: { runId: string; startMs: number; live: boolean }) {
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
          <TargetPanel key={p.key} p={p} startMs={startMs} title={t(TITLE_KEY[p.key] || p.key)} />
        ))}
      </div>
    </div>
  );
}

function TargetPanel({ p, startMs, title }: { p: TargetMetricPanel; startMs: number; title: string }) {
  // All series in a panel share the query_range grid, so the first series' ts
  // axis labels every point.
  const ts = p.series[0]?.points.map((pt) => pt.ts) ?? [];
  const xLabels = ts.map((x) => formatElapsed((x - startMs) / 1000));
  // rx/tx (or multiple lines) get distinct colors AND line styles for a11y.
  const palette = [chartColor.accent, chartColor.yellow, chartColor.violet, chartColor.green];
  const dash = ["", "6 4", "2 3", "10 4 2 4"];
  const series = p.series.map((s, i) => ({
    label: s.label,
    color: palette[i % palette.length],
    dash: dash[i % dash.length],
    data: s.points.map((pt) => pt.v),
  }));
  return (
    <div>
      <div className="target-panel-title">{title}</div>
      <LineChart series={series} xLabels={xLabels} unit={p.unit} height={160} />
    </div>
  );
}
