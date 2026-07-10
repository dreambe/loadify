"use client";

import { useEffect, useMemo, useState, type ReactNode } from "react";
import Link from "next/link";
import Nav from "@/components/Nav";
import Icon from "@/components/Icon";
import RunStatus from "@/components/RunStatus";
import { api } from "@/lib/api";
import { getToken, useAuth } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import { fmtMs } from "@/lib/format";
import Help from "@/components/Help";
import type { Run, TestDefinition, WorkerInfo } from "@/lib/types";

const terminalStatuses = new Set(["completed", "failed", "aborted"]);

export default function DashboardPage() {
  const { t } = useI18n();
  const { ready } = useAuth();
  const [runs, setRuns] = useState<Run[]>([]);
  const [tests, setTests] = useState<TestDefinition[]>([]);
  const [workers, setWorkers] = useState<WorkerInfo[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [err, setErr] = useState("");
  // Whole-table KPI aggregates from the server (correct beyond the capped runs
  // list); null until the first fetch resolves.
  const [rstats, setRstats] = useState<{
    running: number;
    last24h: number;
    pass_rate: number | null;
  } | null>(null);

  useEffect(() => {
    if (!ready) return;
    if (!getToken()) {
      window.location.href = "/login";
      return;
    }
    const load = () => {
      Promise.all([api.listRuns(), api.listWorkers(), api.runStats(), api.listTests()])
        .then(([r, w, st, ts]) => {
          setRuns(r);
          setWorkers(w);
          setRstats(st);
          setTests(ts);
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
    // Prefer the server-side aggregates (whole history); fall back to the
    // fetched page only while the first /runs/stats call is still in flight.
    if (rstats) {
      return { healthy, total: workers.length, running: rstats.running, last24h: rstats.last24h, passRate: rstats.pass_rate };
    }
    const running = runs.filter((r) => !terminalStatuses.has(r.status)).length;
    const cutoff = Date.now() - 24 * 3600 * 1000;
    const last24h = runs.filter((r) => new Date(r.created_at).getTime() >= cutoff).length;
    // SLA pass rate over finished runs that carry a pass/fail verdict.
    const judged = runs.filter((r) => terminalStatuses.has(r.status) && r.summary?.passed !== undefined);
    const passed = judged.filter((r) => r.summary?.passed).length;
    const passRate = judged.length ? Math.round((passed / judged.length) * 100) : null;
    return { healthy, total: workers.length, running, last24h, passRate };
  }, [runs, workers, rstats]);

  // Per-test p95 trend. Latency is only comparable WITHIN one test/target — a
  // 10ms cache API and a 2s LLM API share no meaningful axis — so we group by
  // test and give each its own sparkline + latest verdict, instead of one
  // cross-test line that averages apples and oranges.
  const perTest = useMemo(() => {
    const nameOf = (id: string) => tests.find((t) => t.id === id)?.name;
    const byId = new Map<string, Run[]>();
    for (const r of runs) {
      if (!r.test_def_id) continue;
      (byId.get(r.test_def_id) ?? byId.set(r.test_def_id, []).get(r.test_def_id)!).push(r);
    }
    return [...byId.entries()]
      .map(([id, rs]) => {
        // runs come newest-first; take finished ones with a p95, oldest→newest.
        const finished = rs.filter((r) => terminalStatuses.has(r.status) && r.summary?.summary?.p95_ms != null);
        const p95 = finished.map((r) => r.summary!.summary!.p95_ms!).reverse();
        const latest = rs[0];
        return { id, name: nameOf(id) || latest?.name || id.slice(0, 8), p95, latest, lastAt: new Date(rs[0].created_at).getTime() };
      })
      .filter((tst) => tst.p95.length > 0)
      .sort((a, b) => b.lastAt - a.lastAt)
      .slice(0, 8);
  }, [runs, tests]);

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
          <Link className="cta" href="/runs">
            <Icon name="play" /> {t("dashboard.start")}
          </Link>
        </div>

        {err && !loaded && <div className="error">{err}</div>}

        <div className="metrics-grid">
          <Metric label={t("dashboard.workers")} value={`${stats.healthy}/${stats.total}`} accent={stats.healthy === 0 ? "var(--red)" : undefined} />
          <Metric label={t("dashboard.running")} value={String(stats.running)} accent={stats.running > 0 ? "var(--accent)" : undefined} />
          <Metric label={t("dashboard.last24h")} value={String(stats.last24h)} />
          <Metric
            label={<>{t("dashboard.slaPass")}<Help tip={t("dashboard.slaPassHelp")} /></>}
            value={stats.passRate == null ? "–" : `${stats.passRate}%`}
            accent={slaColor}
          />
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

        {perTest.length > 0 && (
          <div className="panel">
            <div className="row" style={{ justifyContent: "space-between", alignItems: "center", marginBottom: 4 }}>
              <h2 style={{ margin: 0 }}>
                {t("dashboard.perTestTrend")}
                <Help tip={t("dashboard.perTestTrendHelp")} />
              </h2>
            </div>
            <div className="pertest-grid">
              {perTest.map((tst) => (
                <Link key={tst.id} href={`/tests`} className="pertest-card">
                  <div className="pertest-name" title={tst.name}>{tst.name}</div>
                  <div className="row" style={{ alignItems: "center", justifyContent: "space-between", gap: 8 }}>
                    <Sparkline data={tst.p95} />
                    <div style={{ textAlign: "right", flex: "none" }}>
                      <div style={{ fontFamily: "var(--font-mono)", fontVariantNumeric: "tabular-nums", fontSize: 15 }}>
                        {fmtMs(tst.p95[tst.p95.length - 1])}
                      </div>
                      <div className="muted" style={{ fontSize: 11 }}>p95 · {tst.p95.length} {t("dashboard.perTestRuns")}</div>
                    </div>
                  </div>
                  {tst.latest && (
                    <div style={{ marginTop: 6 }}>
                      <RunStatus run={tst.latest} />
                    </div>
                  )}
                </Link>
              ))}
            </div>
          </div>
        )}
      </div>
    </>
  );
}

// Sparkline draws a tiny inline p95 trend for one test (comparable within it).
function Sparkline({ data }: { data: number[] }) {
  const w = 120;
  const h = 34;
  if (data.length === 0) return <svg width={w} height={h} />;
  const max = Math.max(...data);
  const min = Math.min(...data);
  const span = max - min || 1;
  const n = data.length;
  const x = (i: number) => (n === 1 ? w : (i / (n - 1)) * (w - 2) + 1);
  const y = (v: number) => h - 3 - ((v - min) / span) * (h - 6);
  const pts = data.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`);
  const line = pts.map((p, i) => (i === 0 ? "M" : "L") + p).join(" ");
  const last = data[data.length - 1];
  const rising = data.length > 1 && last >= data[0];
  const stroke = rising ? "var(--yellow)" : "var(--accent)";
  return (
    <svg width={w} height={h} role="img" aria-label="p95 sparkline">
      <path d={`${line} L${x(n - 1).toFixed(1)},${h} L${x(0).toFixed(1)},${h} Z`} fill={stroke} fillOpacity={0.12} stroke="none" />
      <path d={line} fill="none" stroke={stroke} strokeWidth={1.5} strokeLinejoin="round" />
      <circle cx={x(n - 1)} cy={y(last)} r={2} fill={stroke} />
    </svg>
  );
}

function Metric({ label, value, accent }: { label: ReactNode; value: string; accent?: string }) {
  return (
    <div className="metric">
      <div className="label">{label}</div>
      <div className="value" style={accent ? { color: accent } : undefined}>
        {value}
      </div>
    </div>
  );
}
