"use client";

import { useState } from "react";
import { useI18n } from "@/lib/i18n";
import type { DrillSample } from "@/lib/types";

// SampleTable renders sampled requests (time / request / status / error /
// latency) with each row expandable to its request & response body. Shared by
// the post-run error drill-down and the per-moment inspect drawer.
export default function SampleTable({ samples }: { samples: DrillSample[] }) {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState<number | null>(null);

  if (samples.length === 0) return <p className="muted">{t("drill.empty")}</p>;

  return (
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
          <Row key={i} s={s} open={expanded === i} onToggle={() => setExpanded(expanded === i ? null : i)} t={t} />
        ))}
      </tbody>
    </table>
  );
}

function Row({
  s,
  open,
  onToggle,
  t,
}: {
  s: DrillSample;
  open: boolean;
  onToggle: () => void;
  t: (k: string) => string;
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
                <div style={{ fontSize: 12, color: "var(--muted)", marginBottom: 4 }}>{t("log.reqBody")}</div>
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
            <div style={{ fontSize: 12, color: "var(--muted)", marginBottom: 4 }}>{t("log.colBody")}</div>
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
    </>
  );
}
