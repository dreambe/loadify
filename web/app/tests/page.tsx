"use client";

import { useEffect, useState } from "react";
import Nav from "@/components/Nav";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import RampBuilder, { type Stage } from "@/components/RampBuilder";
import type { TestDefinition } from "@/lib/types";

const SAMPLE_PLAN = `{
  "protocol": "http",
  "http": { "method": "GET", "url": "http://echo:8088/" }
}`;
const DEFAULT_STAGES: Stage[] = [
  { target_vus: 20, duration_s: 10 },
  { target_vus: 50, duration_s: 20 },
];

export default function TestsPage() {
  const { t } = useI18n();
  const { user, ready } = useAuth();
  const [tests, setTests] = useState<TestDefinition[]>([]);
  const [name, setName] = useState("");
  const [protocol, setProtocol] = useState("http");
  const [plan, setPlan] = useState(SAMPLE_PLAN);
  const [stages, setStages] = useState<Stage[]>(DEFAULT_STAGES);
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
    try {
      planObj = JSON.parse(plan);
    } catch {
      setErr(t("tests.jsonErr"));
      return;
    }
    const rampObj = stages.map((s) => ({
      duration_ms: s.duration_s * 1000,
      target_vus: s.target_vus,
    }));
    try {
      await api.createTest({ name, protocol, plan: planObj, ramp: rampObj, script: script || undefined });
      setOk(t("tests.created"));
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
        <h1>{t("tests.title")}</h1>

        {canCreate && (
          <form className="panel" onSubmit={create}>
            <h2>{t("tests.new")}</h2>
            <div className="row">
              <div style={{ flex: 1 }}>
                <label>{t("tests.name")}</label>
                <input value={name} onChange={(e) => setName(e.target.value)} required style={{ width: "100%" }} />
              </div>
              <div>
                <label>{t("tests.protocol")}</label>
                <select value={protocol} onChange={(e) => setProtocol(e.target.value)}>
                  {["http", "https", "grpc", "websocket", "sse", "script"].map((p) => (
                    <option key={p}>{p}</option>
                  ))}
                </select>
              </div>
            </div>
            <label>{t("tests.plan")}</label>
            <textarea rows={6} value={plan} onChange={(e) => setPlan(e.target.value)} />
            <label>{t("tests.ramp")}</label>
            <RampBuilder value={stages} onChange={setStages} />
            <label>{t("tests.script")}</label>
            <textarea
              rows={4}
              value={script}
              onChange={(e) => setScript(e.target.value)}
              placeholder={'function iteration() { http.get("http://echo:8088/"); }'}
            />
            {err && <div className="error">{err}</div>}
            {ok && <div style={{ color: "var(--green)" }}>{ok}</div>}
            <div style={{ marginTop: 12 }}>
              <button type="submit">{t("tests.create")}</button>
            </div>
          </form>
        )}

        <div className="panel">
          <table>
            <thead>
              <tr>
                <th>{t("tests.colName")}</th>
                <th>{t("tests.colProtocol")}</th>
                <th>{t("tests.colCreated")}</th>
              </tr>
            </thead>
            <tbody>
              {tests.map((td) => (
                <tr key={td.id}>
                  <td>{td.name}</td>
                  <td>{td.protocol}</td>
                  <td className="muted">{new Date(td.created_at).toLocaleString()}</td>
                </tr>
              ))}
              {tests.length === 0 && (
                <tr>
                  <td colSpan={3} className="muted">
                    {t("tests.empty")}
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
