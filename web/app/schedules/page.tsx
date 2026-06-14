"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import Nav from "@/components/Nav";
import Help from "@/components/Help";
import EmptyState from "@/components/EmptyState";
import TableSkeleton from "@/components/TableSkeleton";
import { useToast } from "@/components/Toast";
import { useConfirm } from "@/components/Confirm";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast, ownsOrAdmin } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import type { Schedule, TestDefinition } from "@/lib/types";

export default function SchedulesPage() {
  const { t } = useI18n();
  const { user, ready } = useAuth();
  const toast = useToast();
  const confirm = useConfirm();
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [tests, setTests] = useState<TestDefinition[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [testId, setTestId] = useState("");
  const [interval, setInterval] = useState(60);
  const [workers, setWorkers] = useState(1);

  const canEdit = roleAtLeast(user?.role, "operator");
  const testName = (id: string) => tests.find((td) => td.id === id)?.name || id.slice(0, 8);

  function refresh() {
    api.listSchedules().then(setSchedules).catch((e) => toast.error(e.message)).finally(() => setLoaded(true));
  }
  useEffect(() => {
    if (!ready) return;
    refresh();
    api.listTests().then(setTests).catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready]);

  async function create() {
    try {
      await api.createSchedule(testId, interval, workers);
      toast.success(t("sched.created"));
      setTestId("");
      refresh();
    } catch (e: any) {
      toast.error(e.message);
    }
  }
  async function toggle(sc: Schedule) {
    await api.setScheduleEnabled(sc.id, !sc.enabled).catch((e) => toast.error(e.message));
    refresh();
  }
  async function saveInterval(sc: Schedule, min: number, w: number) {
    try {
      await api.updateSchedule(sc.id, min, w);
      toast.success(t("sched.updated"));
      refresh();
    } catch (e: any) {
      toast.error(e.message);
    }
  }
  async function remove(sc: Schedule) {
    if (!(await confirm({ title: t("sched.delete") + " · " + testName(sc.test_def_id), danger: true, confirmLabel: t("sched.delete") }))) return;
    try {
      await api.deleteSchedule(sc.id);
      toast.success(t("sched.deleted"));
      refresh();
    } catch (e: any) {
      toast.error(e.message);
    }
  }

  if (!ready) return null;

  return (
    <>
      <Nav />
      <div className="container">
        <h1>
          {t("sched.title")}
          <Help tip={t("sched.help")} />
        </h1>

        {canEdit && (
          <div className="panel">
            <h2>{t("sched.new")}</h2>
            <div className="row" style={{ alignItems: "flex-end" }}>
              <div>
                <label className="req">{t("runs.test")}</label>
                <select value={testId} onChange={(e) => setTestId(e.target.value)} style={{ minWidth: 240 }}>
                  <option value="">{t("runs.searchTest")}</option>
                  {tests.map((td) => (
                    <option key={td.id} value={td.id}>
                      {td.name} ({td.protocol})
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label>{t("sched.every")}</label>
                <input type="number" min={1} value={interval} onChange={(e) => setInterval(parseInt(e.target.value || "1", 10))} style={{ width: 100 }} />
              </div>
              <div>
                <label>{t("runs.workers")}</label>
                <input type="number" min={1} value={workers} onChange={(e) => setWorkers(parseInt(e.target.value || "1", 10))} style={{ width: 90 }} />
              </div>
              <button onClick={create} disabled={!testId}>
                {t("sched.create")}
              </button>
            </div>
          </div>
        )}

        <div className="panel">
          {!loaded ? (
            <TableSkeleton cols={canEdit ? 5 : 4} />
          ) : schedules.length === 0 ? (
            <EmptyState title={t("sched.empty")} hint={canEdit ? t("sched.emptyHint") : undefined} />
          ) : (
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>{t("sched.colTest")}</th>
                    <th>{t("sched.colInterval")}</th>
                    <th>{t("sched.colNext")}</th>
                    <th>{t("sched.colLast")}</th>
                    {canEdit && <th className="num">{t("tests.colActions")}</th>}
                  </tr>
                </thead>
                <tbody>
                  {schedules.map((sc) => (
                    <tr key={sc.id} style={{ opacity: sc.enabled ? 1 : 0.55 }}>
                      <td>{testName(sc.test_def_id)}</td>
                      <td>
                        {canEdit ? (
                          <ScheduleEditor sc={sc} onSave={saveInterval} t={t} disabled={!ownsOrAdmin(user, sc.created_by)} />
                        ) : (
                          `${sc.interval_minutes} min · ${sc.desired_workers}w`
                        )}
                      </td>
                      <td className="muted">{new Date(sc.next_run_at).toLocaleString()}</td>
                      <td className="muted">
                        {sc.last_run_id ? <Link href={`/runs/${sc.last_run_id}`}>{sc.last_run_id.slice(0, 8)}</Link> : "–"}
                      </td>
                      {canEdit && (
                        <td>
                          <div className="actions">
                            {(() => {
                              const owns = ownsOrAdmin(user, sc.created_by);
                              const why = owns ? undefined : t("common.ownerOnly");
                              return (
                                <>
                                  <button className="ghost sm" disabled={!owns} title={why} onClick={() => toggle(sc)}>
                                    {sc.enabled ? t("sched.disable") : t("sched.enable")}
                                  </button>
                                  <button className="danger sm" disabled={!owns} title={why} onClick={() => remove(sc)}>
                                    {t("sched.delete")}
                                  </button>
                                </>
                              );
                            })()}
                          </div>
                        </td>
                      )}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>
    </>
  );
}

// ScheduleEditor lets an operator adjust a schedule's interval and worker count
// inline, committing on "save".
function ScheduleEditor({
  sc,
  onSave,
  t,
  disabled,
}: {
  sc: Schedule;
  onSave: (sc: Schedule, min: number, w: number) => void;
  t: (k: string) => string;
  disabled?: boolean;
}) {
  const [min, setMin] = useState(sc.interval_minutes);
  const [w, setW] = useState(sc.desired_workers || 1);
  const dirty = min !== sc.interval_minutes || w !== (sc.desired_workers || 1);
  const why = disabled ? t("common.ownerOnly") : undefined;
  return (
    <div className="row" style={{ gap: 6, alignItems: "center" }} title={why}>
      <input type="number" min={1} disabled={disabled} value={min} onChange={(e) => setMin(parseInt(e.target.value || "1", 10))} style={{ width: 70 }} />
      <span className="muted">min ·</span>
      <input type="number" min={1} disabled={disabled} value={w} onChange={(e) => setW(parseInt(e.target.value || "1", 10))} style={{ width: 56 }} />
      <span className="muted">w</span>
      {dirty && (
        <button className="secondary sm" disabled={disabled} onClick={() => onSave(sc, min, w)}>
          {t("tests.save")}
        </button>
      )}
    </div>
  );
}
