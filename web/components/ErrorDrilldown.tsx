"use client";

import { useState } from "react";
import { api } from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import Icon from "./Icon";
import type { DrillSample, SeriesPoint } from "@/lib/types";

// ErrorDrilldown lets you inspect what failed after a run: pick an error class
// and load the sampled requests for it. The detail is sampled (bounded, errors
// prioritized) — clearly labeled as not exhaustive.
export default function ErrorDrilldown({
  runId,
  series,
}: {
  runId: string;
  series: SeriesPoint[];
}) {
  const { t } = useI18n();
  const [samples, setSamples] = useState<DrillSample[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [active, setActive] = useState<string>("");
  const [expanded, setExpanded] = useState<number | null>(null);

  // Total errors across the run, from the (already-loaded) series.
  const totalErrors = series.reduce((sum, p) => sum + p.rps * p.error_rate, 0);

  async function load(statusClass: string) {
    setLoading(true);
    setActive(statusClass);
    setExpanded(null);
    try {
      const res = await api.runSamples(runId, { status_class: statusClass || undefined, limit: 200 });
      setSamples(res.samples);
    } catch {
      setSamples([]);
    } finally {
      setLoading(false);
    }
  }

  const classes = [
    { key: "", label: t("drill.all") },
    { key: "5xx", label: "5xx" },
    { key: "4xx", label: "4xx" },
    { key: "err", label: t("drill.transport") },
  ];

  return (
    <div className="panel">
      <h2>{t("drill.title")}</h2>
      <p className="muted" style={{ marginTop: -4, fontSize: 12.5 }}>
        <Icon name="warn" size={13} /> {t("drill.sampledNote")}
        {totalErrors > 0 ? ` · ${t("drill.approxErrors")}: ~${Math.round(totalErrors)}` : ""}
      </p>
      <div className="row" style={{ gap: 8 }}>
        {classes.map((c) => (
          <button
            key={c.key || "all"}
            className={active === c.key && samples !== null ? "" : "secondary"}
            onClick={() => load(c.key)}
          >
            {c.label}
          </button>
        ))}
      </div>

      {loading && <p className="muted" style={{ marginTop: 12 }}>{t("drill.loading")}</p>}

      {samples !== null && !loading && (
        <div style={{ marginTop: 12, maxHeight: 360, overflow: "auto" }}>
          {samples.length === 0 ? (
            <p className="muted">{t("drill.empty")}</p>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>{t("log.colTime")}</th>
                  <th>{t("log.colRequest")}</th>
                  <th>{t("log.colStatus")}</th>
                  <th>{t("log.colError")}</th>
                  <th>{t("log.colLatency")}</th>
                </tr>
              </thead>
              <tbody>
                {samples.map((s, i) => (
                  <RowWithBody
                    key={i}
                    s={s}
                    open={expanded === i}
                    onToggle={() => setExpanded(expanded === i ? null : i)}
                    emptyBody={t("log.bodyEmpty")}
                    bodyLabel={t("log.colBody")}
                    reqBodyLabel={t("log.reqBody")}
                  />
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </div>
  );
}

function RowWithBody({
  s,
  open,
  onToggle,
  emptyBody,
  bodyLabel,
  reqBodyLabel,
}: {
  s: DrillSample;
  open: boolean;
  onToggle: () => void;
  emptyBody: string;
  bodyLabel: string;
  reqBodyLabel: string;
}) {
  return (
    <>
      <tr style={{ color: s.ok ? undefined : "var(--red)", cursor: "pointer" }} onClick={onToggle}>
        <td className="muted">{new Date(s.ts).toLocaleTimeString()}</td>
        <td style={{ maxWidth: 320, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          <b>{s.method}</b> {s.url || s.group}
        </td>
        <td>{s.status || "—"}</td>
        <td>{s.error_kind || ""}</td>
        <td>{(s.latency_us / 1000).toFixed(1)} ms</td>
      </tr>
      {open && (
        <tr>
          <td colSpan={5}>
            {s.req_body && (
              <>
                <div style={{ fontSize: 12, color: "var(--muted)", marginBottom: 4 }}>{reqBodyLabel}</div>
                <pre style={{ margin: "0 0 8px", maxHeight: 160, overflow: "auto", whiteSpace: "pre-wrap", wordBreak: "break-all", fontSize: 12 }}>
                  {s.req_body}
                </pre>
              </>
            )}
            <div style={{ fontSize: 12, color: "var(--muted)", marginBottom: 4 }}>{bodyLabel}</div>
            <pre style={{ margin: 0, maxHeight: 160, overflow: "auto", whiteSpace: "pre-wrap", wordBreak: "break-all", fontSize: 12 }}>
              {s.resp_body || emptyBody}
            </pre>
          </td>
        </tr>
      )}
    </>
  );
}
