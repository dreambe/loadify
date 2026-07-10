"use client";

import { useEffect, useState } from "react";
import Nav from "@/components/Nav";
import { api } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useI18n, statusLabel } from "@/lib/i18n";
import type { WorkerInfo } from "@/lib/types";

export default function WorkersPage() {
  const { t } = useI18n();
  const { ready } = useAuth();
  const [workers, setWorkers] = useState<WorkerInfo[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => {
    if (!ready) return;
    const load = () =>
      api
        .listWorkers()
        .then((w) => {
          setWorkers(w);
          setErr("");
        })
        .catch((e: any) => setErr(e?.message || "load failed"))
        .finally(() => setLoaded(true));
    load();
    const id = setInterval(load, 3000);
    return () => clearInterval(id);
  }, [ready]);

  if (!ready) return null;

  const healthy = workers.filter((w) => w.status === "healthy").length;
  const totalVUs = workers.reduce((s, w) => s + (w.active_vus || 0), 0);
  // Stable ordering: the API returns workers in map-iteration order, which
  // reshuffles every poll. Sort by node name so rows stay put.
  const sorted = [...workers].sort((a, b) => a.worker_id.localeCompare(b.worker_id, undefined, { numeric: true }));

  return (
    <>
      <Nav />
      <div className="container">
        <h1>{t("workers.title")}</h1>

        {err && <div className="error">{err}</div>}

        <div className="metrics-grid">
          <Metric label={t("workers.nodes")} value={`${healthy}/${workers.length}`} />
          <Metric label={t("workers.activeVus")} value={String(totalVUs)} />
        </div>

        <div className="panel" style={{ marginTop: 16 }}>
          <table>
            <thead>
              <tr>
                <th>{t("workers.colWorker")}</th>
                <th>{t("workers.colRegion")}</th>
                <th>{t("workers.colStatus")}</th>
                <th>{t("workers.colCpu")}</th>
                <th>{t("workers.colMem")}</th>
                <th>{t("workers.colCores")}</th>
                <th>{t("workers.colActive")}</th>
                <th>{t("workers.colLastSeen")}</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((w) => (
                <tr key={w.worker_id}>
                  <td>{w.worker_id}</td>
                  <td>{w.region}</td>
                  <td>
                    <span className={`badge ${w.status === "healthy" ? "ok" : "failed"}`}>
                      {statusLabel(t, w.status)}
                    </span>
                  </td>
                  <td>
                    <LoadBar pct={w.cpu_pct || 0} />
                  </td>
                  <td>{fmtBytes(w.mem_bytes)}</td>
                  <td>{w.cpu_cores || "–"}</td>
                  <td style={{ fontVariantNumeric: "tabular-nums" }}>{w.active_vus ?? 0}</td>
                  <td className="muted">
                    {w.last_seen_unix_ms ? new Date(w.last_seen_unix_ms).toLocaleTimeString() : "–"}
                  </td>
                </tr>
              ))}
              {loaded && workers.length === 0 && (
                <tr>
                  <td colSpan={8} className="muted">
                    {t("workers.empty")}
                  </td>
                </tr>
              )}
              {!loaded &&
                Array.from({ length: 4 }).map((_, r) => (
                  <tr key={`sk-${r}`}>
                    {Array.from({ length: 8 }).map((_, c) => (
                      <td key={c}>
                        <div className="skeleton" style={{ height: 14, width: c === 0 ? "70%" : "50%" }} />
                      </td>
                    ))}
                  </tr>
                ))}
            </tbody>
          </table>
        </div>
      </div>
    </>
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

// LoadBar renders a CPU percentage as a small bar (green/yellow/red).
function LoadBar({ pct }: { pct: number }) {
  const clamped = Math.max(0, Math.min(100, pct));
  const color = clamped >= 85 ? "var(--red)" : clamped >= 60 ? "var(--yellow)" : "var(--green)";
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
      <div style={{ width: 80, height: 8, background: "var(--panel-2)", borderRadius: 4, overflow: "hidden", border: "1px solid var(--border)" }}>
        <div style={{ width: `${clamped}%`, height: "100%", background: color }} />
      </div>
      <span className="muted" style={{ fontSize: 12 }}>{pct.toFixed(0)}%</span>
    </div>
  );
}

function fmtBytes(n?: number): string {
  if (!n) return "–";
  const mb = n / (1024 * 1024);
  if (mb >= 1024) return (mb / 1024).toFixed(1) + " GB";
  return mb.toFixed(0) + " MB";
}
