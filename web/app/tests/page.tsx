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
import SSEBuilder, { emptySSE, sseToPlan, type SSEConfig } from "@/components/SSEBuilder";
import type { Schedule, TestDefinition, Threshold } from "@/lib/types";

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
  const [sse, setSse] = useState<SSEConfig>(emptySSE);
  const [plan, setPlan] = useState(SAMPLE_PLAN);
  const [ramp, setRamp] = useState<RampSpec>(defaultRamp);
  const [thresholds, setThresholds] = useState<Threshold[]>([{ metric: "p95_ms", op: "<", value: 200 }]);
  const [script, setScript] = useState("");
  const [dataset, setDataset] = useState("");
  const [err, setErr] = useState("");
  const [ok, setOk] = useState("");

  const isHTTP = protocol === "http" || protocol === "https";

  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [schedTestId, setSchedTestId] = useState("");
  const [schedInterval, setSchedInterval] = useState(60);

  function refresh() {
    api.listTests().then(setTests).catch((e) => setErr(e.message));
    api.listSchedules().then(setSchedules).catch(() => {});
  }
  useEffect(() => {
    if (ready) refresh();
  }, [ready]);

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

  const testName = (id: string) => tests.find((t) => t.id === id)?.name || id.slice(0, 8);

  async function create(e: React.FormEvent) {
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
    try {
      await api.createTest({
        name,
        protocol,
        plan: planObj,
        ramp: rampObj,
        script: script || undefined,
        thresholds,
        dataset: datasetObj,
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
  var row = nextRow();              // data feeder (optional)
  var r = http.get("http://echo:8088/", { headers: { "X-User": row ? row.user : "" } });
  check("status 200", r.status === 200);
  // extract & chain: var data = JSON.parse(r.body); http.post(url, JSON.stringify(data));
}`}
                />
                <label>{t("tests.dataset")}</label>
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
            <div style={{ marginTop: 12 }}>
              <button type="submit">{t("tests.create")}</button>
            </div>
          </form>
        )}

        {canCreate && (
          <div className="panel">
            <h2>{t("sched.title")}</h2>
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
