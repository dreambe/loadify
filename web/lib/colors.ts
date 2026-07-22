// Single source of truth for chart series colors. These reference the theme
// CSS variables (SVG stroke/fill accept `var(...)`), so charts recolor with the
// light/dark theme instead of hardcoding hex values per call site.
export const chartColor = {
  accent: "var(--accent)",
  green: "var(--green)",
  violet: "var(--accent-2)",
  yellow: "var(--yellow)",
  red: "var(--red)",
} as const;

// Latency percentile palette, shared by live and historical latency charts.
// Four clearly distinct hues (cyan / indigo / amber / magenta) so the lines are
// easy to tell apart — but NOT green or red, which the design system reserves
// for verdicts (a percentile is a distribution point, not pass/fail). p95 (the
// usual SLA percentile) gets amber so it stands out; the tail (p99) is magenta.
export const latencyColors = {
  p50: chartColor.accent,
  p90: chartColor.violet,
  p95: chartColor.yellow,
  p99: "var(--lat-hi)",
} as const;

// Line-style (SVG stroke-dasharray) paired with latencyColors so the four
// percentile lines are distinguishable by SHAPE, not color alone — colorblind
// users can still tell p50/p90/p95/p99 apart (and match them against the
// legend, which draws the same dash). p50 solid (the median baseline), p95
// dash-dot to stand out (the usual SLA line), p99 dotted (the tail).
export const latencyDash = {
  p50: "",
  p90: "6 4",
  p95: "10 4 2 4",
  p99: "2 3",
} as const;

// Neutral, faint fill for the p50–p99 latency band (the spread envelope) — grey
// so it reads as "range" behind the colored lines without adding a fifth hue.
export const latencyBandColor = "var(--muted)";

// A/B comparison palette (compare page). Dash pairs with it so A (solid) and B
// (dashed) are distinguishable by line style, not color alone.
export const compareColors = { a: chartColor.accent, b: chartColor.yellow } as const;
export const compareDash = { a: "", b: "6 4" } as const;
