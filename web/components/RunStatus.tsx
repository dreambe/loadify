"use client";

import { useI18n, statusLabel } from "@/lib/i18n";
import type { Run } from "@/lib/types";

// RunStatus is the single, context-aware status pill for a run. Lifecycle
// (running/completed/failed/…) and SLA verdict (pass/fail) are two dimensions,
// but rendering both as separate pills is redundant noise: a failed run's
// verdict is moot, and a completed run's verdict is the signal that matters. So
// a finished run surfaces its SLA verdict when one exists; otherwise it shows
// the lifecycle state (completed stays neutral — "done" is not a verdict).
export default function RunStatus({ run }: { run: Run }) {
  const { t } = useI18n();
  if (run.status === "completed" && run.summary?.passed !== undefined) {
    return run.summary.passed ? (
      <span className="badge ok">{t("run.passed")}</span>
    ) : (
      <span className="badge failed">{t("run.slaNotMet")}</span>
    );
  }
  return <span className={`badge ${run.status}`}>{statusLabel(t, run.status)}</span>;
}
