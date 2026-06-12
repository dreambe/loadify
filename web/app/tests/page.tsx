"use client";

import { useEffect, useRef, useState } from "react";
import Nav from "@/components/Nav";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast } from "@/lib/auth";
import { useI18n } from "@/lib/i18n";
import Help from "@/components/Help";
import RampBuilder, { defaultRamp, type RampSpec } from "@/components/RampBuilder";
import HttpRequestBuilder, {
  emptyHttpRequest,
  httpRequestToPlan,
  planToHttpRequest,
  type HttpRequest,
} from "@/components/HttpRequestBuilder";
import ThresholdsEditor from "@/components/ThresholdsEditor";
import SSEBuilder, { emptySSE, planToSSE, sseToPlan, type SSEConfig } from "@/components/SSEBuilder";
import type { TestDefinition, Threshold } from "@/lib/types";

const SAMPLE_PLAN = `{
  "protocol": "grpc",
  "grpc": { "target": "echo:8089", "full_method": "/grpc.health.v1.Health/Check", "plaintext": true }
}`;

// rampToSpec rebuilds the ramp builder state from a stored ramp (edit / copy).
function rampToSpec(ramp: any, plan: any): RampSpec {
  const stages: { duration_ms?: number; target_vus?: number; target_rps?: number }[] =
    Array.isArray(ramp) ? ramp : [];
  if (stages.length === 0) return defaultRamp;
  const isRPS = stages.some((s) => (s.target_rps ?? 0) > 0);
  return {
    mode: isRPS ? "rps" : "vu",
    maxVus: (plan && typeof plan === "object" && (plan as any).max_vus) || 0,
    stages: stages.map((s) => ({
      target: (isRPS ? s.target_rps : s.target_vus) ?? 0,
      duration_s: Math.max(1, Math.round((s.duration_ms ?? 0) / 1000)),
    })),
  };
}

export default function TestsPage() {
  const { t } = useI18n();
  const { user, ready } = useAuth();
  const [tests, setTests] = useState<TestDefinition[]>([]);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [protocol, setProtocol] = useState("http");
  const [http, setHttp] = useState<HttpRequest>({ ...emptyHttpRequest, url: "http://echo:8088/" });
  const [sse, setSse] = useState<SSEConfig>(emptySSE);
  const [plan, setPlan] = useState(SAMPLE_PLAN);
  const [ramp, setRamp] = useState<RampSpec>(defaultRamp);
  const [thresholds, setThresholds] = useState<Threshold[]>([{ metric: "p95_ms", op: "<", value: 200 }]);
  const [script, setScript] = useState("");
  const [dataset, setDataset] = useState("");
  const [err, setErr] = useState("");
  const [ok, setOk] = useState("");
  const formRef = useRef<HTMLFormElement>(null);

  const isHTTP = protocol === "http" || protocol === "https";

  function refresh() {
    api.listTests().then(setTests).catch((e) => setErr(e.message));
  }
  useEffect(() => {
    if (ready) refresh();
  }, [ready]);

  function resetForm() {
    setEditingId(null);
    setName("");
    setProtocol("http");
    setHttp({ ...emptyHttpRequest, url: "http://echo:8088/" });
    setSse(emptySSE);
    setPlan(SAMPLE_PLAN);
    setRamp(defaultRamp);
    setThresholds([{ metric: "p95_ms", op: "<", value: 200 }]);
    setScript("");
    setDataset("");
  }

  // loadIntoForm fills the builder from an existing test (edit keeps the id,
  // copy clears it so submitting creates a new test).
  function loadIntoForm(td: TestDefinition, mode: "edit" | "copy") {
    setEditingId(mode === "edit" ? td.id : null);
    setName(mode === "copy" ? `${td.name} ${t("tests.copySuffix")}` : td.name);
    setProtocol(td.protocol);
    if (td.protocol === "http" || td.protocol === "https") {
      setHttp(planToHttpRequest(td.plan));
    } else if (td.protocol === "sse") {
      setSse(planToSSE(td.plan));
    } else {
      setPlan(JSON.stringify(td.plan, null, 2));
    }
    setRamp(rampToSpec(td.ramp, td.plan));
    setThresholds(td.thresholds && td.thresholds.length ? td.thresholds : []);
    setScript(td.script || "");
    setDataset(td.dataset ? JSON.stringify(td.dataset, null, 2) : "");
    setErr("");
    setOk("");
    formRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
  }

  async function remove(td: TestDefinition) {
    if (!window.confirm(t("tests.deleteConfirm").replace("{name}", td.name))) return;
    try {
      await api.deleteTest(td.id);
      if (editingId === td.id) resetForm();
      refresh();
    } catch (e: any) {
      setErr(e.message);
    }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setErr("");
    setOk("");
    let planObj: any;
    if (protocol === "script") {
      planObj = { protocol: "script" };
    } else if (isHTTP) {
      planObj = httpRequestToPlan(protocol, http);
    } else if (protocol === "sse") {
      planObj = sseToPlan(sse);
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
    let datasetObj: unknown;
    if (dataset.trim()) {
      try {
        datasetObj = JSON.parse(dataset);
      } catch {
        setErr(t("tests.datasetErr"));
        return;
      }
    }
    const body = {
      name,
      protocol,
      plan: planObj,
      ramp: rampObj,
      script: script || undefined,
      thresholds,
      dataset: datasetObj,
    };
    try {
      if (editingId) {
        await api.updateTest(editingId, body);
        setOk(t("tests.updated"));
      } else {
        await api.createTest(body);
        setOk(t("tests.created"));
      }
      resetForm();
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
          <form className="panel" onSubmit={submit} ref={formRef}>
            <h2>{editingId ? t("tests.editTitle") : t("tests.new")}</h2>
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
                <label>
                  {t("tests.request")}
                  <Help tip={t("tests.requestHelp")} />
                </label>
                <HttpRequestBuilder value={http} onChange={setHttp} />
              </>
            )}
            {protocol === "sse" && (
              <>
                <label>{t("tests.sse")}</label>
                <SSEBuilder value={sse} onChange={setSse} />
              </>
            )}
            {!isHTTP && protocol !== "script" && protocol !== "sse" && (
              <>
                <label>{t("tests.plan")}</label>
                <textarea rows={6} value={plan} onChange={(e) => setPlan(e.target.value)} />
              </>
            )}
            <label>
              {t("tests.ramp")}
              <Help tip={t("tests.rampHelp")} />
            </label>
            <RampBuilder value={ramp} onChange={setRamp} />
            <label>
              {t("tests.thresholds")}
              <Help tip={t("tests.thresholdsHelp")} />
            </label>
            <ThresholdsEditor value={thresholds} onChange={setThresholds} />
            {protocol === "script" && (
              <>
                <label>{t("tests.script")}</label>
                <textarea
                  rows={4}
                  value={script}
                  onChange={(e) => setScript(e.target.value)}
                  placeholder={`function iteration() {
  var row = nextRow();              // data feeder (optional)
  var r = http.get("http://echo:8088/", { headers: { "X-User": row ? row.user : "" } });
  check("status 200", r.status === 200);
  // extract & chain: var data = JSON.parse(r.body); http.post(url, JSON.stringify(data));
}`}
                />
                <label>
                  {t("tests.dataset")}
                  <Help tip={t("tests.datasetHelp")} />
                </label>
                <textarea
                  rows={3}
                  value={dataset}
                  onChange={(e) => setDataset(e.target.value)}
                  placeholder={'[{"user":"alice"},{"user":"bob"}]'}
                />
              </>
            )}
            {err && <div className="error">{err}</div>}
            {ok && <div style={{ color: "var(--green)" }}>{ok}</div>}
            <div className="row" style={{ marginTop: 12 }}>
              <button type="submit">{editingId ? t("tests.save") : t("tests.create")}</button>
              {editingId && (
                <button type="button" className="secondary" onClick={resetForm}>
                  {t("tests.cancelEdit")}
                </button>
              )}
            </div>
          </form>
        )}

        <div className="panel">
          <table>
            <thead>
              <tr>
                <th>{t("tests.colName")}</th>
                <th>{t("tests.colProtocol")}</th>
                <th>{t("tests.colCreator")}</th>
                <th>{t("tests.colCreated")}</th>
                {canCreate && <th>{t("tests.colActions")}</th>}
              </tr>
            </thead>
            <tbody>
              {tests.map((td) => (
                <tr key={td.id}>
                  <td>{td.name}</td>
                  <td>{td.protocol}</td>
                  <td className="muted">{td.creator_name || "–"}</td>
                  <td className="muted">{new Date(td.created_at).toLocaleString()}</td>
                  {canCreate && (
                    <td>
                      <div className="row" style={{ gap: 8 }}>
                        <button className="secondary" onClick={() => loadIntoForm(td, "edit")}>
                          {t("tests.edit")}
                        </button>
                        <button className="secondary" onClick={() => loadIntoForm(td, "copy")}>
                          {t("tests.copy")}
                        </button>
                        <button className="secondary" onClick={() => remove(td)}>
                          {t("tests.delete")}
                        </button>
                      </div>
                    </td>
                  )}
                </tr>
              ))}
              {tests.length === 0 && (
                <tr>
                  <td colSpan={canCreate ? 5 : 4} className="muted">
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
