"use client";

import { useEffect, useState } from "react";
import Nav from "@/components/Nav";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import RampBuilder, { defaultRamp, type RampSpec } from "@/components/RampBuilder";
import HttpRequestBuilder, {
  emptyHttpRequest,
  httpRequestToPlan,
  type HttpRequest,
} from "@/components/HttpRequestBuilder";
import ThresholdsEditor from "@/components/ThresholdsEditor";
import type { TestDefinition, Threshold } from "@/lib/types";

const SAMPLE_PLAN = `{
  "protocol": "grpc",
  "grpc": { "target": "echo:8089", "full_method": "/grpc.health.v1.Health/Check", "plaintext": true }
}`;

export default function TestsPage() {
  const { t } = useI18n();
  const { user, ready } = useAuth();
  const [tests, setTests] = useState<TestDefinition[]>([]);
  const [name, setName] = useState("");
  const [protocol, setProtocol] = useState("http");
  const [http, setHttp] = useState<HttpRequest>({ ...emptyHttpRequest, url: "http://echo:8088/" });
  const [plan, setPlan] = useState(SAMPLE_PLAN);
  const [ramp, setRamp] = useState<RampSpec>(defaultRamp);
  const [thresholds, setThresholds] = useState<Threshold[]>([{ metric: "p95_ms", op: "<", value: 200 }]);
  const [script, setScript] = useState("");
  const [err, setErr] = useState("");
  const [ok, setOk] = useState("");

  const isHTTP = protocol === "http" || protocol === "https";

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
    let planObj: any;
    if (protocol === "script") {
      planObj = { protocol: "script" };
    } else if (isHTTP) {
      planObj = httpRequestToPlan(protocol, http);
    } else {
      try {
        planObj = JSON.parse(plan);
      } catch {
        setErr(t("tests.jsonErr"));
        return;
      }
    }
    const rampObj = ramp.stages.map((s) =>
      ramp.mode === "rps"
        ? { duration_ms: s.duration_s * 1000, target_rps: s.target }
        : { duration_ms: s.duration_s * 1000, target_vus: s.target }
    );
    // The open model's pool cap is a plan-level field.
    if (ramp.mode === "rps" && ramp.maxVus > 0 && planObj && typeof planObj === "object") {
      planObj.max_vus = ramp.maxVus;
    }
    try {
      await api.createTest({
        name,
        protocol,
        plan: planObj,
        ramp: rampObj,
        script: script || undefined,
        thresholds,
      });
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
            {isHTTP && (
              <>
                <label>{t("tests.request")}</label>
                <HttpRequestBuilder value={http} onChange={setHttp} />
              </>
            )}
            {!isHTTP && protocol !== "script" && (
              <>
                <label>{t("tests.plan")}</label>
                <textarea rows={6} value={plan} onChange={(e) => setPlan(e.target.value)} />
              </>
            )}
            <label>{t("tests.ramp")}</label>
            <RampBuilder value={ramp} onChange={setRamp} />
            <label>{t("tests.thresholds")}</label>
            <ThresholdsEditor value={thresholds} onChange={setThresholds} />
            {protocol === "script" && (
              <>
                <label>{t("tests.script")}</label>
                <textarea
                  rows={4}
                  value={script}
                  onChange={(e) => setScript(e.target.value)}
                  placeholder={`function iteration() {
  var r = http.get("http://echo:8088/");
  check("status 200", r.status === 200);
  // extract & chain: var data = JSON.parse(r.body); http.post(url, JSON.stringify(data));
}`}
                />
              </>
            )}
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
