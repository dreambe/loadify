"use client";

import { Suspense, useEffect, useState } from "react";
import Nav from "@/components/Nav";
import LineChart from "@/components/LineChart";
import { api } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import type { Run, SeriesPoint } from "@/lib/types";

interface Side {
  run?: Run;
  series: SeriesPoint[];
}

function metricsOf(r?: Run) {
  const s = r?.summary?.summary;
  return {
    total: r?.summary?.total_requests ?? 0,
    error_rate: (s?.error_rate ?? 0) * 100,
    p50: s?.p50_ms ?? 0,
    p95: s?.p95_ms ?? 0,
    p99: s?.p99_ms ?? 0,
  };
}

function CompareInner() {
  const { t } = useI18n();
  const [runs, setRuns] = useState<Run[]>([]);
  const [aId, setAId] = useState("");
  const [bId, setBId] = useState("");
  const [a, setA] = useState<Side>({ series: [] });
  const [b, setB] = useState<Side>({ series: [] });

  useEffect(() => {
    api.listRuns().then(setRuns).catch(() => {});
  }, []);

  useEffect(() => {
    if (!aId) return;
    Promise.all([api.getRun(aId), api.runSeries(aId)]).then(([run, series]) =>
      setA({ run, series })
    );
  }, [aId]);
  useEffect(() => {
    if (!bId) return;
    Promise.all([api.getRun(bId), api.runSeries(bId)]).then(([run, series]) =>
      setB({ run, series })
    );
  }, [bId]);

  const ma = metricsOf(a.run);
  const mb = metricsOf(b.run);

  // For latency/error metrics, lower is better; for total/qps, higher is better.
  function delta(metric: string, av: number, bv: number) {
    if (!a.run || !b.run) return null;
    const lowerBetter = metric !== "total";
    const diff = bv - av;
    if (diff === 0) return <span className="muted"> (=)</span>;
    const better = lowerBetter ? diff < 0 : diff > 0;
    const pct = av !== 0 ? ((diff / av) * 100).toFixed(1) : "—";
    return (
      <span style={{ color: better ? "var(--green)" : "var(--red)" }}>
        {" "}
        ({diff > 0 ? "+" : ""}
        {pct}%)
      </span>
    );
  }

  function picker(label: string, value: string, set: (v: string) => void) {
    return (
      <div>
        <label>{label}</label>
        <select value={value} onChange={(e) => set(e.target.value)}>
          <option value="">{t("compare.select")}</option>
          {runs.map((r) => (
            <option key={r.id} value={r.id}>
              {r.name || r.id.slice(0, 8)} · {r.status} · {new Date(r.created_at).toLocaleString()}
            </option>
          ))}
        </select>
      </div>
    );
  }

  const rows: { key: string; label: string; av: number; bv: number; fmt: (n: number) => string }[] = [
    { key: "total", label: t("compare.total"), av: ma.total, bv: mb.total, fmt: (n) => n.toLocaleString() },
    { key: "error", label: t("compare.errorRate"), av: ma.error_rate, bv: mb.error_rate, fmt: (n) => n.toFixed(2) + "%" },
    { key: "p50", label: "p50", av: ma.p50, bv: mb.p50, fmt: (n) => n.toFixed(1) + " ms" },
    { key: "p95", label: "p95", av: ma.p95, bv: mb.p95, fmt: (n) => n.toFixed(1) + " ms" },
    { key: "p99", label: "p99", av: ma.p99, bv: mb.p99, fmt: (n) => n.toFixed(1) + " ms" },
  ];

  return (
    <>
      <Nav />
      <div className="container">
        <h1>{t("compare.title")}</h1>
        <div className="panel">
          <div className="row">
            {picker(t("compare.runA"), aId, setAId)}
            {picker(t("compare.runB"), bId, setBId)}
          </div>
        </div>

        {a.run && b.run && (
          <>
            <div className="panel">
              <table>
                <thead>
                  <tr>
                    <th>{t("compare.metric")}</th>
                    <th>A · {a.run.id.slice(0, 8)}</th>
                    <th>B · {b.run.id.slice(0, 8)}</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((r) => (
                    <tr key={r.key}>
                      <td>{r.label}</td>
                      <td>{r.fmt(r.av)}</td>
                      <td>
                        {r.fmt(r.bv)}
                        {delta(r.key, r.av, r.bv)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <p className="muted" style={{ marginTop: 8 }}>
                {t("compare.hint")}
              </p>
            </div>

            <div className="panel">
              <h2>p95 (ms)</h2>
              <LineChart
                unit="ms"
                series={[
                  { label: "A", color: "#2f81f7", data: a.series.map((p) => p.p95_ms) },
                  { label: "B", color: "#d29922", data: b.series.map((p) => p.p95_ms) },
                ]}
              />
            </div>
            <div className="panel">
              <h2>QPS</h2>
              <LineChart
                series={[
                  { label: "A", color: "#2f81f7", data: a.series.map((p) => p.rps) },
                  { label: "B", color: "#d29922", data: b.series.map((p) => p.rps) },
                ]}
              />
            </div>
          </>
        )}
      </div>
    </>
  );
}

export default function ComparePage() {
  const { ready } = useAuth();
  if (!ready) return null;
  return (
    <Suspense fallback={null}>
      <CompareInner />
    </Suspense>
  );
}
