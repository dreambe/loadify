"use client";

import { useEffect, useState } from "react";
import Nav from "@/components/Nav";
import { api } from "@/lib/api";
import { useAuth } from "@/lib/auth";
import type { WorkerInfo } from "@/lib/types";

export default function WorkersPage() {
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
        <h1>Workers</h1>
        <div className="panel">
          <table>
            <thead>
              <tr>
                <th>Worker</th>
                <th>Region</th>
                <th>Status</th>
                <th>Active VUs</th>
                <th>Last seen</th>
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
                    No workers connected.
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
