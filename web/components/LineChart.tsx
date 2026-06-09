"use client";

import { useState } from "react";

// LineChart is a dependency-free SVG line chart for one or more series sharing
// the same x-axis (point index). Hovering shows a crosshair and the value of
// every series at the nearest point.
export interface Series {
  label: string;
  color: string;
  data: number[];
}

export default function LineChart({
  series,
  height = 220,
  unit = "",
}: {
  series: Series[];
  height?: number;
  unit?: string;
}) {
  const width = 760;
  const pad = { top: 10, right: 12, bottom: 20, left: 48 };
  const innerW = width - pad.left - pad.right;
  const innerH = height - pad.top - pad.bottom;

  const [hover, setHover] = useState<number | null>(null);

  const maxLen = Math.max(1, ...series.map((s) => s.data.length));
  const maxVal = Math.max(1, ...series.flatMap((s) => s.data));

  const x = (i: number) => pad.left + (maxLen <= 1 ? 0 : (i / (maxLen - 1)) * innerW);
  const y = (v: number) => pad.top + innerH - (v / maxVal) * innerH;

  const path = (data: number[]) =>
    data.map((v, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");

  const ticks = 4;
  const gridVals = Array.from({ length: ticks + 1 }, (_, i) => (maxVal / ticks) * i);

  // Map a mouse position to the nearest data index.
  function onMove(e: React.MouseEvent<SVGSVGElement>) {
    const rect = e.currentTarget.getBoundingClientRect();
    const px = ((e.clientX - rect.left) / rect.width) * width;
    if (maxLen <= 1) {
      setHover(0);
      return;
    }
    const frac = (px - pad.left) / innerW;
    const idx = Math.round(frac * (maxLen - 1));
    setHover(Math.max(0, Math.min(maxLen - 1, idx)));
  }

  const hoverX = hover !== null ? x(hover) : 0;
  const tooltipRight = hoverX > pad.left + innerW * 0.6;

  return (
    <div style={{ position: "relative" }}>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        width="100%"
        height={height}
        role="img"
        aria-label="time series chart"
        onMouseMove={onMove}
        onMouseLeave={() => setHover(null)}
      >
        {gridVals.map((v, i) => (
          <g key={i}>
            <line
              x1={pad.left}
              x2={width - pad.right}
              y1={y(v)}
              y2={y(v)}
              stroke="#30363d"
              strokeWidth={1}
            />
            <text x={4} y={y(v) + 4} fill="#8b949e" fontSize={10}>
              {formatTick(v)}
              {unit}
            </text>
          </g>
        ))}
        {series.map((s) => (
          <path key={s.label} d={path(s.data)} fill="none" stroke={s.color} strokeWidth={2} />
        ))}

        {hover !== null && (
          <g>
            <line
              x1={hoverX}
              x2={hoverX}
              y1={pad.top}
              y2={pad.top + innerH}
              stroke="#8b949e"
              strokeWidth={1}
              strokeDasharray="3 3"
            />
            {series.map((s) =>
              s.data[hover] !== undefined ? (
                <circle key={s.label} cx={hoverX} cy={y(s.data[hover])} r={3} fill={s.color} />
              ) : null
            )}
          </g>
        )}
      </svg>

      {hover !== null && series.some((s) => s.data[hover] !== undefined) && (
        <div
          style={{
            position: "absolute",
            top: 8,
            [tooltipRight ? "left" : "right"]: 16,
            background: "#0d1117",
            border: "1px solid var(--border)",
            borderRadius: 6,
            padding: "6px 10px",
            fontSize: 12,
            pointerEvents: "none",
          }}
        >
          <div style={{ color: "var(--muted)", marginBottom: 2 }}>#{hover + 1}</div>
          {series.map((s) =>
            s.data[hover] !== undefined ? (
              <div key={s.label} style={{ color: s.color }}>
                {s.label}: <b>{formatVal(s.data[hover])}</b>
                {unit}
              </div>
            ) : null
          )}
        </div>
      )}

      <div style={{ display: "flex", gap: 16, marginTop: 6 }}>
        {series.map((s) => (
          <span key={s.label} style={{ color: s.color, fontSize: 12 }}>
            ● {s.label}
          </span>
        ))}
      </div>
    </div>
  );
}

function formatTick(v: number): string {
  if (v >= 1000) return (v / 1000).toFixed(1) + "k";
  return v < 10 ? v.toFixed(1) : Math.round(v).toString();
}

function formatVal(v: number): string {
  if (v >= 1000) return v.toLocaleString(undefined, { maximumFractionDigits: 0 });
  return v < 10 ? v.toFixed(2) : v.toFixed(1);
}
