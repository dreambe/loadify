"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import Nav from "@/components/Nav";
import Help from "@/components/Help";
import Icon from "@/components/Icon";
import { Pager, usePager } from "@/components/Pager";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
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

  async function refresh() {
    try {
      const next = await api.listRuns();
      setRuns((prev) => (runsEqual(prev, next) ? prev : next));
    } catch (e: any) {
      setErr(e.message);
    }
  }

  useEffect(() => {
    if (!ready) return;
    refresh();
    api.listTests().then(setTests).catch(() => {});
    api.listEnvironments().then(setEnvs).catch(() => {});
    const loadWorkers = () =>
      api
        .listWorkers()
        .then((ws) => setMaxWorkers(ws.filter((w) => w.status === "healthy").length))
        .catch(() => {});
    loadWorkers();
    const t = setInterval(() => {
      refresh();
      loadWorkers();
    }, 4000);
    return () => clearInterval(t);
  }, [ready]);

  const testName = (id: string) => tests.find((td) => td.id === id)?.name || id.slice(0, 8);

  // clampWorkers keeps the count within [1, online nodes] — you can't shard
  // across more workers than are connected.
  const clampWorkers = (raw: string) => Math.max(1, Math.min(maxWorkers || 1, parseInt(raw, 10) || 1));

  async function start() {
    if (!testId) return;
    setErr("");
    try {
      const res = await api.startRun(testId, clampWorkers(workers), runName, envId);
      window.location.href = `/runs/${res.run_id}`;
    } catch (e: any) {
      setErr(e.message);
    }
  }

  // rerun fires the same test again with the same worker count; the run name
  // defaults server-side to "<test> @ <time>".
  async function rerun(r: Run) {
    setErr("");
    try {
      const res = await api.startRun(r.test_def_id, Math.max(1, r.desired_workers), "");
      window.location.href = `/runs/${res.run_id}`;
    } catch (e: any) {
      setErr(e.message);
    }
  }

  // Filter the run history so it stays navigable at scale (by run name, test
  // name, status or creator), then paginate.
  const rq = runFilter.trim().toLowerCase();
  const filteredRuns = rq
    ? runs.filter((r) =>
        `${r.name ?? ""} ${testName(r.test_def_id)} ${r.status} ${r.creator_name ?? ""}`
          .toLowerCase()
          .includes(rq)
      )
    : runs;
  const runPager = usePager(filteredRuns, 10);

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
              <button onClick={start} disabled={!testId || maxWorkers === 0}>
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
          <div className="field" style={{ maxWidth: 320, marginBottom: 12 }}>
            <input
              value={runFilter}
              onChange={(e) => setRunFilter(e.target.value)}
              placeholder={t("runs.filterPh")}
            />
          </div>
          <table>
            <thead>
              <tr>
                <th>{t("runs.colName")}</th>
                <th>{t("runs.colTest")}</th>
                <th>{t("runs.colStatus")}</th>
                <th>{t("runs.colCreator")}</th>
                <th>{t("runs.colStarted")}</th>
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
                    <span className={`badge ${r.status}`}>{r.status}</span>
                  </td>
                  <td className="muted">
                    {r.creator_name || t("run.creatorSystem")}
                    {r.source === "schedule" ? ` · ${t("run.scheduled")}` : ""}
                  </td>
                  <td className="muted">
                    {r.started_at ? new Date(r.started_at).toLocaleString() : "–"}
                  </td>
                  {canRun && (
                    <td>
                      <div className="actions">
                        <button className="ghost sm" onClick={() => rerun(r)}>
                          <Icon name="rerun" /> {t("runs.rerun")}
                        </button>
                      </div>
                    </td>
                  )}
                </tr>
              ))}
              {filteredRuns.length === 0 && (
                <tr>
                  <td colSpan={canRun ? 6 : 5} className="muted">
                    {runs.length === 0 ? t("runs.empty") : t("runs.noMatch")}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
          <Pager
            page={runPager.page}
            pages={runPager.pages}
            total={runPager.total}
            onPage={runPager.setPage}
          />
        </div>
      </div>
    </>
  );
}
