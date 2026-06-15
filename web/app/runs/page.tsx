"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import Nav from "@/components/Nav";
import Help from "@/components/Help";
import Icon from "@/components/Icon";
import { Pager, usePager } from "@/components/Pager";
import EmptyState from "@/components/EmptyState";
import TableSkeleton from "@/components/TableSkeleton";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast } from "@/lib/auth";
import { useI18n, statusLabel } from "@/lib/i18n";
import type { Environment, Run, TestDefinition } from "@/lib/types";

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
  const label = (td: TestDefinition) => `${td.name} (${td.protocol})`;
  const [text, setText] = useState("");
  useEffect(() => {
    const td = tests.find((x) => x.id === value);
    setText(td ? label(td) : "");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value, tests.length]);
  return (
    <>
      <input
        list={listId}
        value={text}
        placeholder={placeholder}
        onChange={(e) => {
          setText(e.target.value);
          const td = tests.find((x) => label(x) === e.target.value);
          onChange(td ? td.id : "");
        }}
        style={{ width: 260 }}
      />
      <datalist id={listId}>
        {tests.map((td) => (
          <option key={td.id} value={label(td)} />
        ))}
      </datalist>
    </>
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

export default function RunsPage() {
  const { t } = useI18n();
  const { user, ready } = useAuth();
  const [runs, setRuns] = useState<Run[]>([]);
  const [tests, setTests] = useState<TestDefinition[]>([]);
  const [testId, setTestId] = useState("");
  const [runName, setRunName] = useState("");
  const [workers, setWorkers] = useState("1");
  const [maxWorkers, setMaxWorkers] = useState(0);
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
    const t = setInterval(refresh, 4000);
    return () => clearInterval(t);
  }, [ready]);

  const testName = (id: string) => tests.find((td) => td.id === id)?.name || id.slice(0, 8);

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
        ? (r.name || r.id).toLowerCase()
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
  const sortMark = (k: string) => (sort.key === k ? (sort.dir === "desc" ? " ▼" : " ▲") : "");

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
                  <Link className="badge" href="/tests">
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
                    <th onClick={() => toggleSort("name")} style={{ cursor: "pointer", userSelect: "none" }}>
                      {t("runs.colName")}
                      {sortMark("name")}
                    </th>
                    <th>{t("runs.colTest")}</th>
                    <th onClick={() => toggleSort("status")} style={{ cursor: "pointer", userSelect: "none" }}>
                      {t("runs.colStatus")}
                      {sortMark("status")}
                    </th>
                    <th>{t("runs.colCreator")}</th>
                    <th onClick={() => toggleSort("started")} style={{ cursor: "pointer", userSelect: "none" }}>
                      {t("runs.colStarted")}
                      {sortMark("started")}
                    </th>
                    {canRun && <th>{t("runs.colActions")}</th>}
                  </tr>
                </thead>
                <tbody>
                  {runPager.slice.map((r) => (
                    <tr key={r.id}>
                      <td>
                        <Link href={`/runs/${r.id}`}>{r.name || r.id.slice(0, 8)}</Link>
                      </td>
                      <td className="muted">{testName(r.test_def_id)}</td>
                      <td>
                        <span className={`badge ${r.status}`}>{statusLabel(t, r.status)}</span>
                      </td>
                      <td className="muted">
                        {r.creator_name || t("run.creatorSystem")}
                        {r.source === "schedule" ? ` · ${t("run.scheduled")}` : ""}
                      </td>
                      <td className="muted">{r.started_at ? new Date(r.started_at).toLocaleString() : "–"}</td>
                      {canRun && (
                        <td>
                          <div className="actions">
                            <button className="ghost sm" onClick={() => rerun(r)} disabled={busy}>
                              <Icon name="rerun" /> {t("runs.rerun")}
                            </button>
                          </div>
                        </td>
                      )}
                    </tr>
                  ))}
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
