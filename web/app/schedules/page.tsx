"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import Nav from "@/components/Nav";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import type { Schedule, TestDefinition } from "@/lib/types";

export default function SchedulesPage() {
  const { t } = useI18n();
  const { user, ready } = useAuth();
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [tests, setTests] = useState<TestDefinition[]>([]);
  const [testId, setTestId] = useState("");
  const [interval, setIntervalMin] = useState(60);
  const [workers, setWorkers] = useState(0);
  const [err, setErr] = useState("");

  function refresh() {
    api.listSchedules().then(setSchedules).catch((e) => setErr(e.message));
  }
  useEffect(() => {
    if (!ready) return;
    refresh();
    api.listTests().then(setTests).catch(() => {});
    const tk = setInterval(refresh, 10000);
    return () => clearInterval(tk);
  }, [ready]);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setErr("");
    try {
      await api.createSchedule(testId, interval, workers);
      setTestId("");
      refresh();
    } catch (e: any) {
      setErr(e.message);
    }
  }

  async function toggle(sc: Schedule) {
    try {
      await api.setScheduleEnabled(sc.id, !sc.enabled);
      refresh();
    } catch (e: any) {
      setErr(e.message);
    }
  }

  if (!ready) return null;
  const canManage = roleAtLeast(user?.role, "operator");
  const testName = (id: string) => tests.find((td) => td.id === id)?.name || id.slice(0, 8);

  return (
    <>
      <Nav />
      <div className="container">
        <h1>{t("sched.title")}</h1>

        {canManage && (
          <form className="panel" onSubmit={create}>
            <h2>{t("sched.new")}</h2>
            <div className="row">
              <div>
                <label>{t("runs.test")}</label>
                <select value={testId} onChange={(e) => setTestId(e.target.value)} required>
                  <option value="">{t("runs.selectTest")}</option>
                  {tests.map((td) => (
                    <option key={td.id} value={td.id}>
                      {td.name} ({td.protocol})
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label>{t("sched.interval")}</label>
                <input
                  type="number"
                  min={1}
                  value={interval}
                  onChange={(e) => setIntervalMin(parseInt(e.target.value || "1", 10))}
                  style={{ width: 110 }}
                />
              </div>
              <div>
                <label>{t("runs.workers")}</label>
                <input
                  type="number"
                  min={0}
                  value={workers}
                  onChange={(e) => setWorkers(parseInt(e.target.value || "0", 10))}
                  style={{ width: 90 }}
                />
              </div>
              <button type="submit" disabled={!testId}>
                {t("sched.create")}
              </button>
            </div>
            {err && <div className="error">{err}</div>}
          </form>
        )}

        <div className="panel">
          <table>
            <thead>
              <tr>
                <th>{t("runs.test")}</th>
                <th>{t("sched.interval")}</th>
                <th>{t("runs.workers")}</th>
                <th>{t("sched.next")}</th>
                <th>{t("sched.lastRun")}</th>
                <th>{t("sched.state")}</th>
                {canManage && <th></th>}
              </tr>
            </thead>
            <tbody>
              {schedules.map((sc) => (
                <tr key={sc.id}>
                  <td>{testName(sc.test_def_id)}</td>
                  <td>{sc.interval_minutes} min</td>
                  <td>{sc.desired_workers || t("sched.allWorkers")}</td>
                  <td className="muted">{new Date(sc.next_run_at).toLocaleString()}</td>
                  <td>
                    {sc.last_run_id ? (
                      <Link href={`/runs/${sc.last_run_id}`}>{sc.last_run_id.slice(0, 8)}</Link>
                    ) : (
                      "–"
                    )}
                  </td>
                  <td>
                    <span className={`badge ${sc.enabled ? "running" : "aborted"}`}>
                      {sc.enabled ? t("sched.enabled") : t("sched.disabled")}
                    </span>
                  </td>
                  {canManage && (
                    <td>
                      <button className="secondary" onClick={() => toggle(sc)}>
                        {sc.enabled ? t("sched.disable") : t("sched.enable")}
                      </button>
                    </td>
                  )}
                </tr>
              ))}
              {schedules.length === 0 && (
                <tr>
                  <td colSpan={canManage ? 7 : 6} className="muted">
                    {t("sched.empty")}
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
