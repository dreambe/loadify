"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import Nav from "@/components/Nav";
import RunStatus from "@/components/RunStatus";
import Help from "@/components/Help";
import Icon from "@/components/Icon";
import { Pager, usePager } from "@/components/Pager";
import EmptyState from "@/components/EmptyState";
import TableSkeleton from "@/components/TableSkeleton";
import EntityPicker from "@/components/EntityPicker";
import SortableTh from "@/components/SortableTh";
import { useConfirm } from "@/components/Confirm";
import { useToast } from "@/components/Toast";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast, ownsOrAdmin } from "@/lib/auth";
import { useI18n, statusLabel } from "@/lib/i18n";
import { fmtMs, fmtErrRate } from "@/lib/format";
import type { Capacity, Environment, Run, TestDefinition } from "@/lib/types";

// A run is deletable only once it's terminal (matches the backend guard).
const terminalStatuses = new Set(["completed", "failed", "aborted"]);

// TestPicker is a searchable test selector: type to filter, pick from the
// native datalist. Selection maps the typed label back to the test id.
function TestPicker({
  tests,
  value,
  onChange,
  placeholder,
  listId,
}: {
  tests: TestDefinition[];
  value: string;
  onChange: (id: string) => void;
  placeholder: string;
  listId: string;
}) {
  return (
    <EntityPicker
      items={tests}
      value={value}
      onChange={onChange}
      idOf={(td) => td.id}
      label={(td) => `${td.name} (${td.protocol})`}
      keys={(td) => [td.id, td.name]}
      placeholder={placeholder}
      listId={listId}
      testId={listId}
      style={{ width: 260 }}
    />
  );
}

// runsEqual reports whether two run lists are the same for display purposes, so
// an unchanged 4s poll doesn't replace the array — which would re-render and
// re-trigger the table's row entrance animation, making the list flicker.
function runsEqual(a: Run[], b: Run[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i].id !== b[i].id || a[i].status !== b[i].status || a[i].started_at !== b[i].started_at) {
      return false;
    }
  }
  return true;
}

// RunResult is the run's headline outcome for the list — the number you scan
// for. Shows p95 latency and, when nonzero, the error rate. A run with no
// summary yet (running/queued) shows "–".
function RunResult({ run }: { run: Run }) {
  const s = run.summary?.summary;
  if (!s || s.p95_ms == null) return <span className="muted">–</span>;
  const err = s.error_rate ?? 0;
  return (
    <span className="run-result">
      <span className="muted">p95</span> {fmtMs(s.p95_ms)}
      {err > 0 && <span className="run-result-err"> · {fmtErrRate(err)}</span>}
    </span>
  );
}

export default function RunsPage() {
  const { t } = useI18n();
  const { user, ready } = useAuth();
  const confirm = useConfirm();
  const toast = useToast();
  const [runs, setRuns] = useState<Run[]>([]);
  const [tests, setTests] = useState<TestDefinition[]>([]);
  const [testId, setTestId] = useState("");
  const [runName, setRunName] = useState("");
  const [workers, setWorkers] = useState("1");
  const [maxWorkers, setMaxWorkers] = useState(0);
  const [cap, setCap] = useState<Capacity | null>(null);
  const [envId, setEnvId] = useState("");
  const [envs, setEnvs] = useState<Environment[]>([]);
  const [err, setErr] = useState("");
  const [runFilter, setRunFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [sort, setSort] = useState<{ key: "name" | "status" | "started"; dir: "asc" | "desc" }>({
    key: "started",
    dir: "desc",
  });
  const [busy, setBusy] = useState(false); // guards against double-starting runs
  const [loaded, setLoaded] = useState(false);

  async function refresh() {
    try {
      const next = await api.listRuns();
      setRuns((prev) => (runsEqual(prev, next) ? prev : next));
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setLoaded(true);
    }
  }

  // deleteRun removes a finished run (and its metrics) after confirmation.
  // Only shown to the run's creator or an admin, and only for terminal runs.
  async function deleteRun(r: Run) {
    const name = r.name || r.id.slice(0, 8);
    if (!(await confirm({ title: t("runs.deleteTitle") + " · " + name, body: t("runs.deleteBody"), danger: true, confirmLabel: t("common.delete") }))) {
      return;
    }
    try {
      await api.deleteRun(r.id);
      toast.success(t("runs.deleted"));
      refresh();
    } catch (e: any) {
      toast.error(e.message);
    }
  }

  useEffect(() => {
    if (!ready) return;
    refresh();
    api.listTests().then(setTests).catch(() => {});
    api.listEnvironments().then(setEnvs).catch(() => {});
    // Worker count is only for the input cap — fetch once on mount (the Workers
    // page shows live node status). Avoids a recurring /workers poll here.
    api
      .listWorkers()
      .then((ws) => setMaxWorkers(ws.filter((w) => w.status === "healthy").length))
      .catch(() => {});
    // Poll admission capacity so the start form can warn before a run queues.
    const loadCap = () => api.getCapacity().then(setCap).catch(() => {});
    loadCap();
    const t = setInterval(() => {
      refresh();
      loadCap();
    }, 4000);
    return () => clearInterval(t);
  }, [ready]);

  const testName = (id: string) => tests.find((td) => td.id === id)?.name || id.slice(0, 8);

  // identityOf collapses the redundant "name + test" pair into one column. A run
  // name defaults server-side to "<test> @ <time>", which duplicates the test
  // column and the started-at column — so for that derived form we show just the
  // test name and let the Started column carry the time. Only a custom name adds
  // real information, so it becomes the primary line with the test as a subtitle.
  const identityOf = (r: Run) => {
    const test = testName(r.test_def_id);
    const derived = !r.name || r.name.startsWith(`${test} @ `);
    return derived ? { primary: test, sub: "" } : { primary: r.name, sub: test };
  };

  // clampWorkers keeps the count within [1, online nodes] — you can't shard
  // across more workers than are connected.
  const clampWorkers = (raw: string) => Math.max(1, Math.min(maxWorkers || 1, parseInt(raw, 10) || 1));

  async function start() {
    if (!testId || busy) return;
    setErr("");
    setBusy(true);
    try {
      const res = await api.startRun(testId, clampWorkers(workers), runName, envId);
      window.location.href = `/runs/${res.run_id}`;
    } catch (e: any) {
      setErr(e.message);
      setBusy(false);
    }
  }

  // rerun fires the same test again with the same worker count; the run name
  // defaults server-side to "<test> @ <time>".
  async function rerun(r: Run) {
    if (busy) return;
    setErr("");
    setBusy(true);
    try {
      const res = await api.startRun(r.test_def_id, Math.max(1, r.desired_workers), "");
      window.location.href = `/runs/${res.run_id}`;
    } catch (e: any) {
      setErr(e.message);
      setBusy(false);
    }
  }

  // Filter the run history so it stays navigable at scale (by run name, test
  // name, status or creator), then paginate.
  const rq = runFilter.trim().toLowerCase();
  const filteredRuns = runs
    .filter((r) =>
      rq
        ? `${r.name ?? ""} ${testName(r.test_def_id)} ${r.status} ${r.creator_name ?? ""}`.toLowerCase().includes(rq)
        : true
    )
    .filter((r) => (statusFilter ? r.status === statusFilter : true));
  // Sort a copy by the active column; ties fall back to creation order.
  const sortedRuns = [...filteredRuns].sort((a, b) => {
    const key = (r: Run) =>
      sort.key === "name"
        ? testName(r.test_def_id).toLowerCase()
        : sort.key === "status"
          ? r.status
          : r.started_at || r.created_at;
    const av = key(a);
    const bv = key(b);
    const cmp = av < bv ? -1 : av > bv ? 1 : 0;
    return sort.dir === "asc" ? cmp : -cmp;
  });
  const runPager = usePager(sortedRuns, 10);
  // Statuses present in the data, for the filter dropdown.
  const statuses = Array.from(new Set(runs.map((r) => r.status)));
  const toggleSort = (k: "name" | "status" | "started") =>
    setSort((s) => ({ key: k, dir: s.key === k && s.dir === "desc" ? "asc" : "desc" }));

  if (!ready) return null;
  const canRun = roleAtLeast(user?.role, "operator");

  return (
    <>
      <Nav />
      <div className="container">
        <h1>{t("runs.title")}</h1>

        {canRun && (
          <div className="panel">
            <h2>{t("runs.start")}</h2>
            <div className="row">
              <div>
                <label className="req">{t("runs.test")}</label>
                <TestPicker
                  tests={tests}
                  value={testId}
                  onChange={setTestId}
                  placeholder={t("runs.searchTest")}
                  listId="run-tests"
                />
              </div>
              <div style={{ flex: 1 }}>
                <label>{t("runs.name")}</label>
                <input
                  value={runName}
                  onChange={(e) => setRunName(e.target.value)}
                  placeholder={t("runs.namePh")}
                  style={{ width: "100%" }}
                />
              </div>
              <div>
                <label>
                  {t("runs.workers")}
                  <Help tip={t("runs.workersHelp")} />
                </label>
                <input
                  type="number"
                  min={1}
                  max={maxWorkers || undefined}
                  value={workers}
                  onChange={(e) => setWorkers(e.target.value)}
                  onBlur={() => setWorkers(String(clampWorkers(workers)))}
                  style={{ width: 90 }}
                />
              </div>
              {envs.length > 0 && (
                <div>
                  <label>
                    {t("runs.environment")}
                    <Help tip={t("runs.environmentHelp")} />
                  </label>
                  <select value={envId} onChange={(e) => setEnvId(e.target.value)}>
                    <option value="">{t("runs.noEnv")}</option>
                    {envs.map((e) => (
                      <option key={e.id} value={e.id}>
                        {e.name}
                      </option>
                    ))}
                  </select>
                </div>
              )}
              <button onClick={start} disabled={!testId || maxWorkers === 0 || busy}>
                {t("runs.startBtn")}
              </button>
            </div>
            <p className="muted" style={{ marginTop: 8, color: maxWorkers === 0 ? "var(--yellow)" : undefined }}>
              {maxWorkers === 0 ? t("runs.workersNone") : `${t("runs.workersAvail")}: ${maxWorkers}`}
            </p>
            {cap && !cap.can_accept && maxWorkers > 0 && (
              <p className="muted" style={{ marginTop: 4, color: "var(--yellow)" }}>
                <Icon name="warn" />{" "}
                {/* Distinguish the two reasons a run would queue: all run slots
                    busy vs. every node over its CPU protection threshold. */}
                {cap.running >= cap.max_runs
                  ? t("runs.capacityFull").replace("{running}", String(cap.running)).replace("{max}", String(cap.max_runs))
                  : t("runs.capacityNodesBusy")}
                {cap.queue_depth > 0 ? " · " + t("runs.capacityQueued").replace("{n}", String(cap.queue_depth)) : ""}
              </p>
            )}
          </div>
        )}

        {err && <div className="error">{err}</div>}

        <div className="panel">
          <div className="row" style={{ marginBottom: 12, alignItems: "center" }}>
            <div className="field" style={{ maxWidth: 320, flex: 1 }}>
              <input
                value={runFilter}
                onChange={(e) => setRunFilter(e.target.value)}
                placeholder={t("runs.filterPh")}
              />
            </div>
            <select value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)} style={{ width: 150 }}>
              <option value="">{t("runs.allStatuses")}</option>
              {statuses.map((s) => (
                <option key={s} value={s}>
                  {statusLabel(t, s)}
                </option>
              ))}
            </select>
          </div>
          {!loaded ? (
            <TableSkeleton cols={canRun ? 6 : 5} />
          ) : sortedRuns.length === 0 ? (
            <EmptyState
              title={runs.length === 0 ? t("runs.empty") : t("runs.noMatch")}
              hint={runs.length === 0 && canRun ? t("runs.emptyHint") : undefined}
              action={
                runs.length === 0 && canRun ? (
                  <Link className="cta" href="/tests">
                    + {t("runs.emptyCta")}
                  </Link>
                ) : undefined
              }
            />
          ) : (
            <>
              <table>
                <thead>
                  <tr>
                    <SortableTh label={t("runs.colTest")} active={sort.key === "name"} dir={sort.dir} onToggle={() => toggleSort("name")} />
                    <th>{t("runs.colResult")}</th>
                    <SortableTh label={t("runs.colStatus")} active={sort.key === "status"} dir={sort.dir} onToggle={() => toggleSort("status")} />
                    <th className="col-nowrap">{t("runs.colCreator")}</th>
                    <SortableTh label={t("runs.colStarted")} active={sort.key === "started"} dir={sort.dir} onToggle={() => toggleSort("started")} />
                    {canRun && <th>{t("runs.colActions")}</th>}
                  </tr>
                </thead>
                <tbody>
                  {runPager.slice.map((r) => {
                    const id = identityOf(r);
                    return (
                    <tr key={r.id}>
                      <td>
                        <Link href={`/runs/${r.id}`}>{id.primary}</Link>
                        {id.sub && <div className="run-sub muted">{id.sub}</div>}
                      </td>
                      <td>
                        <RunResult run={r} />
                      </td>
                      <td>
                        <RunStatus run={r} />
                      </td>
                      <td className="muted col-nowrap">
                        {r.creator_name || t("run.creatorSystem")}
                        {r.source === "schedule" ? ` · ${t("run.scheduled")}` : ""}
                      </td>
                      <td className="muted col-nowrap">{r.started_at ? new Date(r.started_at).toLocaleString() : "–"}</td>
                      {canRun && (
                        <td>
                          <div className="actions">
                            <button className="ghost sm" onClick={() => rerun(r)} disabled={busy}>
                              <Icon name="rerun" /> {t("runs.rerun")}
                            </button>
                            {/* Delete: creator or admin, terminal runs only. */}
                            {ownsOrAdmin(user, r.created_by) && terminalStatuses.has(r.status) && (
                              <button className="danger sm" onClick={() => deleteRun(r)}>
                                {t("common.delete")}
                              </button>
                            )}
                          </div>
                        </td>
                      )}
                    </tr>
                    );
                  })}
                </tbody>
              </table>
              <Pager page={runPager.page} pages={runPager.pages} total={runPager.total} onPage={runPager.setPage} />
            </>
          )}
        </div>
      </div>
    </>
  );
}
