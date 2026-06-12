"use client";

import { useI18n } from "@/lib/i18n";
import Help from "./Help";

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

  const setStep = (i: number, patch: Partial<ScenarioStep>) =>
    onChange({ ...value, steps: value.steps.map((s, idx) => (idx === i ? { ...s, ...patch } : s)) });

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
              <button
                type="button"
                className="secondary"
                onClick={() => onChange({ ...value, steps: value.steps.filter((_, idx) => idx !== i) })}
              >
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
