"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import Nav from "@/components/Nav";
import Help from "@/components/Help";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import type { Run, Schedule, TestDefinition } from "@/lib/types";

export default function RunsPage() {
  const { t } = useI18n();
  const { user, ready } = useAuth();
  const [runs, setRuns] = useState<Run[]>([]);
  const [tests, setTests] = useState<TestDefinition[]>([]);
  const [testId, setTestId] = useState("");
  const [runName, setRunName] = useState("");
  const [workers, setWorkers] = useState(1);
  const [err, setErr] = useState("");

  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [showSchedules, setShowSchedules] = useState(false);
  const [schedTestId, setSchedTestId] = useState("");
  const [schedInterval, setSchedInterval] = useState(60);

  async function refresh() {
    try {
      setRuns(await api.listRuns());
    } catch (e: any) {
      setErr(e.message);
    }
    api.listSchedules().then(setSchedules).catch(() => {});
  }

  useEffect(() => {
    if (!ready) return;
    refresh();
    api.listTests().then(setTests).catch(() => {});
    const t = setInterval(refresh, 4000);
    return () => clearInterval(t);
  }, [ready]);

  const testName = (id: string) => tests.find((td) => td.id === id)?.name || id.slice(0, 8);

  async function start() {
    if (!testId) return;
    setErr("");
    try {
      const res = await api.startRun(testId, workers, runName);
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

  async function createSchedule() {
    if (!schedTestId || schedInterval <= 0) return;
    try {
      await api.createSchedule(schedTestId, schedInterval, 0);
      refresh();
    } catch (e: any) {
      setErr(e.message);
    }
  }
  async function toggleSchedule(id: string, enabled: boolean) {
    await api.setScheduleEnabled(id, enabled).catch(() => {});
    refresh();
  }

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
                <label>{t("runs.test")}</label>
                <select value={testId} onChange={(e) => setTestId(e.target.value)}>
                  <option value="">{t("runs.selectTest")}</option>
                  {tests.map((td) => (
                    <option key={td.id} value={td.id}>
                      {td.name} ({td.protocol})
                    </option>
                  ))}
                </select>
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
                  value={workers}
                  onChange={(e) => setWorkers(parseInt(e.target.value || "1", 10))}
                  style={{ width: 90 }}
                />
              </div>
              <button onClick={start} disabled={!testId}>
                {t("runs.startBtn")}
              </button>
            </div>
          </div>
        )}

        {canRun && (
          <div className="panel">
            <div className="row" style={{ justifyContent: "space-between", alignItems: "center" }}>
              <h2 style={{ margin: 0 }}>
                {t("sched.title")}
                <Help tip={t("sched.help")} />
              </h2>
              <button className="secondary" onClick={() => setShowSchedules((v) => !v)}>
                {showSchedules ? t("sched.hide") : t("sched.show")}
                {schedules.length > 0 ? ` (${schedules.length})` : ""}
              </button>
            </div>
            {showSchedules && (
              <div style={{ marginTop: 12 }}>
                <div className="row">
                  <div>
                    <label>{t("runs.test")}</label>
                    <select value={schedTestId} onChange={(e) => setSchedTestId(e.target.value)}>
                      <option value="">{t("runs.selectTest")}</option>
                      {tests.map((td) => (
                        <option key={td.id} value={td.id}>
                          {td.name} ({td.protocol})
                        </option>
                      ))}
                    </select>
                  </div>
                  <div>
                    <label>{t("sched.every")}</label>
                    <input
                      type="number"
                      min={1}
                      value={schedInterval}
                      onChange={(e) => setSchedInterval(parseInt(e.target.value || "1", 10))}
                      style={{ width: 100 }}
                    />
                  </div>
                  <button onClick={createSchedule} disabled={!schedTestId}>
                    {t("sched.create")}
                  </button>
                </div>
                {schedules.length > 0 && (
                  <table style={{ marginTop: 12 }}>
                    <thead>
                      <tr>
                        <th>{t("sched.colTest")}</th>
                        <th>{t("sched.colInterval")}</th>
                        <th>{t("sched.colNext")}</th>
                        <th>{t("sched.colState")}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {schedules.map((sc) => (
                        <tr key={sc.id}>
                          <td>{testName(sc.test_def_id)}</td>
                          <td>{sc.interval_minutes} min</td>
                          <td className="muted">{new Date(sc.next_run_at).toLocaleString()}</td>
                          <td>
                            <button className="secondary" onClick={() => toggleSchedule(sc.id, !sc.enabled)}>
                              {sc.enabled ? t("sched.disable") : t("sched.enable")}
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </div>
            )}
          </div>
        )}

        {err && <div className="error">{err}</div>}

        <div className="panel">
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
              {runs.map((r) => (
                <tr key={r.id}>
                  <td>
                    <Link href={`/runs/${r.id}`}>{r.name || r.id.slice(0, 8)}</Link>
                  </td>
                  <td className="muted">{testName(r.test_def_id)}</td>
                  <td>
                    <span className={`badge ${r.status}`}>{r.status}</span>
                  </td>
                  <td className="muted">{r.creator_name || t("run.creatorSystem")}</td>
                  <td className="muted">
                    {r.started_at ? new Date(r.started_at).toLocaleString() : "–"}
                  </td>
                  {canRun && (
                    <td>
                      <button className="secondary" onClick={() => rerun(r)}>
                        ↻ {t("runs.rerun")}
                      </button>
                    </td>
                  )}
                </tr>
              ))}
              {runs.length === 0 && (
                <tr>
                  <td colSpan={canRun ? 6 : 5} className="muted">
                    {t("runs.empty")}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </>
  );
}
