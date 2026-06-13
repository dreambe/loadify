"use client";

import { useId, useState } from "react";

// LineChart is a dependency-free SVG line chart for one or more series sharing
// the same x-axis. `xLabels` gives each point an x-axis label (e.g. elapsed
// test time); a few are drawn as axis ticks. Hovering shows a crosshair and the
// value of every series at the nearest point. Pass `hoverIndex`/`onHover` to
// synchronize the crosshair across multiple charts (same moment everywhere).
export interface Series {
  label: string;
  color: string;
  data: number[];
}

export default function LineChart({
  series,
  height = 220,
  unit = "",
  xLabels,
  hoverIndex,
  onHover,
}: {
  series: Series[];
  height?: number;
  unit?: string;
  xLabels?: string[];
  hoverIndex?: number | null;
  onHover?: (i: number | null) => void;
}) {
  const width = 760;
  const pad = { top: 10, right: 12, bottom: 22, left: 48 };
  const innerW = width - pad.left - pad.right;
  const innerH = height - pad.top - pad.bottom;

  const gradientId = useId();
  // Uncontrolled fallback; when onHover is provided the parent owns the state.
  const [localHover, setLocalHover] = useState<number | null>(null);
  const hover = hoverIndex !== undefined ? hoverIndex : localHover;
  const setHover = (i: number | null) => {
    if (onHover) onHover(i);
    else setLocalHover(i);
  };

  // Click a legend entry to hide/show that series.
  const [hidden, setHidden] = useState<Set<string>>(new Set());
  // Drag across the plot to zoom into an x-index window; double-click resets.
  const [zoom, setZoom] = useState<{ lo: number; hi: number } | null>(null);
  const [drag, setDrag] = useState<{ start: number; cur: number } | null>(null);

  const maxLen = Math.max(1, ...series.map((s) => s.data.length));
  const lo = zoom ? zoom.lo : 0;
  const hi = zoom ? zoom.hi : maxLen - 1;
  const span = Math.max(1, hi - lo);
  const visible = series.filter((s) => !hidden.has(s.label));
  // Scale the y-axis to the visible series within the current x-window.
  const maxVal = Math.max(
    1,
    ...visible.flatMap((s) => s.data.slice(lo, hi + 1))
  );

  const x = (i: number) => pad.left + ((i - lo) / span) * innerW;
  const y = (v: number) => pad.top + innerH - (v / maxVal) * innerH;

  // Path over the visible x-window only.
  const path = (data: number[]) => {
    let d = "";
    for (let i = lo; i <= hi && i < data.length; i++) {
      d += `${i === lo ? "M" : "L"}${x(i).toFixed(1)},${y(data[i]).toFixed(1)}`;
    }
    return d;
  };

  // Single visible series gets a soft gradient area fill under the line.
  const areaSeries = visible.length === 1 && visible[0].data.length > 1 ? visible[0] : null;
  const areaPath = areaSeries
    ? `${path(areaSeries.data)} L${x(hi).toFixed(1)},${(pad.top + innerH).toFixed(1)} L${x(lo).toFixed(1)},${(
        pad.top + innerH
      ).toFixed(1)} Z`
    : "";

  const ticks = 4;
  const gridVals = Array.from({ length: ticks + 1 }, (_, i) => (maxVal / ticks) * i);

  // Pick a handful of evenly spaced x-axis tick indices within the window.
  const xTickCount = Math.min(6, span + 1);
  const xTickIdx =
    span < 1
      ? [lo]
      : Array.from({ length: xTickCount }, (_, i) => Math.round(lo + (i / (xTickCount - 1)) * span));

  // Map a mouse event to the nearest data index within the window. The SVG
  // preserves aspect ratio ("meet") so account for centered letterboxing.
  function idxAt(e: React.MouseEvent<SVGSVGElement>): number {
    const rect = e.currentTarget.getBoundingClientRect();
    const scale = Math.min(rect.width / width, rect.height / height);
    const offsetX = (rect.width - width * scale) / 2;
    const px = (e.clientX - rect.left - offsetX) / scale;
    const frac = (px - pad.left) / innerW;
    return Math.max(lo, Math.min(hi, Math.round(lo + frac * span)));
  }

  function onMove(e: React.MouseEvent<SVGSVGElement>) {
    if (maxLen <= 1) {
      setHover(0);
      return;
    }
    const idx = idxAt(e);
    setHover(idx);
    if (drag) setDrag({ ...drag, cur: idx });
  }
  function onDown(e: React.MouseEvent<SVGSVGElement>) {
    if (maxLen <= 1) return;
    const idx = idxAt(e);
    setDrag({ start: idx, cur: idx });
  }
  function onUp() {
    if (drag) {
      const a = Math.min(drag.start, drag.cur);
      const b = Math.max(drag.start, drag.cur);
      if (b - a >= 2) setZoom({ lo: a, hi: b });
      setDrag(null);
    }
  }

  const validHover = hover !== null && hover >= lo && hover <= hi ? hover : null;
  const hoverX = validHover !== null ? x(validHover) : 0;
  const tooltipRight = hoverX > pad.left + innerW * 0.6;
  const hoverLabel =
    validHover !== null ? xLabels?.[validHover] ?? `#${validHover + 1}` : "";

  return (
    <div style={{ position: "relative" }}>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        width="100%"
        height={height}
        role="img"
        aria-label="time series chart"
        style={{ cursor: drag ? "ew-resize" : "crosshair" }}
        onMouseMove={onMove}
        onMouseDown={onDown}
        onMouseUp={onUp}
        onMouseLeave={() => {
          setHover(null);
          setDrag(null);
        }}
        onDoubleClick={() => setZoom(null)}
      >
        {areaSeries && (
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={areaSeries.color} stopOpacity={0.22} />
              <stop offset="100%" stopColor={areaSeries.color} stopOpacity={0} />
            </linearGradient>
          </defs>
        )}
        {gridVals.map((v, i) => (
          <g key={i}>
            <line
              x1={pad.left}
              x2={width - pad.right}
              y1={y(v)}
              y2={y(v)}
              stroke="rgba(126,141,166,0.14)"
              strokeWidth={1}
            />
            <text
              x={4}
              y={y(v) + 4}
              fill="var(--muted)"
              fontSize={10}
              fontFamily="var(--font-mono)"
            >
              {formatTick(v)}
              {unit}
            </text>
          </g>
        ))}
        {xLabels &&
          xLabels.length > 0 &&
          xTickIdx.map((i) => (
            <text
              key={i}
              x={x(i)}
              y={height - 6}
              fill="var(--muted)"
              fontSize={10}
              fontFamily="var(--font-mono)"
              textAnchor="middle"
            >
              {xLabels[i] ?? ""}
            </text>
          ))}
        {areaSeries && <path d={areaPath} fill={`url(#${gradientId})`} stroke="none" />}
        {visible.map((s) => (
          <path
            key={s.label}
            d={path(s.data)}
            fill="none"
            stroke={s.color}
            strokeWidth={2}
            strokeLinejoin="round"
            strokeLinecap="round"
          />
        ))}

        {/* Drag-to-zoom selection band. */}
        {drag && drag.start !== drag.cur && (
          <rect
            x={Math.min(x(drag.start), x(drag.cur))}
            y={pad.top}
            width={Math.abs(x(drag.cur) - x(drag.start))}
            height={innerH}
            fill="var(--accent)"
            fillOpacity={0.12}
            stroke="var(--accent)"
            strokeOpacity={0.4}
          />
        )}

        {validHover !== null && (
          <g>
            <line
              x1={hoverX}
              x2={hoverX}
              y1={pad.top}
              y2={pad.top + innerH}
              stroke="var(--accent)"
              strokeOpacity={0.65}
              strokeWidth={1}
              strokeDasharray="3 3"
            />
            {series.map((s) =>
              s.data[validHover] !== undefined ? (
                <circle
                  key={s.label}
                  cx={hoverX}
                  cy={y(s.data[validHover])}
                  r={3.5}
                  fill={s.color}
                  stroke="var(--bg)"
                  strokeWidth={1.5}
                />
              ) : null
            )}
          </g>
        )}
      </svg>

      {validHover !== null && series.some((s) => s.data[validHover] !== undefined) && (
        <div
          style={{
            position: "absolute",
            top: 8,
            [tooltipRight ? "left" : "right"]: 16,
            background: "rgba(10, 16, 27, 0.85)",
            backdropFilter: "blur(8px)",
            WebkitBackdropFilter: "blur(8px)",
            border: "1px solid var(--border-strong)",
            borderRadius: 8,
            padding: "7px 11px",
            fontSize: 12,
            fontFamily: "var(--font-mono)",
            fontVariantNumeric: "tabular-nums",
            pointerEvents: "none",
            boxShadow: "0 8px 24px rgba(0,0,0,0.4)",
          }}
        >
          <div style={{ color: "var(--muted)", marginBottom: 2 }}>{hoverLabel}</div>
          {visible.map((s) =>
            s.data[validHover] !== undefined ? (
              <div key={s.label} style={{ color: s.color }}>
                {s.label}: <b>{formatVal(s.data[validHover])}</b>
                {unit}
              </div>
            ) : null
          )}
        </div>
      )}

      <div style={{ display: "flex", gap: 14, marginTop: 6, alignItems: "center", flexWrap: "wrap" }}>
        {series.map((s) => {
          const off = hidden.has(s.label);
          return (
            <button
              key={s.label}
              type="button"
              onClick={() =>
                setHidden((cur) => {
                  const n = new Set(cur);
                  n.has(s.label) ? n.delete(s.label) : n.add(s.label);
                  return n;
                })
              }
              title={off ? "显示 / show" : "隐藏 / hide"}
              style={{
                background: "transparent",
                border: "none",
                padding: 0,
                cursor: "pointer",
                fontSize: 12,
                color: off ? "var(--muted)" : s.color,
                opacity: off ? 0.5 : 1,
                textDecoration: off ? "line-through" : "none",
                transform: "none",
                fontWeight: 500,
              }}
            >
              ● {s.label}
            </button>
          );
        })}
        {zoom && (
          <button
            type="button"
            className="ghost sm"
            onClick={() => setZoom(null)}
            style={{ marginLeft: "auto" }}
          >
            ⤢ 复位 / reset zoom
          </button>
        )}
      </div>
    </div>
  );
}

// formatElapsed renders seconds since test start as "5s" / "1m05s" / "1h02m".
export function formatElapsed(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m${String(s % 60).padStart(2, "0")}s`;
  const h = Math.floor(m / 60);
  return `${h}h${String(m % 60).padStart(2, "0")}m`;
}

function formatTick(v: number): string {
  if (v >= 1000) return (v / 1000).toFixed(1) + "k";
  return v < 10 ? v.toFixed(1) : Math.round(v).toString();
}

function formatVal(v: number): string {
  if (v >= 1000) return v.toLocaleString(undefined, { maximumFractionDigits: 0 });
  return v < 10 ? v.toFixed(2) : v.toFixed(1);
}
