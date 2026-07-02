"use client";

import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { api } from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import { fmtMs } from "@/lib/format";
import Icon from "./Icon";
import SampleTable from "./SampleTable";
import type { DrillSample, SeriesPoint } from "@/lib/types";

// InspectDrawer slides in from the right when a chart point is clicked. It shows
// that moment's metrics and the sampled requests within that time window (errors
// are prioritized in the sample, so error moments are the most fruitful).
export default function InspectDrawer({
  runId,
  series,
  index,
  label,
  onClose,
}: {
  runId: string;
  series: SeriesPoint[];
  index: number;
  label: string;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const [samples, setSamples] = useState<DrillSample[] | null>(null);
  const [loading, setLoading] = useState(false);
  const point = series[index];

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  useEffect(() => {
    if (!point) return;
    setLoading(true);
    setSamples(null);
    const fromMs = new Date(point.ts).getTime();
    const nextTs = series[index + 1]?.ts;
    const toMs = nextTs ? new Date(nextTs).getTime() : fromMs + 2000;
    api
      .runSamples(runId, { from_ms: fromMs, to_ms: toMs, limit: 200 })
      .then((r) => setSamples(r.samples))
      .catch(() => setSamples([]))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runId, index]);

  if (!point || typeof document === "undefined") return null;
  const rps = point.rps ?? 0;
  const errPct = (point.error_rate ?? 0) * 100;

  return createPortal(
    <div className="drawer-backdrop" onClick={onClose}>
      <aside className="drawer" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <div className="drawer-head">
          <div>
            <div className="caption" style={{ color: "var(--muted)" }}>{t("drawer.title")}</div>
            <h2 style={{ margin: "2px 0 0" }}>{label}</h2>
          </div>
          <button className="ghost sm" aria-label="close" onClick={onClose}>
            <Icon name="x" size={16} />
          </button>
        </div>

        <div className="metrics-grid" style={{ marginTop: 4 }}>
          <div className="metric">
            <div className="label">RPS</div>
            <div className="value">{rps.toFixed(rps < 100 ? 1 : 0)}</div>
          </div>
          <div className="metric">
            <div className="label">p50</div>
            <div className="value">{fmtMs(point.p50_ms ?? 0)}</div>
          </div>
          <div className="metric">
            <div className="label">p95</div>
            <div className="value">{fmtMs(point.p95_ms ?? 0)}</div>
          </div>
          <div className="metric">
            <div className="label">p99</div>
            <div className="value">{fmtMs(point.p99_ms ?? 0)}</div>
          </div>
          <div className="metric">
            <div className="label">{t("run.errorRate")}</div>
            <div className="value" style={errPct > 0 ? { color: "var(--red)" } : undefined}>
              {errPct.toFixed(2)}%
            </div>
          </div>
        </div>

        <div style={{ marginTop: 18 }}>
          <h3 style={{ margin: 0 }}>{t("drawer.samples")}</h3>
          <p className="caption" style={{ color: "var(--muted)", marginTop: 2, marginBottom: 8 }}>
            <Icon name="warn" size={12} /> {t("drawer.samplesNote")}
          </p>
          {loading ? <p className="muted">{t("drill.loading")}</p> : <SampleTable samples={samples ?? []} />}
        </div>
      </aside>
    </div>,
    document.body
  );
}
