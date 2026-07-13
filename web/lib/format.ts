// Shared number/unit formatting so latency and counts read consistently across
// pages (dashboard, run detail, compare). Below 100ms keep one decimal for
// precision; above, round and group thousands.
export function fmtMs(n: number | undefined | null): string {
  // Non-finite (NaN/Infinity, e.g. a divide-by-zero from the API) renders "–"
  // rather than "NaN ms" / "∞ ms".
  if (n == null || !Number.isFinite(n)) return "–";
  const v = n >= 100 ? Math.round(n).toLocaleString() : n.toFixed(1);
  return `${v} ms`;
}

export function fmtInt(n: number | undefined | null): string {
  if (n == null || !Number.isFinite(n)) return "–";
  return Math.round(n).toLocaleString();
}

// fmtErrRate renders a 0..1 error fraction as a percentage. A nonzero rate below
// 0.01% keeps two significant figures instead of rounding to "0.00%", so a tiny-
// but-present error rate stays visible (matches the fine-grained thresholds).
export function fmtErrRate(n: number | undefined | null): string {
  if (n == null || !Number.isFinite(n)) return "–";
  const pct = n * 100;
  if (pct <= 0) return "0%";
  if (pct < 0.01) return `${Number(pct.toPrecision(2))}%`;
  return `${pct.toFixed(2)}%`;
}
