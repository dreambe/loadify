"use client";

import { useState } from "react";
import { api, type DebugResponse } from "@/lib/api";
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
export interface ScenarioStep {
  name: string;
  weight: number;
  method: string;
  url: string;
  headers: { key: string; value: string }[];
  body: string;
  extracts: ScenarioExtract[];
}
export interface ScenarioSpec {
  mode: "sequence" | "weighted";
  steps: ScenarioStep[];
}

const emptyStep = (): ScenarioStep => ({
  name: "",
  weight: 1,
  method: "GET",
  url: "",
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
        return {
          ...(st.name ? { name: st.name } : {}),
          ...(s.mode === "weighted" ? { weight: st.weight || 1 } : {}),
          method: st.method,
          url: st.url,
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

  // Per-step debug responses, keyed by step index. A step's "send test request"
  // fires its literal request (no {{var}} resolution) so the user can inspect
  // the response and click fields to build extract rows.
  const [debug, setDebug] = useState<Record<number, DebugResponse>>({});
  const [debugging, setDebugging] = useState<number | null>(null);
  const [rawView, setRawView] = useState<Record<number, boolean>>({});

  const setStep = (i: number, patch: Partial<ScenarioStep>) =>
    onChange({ ...value, steps: value.steps.map((s, idx) => (idx === i ? { ...s, ...patch } : s)) });

  // removeStep drops the step and clears cached debug results, whose numeric
  // keys would otherwise drift onto the wrong steps after the indices shift.
  const removeStep = (i: number) => {
    onChange({ ...value, steps: value.steps.filter((_, idx) => idx !== i) });
    setDebug({});
  };

  const stepHeaders = (st: ScenarioStep): Record<string, string> => {
    const h: Record<string, string> = {};
    for (const hd of st.headers) if (hd.key) h[hd.key] = hd.value;
    return h;
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
      let res: DebugResponse;
      if (weighted) {
        // Weighted steps are independent — fire just this one.
        res = await api.debugRequest({ method: st.method, url: st.url, headers: stepHeaders(st), body: st.body || undefined });
      } else {
        // Sequence: run steps 1..i in order so {{vars}} extracted upstream are
        // resolved, then surface this step's (now correctly chained) response.
        const steps = value.steps.slice(0, i + 1).map((s) => ({
          name: s.name,
          method: s.method,
          url: s.url,
          headers: stepHeaders(s),
          body: s.body || undefined,
          extracts: s.extracts.filter((e) => e.var && e.path),
        }));
        const chain = await api.debugScenario(steps);
        if (chain.error) {
          res = { status: 0, status_text: "", latency_ms: 0, headers: {}, body: "", body_truncated: false, recv_bytes: 0, error: chain.error };
        } else {
          const last = chain.steps[chain.steps.length - 1];
          res = {
            status: last?.status ?? 0,
            status_text: last ? (last.ok ? "OK" : last.error_kind || "FAILED") : "",
            latency_ms: last?.latency_ms ?? 0,
            headers: {},
            body: last?.body ?? "",
            body_truncated: false,
            recv_bytes: 0,
          };
        }
      }
      setDebug((d) => ({ ...d, [i]: res }));
    } catch (e: any) {
      setDebug((d) => ({
        ...d,
        [i]: { status: 0, status_text: "", latency_ms: 0, headers: {}, body: "", body_truncated: false, recv_bytes: 0, error: e.message },
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
              <button type="button" className="secondary" onClick={() => removeStep(i)}>
                {t("ramp.remove")}
              </button>
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

          {/* Headers */}
          {st.headers.map((h, hi) => (
            <div className="row" key={hi} style={{ marginBottom: 6 }}>
              <input
                placeholder="Header"
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
                  <div className="row" style={{ alignItems: "center", justifyContent: "space-between", marginBottom: 6 }}>
                    <span className="row" style={{ alignItems: "center", gap: 8 }}>
                      <span className={`badge ${debug[i].status < 400 ? "completed" : "failed"}`}>
                        {debug[i].status} {debug[i].status_text}
                      </span>
                      <span className="muted" style={{ fontFamily: "var(--font-mono)", fontSize: 12 }}>
                        {debug[i].latency_ms.toFixed(1)} ms
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
