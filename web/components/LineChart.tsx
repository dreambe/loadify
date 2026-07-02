"use client";

import { useEffect, useId, useRef, useState } from "react";
import { useI18n } from "@/lib/i18n";
import { useToast } from "@/components/Toast";

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
  band,
  zoom: zoomProp,
  onZoom,
  onSelect,
  markIndices,
  markColor,
  panZoom,
  fileName,
}: {
  series: Series[];
  height?: number;
  unit?: string;
  xLabels?: string[];
  hoverIndex?: number | null;
  onHover?: (i: number | null) => void;
  // Optional shaded envelope between two series (e.g. the p50–p99 latency spread).
  band?: { lower: number[]; upper: number[]; color: string };
  // Controlled x-zoom window (share it across charts so they zoom together);
  // falls back to internal state when onZoom is omitted.
  zoom?: { lo: number; hi: number } | null;
  onZoom?: (z: { lo: number; hi: number } | null) => void;
  // Fires when a point is clicked (not dragged) — used to inspect that moment.
  onSelect?: (index: number) => void;
  // Indices to flag with a dot (e.g. seconds that had errors) + the dot color.
  markIndices?: number[];
  markColor?: string;
  // K-line-style interaction: wheel to zoom the time axis in/out (anchored at
  // the cursor) and drag to pan. Enabled in the expanded (fullscreen) view.
  panZoom?: boolean;
  // Base name for the exported PNG (e.g. "<run> - 吞吐 (QPS)"); ".png" is added.
  fileName?: string;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const svgRef = useRef<SVGSVGElement>(null);
  // Disambiguate single-click (inspect) from double-click (reset): a click waits
  // briefly and is cancelled if a second click arrives.
  const clickTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Responsive width: match the container so the chart fills any panel or the
  // expanded modal exactly (viewBox == element width → no letterboxing).
  const wrapRef = useRef<HTMLDivElement>(null);
  const [measuredW, setMeasuredW] = useState(760);
  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width;
      if (w && w > 0) setMeasuredW(Math.round(w));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const width = Math.max(320, measuredW);
  const pad = { top: 10, right: 12, bottom: 22, left: 48 };

  // exportPNG rasterizes the chart SVG to a PNG download. Hardened so it can
  // never silently do nothing:
  //  - color CSS vars are inlined (presentation attributes don't resolve var());
  //  - font-family attrs are dropped (their var(--font-*) can't resolve in a
  //    standalone SVG and the nested var made the <img> fail to load → silent);
  //  - the SVG loads from a blob: URL and the PNG downloads via an object URL on
  //    an anchor attached to the DOM (a detached anchor + big data-URL is ignored
  //    by some Chrome builds);
  //  - onerror / catch surface a toast instead of failing quietly.
  function exportPNG() {
    const svg = svgRef.current;
    if (!svg) {
      toast.error(t("chart.exportFailed"));
      return;
    }
    let svgURL = "";
    try {
      const cs = getComputedStyle(document.documentElement);
      const clone = svg.cloneNode(true) as SVGSVGElement;
      clone.setAttribute("xmlns", "http://www.w3.org/2000/svg");
      clone.setAttribute("width", String(width));
      clone.setAttribute("height", String(height));
      clone.querySelectorAll("[font-family]").forEach((el) => el.removeAttribute("font-family"));
      const raw = new XMLSerializer().serializeToString(clone);
      const s = inlineCssVars(raw, (name) => cs.getPropertyValue(name).trim());
      const bg = cs.getPropertyValue("--panel").trim() || "#0e1522";
      svgURL = URL.createObjectURL(new Blob([s], { type: "image/svg+xml;charset=utf-8" }));

      const img = new Image();
      img.onload = () => {
        try {
          const scale = 2;
          const canvas = document.createElement("canvas");
          canvas.width = width * scale;
          canvas.height = height * scale;
          const ctx = canvas.getContext("2d");
          if (!ctx) throw new Error("no 2d context");
          ctx.fillStyle = bg;
          ctx.fillRect(0, 0, canvas.width, canvas.height);
          ctx.scale(scale, scale);
          ctx.drawImage(img, 0, 0, width, height);
          canvas.toBlob((blob) => {
            if (!blob) {
              toast.error(t("chart.exportFailed"));
              return;
            }
            const url = URL.createObjectURL(blob);
            const a = document.createElement("a");
            a.download = (fileName ? fileName.replace(/[\\/:*?"<>|]+/g, "-").trim() : "loadify-chart") + ".png";
            a.href = url;
            document.body.appendChild(a);
            a.click();
            a.remove();
            URL.revokeObjectURL(url);
          }, "image/png");
        } catch (err) {
          console.error("chart PNG export failed", err);
          toast.error(t("chart.exportFailed"));
        } finally {
          URL.revokeObjectURL(svgURL);
        }
      };
      img.onerror = () => {
        URL.revokeObjectURL(svgURL);
        console.error("chart PNG export: SVG failed to load");
        toast.error(t("chart.exportFailed"));
      };
      img.src = svgURL;
    } catch (err) {
      if (svgURL) URL.revokeObjectURL(svgURL);
      console.error("chart PNG export failed", err);
      toast.error(t("chart.exportFailed"));
    }
  }
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
  // Controlled by the parent when onZoom is given (so sibling charts share one
  // window), otherwise kept locally.
  const [localZoom, setLocalZoom] = useState<{ lo: number; hi: number } | null>(null);
  const zoom = onZoom ? zoomProp ?? null : localZoom;
  const setZoom = (z: { lo: number; hi: number } | null) => (onZoom ? onZoom(z) : setLocalZoom(z));
  const [drag, setDrag] = useState<{ start: number; cur: number } | null>(null);
  // Pan origin for panZoom mode — pixel-anchored so it stays stable while the
  // zoom window shifts under it.
  const [pan, setPan] = useState<{ startX: number; startIdx: number; lo: number; hi: number; moved: boolean } | null>(
    null
  );

  const maxLen = Math.max(1, ...series.map((s) => s.data.length));
  const lo = zoom ? zoom.lo : 0;
  const hi = zoom ? zoom.hi : maxLen - 1;
  const span = Math.max(1, hi - lo);
  const visible = series.filter((s) => !hidden.has(s.label));
  // Scale the y-axis to the visible series within the current x-window. Only
  // finite values count, so a stray NaN/Infinity from the API can't make maxVal
  // NaN and blank every line.
  const maxVal = Math.max(
    1,
    ...visible.flatMap((s) => s.data.slice(lo, hi + 1)).filter((v) => Number.isFinite(v))
  );

  const x = (i: number) => pad.left + ((i - lo) / span) * innerW;
  const y = (v: number) => pad.top + innerH - (v / maxVal) * innerH;

  // Path over the visible x-window only; non-finite points are skipped (the line
  // just breaks across the gap) instead of poisoning the whole path with NaN.
  const path = (data: number[]) => {
    let d = "";
    let pen = false;
    for (let i = lo; i <= hi && i < data.length; i++) {
      const v = data[i];
      if (!Number.isFinite(v)) {
        pen = false;
        continue;
      }
      d += `${pen ? "L" : "M"}${x(i).toFixed(1)},${y(v).toFixed(1)}`;
      pen = true;
    }
    return d;
  };

  // Shaded band between two ordered series (upper >= lower, so the polygon is
  // always well-formed): forward along the upper edge, back along the lower.
  const bandPath = (() => {
    if (!band) return "";
    const pts: string[] = [];
    for (let i = lo; i <= hi && i < band.upper.length; i++) {
      const v = band.upper[i];
      if (Number.isFinite(v)) pts.push(`${pts.length ? "L" : "M"}${x(i).toFixed(1)},${y(v).toFixed(1)}`);
    }
    for (let i = Math.min(hi, band.lower.length - 1); i >= lo; i--) {
      const v = band.lower[i];
      if (Number.isFinite(v)) pts.push(`L${x(i).toFixed(1)},${y(v).toFixed(1)}`);
    }
    return pts.length ? pts.join("") + "Z" : "";
  })();

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

  // Unclamped fractional x position across the plot (0..1), for stable panning.
  function fracAt(clientX: number, rect: DOMRect): number {
    const scale = Math.min(rect.width / width, rect.height / height);
    const offsetX = (rect.width - width * scale) / 2;
    const px = (clientX - rect.left - offsetX) / scale;
    return (px - pad.left) / innerW;
  }

  // Wheel zooms the time axis in/out, anchored at the cursor (K-line style).
  function onWheel(e: React.WheelEvent<SVGSVGElement>) {
    if (!panZoom || maxLen <= 2) return;
    const anchor = idxAt(e);
    const curLo = zoom ? zoom.lo : 0;
    const curHi = zoom ? zoom.hi : maxLen - 1;
    const curSpan = Math.max(1, curHi - curLo);
    let newSpan = Math.round(curSpan * (e.deltaY > 0 ? 1.3 : 1 / 1.3));
    newSpan = Math.max(2, Math.min(maxLen - 1, newSpan));
    if (newSpan >= maxLen - 1) {
      setZoom(null);
      return;
    }
    const rel = (anchor - curLo) / curSpan;
    let newLo = Math.round(anchor - rel * newSpan);
    newLo = Math.max(0, Math.min(maxLen - 1 - newSpan, newLo));
    setZoom({ lo: newLo, hi: newLo + newSpan });
  }

  function onMove(e: React.MouseEvent<SVGSVGElement>) {
    if (maxLen <= 1) {
      setHover(0);
      return;
    }
    const idx = idxAt(e);
    setHover(idx);
    if (panZoom && pan) {
      const span0 = pan.hi - pan.lo;
      const deltaFrac = fracAt(e.clientX, e.currentTarget.getBoundingClientRect()) - fracAt(pan.startX, e.currentTarget.getBoundingClientRect());
      let newLo = Math.round(pan.lo - deltaFrac * span0);
      newLo = Math.max(0, Math.min(maxLen - 1 - span0, newLo));
      if (!pan.moved && Math.abs(deltaFrac) > 0.008) setPan({ ...pan, moved: true });
      setZoom({ lo: newLo, hi: newLo + span0 });
      return;
    }
    if (drag) setDrag({ ...drag, cur: idx });
  }
  function onDown(e: React.MouseEvent<SVGSVGElement>) {
    if (maxLen <= 1) return;
    const idx = idxAt(e);
    if (panZoom) {
      setPan({ startX: e.clientX, startIdx: idx, lo: zoom ? zoom.lo : 0, hi: zoom ? zoom.hi : maxLen - 1, moved: false });
      return;
    }
    setDrag({ start: idx, cur: idx });
  }
  // Delay the click so a double-click (reset) can cancel it before it inspects.
  function scheduleSelect(idx: number) {
    if (!onSelect) return;
    if (clickTimer.current) clearTimeout(clickTimer.current);
    clickTimer.current = setTimeout(() => {
      clickTimer.current = null;
      onSelect(idx);
    }, 220);
  }
  function onUp() {
    if (panZoom) {
      if (pan && !pan.moved) scheduleSelect(pan.startIdx); // a click, not a pan
      setPan(null);
      return;
    }
    if (drag) {
      const a = Math.min(drag.start, drag.cur);
      const b = Math.max(drag.start, drag.cur);
      if (b - a >= 2) setZoom({ lo: a, hi: b });
      else scheduleSelect(drag.start); // no meaningful drag → treat as a click
      setDrag(null);
    }
  }

  const validHover = hover !== null && hover >= lo && hover <= hi ? hover : null;
  const hoverX = validHover !== null ? x(validHover) : 0;
  const tooltipRight = hoverX > pad.left + innerW * 0.6;
  const hoverLabel =
    validHover !== null ? xLabels?.[validHover] ?? `#${validHover + 1}` : "";

  return (
    <div ref={wrapRef} style={{ position: "relative" }}>
      <svg
        ref={svgRef}
        viewBox={`0 0 ${width} ${height}`}
        width="100%"
        height={height}
        role="img"
        aria-label="time series chart"
        style={{ cursor: panZoom ? (pan?.moved ? "grabbing" : "grab") : drag ? "ew-resize" : "crosshair" }}
        onMouseMove={onMove}
        onMouseDown={onDown}
        onMouseUp={onUp}
        onWheel={onWheel}
        onMouseLeave={() => {
          setHover(null);
          setDrag(null);
          setPan(null);
        }}
        onDoubleClick={() => {
          if (clickTimer.current) {
            clearTimeout(clickTimer.current);
            clickTimer.current = null;
          }
          setZoom(null);
        }}
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
              stroke="var(--chart-grid)"
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
        {band && bandPath && <path d={bandPath} fill={band.color} fillOpacity={0.1} stroke="none" />}
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

        {/* Flagged points (e.g. seconds that had errors) — a dot on the line. */}
        {markIndices?.map((i) =>
          i >= lo && i <= hi && Number.isFinite(series[0]?.data[i]) ? (
            <circle
              key={"mk" + i}
              cx={x(i)}
              cy={y(series[0].data[i])}
              r={3}
              fill={markColor ?? "var(--red)"}
              stroke="var(--bg)"
              strokeWidth={1}
            />
          ) : null
        )}

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
              Number.isFinite(s.data[validHover]) ? (
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
            background: "color-mix(in srgb, var(--panel) 88%, transparent)",
            backdropFilter: "blur(8px)",
            WebkitBackdropFilter: "blur(8px)",
            border: "1px solid var(--border-strong)",
            borderRadius: 8,
            padding: "7px 11px",
            fontSize: 12,
            fontFamily: "var(--font-mono)",
            fontVariantNumeric: "tabular-nums",
            pointerEvents: "none",
            boxShadow: "var(--shadow-pop)",
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
              title={off ? t("chart.show") : t("chart.hide")}
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
        <div style={{ marginLeft: "auto", display: "flex", gap: 8, alignItems: "center" }}>
          {(onZoom || onSelect) && (
            <span className="caption" style={{ color: "var(--muted)" }}>
              {panZoom ? t("chart.hintPan") : t("chart.hint")}
            </span>
          )}
          {zoom && (
            <button type="button" className="ghost sm" onClick={() => setZoom(null)}>
              ⤢ {t("chart.resetZoom")}
            </button>
          )}
          <button type="button" className="ghost sm" onClick={exportPNG} title={t("chart.exportPng")}>
            ↓ {t("chart.exportPng")}
          </button>
        </div>
      </div>
    </div>
  );
}

// inlineCssVars replaces every var(--name) token in an SVG string with its
// resolved value (via lookup), so a serialized standalone SVG keeps all its
// colors — including series strokes/gradients like var(--yellow). Exported so
// it can be unit-tested without a browser. An unresolved var keeps its token
// (better a wrong-but-visible default than silently dropping a color).
export function inlineCssVars(svg: string, lookup: (name: string) => string): string {
  return svg.replace(/var\(\s*(--[a-zA-Z0-9-]+)\s*(?:,[^)]*)?\)/g, (m, name) => {
    const val = lookup(name);
    return val || m;
  });
}

// formatElapsed renders seconds since test start as "5s" / "1m05s" / "1h02m".
export function formatElapsed(seconds: number): string {
  if (!Number.isFinite(seconds)) return "–";
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m${String(s % 60).padStart(2, "0")}s`;
  const h = Math.floor(m / 60);
  return `${h}h${String(m % 60).padStart(2, "0")}m`;
}

function formatTick(v: number): string {
  if (!Number.isFinite(v)) return "–";
  if (v >= 1000) return (v / 1000).toFixed(1) + "k";
  return v < 10 ? v.toFixed(1) : Math.round(v).toString();
}

function formatVal(v: number): string {
  if (!Number.isFinite(v)) return "–";
  if (v >= 1000) return v.toLocaleString(undefined, { maximumFractionDigits: 0 });
  return v < 10 ? v.toFixed(2) : v.toFixed(1);
}
