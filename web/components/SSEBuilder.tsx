"use client";

import { useI18n } from "@/lib/i18n";

// SSEConfig is the structured form of a Server-Sent-Events plan.
export interface SSEConfig {
  url: string;
  maxEvents: number;
  timeoutMs: number;
  insecureSkipVerify: boolean;
}

export const emptySSE: SSEConfig = { url: "", maxEvents: 5, timeoutMs: 30000, insecureSkipVerify: false };

// planToSSE rebuilds the form state from a stored plan (edit / copy).
export function planToSSE(plan: any): SSEConfig {
  const s = plan?.sse ?? {};
  return {
    url: s.url || "",
    maxEvents: s.max_events || 5,
    timeoutMs: s.timeout_ms || 30000,
    insecureSkipVerify: !!s.insecure_skip_verify,
  };
}

// sseToPlan converts the form into the backend plan object.
export function sseToPlan(c: SSEConfig): unknown {
  return {
    protocol: "sse",
    sse: {
      url: c.url,
      ...(c.maxEvents ? { max_events: c.maxEvents } : {}),
      ...(c.timeoutMs ? { timeout_ms: c.timeoutMs } : {}),
      ...(c.insecureSkipVerify ? { insecure_skip_verify: true } : {}),
    },
  };
}

// SSEBuilder edits an SSE load test. Reminder: SSE is a long-lived stream, so
// drive it with the VU (closed) model — VUs ≈ concurrent open streams.
export default function SSEBuilder({
  value,
  onChange,
}: {
  value: SSEConfig;
  onChange: (c: SSEConfig) => void;
}) {
  const { t } = useI18n();
  return (
    <div style={{ border: "1px solid var(--border)", borderRadius: 6, padding: 12 }}>
      <p className="muted" style={{ marginTop: 0 }}>
        {t("sse.hint")}
      </p>
      <label>{t("sse.url")}</label>
      <input
        value={value.url}
        onChange={(e) => onChange({ ...value, url: e.target.value })}
        placeholder="https://api/stream"
        style={{ width: "100%" }}
      />
      <div className="row">
        <div>
          <label>{t("sse.maxEvents")}</label>
          <input
            type="number"
            min={1}
            value={value.maxEvents}
            onChange={(e) => onChange({ ...value, maxEvents: parseInt(e.target.value || "1", 10) })}
            style={{ width: 120 }}
          />
        </div>
        <div>
          <label>{t("sse.timeoutMs")}</label>
          <input
            type="number"
            min={1000}
            step={1000}
            value={value.timeoutMs}
            onChange={(e) => onChange({ ...value, timeoutMs: parseInt(e.target.value || "1000", 10) })}
            style={{ width: 140 }}
          />
        </div>
      </div>
      <label style={{ display: "flex", gap: 6, alignItems: "center", marginTop: 10 }}>
        <input
          type="checkbox"
          checked={value.insecureSkipVerify}
          onChange={(e) => onChange({ ...value, insecureSkipVerify: e.target.checked })}
        />
        {t("http.insecure")}
      </label>
    </div>
  );
}
