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

// Neutral, faint fill for the p50–p99 latency band (the spread envelope) — grey
// so it reads as "range" behind the colored lines without adding a fifth hue.
export const latencyBandColor = "var(--muted)";

// A/B comparison palette (compare page).
export const compareColors = { a: chartColor.accent, b: chartColor.yellow } as const;
