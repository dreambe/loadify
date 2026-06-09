"use client";

import { useEffect, useState } from "react";
import Nav from "@/components/Nav";
import { api } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import type { WorkerInfo } from "@/lib/types";

export default function WorkersPage() {
  const { t } = useI18n();
  const { ready } = useAuth();
  const [workers, setWorkers] = useState<WorkerInfo[]>([]);

  useEffect(() => {
    if (!ready) return;
    const load = () => api.listWorkers().then(setWorkers).catch(() => {});
    load();
    const t = setInterval(load, 3000);
    return () => clearInterval(t);
  }, [ready]);

  if (!ready) return null;

  return (
    <>
      <Nav />
      <div className="container">
        <h1>{t("workers.title")}</h1>
        <div className="panel">
          <table>
            <thead>
              <tr>
                <th>{t("workers.colWorker")}</th>
                <th>{t("workers.colRegion")}</th>
                <th>{t("workers.colStatus")}</th>
                <th>{t("workers.colActive")}</th>
                <th>{t("workers.colLastSeen")}</th>
              </tr>
            </thead>
            <tbody>
              {workers.map((w) => (
                <tr key={w.worker_id}>
                  <td>{w.worker_id}</td>
                  <td>{w.region}</td>
                  <td>
                    <span className={`badge ${w.status === "healthy" ? "running" : "failed"}`}>
                      {w.status}
                    </span>
                  </td>
                  <td>{w.active_vus}</td>
                  <td className="muted">
                    {w.last_seen_unix_ms ? new Date(w.last_seen_unix_ms).toLocaleTimeString() : "–"}
                  </td>
                </tr>
              ))}
              {workers.length === 0 && (
                <tr>
                  <td colSpan={5} className="muted">
                    {t("workers.empty")}
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
