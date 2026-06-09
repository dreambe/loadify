"use client";

import { useState } from "react";
import { useI18n } from "@/lib/i18n";

// Stage mirrors a backend RampStage as authored in the UI (duration in seconds).
export interface Stage {
  target_vus: number;
  duration_s: number;
}

// RampBuilder edits a load profile as a table of stages (each: target VUs over a
// duration, linearly interpolated from the previous target — the k6 model). It
// also offers a "stepped" generator: N rounds, a per-round VU step, and a hold
// duration, producing a staircase profile.
export default function RampBuilder({
  value,
  onChange,
}: {
  value: Stage[];
  onChange: (s: Stage[]) => void;
}) {
  const { t } = useI18n();
  const [startVus, setStartVus] = useState(0);
  const [step, setStep] = useState(10);
  const [rounds, setRounds] = useState(3);
  const [hold, setHold] = useState(30);

  function update(i: number, patch: Partial<Stage>) {
    onChange(value.map((s, idx) => (idx === i ? { ...s, ...patch } : s)));
  }
  function addStage() {
    const last = value[value.length - 1];
    onChange([...value, { target_vus: last ? last.target_vus : 10, duration_s: 30 }]);
  }
  function removeStage(i: number) {
    onChange(value.filter((_, idx) => idx !== i));
  }
  function generate() {
    const stages: Stage[] = [];
    for (let r = 1; r <= Math.max(1, rounds); r++) {
      stages.push({ target_vus: startVus + step * r, duration_s: hold });
    }
    onChange(stages);
  }

  const peak = value.reduce((m, s) => Math.max(m, s.target_vus), 0);
  const total = value.reduce((sum, s) => sum + s.duration_s, 0);

  return (
    <div>
      {/* Stepped generator */}
      <div
        style={{
          border: "1px solid var(--border)",
          borderRadius: 6,
          padding: 12,
          marginBottom: 12,
        }}
      >
        <div className="muted" style={{ marginBottom: 8 }}>
          {t("ramp.stepped")}
        </div>
        <div className="row">
          <NumField label={t("ramp.startVus")} value={startVus} onChange={setStartVus} min={0} />
          <NumField label={t("ramp.step")} value={step} onChange={setStep} min={1} />
          <NumField label={t("ramp.rounds")} value={rounds} onChange={setRounds} min={1} />
          <NumField label={t("ramp.hold")} value={hold} onChange={setHold} min={1} />
          <button type="button" className="secondary" onClick={generate}>
            {t("ramp.generate")}
          </button>
        </div>
      </div>

      {/* Stages table */}
      <table>
        <thead>
          <tr>
            <th style={{ width: 60 }}>{t("ramp.stage")}</th>
            <th>{t("ramp.targetVus")}</th>
            <th>{t("ramp.durationS")}</th>
            <th style={{ width: 80 }}></th>
          </tr>
        </thead>
        <tbody>
          {value.map((s, i) => (
            <tr key={i}>
              <td className="muted">#{i + 1}</td>
              <td>
                <input
                  type="number"
                  min={0}
                  value={s.target_vus}
                  onChange={(e) => update(i, { target_vus: parseInt(e.target.value || "0", 10) })}
                  style={{ width: 120 }}
                />
              </td>
              <td>
                <input
                  type="number"
                  min={1}
                  value={s.duration_s}
                  onChange={(e) => update(i, { duration_s: parseInt(e.target.value || "1", 10) })}
                  style={{ width: 120 }}
                />
              </td>
              <td>
                <button type="button" className="secondary" onClick={() => removeStage(i)}>
                  {t("ramp.remove")}
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      <div className="row" style={{ marginTop: 8, justifyContent: "space-between" }}>
        <button type="button" className="secondary" onClick={addStage}>
          + {t("ramp.addStage")}
        </button>
        <span className="muted">
          {t("ramp.peak")}: {peak} VU · {t("ramp.total")}: {total}s
        </span>
      </div>
    </div>
  );
}

function NumField({
  label,
  value,
  onChange,
  min,
}: {
  label: string;
  value: number;
  onChange: (n: number) => void;
  min?: number;
}) {
  return (
    <div>
      <label>{label}</label>
      <input
        type="number"
        min={min}
        value={value}
        onChange={(e) => onChange(parseInt(e.target.value || "0", 10))}
        style={{ width: 90 }}
      />
    </div>
  );
}
