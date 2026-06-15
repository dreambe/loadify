// Shared number/unit formatting so latency and counts read consistently across
// pages (dashboard, run detail, compare). Below 100ms keep one decimal for
// precision; above, round and group thousands.
export function fmtMs(n: number | undefined | null): string {
  if (n == null) return "–";
  const v = n >= 100 ? Math.round(n).toLocaleString() : n.toFixed(1);
  return `${v} ms`;
}

export function fmtInt(n: number | undefined | null): string {
  if (n == null) return "–";
  return Math.round(n).toLocaleString();
}
