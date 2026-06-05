"use client";

// LineChart is a dependency-free SVG line chart for one or more series sharing
// the same x-axis (point index). Each series is auto-scaled to the shared max.
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

  const maxLen = Math.max(1, ...series.map((s) => s.data.length));
  const maxVal = Math.max(1, ...series.flatMap((s) => s.data));

  const x = (i: number) => pad.left + (maxLen <= 1 ? 0 : (i / (maxLen - 1)) * innerW);
  const y = (v: number) => pad.top + innerH - (v / maxVal) * innerH;

  const path = (data: number[]) =>
    data.map((v, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");

  const ticks = 4;
  const gridVals = Array.from({ length: ticks + 1 }, (_, i) => (maxVal / ticks) * i);

  return (
    <div>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        width="100%"
        height={height}
        role="img"
        aria-label="time series chart"
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
      </svg>
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
