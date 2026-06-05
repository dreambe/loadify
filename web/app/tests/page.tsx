"use client";

import { useEffect, useState } from "react";
import Nav from "@/components/Nav";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast } from "@/lib/auth";
import type { TestDefinition } from "@/lib/types";

const SAMPLE_PLAN = `{
  "protocol": "http",
  "http": { "method": "GET", "url": "http://echo:8088/" }
}`;
const SAMPLE_RAMP = `[
  { "duration_ms": 10000, "target_vus": 20 },
  { "duration_ms": 20000, "target_vus": 50 }
]`;

export default function TestsPage() {
  const { user, ready } = useAuth();
  const [tests, setTests] = useState<TestDefinition[]>([]);
  const [name, setName] = useState("");
  const [protocol, setProtocol] = useState("http");
  const [plan, setPlan] = useState(SAMPLE_PLAN);
  const [ramp, setRamp] = useState(SAMPLE_RAMP);
  const [script, setScript] = useState("");
  const [err, setErr] = useState("");
  const [ok, setOk] = useState("");

  function refresh() {
    api.listTests().then(setTests).catch((e) => setErr(e.message));
  }
  useEffect(() => {
    if (ready) refresh();
  }, [ready]);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setErr("");
    setOk("");
    let planObj: unknown;
    let rampObj: unknown;
    try {
      planObj = JSON.parse(plan);
      rampObj = ramp.trim() ? JSON.parse(ramp) : [];
    } catch {
      setErr("plan/ramp must be valid JSON");
      return;
    }
    try {
      await api.createTest({ name, protocol, plan: planObj, ramp: rampObj, script: script || undefined });
      setOk("Test created");
      setName("");
      refresh();
    } catch (e: any) {
      setErr(e.message);
    }
  }

  if (!ready) return null;
  const canCreate = roleAtLeast(user?.role, "operator");

  return (
    <>
      <Nav />
      <div className="container">
        <h1>Tests</h1>

        {canCreate && (
          <form className="panel" onSubmit={create}>
            <h2>New test</h2>
            <div className="row">
              <div style={{ flex: 1 }}>
                <label>Name</label>
                <input value={name} onChange={(e) => setName(e.target.value)} required style={{ width: "100%" }} />
              </div>
              <div>
                <label>Protocol</label>
                <select value={protocol} onChange={(e) => setProtocol(e.target.value)}>
                  {["http", "https", "grpc", "websocket", "sse", "script"].map((p) => (
                    <option key={p}>{p}</option>
                  ))}
                </select>
              </div>
            </div>
            <label>Plan (JSON)</label>
            <textarea rows={6} value={plan} onChange={(e) => setPlan(e.target.value)} />
            <label>Ramp (JSON array of stages)</label>
            <textarea rows={4} value={ramp} onChange={(e) => setRamp(e.target.value)} />
            <label>Script (optional, goja JS — define iteration())</label>
            <textarea
              rows={4}
              value={script}
              onChange={(e) => setScript(e.target.value)}
              placeholder={'function iteration() { http.get("http://echo:8088/"); }'}
            />
            {err && <div className="error">{err}</div>}
            {ok && <div style={{ color: "var(--green)" }}>{ok}</div>}
            <div style={{ marginTop: 12 }}>
              <button type="submit">Create test</button>
            </div>
          </form>
        )}

        <div className="panel">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Protocol</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody>
              {tests.map((t) => (
                <tr key={t.id}>
                  <td>{t.name}</td>
                  <td>{t.protocol}</td>
                  <td className="muted">{new Date(t.created_at).toLocaleString()}</td>
                </tr>
              ))}
              {tests.length === 0 && (
                <tr>
                  <td colSpan={3} className="muted">
                    No tests defined.
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
