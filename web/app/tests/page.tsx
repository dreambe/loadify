"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import Nav from "@/components/Nav";
import { api } from "@/lib/api";
import { useAuth, roleAtLeast } from "@/lib/auth";
import { useI18n, protocolLabel } from "@/lib/i18n";
import Help from "@/components/Help";
import EntityPicker from "@/components/EntityPicker";
import { Pager, usePager } from "@/components/Pager";
import EmptyState from "@/components/EmptyState";
import TableSkeleton from "@/components/TableSkeleton";
import { useToast } from "@/components/Toast";
import { useConfirm } from "@/components/Confirm";
import SortableTh from "@/components/SortableTh";
import NumberInput from "@/components/NumberInput";
import FormSection from "@/components/FormSection";
import Icon from "@/components/Icon";
import { parseCSV } from "@/lib/csv";
import RampBuilder, { defaultRamp, type RampSpec } from "@/components/RampBuilder";
import HttpRequestBuilder, {
  emptyHttpRequest,
  httpRequestToPlan,
  planToHttpRequest,
  type HttpRequest,
} from "@/components/HttpRequestBuilder";
import ThresholdsEditor from "@/components/ThresholdsEditor";
import SSEBuilder, { emptySSE, planToSSE, sseToPlan, type SSEConfig } from "@/components/SSEBuilder";
import ScenarioBuilder, {
  emptyScenario,
  planToScenario,
  scenarioToPlan,
  type ScenarioSpec,
} from "@/components/ScenarioBuilder";
import type { TestDefinition, Threshold } from "@/lib/types";

const SAMPLE_PLAN = `{
  "protocol": "grpc",
  "grpc": { "target": "echo:8089", "full_method": "/grpc.health.v1.Health/Check", "plaintext": true }
}`;

// SCRIPT_TEMPLATE shows the multi-interface pattern: several requests per
// iteration, parameter chaining between them, and per-interface groups.
const SCRIPT_TEMPLATE = `function iteration() {
  // 1) login — interfaces are separated into metric groups via headers/urls
  var login = http.post("https://api.example.com/login",
    JSON.stringify({ user: "alice", pass: "secret" }),
    { headers: { "Content-Type": "application/json" } });
  check("login 200", login.status === 200);

  // 2) chain: extract the token from A and pass it to B
  var token = JSON.parse(login.body).token;
  var me = http.get("https://api.example.com/me",
    { headers: { "Authorization": "Bearer " + token } });
  check("me ok", me.status === 200);

  // optional dataset row: var row = nextRow();
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
  const toast = useToast();
  const confirm = useConfirm();
  const [tests, setTests] = useState<TestDefinition[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [filter, setFilter] = useState("");
  const [showForm, setShowForm] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [savedId, setSavedId] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [protocol, setProtocol] = useState("http");
  const [http, setHttp] = useState<HttpRequest>({ ...emptyHttpRequest });
  const [sse, setSse] = useState<SSEConfig>(emptySSE);
  const [scenario, setScenario] = useState<ScenarioSpec>(emptyScenario);
  const [plan, setPlan] = useState(SAMPLE_PLAN);
  const [ramp, setRamp] = useState<RampSpec>(defaultRamp);
  const [thresholds, setThresholds] = useState<Threshold[]>([{ metric: "p95_ms", op: "<", value: 200 }]);
  const [script, setScript] = useState("");
  const [dataset, setDataset] = useState("");
  const [tags, setTags] = useState("");
  const [autoStop, setAutoStop] = useState(true);
  const [autoStopPct, setAutoStopPct] = useState(50);
  const [alertOn, setAlertOn] = useState(true);
  const [alertPct, setAlertPct] = useState(30);
  const [targetMon, setTargetMon] = useState(false);
  const [targetLabel, setTargetLabel] = useState("job"); // which label distinguishes services
  const [targetValue, setTargetValue] = useState(""); // the picked service
  const [targetSelector, setTargetSelector] = useState(""); // advanced: raw PromQL matcher (overrides)
  const [targetAdvanced, setTargetAdvanced] = useState(false);
  const [targetPromOn, setTargetPromOn] = useState(true); // false when no Prometheus configured
  const [targetServices, setTargetServices] = useState<string[]>([]); // dropdown values
  const [targetLabelOpts, setTargetLabelOpts] = useState<string[]>([]); // advanced label dropdown
  const [importing, setImporting] = useState(false);
  const [err, setErr] = useState("");
  const [ok, setOk] = useState("");
  const [submitting, setSubmitting] = useState(false);
  // Id of the test whose run is being launched — guards against a double-click
  // firing two runs before the page navigates to the new run.
  const [launchingId, setLaunchingId] = useState<string | null>(null);
  const [sort, setSort] = useState<{ key: "name" | "protocol" | "creator" | "created"; dir: "asc" | "desc" }>({
    key: "created",
    dir: "desc",
  });

  // Live-parsed dataset shape: row count + column names. Feeds the {{...}}
  // autocomplete in the builders and the status line under the dataset field.
  const dataInfo = useMemo(() => {
    if (!dataset.trim()) return null;
    try {
      const rows = JSON.parse(dataset);
      if (Array.isArray(rows) && rows.length > 0 && rows.every((r) => r && typeof r === "object" && !Array.isArray(r))) {
        return { rows: rows.length, cols: Object.keys(rows[0]) };
      }
    } catch {
      /* invalid JSON → status line shows the format hint */
    }
    return { rows: 0, cols: [] as string[] };
  }, [dataset]);
  const dataColumns = dataInfo?.cols ?? [];

  // downloadTemplate hands the user a working CSV example — the fastest way to
  // communicate the expected format is a file that already has it.
  function downloadTemplate() {
    const csv = "user,password,phone\nalice,secret1,13800000001\nbob,secret2,13800000002\ncarol,secret3,13800000003\n";
    const url = URL.createObjectURL(new Blob(["\ufeff" + csv], { type: "text/csv;charset=utf-8" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = "loadify-dataset-template.csv";
    a.click();
    URL.revokeObjectURL(url);
  }

  // launch starts a run for a test and navigates to it, ignoring re-entry while
  // a launch is already in flight.
  function launch(id: string) {
    if (launchingId) return;
    setLaunchingId(id);
    api
      .startRun(id, 1, "")
      .then((res) => (window.location.href = `/runs/${res.run_id}`))
      .catch((e) => {
        toast.error(e.message);
        setLaunchingId(null);
      });
  }
  const formRef = useRef<HTMLFormElement>(null);

  const isHTTP = protocol === "http";

  function refresh() {
    api
      .listTests()
      .then(setTests)
      .catch((e) => toast.error(e.message))
      .finally(() => setLoaded(true));
  }
  useEffect(() => {
    if (ready) refresh();
  }, [ready]);

  // When target monitoring is on, discover the service list (values of the
  // chosen label) + available labels from the operator's Prometheus for the
  // dropdowns. Refetches when the distinguishing label changes.
  useEffect(() => {
    if (!targetMon) return;
    let alive = true;
    api
      .promServices(targetLabel || "job")
      .then((r) => {
        if (!alive) return;
        setTargetPromOn(r.enabled);
        setTargetServices(r.values || []);
        setTargetLabelOpts(r.labels || []);
      })
      .catch(() => alive && setTargetPromOn(false));
    return () => {
      alive = false;
    };
  }, [targetMon, targetLabel]);

  const filtered = filter
    ? tests.filter((td) =>
        (td.name + td.protocol + (td.creator_name || "") + " " + (td.tags || []).join(" "))
          .toLowerCase()
          .includes(filter.toLowerCase())
      )
    : tests;
  // Sort a copy by the active column (default: newest first, matching the API).
  const sorted = [...filtered].sort((a, b) => {
    const key = (td: TestDefinition) =>
      sort.key === "name"
        ? td.name.toLowerCase()
        : sort.key === "protocol"
          ? td.protocol
          : sort.key === "creator"
            ? (td.creator_name || "").toLowerCase()
            : td.created_at;
    const av = key(a);
    const bv = key(b);
    const cmp = av < bv ? -1 : av > bv ? 1 : 0;
    return sort.dir === "asc" ? cmp : -cmp;
  });
  const pager = usePager(sorted, 10);
  const toggleSort = (k: typeof sort.key) =>
    setSort((s) => ({ key: k, dir: s.key === k && s.dir === "desc" ? "asc" : "desc" }));

  function resetForm() {
    setEditingId(null);
    setName("");
    setProtocol("http");
    setHttp({ ...emptyHttpRequest });
    setSse(emptySSE);
    setScenario(emptyScenario);
    setPlan(SAMPLE_PLAN);
    setRamp(defaultRamp);
    setThresholds([{ metric: "p95_ms", op: "<", value: 200 }]);
    setScript("");
    setDataset("");
    setTags("");
    setAutoStop(true);
    setAutoStopPct(50);
    setAlertOn(true);
    setAlertPct(30);
    setTargetMon(false);
    setTargetLabel("job");
    setTargetValue("");
    setTargetSelector("");
    setTargetAdvanced(false);
  }

  // loadIntoForm fills the builder from an existing test (edit keeps the id,
  // copy clears it so submitting creates a new test).
  function loadIntoForm(td: TestDefinition, mode: "edit" | "copy") {
    setShowForm(true);
    setSavedId(null);
    setEditingId(mode === "edit" ? td.id : null);
    setName(mode === "copy" ? `${td.name} ${t("tests.copySuffix")}` : td.name);
    setProtocol(td.protocol === "https" ? "http" : td.protocol);
    if (td.protocol === "http" || td.protocol === "https") {
      setHttp(planToHttpRequest(td.plan));
    } else if (td.protocol === "sse") {
      setSse(planToSSE(td.plan));
    } else if (td.protocol === "scenario") {
      setScenario(planToScenario(td.plan));
    } else {
      setPlan(JSON.stringify(td.plan, null, 2));
    }
    setRamp(rampToSpec(td.ramp, td.plan));
    setThresholds(td.thresholds && td.thresholds.length ? td.thresholds : []);
    setScript(td.script || "");
    setDataset(td.dataset ? JSON.stringify(td.dataset, null, 2) : "");
    setTags((td.tags || []).join(", "));
    const as = (td.plan as any)?.auto_stop;
    setAutoStop(!as || as.enabled !== false);
    setAutoStopPct(as?.error_rate_pct ?? 50);
    const al = (td.plan as any)?.alert;
    setAlertOn(!al || al.enabled !== false);
    setAlertPct(al?.error_rate_pct ?? 30);
    const tm = (td.plan as any)?.target_monitor;
    setTargetMon(!!tm?.enabled);
    setTargetSelector(tm?.selector || "");
    setTargetAdvanced(!!tm?.selector || (!!tm?.label && tm.label !== "job"));
    if (tm?.instance && !tm?.value) {
      // Legacy single-instance config.
      setTargetLabel("instance");
      setTargetValue(tm.instance);
    } else {
      setTargetLabel(tm?.label || "job");
      setTargetValue(tm?.value || "");
    }
    setErr("");
    setOk("");
    setTimeout(() => formRef.current?.scrollIntoView({ behavior: "smooth", block: "start" }), 50);
  }

  async function remove(td: TestDefinition) {
    const okToDelete = await confirm({
      title: t("tests.delete") + " · " + td.name,
      body: t("tests.deleteConfirm").replace("{name}", td.name),
      confirmLabel: t("tests.delete"),
      danger: true,
    });
    if (!okToDelete) return;
    try {
      await api.deleteTest(td.id);
      if (editingId === td.id) {
        resetForm();
        setShowForm(false);
      }
      toast.success(t("tests.deleted"));
      refresh();
    } catch (e: any) {
      toast.error(e.message);
    }
  }

  // applyImport prefills the builder from an imported draft (http or scenario).
  function applyImport(draft: { name: string; protocol: string; plan: any }) {
    resetForm();
    setSavedId(null);
    setEditingId(null);
    setShowForm(true);
    setName(draft.name || "imported");
    const proto = draft.protocol === "https" ? "http" : draft.protocol;
    setProtocol(proto);
    if (proto === "http") setHttp(planToHttpRequest(draft.plan));
    else if (proto === "scenario") setScenario(planToScenario(draft.plan));
    else setPlan(JSON.stringify(draft.plan, null, 2));
    setTimeout(() => formRef.current?.scrollIntoView({ behavior: "smooth", block: "start" }), 50);
  }

  function quickRun() {
    if (!savedId) return;
    launch(savedId);
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (submitting) return;
    setErr("");
    setOk("");
    // Required-field validation with a human message (HTML required attrs
    // cover most, but builder fields live outside native validation).
    if (!name.trim()) {
      setErr(t("tests.errName"));
      return;
    }
    if (isHTTP && !http.url.trim()) {
      setErr(t("tests.errUrl"));
      return;
    }
    if (protocol === "sse" && !sse.url.trim()) {
      setErr(t("tests.errUrl"));
      return;
    }
    if (protocol === "script" && !script.trim()) {
      setErr(t("tests.errScript"));
      return;
    }
    if (protocol === "scenario" && !scenario.steps.some((s) => s.url.trim())) {
      setErr(t("tests.errUrl"));
      return;
    }
    // https is not a separate choice — it is derived from the URL scheme.
    const effProtocol = isHTTP && http.url.trim().toLowerCase().startsWith("https") ? "https" : protocol;
    let planObj: any;
    if (protocol === "script") {
      planObj = { protocol: "script" };
    } else if (isHTTP) {
      planObj = httpRequestToPlan(effProtocol, http);
    } else if (protocol === "sse") {
      planObj = sseToPlan(sse);
    } else if (protocol === "scenario") {
      planObj = scenarioToPlan(scenario);
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
    // Auto-stop circuit breaker (plan-level; default on). Disabled is explicit.
    if (planObj && typeof planObj === "object") {
      planObj.auto_stop = autoStop
        ? { enabled: true, error_rate_pct: autoStopPct }
        : { enabled: false };
      planObj.alert = alertOn
        ? { enabled: true, error_rate_pct: alertPct }
        : { enabled: false };
      // Target-service monitoring: persist when enabled AND we have something to
      // query — a custom selector (advanced) wins, else the picked service.
      if (targetMon && targetSelector.trim()) {
        planObj.target_monitor = { enabled: true, selector: targetSelector.trim() };
      } else if (targetMon && targetValue.trim()) {
        planObj.target_monitor = { enabled: true, label: targetLabel || "job", value: targetValue.trim() };
      } else {
        delete planObj.target_monitor;
      }
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
      protocol: effProtocol,
      plan: planObj,
      ramp: rampObj,
      script: script || undefined,
      thresholds,
      dataset: datasetObj,
      tags: tags.split(",").map((s) => s.trim()).filter(Boolean),
    };
    setSubmitting(true);
    try {
      if (editingId) {
        await api.updateTest(editingId, body);
        setSavedId(editingId);
        setOk(t("tests.updated"));
      } else {
        const res = await api.createTest(body);
        setSavedId(res.id);
        setOk(t("tests.created"));
      }
      refresh();
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setSubmitting(false);
    }
  }

  if (!ready) return null;
  const canCreate = roleAtLeast(user?.role, "operator");
  // Shared read, owner-or-admin write: anyone may run or copy a test, but only
  // its creator (or an admin) may edit or delete it.
  const canModify = (td: TestDefinition) =>
    roleAtLeast(user?.role, "admin") || (!!td.created_by && td.created_by === user?.id);

  return (
    <>
      <Nav />
      <div className="container">
        <div className="row" style={{ justifyContent: "space-between", alignItems: "center" }}>
          <h1 style={{ margin: 0 }}>{t("tests.title")}</h1>
          {canCreate && (
            <div className="row" style={{ gap: 8 }}>
              <button className="secondary" onClick={() => setImporting(true)}>
                <Icon name="upload" /> {t("tests.import")}
              </button>
              <button
                onClick={() => {
                  if (showForm) {
                    setShowForm(false);
                  } else {
                    resetForm();
                    setSavedId(null);
                    setShowForm(true);
                  }
                }}
              >
                {showForm ? t("tests.closeForm") : `+ ${t("tests.new")}`}
              </button>
            </div>
          )}
        </div>

        {importing && (
          <ImportModal
            onClose={() => setImporting(false)}
            onImported={(draft) => {
              setImporting(false);
              applyImport(draft);
            }}
          />
        )}
        <div style={{ height: 16 }} />

        {canCreate && showForm && (
          <form className="panel" onSubmit={submit} ref={formRef}>
            <h2 style={{ marginBottom: 0 }}>{editingId ? t("tests.editTitle") : t("tests.new")}</h2>

            <FormSection num="01" title={t("tests.secBasics")}>
              <div className="form-grid">
                <div className="field span-2">
                  <label className="req">{t("tests.name")}</label>
                  <input value={name} onChange={(e) => setName(e.target.value)} required />
                </div>
                <div className="field span-2">
                  <label>
                    {t("tests.tags")}
                    <Help tip={t("tests.tagsHelp")} />
                  </label>
                  <input value={tags} onChange={(e) => setTags(e.target.value)} placeholder={t("tests.tagsPh")} />
                </div>
                <div className="field">
                  <label>{t("tests.protocol")}</label>
                  <select value={protocol} onChange={(e) => setProtocol(e.target.value)}>
                    <option value="http">HTTP / HTTPS</option>
                    <option value="scenario">{t("tests.protoScenario")}</option>
                    <option value="grpc">gRPC</option>
                    <option value="websocket">WebSocket</option>
                    <option value="sse">SSE</option>
                    <option value="script">{t("tests.protoScript")}</option>
                  </select>
                </div>
              </div>
            </FormSection>

            <FormSection num="02" title={t("tests.secRequest")} hint={t("tests.secRequestHint")}>
              {isHTTP && <HttpRequestBuilder value={http} onChange={setHttp} dataColumns={dataColumns} />}
              {protocol === "scenario" && <ScenarioBuilder value={scenario} onChange={setScenario} dataColumns={dataColumns} />}
              {protocol === "sse" && <SSEBuilder value={sse} onChange={setSse} />}
              {protocol === "script" && (
                <>
                  <label className="req">
                    {t("tests.script")}
                    <Help tip={t("tests.scriptHelp")} />
                  </label>
                  <textarea
                    rows={10}
                    value={script}
                    onChange={(e) => setScript(e.target.value)}
                    placeholder={SCRIPT_TEMPLATE}
                  />
                </>
              )}
              {!isHTTP && protocol !== "script" && protocol !== "sse" && protocol !== "scenario" && (
                <>
                  <label>{t("tests.plan")}</label>
                  <textarea rows={6} value={plan} onChange={(e) => setPlan(e.target.value)} />
                </>
              )}
            </FormSection>

            {/* Data feeder — dynamic per-request parameters. Available wherever
                the engine interpolates {{var}}: single HTTP requests (httpd
                driver), scenarios and scripts. CSV columns / JSON keys become
                {{column}} tokens in URL, params, headers and body. */}
            {(isHTTP || protocol === "scenario" || protocol === "script") && (
              <FormSection num="03" title={t("tests.secData")} hint={t("tests.secDataHint")}>
                <div className="row" style={{ marginBottom: 6, marginTop: 8, alignItems: "center" }}>
                  <label className="secondary" style={{ margin: 0, cursor: "pointer", padding: "8px 11px", border: "1px solid var(--border-strong)", borderRadius: 8 }}>
                    ⬆ {t("tests.dataUpload")}
                    <input
                      type="file"
                      accept=".csv,.json"
                      style={{ display: "none" }}
                      onChange={(e) => {
                        const f = e.target.files?.[0];
                        if (!f) return;
                        f.text().then((text) => {
                          if (f.name.toLowerCase().endsWith(".csv")) {
                            setDataset(JSON.stringify(parseCSV(text), null, 2));
                          } else {
                            setDataset(text);
                          }
                        });
                      }}
                    />
                  </label>
                  <Help tip={t("tests.dataUploadHelp")} />
                  <button type="button" className="ghost sm" onClick={downloadTemplate}>
                    ⬇ {t("tests.dataTemplate")}
                  </button>
                </div>
                <textarea
                  rows={3}
                  value={dataset}
                  onChange={(e) => setDataset(e.target.value)}
                  placeholder={'[{"user":"alice"},{"user":"bob"}]  →  {{user}}'}
                />
                {/* Live parse status: the format contract, answered as the user
                    types — valid rows become clickable {{column}} chips, invalid
                    input says exactly what shape is expected. */}
                {dataInfo &&
                  (dataInfo.rows > 0 ? (
                    <div className="row" style={{ alignItems: "center", gap: 6, marginTop: 6, fontSize: 12.5 }}>
                      <span style={{ color: "var(--green)" }}>✓</span>
                      <span className="muted">
                        {t("tests.dataParsedA")}
                        {dataInfo.rows}
                        {t("tests.dataParsedB")}
                      </span>
                      {dataInfo.cols.map((c) => (
                        <button
                          key={c}
                          type="button"
                          className="tag-chip"
                          style={{ fontFamily: "var(--font-mono)" }}
                          onClick={() => {
                            navigator.clipboard.writeText(`{{${c}}}`);
                            toast.success(t("tests.dataVarCopied"));
                          }}
                        >
                          {"{{" + c + "}}"}
                        </button>
                      ))}
                    </div>
                  ) : (
                    <div className="muted" style={{ marginTop: 6, fontSize: 12.5, color: "var(--yellow)" }}>
                      {t("tests.datasetErr")}
                    </div>
                  ))}
              </FormSection>
            )}

            <FormSection num={isHTTP || protocol === "scenario" || protocol === "script" ? "04" : "03"} title={t("tests.secLoad")}>
              <RampBuilder value={ramp} onChange={setRamp} />
            </FormSection>

            <FormSection num={isHTTP || protocol === "scenario" || protocol === "script" ? "05" : "04"} title={t("tests.secPass")}>
              <label>
                {t("tests.thresholds")}
                <Help tip={t("tests.thresholdsHelp")} />
              </label>
              <ThresholdsEditor value={thresholds} onChange={setThresholds} />
              <div className="row" style={{ alignItems: "center", marginTop: 12 }}>
                <label style={{ margin: 0, display: "flex", gap: 6, alignItems: "center" }}>
                  <input type="checkbox" checked={autoStop} onChange={(e) => setAutoStop(e.target.checked)} />
                  {t("tests.autoStop")}
                  <Help tip={t("tests.autoStopHelp")} />
                </label>
                {autoStop && (
                  <div>
                    <label style={{ margin: 0 }}>{t("tests.autoStopPct")}</label>
                    <NumberInput float min={0} max={100} value={autoStopPct} onChange={setAutoStopPct} style={{ width: 90 }} />
                  </div>
                )}
              </div>
              <div className="row" style={{ alignItems: "center", marginTop: 10 }}>
                <label style={{ margin: 0, display: "flex", gap: 6, alignItems: "center" }}>
                  <input type="checkbox" checked={alertOn} onChange={(e) => setAlertOn(e.target.checked)} />
                  {t("tests.alert")}
                  <Help tip={t("tests.alertHelp")} />
                </label>
                {alertOn && (
                  <div>
                    <label style={{ margin: 0 }}>{t("tests.alertPct")}</label>
                    <NumberInput float min={0} max={100} value={alertPct} onChange={setAlertPct} style={{ width: 90 }} />
                  </div>
                )}
              </div>
              <div style={{ marginTop: 10 }}>
                <label style={{ margin: 0, display: "flex", gap: 6, alignItems: "center" }}>
                  <input type="checkbox" checked={targetMon} onChange={(e) => setTargetMon(e.target.checked)} />
                  {t("tests.targetEnable")}
                  <Help tip={t("tests.targetMonitorHelp")} />
                </label>
                {targetMon && !targetPromOn && (
                  <p className="muted" style={{ marginTop: 6, color: "var(--yellow)" }}>
                    {t("tests.targetNoProm")}
                  </p>
                )}
                {targetMon && targetPromOn && (
                  <div style={{ marginTop: 8, maxWidth: 380 }}>
                    {/* Primary: pick a service from what's actually in Prometheus.
                        A styled combobox (not a native select/datalist) so it
                        matches the rest of the form. */}
                    <label style={{ margin: 0 }}>{t("tests.targetService")}</label>
                    <EntityPicker
                      items={targetSelector.trim() ? [] : targetServices}
                      value={targetSelector.trim() ? "" : targetValue}
                      onChange={setTargetValue}
                      idOf={(s) => s}
                      label={(s) => s}
                      keys={(s) => [s]}
                      accept={(raw) => raw}
                      placeholder={t("tests.targetPick")}
                      listId="target-svc"
                    />
                    <button
                      type="button"
                      className="ghost sm"
                      style={{ marginTop: 8 }}
                      onClick={() => setTargetAdvanced((v) => !v)}
                    >
                      {targetAdvanced ? "▾ " : "▸ "}
                      {t("tests.targetAdvanced")}
                    </button>
                    {targetAdvanced && (
                      <div style={{ marginTop: 8, paddingLeft: 12, borderLeft: "2px solid var(--border)" }}>
                        <label style={{ margin: 0 }}>{t("tests.targetLabel")}</label>
                        <EntityPicker
                          items={targetLabelOpts.length ? targetLabelOpts : [targetLabel]}
                          value={targetLabel}
                          onChange={(v) => { setTargetLabel(v); setTargetValue(""); }}
                          idOf={(s) => s}
                          label={(s) => s}
                          keys={(s) => [s]}
                          accept={(raw) => raw}
                          listId="target-label"
                        />
                        <label style={{ margin: "10px 0 0" }}>{t("tests.targetSelector")}</label>
                        <input
                          value={targetSelector}
                          onChange={(e) => setTargetSelector(e.target.value)}
                          placeholder={`job="prism-api"  或  instance=~"web-.*"`}
                          style={{ width: "100%", fontFamily: "var(--font-mono)" }}
                        />
                      </div>
                    )}
                  </div>
                )}
              </div>
            </FormSection>

            {err && <div className="error">{err}</div>}
            {ok && savedId && (
              <div className="row" style={{ alignItems: "center", marginTop: 8 }}>
                <span style={{ color: "var(--green)" }}>{ok}</span>
                <button type="button" onClick={quickRun} disabled={launchingId === savedId}>
                  <Icon name="play" /> {t("tests.runNow")}
                </button>
              </div>
            )}
            <div className="row" style={{ marginTop: 22, paddingTop: 16, borderTop: "1px solid var(--border)" }}>
              <button type="submit" disabled={submitting}>{editingId ? t("tests.save") : t("tests.create")}</button>
              <button
                type="button"
                className="secondary"
                onClick={() => {
                  resetForm();
                  setShowForm(false);
                }}
              >
                {t("tests.cancelEdit")}
              </button>
            </div>
          </form>
        )}

        {/* List is hidden while the create/edit form is open, so the page is
            either "browse tests" or "edit a test" — never a crowded mix. */}
        {!showForm && (
        <div className="panel">
          <div className="row" style={{ marginBottom: 4 }}>
            <input
              placeholder={t("tests.searchPh")}
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              style={{ width: 280 }}
            />
          </div>
          {!loaded ? (
            <TableSkeleton cols={canCreate ? 5 : 4} />
          ) : filtered.length === 0 ? (
            <EmptyState
              title={t("tests.empty")}
              hint={canCreate ? t("tests.emptyHint") : undefined}
              action={
                canCreate && !showForm ? (
                  <button onClick={() => setShowForm(true)}>+ {t("tests.new")}</button>
                ) : undefined
              }
            />
          ) : (
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <SortableTh label={t("tests.colName")} active={sort.key === "name"} dir={sort.dir} onToggle={() => toggleSort("name")} />
                    <SortableTh label={t("tests.colProtocol")} active={sort.key === "protocol"} dir={sort.dir} onToggle={() => toggleSort("protocol")} />
                    <SortableTh label={t("tests.colCreator")} active={sort.key === "creator"} dir={sort.dir} onToggle={() => toggleSort("creator")} />
                    <SortableTh label={t("tests.colCreated")} active={sort.key === "created"} dir={sort.dir} onToggle={() => toggleSort("created")} />
                    {canCreate && <th>{t("tests.colActions")}</th>}
                  </tr>
                </thead>
                <tbody>
                  {pager.slice.map((td) => (
                    <tr key={td.id}>
                      <td>
                        {td.name}
                        {td.tags && td.tags.length > 0 && (
                          <div style={{ display: "flex", flexWrap: "wrap", gap: 4, marginTop: 4 }}>
                            {td.tags.map((tag) => (
                              <button
                                key={tag}
                                type="button"
                                className="tag-chip"
                                onClick={() => setFilter(tag)}
                                title={t("tests.tagFilter")}
                              >
                                {tag}
                              </button>
                            ))}
                          </div>
                        )}
                      </td>
                      <td className="muted">{protocolLabel(t, td.protocol)}</td>
                      <td className="muted">{td.creator_name || "–"}</td>
                      <td className="muted">{new Date(td.created_at).toLocaleString()}</td>
                      {canCreate && (
                        <td>
                          <div className="actions">
                            <button
                              className="ghost sm"
                              disabled={launchingId === td.id}
                              onClick={() => launch(td.id)}
                            >
                              <Icon name="play" /> {t("tests.run")}
                            </button>
                            <button
                              className="ghost sm"
                              disabled={!canModify(td)}
                              title={canModify(td) ? undefined : t("common.ownerOnly")}
                              onClick={() => loadIntoForm(td, "edit")}
                            >
                              {t("tests.edit")}
                            </button>
                            <button className="ghost sm" onClick={() => loadIntoForm(td, "copy")}>
                              {t("tests.copy")}
                            </button>
                            <button
                              className="danger sm"
                              disabled={!canModify(td)}
                              title={canModify(td) ? undefined : t("common.ownerOnly")}
                              onClick={() => remove(td)}
                            >
                              {t("tests.delete")}
                            </button>
                          </div>
                        </td>
                      )}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <Pager page={pager.page} pages={pager.pages} total={pager.total} onPage={pager.setPage} />
        </div>
        )}
      </div>
    </>
  );
}

// ImportModal converts curl / HAR / Postman / OpenAPI into a draft the builder
// prefills. Paste text or upload a file; nothing is saved until the user
// reviews and submits the form.
function ImportModal({
  onClose,
  onImported,
}: {
  onClose: () => void;
  onImported: (draft: { name: string; protocol: string; plan: any }) => void;
}) {
  const { t } = useI18n();
  const [format, setFormat] = useState("curl");
  const [content, setContent] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit() {
    if (!content.trim()) return;
    setBusy(true);
    setErr("");
    try {
      const draft = await api.importTest(format, content);
      onImported(draft as any);
    } catch (e: any) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      onClick={onClose}
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,.5)",
        display: "grid",
        placeItems: "center",
        zIndex: 50,
      }}
    >
      <div className="panel" onClick={(e) => e.stopPropagation()} style={{ width: 640, maxWidth: "92vw" }}>
        <h2>{t("import.title")}</h2>
        <div className="row">
          <div>
            <label>{t("import.format")}</label>
            <select value={format} onChange={(e) => setFormat(e.target.value)}>
              <option value="curl">curl</option>
              <option value="har">HAR</option>
              <option value="postman">Postman</option>
              <option value="openapi">OpenAPI / Swagger</option>
            </select>
          </div>
          <div>
            <label>{t("import.upload")}</label>
            <input
              type="file"
              accept=".json,.har,.txt,.yaml,.yml"
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) f.text().then(setContent);
              }}
            />
          </div>
        </div>
        <label>{t("import.content")}</label>
        <textarea
          rows={8}
          value={content}
          onChange={(e) => setContent(e.target.value)}
          placeholder={format === "curl" ? t("import.curlPh") : ""}
        />
        <p className="muted" style={{ fontSize: 12.5 }}>
          {t("import.hint")}
        </p>
        {err && <div className="error">{err}</div>}
        <div className="row" style={{ marginTop: 8 }}>
          <button onClick={submit} disabled={busy || !content.trim()}>
            {t("import.submit")}
          </button>
          <button className="secondary" onClick={onClose}>
            {t("tests.cancelEdit")}
          </button>
        </div>
      </div>
    </div>
  );
}
