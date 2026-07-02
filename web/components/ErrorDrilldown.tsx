"use client";

import { useState } from "react";
import { api } from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import Icon from "./Icon";
import SampleTable from "./SampleTable";
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

  // Total errors across the run, from the (already-loaded) series.
  const totalErrors = series.reduce((sum, p) => sum + p.rps * p.error_rate, 0);

  async function load(statusClass: string) {
    setLoading(true);
    setActive(statusClass);
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
          <SampleTable samples={samples} />
        </div>
      )}
    </div>
  );
}
