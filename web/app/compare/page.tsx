"use client";

import { Suspense, useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import Nav from "@/components/Nav";
import LineChart, { formatElapsed } from "@/components/LineChart";
import EntityPicker from "@/components/EntityPicker";
import { api } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useI18n, statusLabel } from "@/lib/i18n";
import { fmtMs } from "@/lib/format";
import { compareColors, compareDash } from "@/lib/colors";
import type { Run, SeriesPoint, TestDefinition } from "@/lib/types";

interface Side {
  run?: Run;
  series: SeriesPoint[];
}

// A full UUID (what the run detail page's copy button yields) that may be
// outside the loaded window — accept it so getRun() fetches it on selection.
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function metricsOf(r?: Run) {
  const s = r?.summary?.summary;
  return {
    // A run only has final metrics once it finishes; a still-running run has no
    // summary, so its column shows "—" rather than misleading zeros.
    has: !!s,
    total: r?.summary?.total_requests ?? 0,
    error_rate: (s?.error_rate ?? 0) * 100,
    p50: s?.p50_ms ?? 0,
    p90: s?.p90_ms ?? 0,
    p95: s?.p95_ms ?? 0,
    p99: s?.p99_ms ?? 0,
  };
}

function CompareInner() {
  const { t } = useI18n();
  const params = useSearchParams();
  const [runs, setRuns] = useState<Run[]>([]);
  const [tests, setTests] = useState<TestDefinition[]>([]);
  // Seed the two sides from the URL so a comparison is a shareable link
  // (/compare?a=<run>&b=<run>) and the run page can deep-link into it.
  const [aId, setAId] = useState(() => params.get("a") || "");
  const [bId, setBId] = useState(() => params.get("b") || "");
  const [a, setA] = useState<Side>({ series: [] });
  const [b, setB] = useState<Side>({ series: [] });
  const [hover, setHover] = useState<number | null>(null);
  const [err, setErr] = useState("");

  // Keep the URL in sync with the current selection without a full navigation,
  // so copying the address bar reproduces the comparison.
  useEffect(() => {
    const q = new URLSearchParams();
    if (aId) q.set("a", aId);
    if (bId) q.set("b", bId);
    const qs = q.toString();
    window.history.replaceState(null, "", qs ? `?${qs}` : window.location.pathname);
  }, [aId, bId]);

  useEffect(() => {
    // Pull a deep history so older runs are searchable here, not just the last
    // 100 the runs list shows.
    Promise.all([api.listRuns(500), api.listTests()])
      .then(([r, ts]) => {
        setRuns(r);
        setTests(ts);
        setErr("");
      })
      .catch((e: any) => setErr(e?.message || "load failed"));
  }, []);

  // Map a run to its test (用例) name so the picker can search and label by it —
  // runs are often named "<test> @ <time>", but a custom-named run would
  // otherwise be unfindable by its test name.
  const testName = (r: Run) => tests.find((td) => td.id === r.test_def_id)?.name ?? "";

  // How a run reads in the picker (test name first so prefix-matching browsers
  // find it by 用例), and every string the typed/pasted value may match.
  const runLabel = (r: Run) => {
    const tn = testName(r);
    const name = r.name || r.id.slice(0, 8);
    const head = tn && !name.includes(tn) ? `${tn} · ${name}` : name;
    return `${head} · ${statusLabel(t, r.status)} · ${new Date(r.created_at).toLocaleString()} · ${r.id.slice(0, 8)}`;
  };
  const runKeys = (r: Run) => [r.id, r.id.slice(0, 8), r.name ?? "", testName(r)].filter(Boolean);
  const acceptId = (raw: string) => (UUID_RE.test(raw) ? raw : undefined);

  useEffect(() => {
    if (!aId) return;
    Promise.all([api.getRun(aId), api.runSeries(aId)])
      .then(([run, series]) => setA({ run, series }))
      .catch(() => {});
  }, [aId]);
  useEffect(() => {
    if (!bId) return;
    Promise.all([api.getRun(bId), api.runSeries(bId)])
      .then(([run, series]) => setB({ run, series }))
      .catch(() => {});
  }, [bId]);

  const ma = metricsOf(a.run);
  const mb = metricsOf(b.run);
  // A run only has chart data if its per-second rollups exist (a finished run
  // that produced traffic and hasn't aged past retention). Otherwise the lines
  // would be blank, so show a clear note instead.
  const hasSeries = a.series.length > 0 || b.series.length > 0;

  // Charts align both runs on elapsed time since their own first sample, so
  // the crosshair compares "the same moment into the test" across A and B.
  const longer = a.series.length >= b.series.length ? a.series : b.series;
  const base = longer.length > 0 ? new Date(longer[0].ts).getTime() : 0;
  const xLabels = longer.map((p) => formatElapsed((new Date(p.ts).getTime() - base) / 1000));

  // For latency/error metrics, lower is better; for total/qps, higher is better.
  function delta(metric: string, av: number, bv: number) {
    // Only meaningful when both runs have final metrics to compare.
    if (!a.run || !b.run || !ma.has || !mb.has) return null;
    const lowerBetter = metric !== "total";
    const diff = bv - av;
    if (diff === 0) return <span className="muted"> (=)</span>;
    // A percentage change needs a non-zero baseline; with av==0 there's nothing
    // to divide by, so show a neutral "new" marker instead of "+—%".
    if (av === 0) return <span className="muted"> ({t("compare.new")})</span>;
    const better = lowerBetter ? diff < 0 : diff > 0;
    const pct = ((diff / av) * 100).toFixed(1);
    return (
      <span style={{ color: better ? "var(--green)" : "var(--red)" }}>
        {" "}
        ({diff > 0 ? "+" : ""}
        {pct}%)
      </span>
    );
  }

  const rows: { key: string; label: string; av: number; bv: number; fmt: (n: number) => string }[] = [
    { key: "total", label: t("compare.total"), av: ma.total, bv: mb.total, fmt: (n) => n.toLocaleString() },
    { key: "error", label: t("compare.errorRate"), av: ma.error_rate, bv: mb.error_rate, fmt: (n) => n.toFixed(2) + "%" },
    { key: "p50", label: "p50", av: ma.p50, bv: mb.p50, fmt: fmtMs },
    { key: "p90", label: "p90", av: ma.p90, bv: mb.p90, fmt: fmtMs },
    { key: "p95", label: "p95", av: ma.p95, bv: mb.p95, fmt: fmtMs },
    { key: "p99", label: "p99", av: ma.p99, bv: mb.p99, fmt: fmtMs },
  ];

  return (
    <>
      <Nav />
      <div className="container">
        <h1>{t("compare.title")}</h1>
        {err && <div className="error">{err}</div>}
        <div className="panel">
          <div className="row">
            <div style={{ flex: "1 1 0", minWidth: 0 }}>
              <label>{t("compare.runA")}</label>
              <EntityPicker
                items={runs}
                value={aId}
                onChange={setAId}
                idOf={(r) => r.id}
                label={runLabel}
                keys={runKeys}
                accept={acceptId}
                placeholder={t("compare.filterPh")}
                listId="compare-a"
                testId="compare-a"
                style={{ width: "100%" }}
              />
            </div>
            <div style={{ flex: "1 1 0", minWidth: 0 }}>
              <label>{t("compare.runB")}</label>
              <EntityPicker
                items={runs}
                value={bId}
                onChange={setBId}
                idOf={(r) => r.id}
                label={runLabel}
                keys={runKeys}
                accept={acceptId}
                placeholder={t("compare.filterPh")}
                listId="compare-b"
                testId="compare-b"
                style={{ width: "100%" }}
              />
            </div>
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
                      <td>{ma.has ? r.fmt(r.av) : "—"}</td>
                      <td>
                        {mb.has ? r.fmt(r.bv) : "—"}
                        {delta(r.key, r.av, r.bv)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <p className="muted" style={{ marginTop: 8 }}>
                {t("compare.hint")}
              </p>
              {(!ma.has || !mb.has) && (
                <p className="muted" style={{ marginTop: 4 }}>
                  {t("compare.running")}
                </p>
              )}
            </div>

            {!hasSeries ? (
              <div className="panel">
                <p className="muted">{t("compare.noChartData")}</p>
              </div>
            ) : (
            <>
            <div className="panel">
              <h2>QPS</h2>
              <LineChart
                series={[
                  { label: "A", color: compareColors.a, dash: compareDash.a, data: a.series.map((p) => p.rps) },
                  { label: "B", color: compareColors.b, dash: compareDash.b, data: b.series.map((p) => p.rps) },
                ]}
                xLabels={xLabels}
                hoverIndex={hover}
                onHover={setHover}
              />
            </div>
            <div className="panel">
              <h2>p95 (ms)</h2>
              <LineChart
                unit="ms"
                series={[
                  { label: "A", color: compareColors.a, dash: compareDash.a, data: a.series.map((p) => p.p95_ms) },
                  { label: "B", color: compareColors.b, dash: compareDash.b, data: b.series.map((p) => p.p95_ms) },
                ]}
                xLabels={xLabels}
                hoverIndex={hover}
                onHover={setHover}
              />
            </div>
            <div className="panel">
              <h2>{t("compare.errorRate")} (%)</h2>
              <LineChart
                unit="%"
                series={[
                  { label: "A", color: compareColors.a, dash: compareDash.a, data: a.series.map((p) => p.error_rate * 100) },
                  { label: "B", color: compareColors.b, dash: compareDash.b, data: b.series.map((p) => p.error_rate * 100) },
                ]}
                xLabels={xLabels}
                hoverIndex={hover}
                onHover={setHover}
              />
            </div>
            </>
            )}
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
