"use client";

import { useEffect, useState } from "react";
import Nav from "@/components/Nav";
import LiveRunChart from "@/components/LiveRunChart";
import LineChart from "@/components/LineChart";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import type { Run, SeriesPoint } from "@/lib/types";

export default function RunDetailPage({ params }: { params: { id: string } }) {
  const { t } = useI18n();
  const { user, ready } = useAuth();
  const [run, setRun] = useState<Run | null>(null);
  const [series, setSeries] = useState<SeriesPoint[]>([]);
  const runId = params.id;

  useEffect(() => {
    if (!ready) return;
    const load = () => api.getRun(runId).then(setRun).catch(() => {});
    load();
    const t = setInterval(load, 4000);
    return () => clearInterval(t);
  }, [ready, runId]);

  const terminal = run && run.status !== "running" && run.status !== "pending";

  useEffect(() => {
    if (terminal) {
      api.runSeries(runId).then(setSeries).catch(() => {});
    }
  }, [terminal, runId]);

  if (!ready) return null;
  const canStop = roleAtLeast(user?.role, "operator");

  return (
    <>
      <Nav />
      <div className="container">
        <div className="row" style={{ justifyContent: "space-between" }}>
          <h1>
            {t("run.title")} {runId.slice(0, 8)}
          </h1>
          {run && <span className={`badge ${run.status}`}>{run.status}</span>}
        </div>

        {run?.status === "running" && canStop && (
          <button
            className="secondary"
            onClick={() => api.stopRun(runId).then(() => api.getRun(runId).then(setRun))}
          >
            {t("run.stop")}
          </button>
        )}

        {!terminal && <LiveRunChart runId={runId} />}

        {terminal && (
          <div>
            <div className="panel">
              <h2>{t("run.throughput")}</h2>
              <LineChart
                series={[{ label: "qps", color: "#2f81f7", data: series.map((p) => p.rps) }]}
              />
            </div>
            <div className="panel">
              <h2>{t("run.latency")}</h2>
              <LineChart
                unit="ms"
                series={[
                  { label: "p50", color: "#3fb950", data: series.map((p) => p.p50_ms) },
                  { label: "p95", color: "#d29922", data: series.map((p) => p.p95_ms) },
                  { label: "p99", color: "#f85149", data: series.map((p) => p.p99_ms) },
                ]}
              />
            </div>
            {run?.summary?.checks && run.summary.checks.length > 0 && (
              <div className="panel">
                <div className="row" style={{ justifyContent: "space-between", alignItems: "center" }}>
                  <h2 style={{ margin: 0 }}>{t("run.sla")}</h2>
                  <span className={`badge ${run.summary.passed ? "completed" : "failed"}`}>
                    {run.summary.passed ? t("run.passed") : t("run.failed")}
                  </span>
                </div>
                <table style={{ marginTop: 12 }}>
                  <thead>
                    <tr>
                      <th>{t("sla.metric")}</th>
                      <th>{t("sla.op")}</th>
                      <th>{t("sla.value")}</th>
                      <th>{t("sla.actual")}</th>
                      <th>{t("sla.ok")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {run.summary.checks.map((c, i) => (
                      <tr key={i} style={{ color: c.ok ? "var(--green)" : "var(--red)" }}>
                        <td>{c.metric}</td>
                        <td>{c.op}</td>
                        <td>{c.value}</td>
                        <td>{c.actual.toFixed(2)}</td>
                        <td>{c.ok ? "✓" : "✗"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {run?.summary != null && (
              <div className="panel">
                <h2>{t("run.summary")}</h2>
                <pre style={{ overflow: "auto" }}>{JSON.stringify(run.summary, null, 2)}</pre>
              </div>
            )}
          </div>
        )}
      </div>
    </>
  );
}
