"use client";

import { useState } from "react";
import { useI18n } from "@/lib/i18n";
import Help from "./Help";

// Stage is one segment of the load profile: a target (VUs or req/s depending on
// the mode) reached over a duration, linearly interpolated from the previous.
export interface Stage {
  target: number;
  duration_s: number;
}

export interface RampSpec {
  mode: "vu" | "rps"; // closed (VU) or open (arrival-rate) model
  maxVus: number; // pool cap for the open model (0 = derive)
  stages: Stage[];
}

export const defaultRamp: RampSpec = {
  mode: "vu",
  maxVus: 0,
  stages: [
    { target: 20, duration_s: 10 },
    { target: 50, duration_s: 20 },
  ],
};

export default function RampBuilder({
  value,
  onChange,
}: {
  value: RampSpec;
  onChange: (s: RampSpec) => void;
}) {
  const { t } = useI18n();
  const [startN, setStartN] = useState(1);
  const [step, setStep] = useState(10);
  const [rounds, setRounds] = useState(3);
  const [hold, setHold] = useState(30);

  const isRPS = value.mode === "rps";
  const targetLabel = isRPS ? t("ramp.targetRps") : t("ramp.targetVus");

  const set = (patch: Partial<RampSpec>) => onChange({ ...value, ...patch });
  function updateStage(i: number, patch: Partial<Stage>) {
    set({ stages: value.stages.map((s, idx) => (idx === i ? { ...s, ...patch } : s)) });
  }
  function addStage() {
    const last = value.stages[value.stages.length - 1];
    set({ stages: [...value.stages, { target: last ? last.target : 10, duration_s: 30 }] });
  }
  function removeStage(i: number) {
    set({ stages: value.stages.filter((_, idx) => idx !== i) });
  }
  function generate() {
    const stages: Stage[] = [];
    for (let r = 1; r <= Math.max(1, rounds); r++) {
      stages.push({ target: startN + step * r, duration_s: hold });
    }
    set({ stages });
  }

  const peak = value.stages.reduce((m, s) => Math.max(m, s.target), 0);
  const total = value.stages.reduce((sum, s) => sum + s.duration_s, 0);

  return (
    <div>
      {/* Model toggle */}
      <div className="row" style={{ alignItems: "center", marginBottom: 10 }}>
        <span className="muted">
          {t("ramp.model")}
          <Help tip={t("ramp.modelHelp")} />:
        </span>
        <button
          type="button"
          className={value.mode === "vu" ? "" : "secondary"}
          onClick={() => set({ mode: "vu" })}
        >
          {t("ramp.modeVu")}
        </button>
        <Help tip={t("ramp.vuHelp")} />
        <button
          type="button"
          className={value.mode === "rps" ? "" : "secondary"}
          onClick={() => set({ mode: "rps" })}
        >
          {t("ramp.modeRps")}
        </button>
        <Help tip={t("ramp.rpsHelp")} />
        {/* Always occupy this slot so toggling modes never shifts the layout. */}
        <div style={{ visibility: isRPS ? "visible" : "hidden" }}>
          <label style={{ margin: 0 }}>
            {t("ramp.maxVus")}
            <Help tip={t("ramp.maxVusHelp")} />
          </label>
          <input
            type="number"
            min={0}
            value={value.maxVus}
            onChange={(e) => set({ maxVus: parseInt(e.target.value || "0", 10) })}
            style={{ width: 100 }}
          />
        </div>
      </div>

      {/* Stepped generator */}
      <div style={{ border: "1px solid var(--border)", borderRadius: 6, padding: 12, marginBottom: 12 }}>
        <div className="muted" style={{ marginBottom: 8 }}>
          {t("ramp.stepped")}
        </div>
        <div className="row">
          <NumField label={isRPS ? t("ramp.startRps") : t("ramp.startVus")} value={startN} onChange={setStartN} min={0} />
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
            <th>{targetLabel}</th>
            <th>{t("ramp.durationS")}</th>
            <th style={{ width: 80 }}></th>
          </tr>
        </thead>
        <tbody>
          {value.stages.map((s, i) => (
            <tr key={i}>
              <td className="muted">#{i + 1}</td>
              <td>
                <input
                  type="number"
                  min={0}
                  value={s.target}
                  onChange={(e) => updateStage(i, { target: parseInt(e.target.value || "0", 10) })}
                  style={{ width: 120 }}
                />
              </td>
              <td>
                <input
                  type="number"
                  min={1}
                  value={s.duration_s}
                  onChange={(e) => updateStage(i, { duration_s: parseInt(e.target.value || "1", 10) })}
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
          {t("ramp.peak")}: {peak} {isRPS ? "req/s" : "VU"} · {t("ramp.total")}: {total}s
        </span>
      </div>

      <RampPreview stages={value.stages} unit={isRPS ? "req/s" : "VU"} />
    </div>
  );
}

// RampPreview draws the load profile the stages describe: linear ramp from
// the previous target to each stage's target over its duration.
function RampPreview({ stages, unit }: { stages: Stage[]; unit: string }) {
  const { t } = useI18n();
  const width = 720;
  const height = 130;
  const pad = { top: 18, right: 40, bottom: 22, left: 44 };
  const innerW = width - pad.left - pad.right;
  const innerH = height - pad.top - pad.bottom;

  const total = stages.reduce((s, st) => s + Math.max(0, st.duration_s), 0);
  const peak = Math.max(1, ...stages.map((s) => s.target));
  if (total <= 0 || stages.length === 0) return null;

  // Points: start at (0, 0-or-first-target? ramps interpolate from previous
  // target, starting from 0).
  const pts: [number, number][] = [[0, 0]];
  let elapsed = 0;
  for (const st of stages) {
    elapsed += Math.max(0, st.duration_s);
    pts.push([elapsed, st.target]);
  }
  const x = (s: number) => pad.left + (s / total) * innerW;
  const y = (v: number) => pad.top + innerH - (v / peak) * innerH;
  const path = pts.map(([s, v], i) => `${i === 0 ? "M" : "L"}${x(s).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
  const area = `${path} L${x(total).toFixed(1)},${(pad.top + innerH).toFixed(1)} L${pad.left},${(pad.top + innerH).toFixed(1)} Z`;

  return (
    <div style={{ marginTop: 12 }}>
      <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>
        {t("ramp.preview")}
      </div>
      <svg viewBox={`0 0 ${width} ${height}`} width="100%" height={height} role="img" aria-label="ramp preview">
        <defs>
          <linearGradient id="ramp-fill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--accent)" stopOpacity={0.25} />
            <stop offset="100%" stopColor="var(--accent)" stopOpacity={0.02} />
          </linearGradient>
        </defs>
        <line x1={pad.left} x2={width - pad.right} y1={pad.top + innerH} y2={pad.top + innerH} stroke="var(--border-strong)" strokeWidth={1} />
        <path d={area} fill="url(#ramp-fill)" stroke="none" />
        <path d={path} fill="none" stroke="var(--accent)" strokeWidth={2} strokeLinejoin="round" />
        {pts.slice(1).map(([s, v], i, arr) => {
          // Anchor labels so the first/last points don't clip at the edges.
          const anchor = i === arr.length - 1 ? "end" : i === 0 ? "start" : "middle";
          return (
            <g key={i}>
              <circle cx={x(s)} cy={y(v)} r={3} fill="var(--accent)" stroke="var(--bg)" strokeWidth={1.5} />
              <text x={x(s)} y={y(v) - 7} fill="var(--muted)" fontSize={10} textAnchor={anchor} fontFamily="var(--font-mono)">
                {v}
              </text>
              <text x={x(s)} y={height - 5} fill="var(--muted)" fontSize={10} textAnchor={anchor} fontFamily="var(--font-mono)">
                {s}s
              </text>
            </g>
          );
        })}
        <text x={4} y={pad.top + 4} fill="var(--muted)" fontSize={10} fontFamily="var(--font-mono)">
          {peak} {unit}
        </text>
      </svg>
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
