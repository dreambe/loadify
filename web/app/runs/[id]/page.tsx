"use client";

import { useEffect, useState } from "react";
import Nav from "@/components/Nav";
import LiveRunChart from "@/components/LiveRunChart";
import LineChart from "@/components/LineChart";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast } from "@/lib/auth";
import type { Run, SeriesPoint } from "@/lib/types";

export default function RunDetailPage({ params }: { params: { id: string } }) {
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
          <h1>Run {runId.slice(0, 8)}</h1>
          {run && <span className={`badge ${run.status}`}>{run.status}</span>}
        </div>

        {run?.status === "running" && canStop && (
          <button
            className="secondary"
            onClick={() => api.stopRun(runId).then(() => api.getRun(runId).then(setRun))}
          >
            Stop run
          </button>
        )}

        {!terminal && <LiveRunChart runId={runId} />}

        {terminal && (
          <div>
            <div className="panel">
              <h2>Throughput (req/s)</h2>
              <LineChart
                series={[{ label: "rps", color: "#2f81f7", data: series.map((p) => p.rps) }]}
              />
            </div>
            <div className="panel">
              <h2>Latency (ms)</h2>
              <LineChart
                unit="ms"
                series={[
                  { label: "p50", color: "#3fb950", data: series.map((p) => p.p50_ms) },
                  { label: "p95", color: "#d29922", data: series.map((p) => p.p95_ms) },
                  { label: "p99", color: "#f85149", data: series.map((p) => p.p99_ms) },
                ]}
              />
            </div>
            {run?.summary != null && (
              <div className="panel">
                <h2>Summary</h2>
                <pre style={{ overflow: "auto" }}>{JSON.stringify(run.summary, null, 2)}</pre>
              </div>
            )}
          </div>
        )}
      </div>
    </>
  );
}
