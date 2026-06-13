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
export const latencyColors = {
  p50: chartColor.green,
  p90: chartColor.violet,
  p95: chartColor.yellow,
  p99: chartColor.red,
} as const;

// A/B comparison palette (compare page).
export const compareColors = { a: chartColor.accent, b: chartColor.yellow } as const;
