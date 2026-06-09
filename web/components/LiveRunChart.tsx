"use client";

import { useEffect, useRef, useState } from "react";
import { liveSocketURL } from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import type { LiveTick } from "@/lib/types";
import LineChart from "./LineChart";

const MAX_POINTS = 120;

// LiveRunChart opens a WebSocket to the run's live stream and renders rolling
// RPS / latency / error-rate charts plus the latest headline metrics.
export default function LiveRunChart({ runId }: { runId: string }) {
  const { t } = useI18n();
  const [ticks, setTicks] = useState<LiveTick[]>([]);
  const [connected, setConnected] = useState(false);
  const wsRef = useRef<WebSocket | null>(null);

  useEffect(() => {
    const ws = new WebSocket(liveSocketURL(runId));
    wsRef.current = ws;
    ws.onopen = () => setConnected(true);
    ws.onclose = () => setConnected(false);
    ws.onmessage = (ev) => {
      try {
        const tick = JSON.parse(ev.data) as LiveTick;
        setTicks((prev) => [...prev.slice(-(MAX_POINTS - 1)), tick]);
      } catch {
        /* ignore malformed frame */
      }
    };
    return () => ws.close();
  }, [runId]);

  const last = ticks[ticks.length - 1];

  return (
    <div>
      <div className="metrics-grid">
        <Metric label={t("live.status")} value={connected ? t("live.live") : t("live.closed")} />
        <Metric label={t("live.qps")} value={fmt(last?.rps)} />
        <Metric label={t("live.activeVus")} value={last ? String(last.active_vus) : "–"} />
        <Metric
          label={t("live.errorRate")}
          value={last ? (last.error_rate * 100).toFixed(2) + "%" : "–"}
        />
        <Metric label="p50" value={fmt(last?.p50_ms) + " ms"} />
        <Metric label="p95" value={fmt(last?.p95_ms) + " ms"} />
        <Metric label="p99" value={fmt(last?.p99_ms) + " ms"} />
      </div>

      <div className="panel" style={{ marginTop: 16 }}>
        <h2>{t("run.throughput")}</h2>
        <LineChart series={[{ label: "qps", color: "#2f81f7", data: ticks.map((tk) => tk.rps) }]} />
      </div>

      <div className="panel">
        <h2>{t("run.latency")}</h2>
        <LineChart
          unit="ms"
          series={[
            { label: "p50", color: "#3fb950", data: ticks.map((tk) => tk.p50_ms) },
            { label: "p95", color: "#d29922", data: ticks.map((tk) => tk.p95_ms) },
            { label: "p99", color: "#f85149", data: ticks.map((tk) => tk.p99_ms) },
          ]}
        />
      </div>

      <div className="panel">
        <h2>{t("run.errorRate")}</h2>
        <LineChart
          series={[
            { label: "errors", color: "#f85149", data: ticks.map((tk) => tk.error_rate * 100) },
          ]}
        />
      </div>
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="metric">
      <div className="label">{label}</div>
      <div className="value">{value}</div>
    </div>
  );
}

function fmt(v: number | undefined): string {
  if (v === undefined) return "–";
  return v < 10 ? v.toFixed(1) : Math.round(v).toLocaleString();
}
