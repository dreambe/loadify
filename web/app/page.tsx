"use client";

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import Nav from "@/components/Nav";
import Icon from "@/components/Icon";
import LineChart from "@/components/LineChart";
import RunStatus from "@/components/RunStatus";
import { api } from "@/lib/api";
import { getToken, useAuth } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import { fmtMs } from "@/lib/format";
import { latencyColors } from "@/lib/colors";
import type { Run, WorkerInfo } from "@/lib/types";

const terminalStatuses = new Set(["completed", "failed", "stopped"]);

export default function DashboardPage() {
  const { t } = useI18n();
  const { ready } = useAuth();
  const [runs, setRuns] = useState<Run[]>([]);
  const [workers, setWorkers] = useState<WorkerInfo[]>([]);
  const [hover, setHover] = useState<number | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => {
    if (!ready) return;
    if (!getToken()) {
      window.location.href = "/login";
      return;
    }
    const load = () => {
      Promise.all([api.listRuns(), api.listWorkers()])
        .then(([r, w]) => {
          setRuns(r);
          setWorkers(w);
          setErr("");
        })
        .catch((e: any) => setErr(e?.message || "load failed"))
        .finally(() => setLoaded(true));
    };
    load();
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, [ready]);

  const stats = useMemo(() => {
    const healthy = workers.filter((w) => w.status === "healthy").length;
    const running = runs.filter((r) => !terminalStatuses.has(r.status)).length;
    const cutoff = Date.now() - 24 * 3600 * 1000;
    const last24h = runs.filter((r) => new Date(r.created_at).getTime() >= cutoff).length;
    // SLA pass rate over finished runs that carry a pass/fail verdict.
    const judged = runs.filter((r) => terminalStatuses.has(r.status) && r.summary?.passed !== undefined);
    const passed = judged.filter((r) => r.summary?.passed).length;
    const passRate = judged.length ? Math.round((passed / judged.length) * 100) : null;
    return { healthy, total: workers.length, running, last24h, passRate };
  }, [runs, workers]);

  // p95 of the most recent finished runs, oldest→newest, for the SLA trend line.
  const trend = useMemo(() => {
    const finished = runs
      .filter((r) => terminalStatuses.has(r.status) && r.summary?.summary?.p95_ms != null)
      .slice(0, 20)
      .reverse();
    return {
      labels: finished.map((r) => new Date(r.created_at).toLocaleDateString()),
      p95: finished.map((r) => r.summary!.summary!.p95_ms || 0),
    };
  }, [runs]);

  const recent = runs.slice(0, 6);

  if (!ready) return null;

  const slaColor =
    stats.passRate == null ? undefined : stats.passRate >= 90 ? "var(--green)" : stats.passRate >= 70 ? "var(--yellow)" : "var(--red)";

  return (
    <>
      <Nav />
      <div className="container">
        <div className="row" style={{ justifyContent: "space-between", alignItems: "center" }}>
          <h1>{t("dashboard.title")}</h1>
          <Link className="badge" href="/runs">
            <Icon name="play" /> {t("dashboard.start")}
          </Link>
        </div>

        {err && !loaded && <div className="error">{err}</div>}

        <div className="metrics-grid">
          <Metric label={t("dashboard.workers")} value={`${stats.healthy}/${stats.total}`} accent={stats.healthy === 0 ? "var(--red)" : undefined} />
          <Metric label={t("dashboard.running")} value={String(stats.running)} accent={stats.running > 0 ? "var(--accent)" : undefined} />
          <Metric label={t("dashboard.last24h")} value={String(stats.last24h)} />
          <Metric label={t("dashboard.slaPass")} value={stats.passRate == null ? "–" : `${stats.passRate}%`} accent={slaColor} />
        </div>

        <div className="panel">
          <div className="row" style={{ justifyContent: "space-between", alignItems: "center", marginBottom: 4 }}>
            <h2 style={{ margin: 0 }}>{t("dashboard.recentRuns")}</h2>
            <Link className="ghost sm" href="/runs">
              {t("dashboard.viewAll")} →
            </Link>
          </div>
          {recent.length === 0 ? (
            <p className="muted" style={{ marginTop: 12 }}>
              {t("dashboard.empty")}
            </p>
          ) : (
            <table>
              <tbody>
                {recent.map((r) => (
                  <tr key={r.id}>
                    <td>
                      <Link href={`/runs/${r.id}`}>{r.name || r.id.slice(0, 8)}</Link>
                    </td>
                    <td>
                      <RunStatus run={r} />
                    </td>
                    <td className="muted" style={{ fontVariantNumeric: "tabular-nums" }}>
                      {r.summary?.summary?.p95_ms != null ? `p95 ${fmtMs(r.summary.summary.p95_ms)}` : "—"}
                    </td>
                    <td className="muted" style={{ textAlign: "right" }}>
                      {r.started_at ? new Date(r.started_at).toLocaleString() : new Date(r.created_at).toLocaleString()}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        {trend.p95.length > 1 && (
          <div className="panel">
            <h2>{t("dashboard.slaTrend")}</h2>
            <LineChart
              unit="ms"
              series={[{ label: "p95", color: latencyColors.p95, data: trend.p95 }]}
              xLabels={trend.labels}
              hoverIndex={hover}
              onHover={setHover}
            />
          </div>
        )}
      </div>
    </>
  );
}

function Metric({ label, value, accent }: { label: string; value: string; accent?: string }) {
  return (
    <div className="metric">
      <div className="label">{label}</div>
      <div className="value" style={accent ? { color: accent } : undefined}>
        {value}
      </div>
    </div>
  );
}
