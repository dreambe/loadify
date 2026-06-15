"use client";

import { Fragment, useEffect, useRef, useState } from "react";
import { liveSocketURL } from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import type { LiveTick, LogSample } from "@/lib/types";
import { chartColor, latencyColors } from "@/lib/colors";
import LineChart, { formatElapsed } from "./LineChart";

const MAX_POINTS = 120;
const MAX_LOG = 500;

// LiveRunChart opens a WebSocket to the run's live stream and renders rolling
// QPS / latency / error-rate charts, headline metrics, and a live response log.
// All charts share one hover crosshair so a moment can be read across metrics.
type KeyedSample = LogSample & { _id: number };

export default function LiveRunChart({ runId }: { runId: string }) {
  const { t } = useI18n();
  const [ticks, setTicks] = useState<LiveTick[]>([]);
  const [samples, setSamples] = useState<KeyedSample[]>([]);
  const [connected, setConnected] = useState(false);
  const [everConnected, setEverConnected] = useState(false);
  const [closeInfo, setCloseInfo] = useState("");
  const [showLog, setShowLog] = useState(true);
  const [errorsOnly, setErrorsOnly] = useState(false);
  const [hover, setHover] = useState<number | null>(null);
  const [expanded, setExpanded] = useState<number | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const startRef = useRef<number | null>(null);
  const seqRef = useRef(0);
  // While a log row is expanded, hold incoming samples in a buffer instead of
  // prepending them — otherwise each tick pushes the row being read down out of
  // view. The buffer is flushed when the row is collapsed. A ref mirrors the
  // expanded state so the WS onmessage closure reads the current value.
  const expandedRef = useRef<number | null>(null);
  const bufferRef = useRef<KeyedSample[]>([]);
  useEffect(() => {
    expandedRef.current = expanded;
  }, [expanded]);

  // toggleRow expands/collapses a sample, flushing buffered samples on collapse.
  const toggleRow = (id: number) => {
    if (expanded === id) {
      setExpanded(null);
      if (bufferRef.current.length > 0) {
        const buffered = bufferRef.current;
        bufferRef.current = [];
        setSamples((prev) => [...buffered, ...prev].slice(0, MAX_LOG));
      }
    } else {
      setExpanded(id);
    }
  };

  useEffect(() => {
    // Reconnect on close with exponential backoff (capped). A live run's stream
    // can drop transiently (coordinator failover, brief network blip); without
    // this the status would stick on "closed" and stop updating until reload.
    // This component is only mounted while the run is non-terminal, so retries
    // stop naturally once the run finishes and the parent unmounts it.
    let stopped = false;
    let retries = 0;
    let retryTimer: ReturnType<typeof setTimeout>;

    const connect = () => {
      if (stopped) return;
      const ws = new WebSocket(liveSocketURL(runId));
      wsRef.current = ws;
      ws.onopen = () => {
        setConnected(true);
        setEverConnected(true);
        setCloseInfo("");
        retries = 0;
      };
      ws.onclose = (ev) => {
        setConnected(false);
        // Surface the server's close reason (e.g. "run is queued",
        // "stream unavailable") so a stalled live view is diagnosable instead
        // of an opaque "disconnected".
        if (ev && (ev.reason || ev.code)) setCloseInfo(ev.reason || `code ${ev.code}`);
        if (stopped) return;
        const delay = Math.min(1000 * 2 ** retries, 10000);
        retries++;
        retryTimer = setTimeout(connect, delay);
      };
      ws.onmessage = (ev) => {
        try {
          const tick = JSON.parse(ev.data) as LiveTick;
          if (startRef.current === null) startRef.current = tick.ts_unix_ms;
          setTicks((prev) => [...prev.slice(-(MAX_POINTS - 1)), tick]);
          if (tick.samples && tick.samples.length > 0) {
            // Stable per-sample ids keep row expansion anchored as new samples
            // are prepended.
            const keyed = tick.samples.map((s) => ({ ...s, _id: seqRef.current++ }));
            if (expandedRef.current !== null) {
              // A row is being inspected — buffer so the view doesn't jump.
              bufferRef.current = [...keyed, ...bufferRef.current].slice(0, MAX_LOG);
            } else {
              setSamples((prev) => [...keyed, ...prev].slice(0, MAX_LOG));
            }
          }
        } catch {
          /* ignore malformed frame */
        }
      };
    };

    connect();
    return () => {
      stopped = true;
      clearTimeout(retryTimer);
      wsRef.current?.close();
    };
  }, [runId]);

  const last = ticks[ticks.length - 1];
  const shownSamples = errorsOnly ? samples.filter((s) => !s.ok) : samples;

  // X-axis: elapsed run time of each retained tick (the window scrolls, so the
  // first visible point is not necessarily t=0).
  const base = startRef.current ?? ticks[0]?.ts_unix_ms ?? 0;
  const xLabels = ticks.map((tk) => formatElapsed((tk.ts_unix_ms - base) / 1000));

  // Distinguish connecting (never opened yet) / live (open, data flowing) /
  // waiting (open, no ticks yet — e.g. no workers reporting) / closed.
  const statusValue = connected
    ? ticks.length > 0
      ? t("live.live")
      : t("live.waiting")
    : everConnected
      ? t("live.closed")
      : t("live.connecting");

  return (
    <div>
      <div className="metrics-grid">
        <Metric label={t("live.status")} value={statusValue} />
        <Metric label={t("live.qps")} value={fmt(last?.rps)} />
        <Metric label={t("live.activeVus")} value={last ? String(last.active_vus) : "–"} />
        <Metric
          label={t("live.errorRate")}
          value={last ? (last.error_rate * 100).toFixed(2) + "%" : "–"}
        />
        <Metric label="p50" value={fmt(last?.p50_ms) + " ms"} />
        <Metric label="p90" value={fmt(last?.p90_ms) + " ms"} />
        <Metric label="p95" value={fmt(last?.p95_ms) + " ms"} />
        <Metric label="p99" value={fmt(last?.p99_ms) + " ms"} />
      </div>

      {!connected && everConnected && closeInfo && (
        <p className="muted" style={{ marginTop: 4, fontSize: 12, color: "var(--yellow)" }}>
          {t("live.closedReason")}: {closeInfo}
        </p>
      )}

      <div className="panel" style={{ marginTop: 16 }}>
        <h2>{t("run.throughput")}</h2>
        <LineChart
          series={[{ label: "qps", color: chartColor.accent, data: ticks.map((tk) => tk.rps) }]}
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
            { label: "p50", color: latencyColors.p50, data: ticks.map((tk) => tk.p50_ms) },
            { label: "p90", color: latencyColors.p90, data: ticks.map((tk) => tk.p90_ms) },
            { label: "p95", color: latencyColors.p95, data: ticks.map((tk) => tk.p95_ms) },
            { label: "p99", color: latencyColors.p99, data: ticks.map((tk) => tk.p99_ms) },
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
            { label: "errors", color: chartColor.red, data: ticks.map((tk) => tk.error_rate * 100) },
          ]}
          xLabels={xLabels}
          hoverIndex={hover}
          onHover={setHover}
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
          <div style={{ maxHeight: 400, overflow: "auto", marginTop: 12 }}>
            <table>
              <thead>
                <tr>
                  <th>{t("log.colTime")}</th>
                  <th>{t("log.colRequest")}</th>
                  <th>{t("log.colStatus")}</th>
                  <th>{t("log.colLatency")}</th>
                  <th>{t("log.colError")}</th>
                </tr>
              </thead>
              <tbody>
                {shownSamples.map((s) => (
                  <Fragment key={s._id}>
                    <tr
                      style={{ color: s.ok ? undefined : "var(--red)", cursor: "pointer" }}
                      onClick={() => toggleRow(s._id)}
                      title={t("log.expandHint")}
                    >
                      <td className="muted">{new Date(s.ts_unix_ms).toLocaleTimeString()}</td>
                      <td
                        style={{
                          maxWidth: 320,
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                          whiteSpace: "nowrap",
                        }}
                      >
                        {s.method || s.url ? (
                          <>
                            <b>{s.method}</b> {s.url}
                          </>
                        ) : (
                          s.group
                        )}
                      </td>
                      <td>{s.status || (s.ok ? "—" : "✗")}</td>
                      <td>{s.latency_ms.toFixed(1)} ms</td>
                      <td>{s.error_kind || ""}</td>
                    </tr>
                    {expanded === s._id && (
                      <tr>
                        <td colSpan={5}>
                          {s.req_body && (
                            <>
                              <div style={{ fontSize: 12, color: "var(--muted)", marginBottom: 4 }}>
                                {t("log.reqBody")}
                              </div>
                              <pre
                                style={{
                                  margin: "0 0 8px",
                                  maxHeight: 160,
                                  overflow: "auto",
                                  whiteSpace: "pre-wrap",
                                  wordBreak: "break-all",
                                  fontSize: 12,
                                }}
                              >
                                {s.req_body}
                              </pre>
                            </>
                          )}
                          <div style={{ fontSize: 12, color: "var(--muted)", marginBottom: 4 }}>
                            {t("log.colBody")}
                          </div>
                          <pre
                            style={{
                              margin: 0,
                              maxHeight: 160,
                              overflow: "auto",
                              whiteSpace: "pre-wrap",
                              wordBreak: "break-all",
                              fontSize: 12,
                            }}
                          >
                            {s.resp_body || t("log.bodyEmpty")}
                          </pre>
                        </td>
                      </tr>
                    )}
                  </Fragment>
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
