"use client";

import { useState } from "react";
import { api } from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import Help from "./Help";
import Icon from "./Icon";
import JsonExplorer from "./JsonExplorer";

// A scenario is a multi-step HTTP plan: steps run in sequence (chaining
// extracted variables) or one-per-iteration by weight (traffic mix).
export interface ScenarioExtract {
  var: string;
  path: string;
}
// ScopeValue mirrors plan.Scope* — "" runs every iteration (workload), the
// others are run-once setup steps used to extract values for later steps.
export type ScopeValue = "" | "once_per_vu" | "once_global";
export interface ScenarioStep {
  name: string;
  weight: number;
  method: string;
  url: string;
  scope: ScopeValue;
  params: { key: string; value: string }[];
  headers: { key: string; value: string }[];
  body: string;
  extracts: ScenarioExtract[];
}
export interface ScenarioSpec {
  mode: "sequence" | "weighted";
  steps: ScenarioStep[];
}

// StepDebug is the resolved request + response shown after "send test request".
type StepDebug = {
  error?: string;
  method: string;
  url: string; // resolved (interpolation + query params)
  reqBody: string; // resolved request body
  status: number;
  ok: boolean;
  errorKind: string;
  latencyMs: number;
  body: string;
};

const emptyStep = (): ScenarioStep => ({
  name: "",
  weight: 1,
  method: "GET",
  url: "",
  scope: "",
  params: [],
  headers: [],
  body: "",
  extracts: [],
});

export const emptyScenario: ScenarioSpec = { mode: "sequence", steps: [emptyStep()] };

// scenarioToPlan serializes the builder into the backend plan object.
export function scenarioToPlan(s: ScenarioSpec): unknown {
  return {
    protocol: "scenario",
    scenario: {
      mode: s.mode,
      steps: s.steps.map((st) => {
        const headers: Record<string, string> = {};
        for (const h of st.headers) if (h.key) headers[h.key] = h.value;
        const params = st.params.filter((p) => p.key);
        return {
          ...(st.name ? { name: st.name } : {}),
          ...(s.mode === "weighted" ? { weight: st.weight || 1 } : {}),
          method: st.method,
          url: st.url,
          ...(st.scope ? { scope: st.scope } : {}),
          ...(params.length ? { params } : {}),
          ...(Object.keys(headers).length ? { headers } : {}),
          ...(st.body ? { body: st.body } : {}),
          ...(s.mode === "sequence" && st.extracts.length
            ? { extracts: st.extracts.filter((e) => e.var && e.path) }
            : {}),
        };
      }),
    },
  };
}

// planToScenario rebuilds builder state from a stored plan (edit / copy).
export function planToScenario(plan: any): ScenarioSpec {
  const sc = plan?.scenario ?? {};
  const steps = (sc.steps ?? []).map((st: any) => ({
    name: st.name ?? "",
    weight: st.weight ?? 1,
    method: st.method ?? "GET",
    url: st.url ?? "",
    scope: (st.scope ?? "") as ScopeValue,
    params: (st.params ?? []).map((p: any) => ({ key: p.key ?? "", value: String(p.value ?? "") })),
    headers: Object.entries(st.headers ?? {}).map(([key, value]) => ({ key, value: String(value) })),
    body: st.body ?? "",
    extracts: (st.extracts ?? []).map((e: any) => ({ var: e.var ?? "", path: e.path ?? "" })),
  }));
  return { mode: sc.mode === "weighted" ? "weighted" : "sequence", steps: steps.length ? steps : [emptyStep()] };
}

const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"];

// isJson gates the interactive tree view; non-JSON bodies show as raw text.
function isJson(s: string): boolean {
  try {
    JSON.parse(s);
    return true;
  } catch {
    return false;
  }
}

// prettyBody re-indents JSON for the raw view; other content is left as-is.
function prettyBody(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}

export default function ScenarioBuilder({
  value,
  onChange,
}: {
  value: ScenarioSpec;
  onChange: (s: ScenarioSpec) => void;
}) {
  const { t } = useI18n();
  const weighted = value.mode === "weighted";
  const totalWeight = value.steps.reduce((sum, s) => sum + (s.weight || 1), 0);

  // Per-step debug result, keyed by step index. "Send test request" runs the
  // step through the real scenario engine so {{var}} interpolation and query
  // params resolve, and surfaces the *resolved* request (method, URL, body)
  // next to the response — so the user can trust exactly what was sent.
  const [debug, setDebug] = useState<Record<number, StepDebug>>({});
  const [debugging, setDebugging] = useState<number | null>(null);
  const [rawView, setRawView] = useState<Record<number, boolean>>({});

  const setStep = (i: number, patch: Partial<ScenarioStep>) =>
    onChange({ ...value, steps: value.steps.map((s, idx) => (idx === i ? { ...s, ...patch } : s)) });

  // remapAfterRemove shifts an index-keyed record down past a removed index, so
  // cached results stay attached to the right steps instead of being wiped.
  function remapAfterRemove<T>(rec: Record<number, T>, removed: number): Record<number, T> {
    const out: Record<number, T> = {};
    for (const k of Object.keys(rec)) {
      const idx = Number(k);
      if (idx === removed) continue;
      out[idx > removed ? idx - 1 : idx] = rec[idx];
    }
    return out;
  }

  // removeStep drops the step but keeps the other steps' cached debug/raw-view
  // state, re-keyed for the shifted indices.
  const removeStep = (i: number) => {
    onChange({ ...value, steps: value.steps.filter((_, idx) => idx !== i) });
    setDebug((d) => remapAfterRemove(d, i));
    setRawView((r) => remapAfterRemove(r, i));
  };

  // remapAfterSwap swaps two index keys in an index-keyed record, keeping
  // cached debug/raw-view state attached to the steps that moved.
  function remapAfterSwap<T>(rec: Record<number, T>, i: number, j: number): Record<number, T> {
    const out = { ...rec };
    const a = out[i];
    const b = out[j];
    delete out[i];
    delete out[j];
    if (a !== undefined) out[j] = a;
    if (b !== undefined) out[i] = b;
    return out;
  }

  // moveStep swaps a step with its neighbor. In sequence mode order IS the
  // request chain, so reordering must not require deleting and re-entering.
  const moveStep = (i: number, dir: -1 | 1) => {
    const j = i + dir;
    if (j < 0 || j >= value.steps.length) return;
    const steps = [...value.steps];
    [steps[i], steps[j]] = [steps[j], steps[i]];
    onChange({ ...value, steps });
    setDebug((d) => remapAfterSwap(d, i, j));
    setRawView((r) => remapAfterSwap(r, i, j));
  };

  // payloadStep serializes a builder step into the debug-scenario wire shape.
  const payloadStep = (s: ScenarioStep) => {
    const headers: Record<string, string> = {};
    for (const h of s.headers) if (h.key) headers[h.key] = h.value;
    return {
      name: s.name,
      method: s.method,
      url: s.url,
      params: s.params.filter((p) => p.key),
      headers,
      body: s.body || undefined,
      extracts: s.extracts.filter((e) => e.var && e.path),
    };
  };

  async function runDebug(i: number) {
    const st = value.steps[i];
    if (!st.url) return;
    setDebugging(i);
    setDebug((d) => {
      const n = { ...d };
      delete n[i];
      return n;
    });
    try {
      // Sequence: run steps 1..i so upstream {{vars}} resolve. Weighted steps are
      // independent, so debug just this one — but still via the engine so its
      // params and template functions resolve.
      const steps = weighted ? [payloadStep(st)] : value.steps.slice(0, i + 1).map(payloadStep);
      const chain = await api.debugScenario(steps);
      let res: StepDebug;
      if (chain.error) {
        res = { error: chain.error, method: st.method, url: st.url, reqBody: "", status: 0, ok: false, errorKind: "", latencyMs: 0, body: "" };
      } else {
        const last = chain.steps[chain.steps.length - 1];
        res = {
          method: last?.method ?? st.method,
          url: last?.url ?? st.url,
          reqBody: last?.req_body ?? "",
          status: last?.status ?? 0,
          ok: last?.ok ?? false,
          errorKind: last?.error_kind ?? "",
          latencyMs: last?.latency_ms ?? 0,
          body: last?.body ?? "",
        };
      }
      setDebug((d) => ({ ...d, [i]: res }));
    } catch (e: any) {
      setDebug((d) => ({
        ...d,
        [i]: { error: e.message, method: st.method, url: st.url, reqBody: "", status: 0, ok: false, errorKind: "", latencyMs: 0, body: "" },
      }));
    } finally {
      setDebugging(null);
    }
  }

  // pickExtract appends an extract row pre-filled with the leaf key as the
  // variable name and the clicked field's dot-path; the user renames to confirm.
  const pickExtract = (i: number) => (path: string, leafKey: string) =>
    setStep(i, { extracts: [...value.steps[i].extracts, { var: leafKey, path }] });

  return (
    <div style={{ border: "1px solid var(--border)", borderRadius: 8, padding: 14 }}>
      <div className="row" style={{ alignItems: "center", marginBottom: 4 }}>
        <span className="muted">
          {t("scenario.mode")}
          <Help tip={t("scenario.modeHelp")} />:
        </span>
        <button
          type="button"
          className={value.mode === "sequence" ? "" : "secondary"}
          onClick={() => onChange({ ...value, mode: "sequence" })}
        >
          {t("scenario.sequence")}
        </button>
        <Help tip={t("scenario.sequenceHelp")} />
        <button
          type="button"
          className={weighted ? "" : "secondary"}
          onClick={() => onChange({ ...value, mode: "weighted" })}
        >
          {t("scenario.weighted")}
        </button>
        <Help tip={t("scenario.weightedHelp")} />
      </div>

      {value.steps.map((st, i) => (
        <div
          key={i}
          style={{
            border: "1px solid var(--border)",
            borderRadius: 8,
            padding: 12,
            marginTop: 10,
            background: "var(--panel-2)",
          }}
        >
          <div className="row" style={{ alignItems: "center", justifyContent: "space-between" }}>
            <b style={{ fontFamily: "var(--font-mono)" }}>
              {weighted ? "◆" : `${i + 1}.`} {st.name || t("scenario.step") + " " + (i + 1)}
            </b>
            {value.steps.length > 1 && (
              <div className="row" style={{ gap: 4, alignItems: "center" }}>
                <button
                  type="button"
                  className="ghost sm"
                  disabled={i === 0}
                  title={t("scenario.moveUp")}
                  aria-label={t("scenario.moveUp")}
                  onClick={() => moveStep(i, -1)}
                >
                  ↑
                </button>
                <button
                  type="button"
                  className="ghost sm"
                  disabled={i === value.steps.length - 1}
                  title={t("scenario.moveDown")}
                  aria-label={t("scenario.moveDown")}
                  onClick={() => moveStep(i, 1)}
                >
                  ↓
                </button>
                <button type="button" className="secondary" onClick={() => removeStep(i)}>
                  {t("ramp.remove")}
                </button>
              </div>
            )}
          </div>

          <div className="row">
            <div>
              <label>{t("scenario.stepName")}</label>
              <input value={st.name} onChange={(e) => setStep(i, { name: e.target.value })} style={{ width: 150 }} />
            </div>
            <div>
              <label>{t("http.method")}</label>
              <select value={st.method} onChange={(e) => setStep(i, { method: e.target.value })}>
                {METHODS.map((m) => (
                  <option key={m}>{m}</option>
                ))}
              </select>
            </div>
            <div>
              <label>
                {t("scenario.scope")}
                <Help tip={t("scenario.scopeHelp")} />
              </label>
              <select value={st.scope} onChange={(e) => setStep(i, { scope: e.target.value as ScopeValue })}>
                <option value="">{t("scenario.scopeEach")}</option>
                <option value="once_per_vu">{t("scenario.scopePerVu")}</option>
                <option value="once_global">{t("scenario.scopeGlobal")}</option>
              </select>
            </div>
            <div style={{ flex: 1 }}>
              <label className="req">{t("http.url")}</label>
              <input
                value={st.url}
                onChange={(e) => setStep(i, { url: e.target.value })}
                placeholder="https://api.example.com/v1/..."
                style={{ width: "100%" }}
              />
            </div>
            {weighted && (
              <div>
                <label>
                  {t("scenario.weight")}
                  <Help tip={t("scenario.weightHelp")} />
                </label>
                <input
                  type="number"
                  min={1}
                  value={st.weight}
                  onChange={(e) => setStep(i, { weight: parseInt(e.target.value || "1", 10) })}
                  style={{ width: 80 }}
                />
                <div className="muted" style={{ fontSize: 11, marginTop: 2 }}>
                  {totalWeight > 0 ? Math.round(((st.weight || 1) / totalWeight) * 100) : 0}%
                </div>
              </div>
            )}
          </div>

          {/* Query parameters: appended to the URL, interpolated then encoded. */}
          <label>
            {t("scenario.params")}
            <Help tip={t("scenario.paramsHelp")} />
          </label>
          {st.params.map((p, pi) => (
            <div className="row" key={pi} style={{ marginBottom: 6 }}>
              <input
                placeholder={t("scenario.paramKeyPh")}
                value={p.key}
                onChange={(e) =>
                  setStep(i, { params: st.params.map((x, xi) => (xi === pi ? { ...x, key: e.target.value } : x)) })
                }
                style={{ width: 200 }}
              />
              <input
                placeholder={t("scenario.valuePh")}
                value={p.value}
                onChange={(e) =>
                  setStep(i, { params: st.params.map((x, xi) => (xi === pi ? { ...x, value: e.target.value } : x)) })
                }
                style={{ flex: 1 }}
              />
              <button
                type="button"
                className="secondary"
                onClick={() => setStep(i, { params: st.params.filter((_, xi) => xi !== pi) })}
              >
                {t("ramp.remove")}
              </button>
            </div>
          ))}
          <div className="row">
            <button
              type="button"
              className="secondary"
              onClick={() => setStep(i, { params: [...st.params, { key: "", value: "" }] })}
            >
              + {t("scenario.addParam")}
            </button>
          </div>

          {/* Headers */}
          <label style={{ display: "block", marginTop: 4 }}>{t("http.headers")}</label>
          {st.headers.map((h, hi) => (
            <div className="row" key={hi} style={{ marginBottom: 6 }}>
              <input
                placeholder={t("kv.key")}
                value={h.key}
                onChange={(e) =>
                  setStep(i, { headers: st.headers.map((x, xi) => (xi === hi ? { ...x, key: e.target.value } : x)) })
                }
                style={{ width: 200 }}
              />
              <input
                placeholder={t("scenario.valuePh")}
                value={h.value}
                onChange={(e) =>
                  setStep(i, { headers: st.headers.map((x, xi) => (xi === hi ? { ...x, value: e.target.value } : x)) })
                }
                style={{ flex: 1 }}
              />
              <button
                type="button"
                className="secondary"
                onClick={() => setStep(i, { headers: st.headers.filter((_, xi) => xi !== hi) })}
              >
                {t("ramp.remove")}
              </button>
            </div>
          ))}
          <div className="row">
            <button
              type="button"
              className="secondary"
              onClick={() => setStep(i, { headers: [...st.headers, { key: "", value: "" }] })}
            >
              + {t("http.addHeader")}
            </button>
          </div>

          <label>{t("http.body")}</label>
          <textarea
            rows={2}
            value={st.body}
            onChange={(e) => setStep(i, { body: e.target.value })}
            placeholder={weighted ? "" : t("scenario.bodyPh")}
          />

          {/* Per-step debug: fire the literal request, inspect & pick fields. */}
          <div className="row" style={{ marginTop: 8, alignItems: "center" }}>
            <button
              type="button"
              className="secondary"
              disabled={!st.url || debugging === i}
              onClick={() => runDebug(i)}
            >
              {debugging === i ? (
                t("debug.sending")
              ) : (
                <>
                  <Icon name="play" /> {t("scenario.sendTest")}
                </>
              )}
            </button>
            <Help tip={t("scenario.sendTestHint")} />
          </div>
          {debug[i] && (
            <div
              style={{
                marginTop: 8,
                border: "1px solid var(--border-strong)",
                borderRadius: 8,
                padding: 10,
                background: "var(--bg)",
              }}
            >
              {debug[i].error ? (
                <div className="error" style={{ margin: 0 }}>
                  {t("debug.failed")}: {debug[i].error}
                </div>
              ) : (
                <>
                  {/* Resolved request — the exact method, URL and body sent. */}
                  <div style={{ marginBottom: 8 }}>
                    <div className="muted" style={{ fontSize: 11, marginBottom: 2 }}>{t("debug.sentRequest")}</div>
                    <div style={{ fontFamily: "var(--font-mono)", fontSize: 12, wordBreak: "break-all" }}>
                      <span className="badge" style={{ marginRight: 6 }}>{debug[i].method}</span>
                      {debug[i].url}
                    </div>
                    {debug[i].reqBody && (
                      <pre
                        style={{
                          margin: "4px 0 0",
                          maxHeight: 120,
                          overflow: "auto",
                          whiteSpace: "pre-wrap",
                          wordBreak: "break-all",
                          fontSize: 12,
                        }}
                      >
                        {prettyBody(debug[i].reqBody)}
                      </pre>
                    )}
                  </div>
                  <div className="row" style={{ alignItems: "center", justifyContent: "space-between", marginBottom: 6 }}>
                    <span className="row" style={{ alignItems: "center", gap: 8 }}>
                      <span className="muted" style={{ fontSize: 11 }}>{t("debug.response")}</span>
                      <span className={`badge ${debug[i].ok ? "ok" : "failed"}`}>
                        {debug[i].status} {debug[i].ok ? t("debug.ok") : debug[i].errorKind || t("debug.fail")}
                      </span>
                      <span className="muted" style={{ fontFamily: "var(--font-mono)", fontSize: 12 }}>
                        {debug[i].latencyMs.toFixed(1)} ms
                      </span>
                    </span>
                    {!weighted && isJson(debug[i].body) && (
                      <span style={{ display: "flex", gap: 4 }}>
                        <button
                          type="button"
                          className={rawView[i] ? "secondary" : ""}
                          style={{ padding: "2px 10px", fontSize: 12 }}
                          onClick={() => setRawView((v) => ({ ...v, [i]: false }))}
                        >
                          {t("json.viewTree")}
                        </button>
                        <button
                          type="button"
                          className={rawView[i] ? "" : "secondary"}
                          style={{ padding: "2px 10px", fontSize: 12 }}
                          onClick={() => setRawView((v) => ({ ...v, [i]: true }))}
                        >
                          {t("json.viewRaw")}
                        </button>
                      </span>
                    )}
                  </div>
                  {!weighted && !rawView[i] && isJson(debug[i].body) ? (
                    <>
                      <div className="muted" style={{ fontSize: 11, marginBottom: 6 }}>
                        {t("json.pickHint")}
                      </div>
                      <JsonExplorer body={debug[i].body} mode="extract" onPick={pickExtract(i)} />
                    </>
                  ) : (
                    <pre
                      style={{
                        margin: 0,
                        maxHeight: 200,
                        overflow: "auto",
                        whiteSpace: "pre-wrap",
                        wordBreak: "break-all",
                        fontSize: 12,
                      }}
                    >
                      {prettyBody(debug[i].body) || t("log.bodyEmpty")}
                    </pre>
                  )}
                </>
              )}
            </div>
          )}

          {/* Extracts (sequence chaining only) */}
          {!weighted && (
            <>
              <label>
                {t("scenario.extracts")}
                <Help tip={t("scenario.extractsHelp")} />
              </label>
              {st.extracts.map((ex, ei) => (
                <div className="row" key={ei} style={{ marginBottom: 6, alignItems: "center" }}>
                  <input
                    placeholder={t("scenario.varPh")}
                    value={ex.var}
                    onChange={(e) =>
                      setStep(i, {
                        extracts: st.extracts.map((x, xi) => (xi === ei ? { ...x, var: e.target.value } : x)),
                      })
                    }
                    style={{ width: 140, fontFamily: "var(--font-mono)" }}
                  />
                  <span className="muted">=</span>
                  <input
                    placeholder={t("assert.pathPh")}
                    value={ex.path}
                    onChange={(e) =>
                      setStep(i, {
                        extracts: st.extracts.map((x, xi) => (xi === ei ? { ...x, path: e.target.value } : x)),
                      })
                    }
                    style={{ flex: 1, fontFamily: "var(--font-mono)" }}
                  />
                  <button
                    type="button"
                    className="secondary"
                    onClick={() => setStep(i, { extracts: st.extracts.filter((_, xi) => xi !== ei) })}
                  >
                    {t("ramp.remove")}
                  </button>
                </div>
              ))}
              <button
                type="button"
                className="secondary"
                onClick={() => setStep(i, { extracts: [...st.extracts, { var: "", path: "" }] })}
              >
                + {t("scenario.addExtract")}
              </button>
            </>
          )}
        </div>
      ))}

      <div style={{ marginTop: 12 }}>
        <button type="button" className="secondary" onClick={() => onChange({ ...value, steps: [...value.steps, emptyStep()] })}>
          + {t("scenario.addStep")}
        </button>
      </div>
    </div>
  );
}
