"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import Nav from "@/components/Nav";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import type { Run, TestDefinition } from "@/lib/types";

export default function RunsPage() {
  const { t } = useI18n();
  const { user, ready } = useAuth();
  const [runs, setRuns] = useState<Run[]>([]);
  const [tests, setTests] = useState<TestDefinition[]>([]);
  const [testId, setTestId] = useState("");
  const [workers, setWorkers] = useState(1);
  const [err, setErr] = useState("");

  async function refresh() {
    try {
      setRuns(await api.listRuns());
    } catch (e: any) {
      setErr(e.message);
    }
  }

  useEffect(() => {
    if (!ready) return;
    refresh();
    api.listTests().then(setTests).catch(() => {});
    const t = setInterval(refresh, 4000);
    return () => clearInterval(t);
  }, [ready]);

  async function start() {
    if (!testId) return;
    setErr("");
    try {
      const res = await api.startRun(testId, workers);
      window.location.href = `/runs/${res.run_id}`;
    } catch (e: any) {
      setErr(e.message);
    }
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
              <div>
                <label>{t("runs.workers")}</label>
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

        {err && <div className="error">{err}</div>}

        <div className="panel">
          <table>
            <thead>
              <tr>
                <th>{t("runs.colRun")}</th>
                <th>{t("runs.colStatus")}</th>
                <th>{t("runs.colWorkers")}</th>
                <th>{t("runs.colStarted")}</th>
              </tr>
            </thead>
            <tbody>
              {runs.map((r) => (
                <tr key={r.id}>
                  <td>
                    <Link href={`/runs/${r.id}`}>{r.id.slice(0, 8)}</Link>
                  </td>
                  <td>
                    <span className={`badge ${r.status}`}>{r.status}</span>
                  </td>
                  <td>{r.desired_workers}</td>
                  <td className="muted">
                    {r.started_at ? new Date(r.started_at).toLocaleString() : "–"}
                  </td>
                </tr>
              ))}
              {runs.length === 0 && (
                <tr>
                  <td colSpan={4} className="muted">
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
