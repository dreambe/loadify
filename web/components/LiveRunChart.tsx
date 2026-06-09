"use client";

import { useEffect, useRef, useState } from "react";
import { liveSocketURL } from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import type { LiveTick, LogSample } from "@/lib/types";
import LineChart from "./LineChart";

const MAX_POINTS = 120;
const MAX_LOG = 500;

// LiveRunChart opens a WebSocket to the run's live stream and renders rolling
// QPS / latency / error-rate charts, headline metrics, and a live response log.
export default function LiveRunChart({ runId }: { runId: string }) {
  const { t } = useI18n();
  const [ticks, setTicks] = useState<LiveTick[]>([]);
  const [samples, setSamples] = useState<LogSample[]>([]);
  const [connected, setConnected] = useState(false);
  const [showLog, setShowLog] = useState(true);
  const [errorsOnly, setErrorsOnly] = useState(false);
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
        if (tick.samples && tick.samples.length > 0) {
          setSamples((prev) => [...tick.samples!, ...prev].slice(0, MAX_LOG));
        }
      } catch {
        /* ignore malformed frame */
      }
    };
    return () => ws.close();
  }, [runId]);

  const last = ticks[ticks.length - 1];
  const shownSamples = errorsOnly ? samples.filter((s) => !s.ok) : samples;

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

      <div className="panel">
        <div className="row" style={{ justifyContent: "space-between", alignItems: "center" }}>
          <h2 style={{ margin: 0 }}>{t("log.title")}</h2>
          <div className="row" style={{ alignItems: "center", gap: 16 }}>
            <label style={{ margin: 0, display: "flex", gap: 6, alignItems: "center" }}>
              <input
                type="checkbox"
                checked={errorsOnly}
                onChange={(e) => setErrorsOnly(e.target.checked)}
              />
              {t("log.errorsOnly")}
            </label>
            <button className="secondary" onClick={() => setShowLog((v) => !v)}>
              {showLog ? t("log.hide") : t("log.show")}
            </button>
          </div>
        </div>

        {showLog && (
          <div style={{ maxHeight: 320, overflow: "auto", marginTop: 12 }}>
            <table>
              <thead>
                <tr>
                  <th>{t("log.colTime")}</th>
                  <th>{t("log.colGroup")}</th>
                  <th>{t("log.colStatus")}</th>
                  <th>{t("log.colLatency")}</th>
                  <th>{t("log.colError")}</th>
                </tr>
              </thead>
              <tbody>
                {shownSamples.map((s, i) => (
                  <tr key={i} style={{ color: s.ok ? undefined : "var(--red)" }}>
                    <td className="muted">{new Date(s.ts_unix_ms).toLocaleTimeString()}</td>
                    <td>{s.group}</td>
                    <td>{s.status || (s.ok ? "—" : "✗")}</td>
                    <td>{s.latency_ms.toFixed(1)} ms</td>
                    <td>{s.error_kind || ""}</td>
                  </tr>
                ))}
                {shownSamples.length === 0 && (
                  <tr>
                    <td colSpan={5} className="muted">
                      {t("log.empty")}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}
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
