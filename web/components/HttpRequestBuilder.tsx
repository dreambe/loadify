"use client";

import { useState } from "react";
import { api, type DebugResponse } from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import Help from "./Help";

// HttpRequest is the structured form of an HTTP/HTTPS plan, kept in component
// state and serialized into the plan JSON the API expects.
export interface HttpRequest {
  method: string;
  url: string;
  headers: { key: string; value: string }[];
  body: string;
  expectStatus: number;
  bodyContains: string;
  insecureSkipVerify: boolean;
}

export const emptyHttpRequest: HttpRequest = {
  method: "GET",
  url: "",
  headers: [],
  body: "",
  expectStatus: 0,
  bodyContains: "",
  insecureSkipVerify: false,
};

// toPlan converts the form into the backend plan object.
export function httpRequestToPlan(protocol: string, r: HttpRequest): unknown {
  const headers: Record<string, string> = {};
  for (const h of r.headers) if (h.key) headers[h.key] = h.value;
  return {
    protocol,
    http: {
      method: r.method,
      url: r.url,
      ...(Object.keys(headers).length ? { headers } : {}),
      ...(r.body ? { body: r.body } : {}),
      ...(r.expectStatus ? { expect_status: r.expectStatus } : {}),
      ...(r.bodyContains ? { body_contains: r.bodyContains } : {}),
      ...(r.insecureSkipVerify ? { insecure_skip_verify: true } : {}),
    },
  };
}

// planToHttpRequest rebuilds the form state from a stored plan (edit / copy).
export function planToHttpRequest(plan: any): HttpRequest {
  const h = plan?.http ?? {};
  return {
    method: h.method || "GET",
    url: h.url || "",
    headers: Object.entries(h.headers ?? {}).map(([key, value]) => ({
      key,
      value: String(value),
    })),
    body: h.body || "",
    expectStatus: h.expect_status || 0,
    bodyContains: h.body_contains || "",
    insecureSkipVerify: !!h.insecure_skip_verify,
  };
}

const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"];

export default function HttpRequestBuilder({
  value,
  onChange,
}: {
  value: HttpRequest;
  onChange: (r: HttpRequest) => void;
}) {
  const { t } = useI18n();
  const [debugging, setDebugging] = useState(false);
  const [debug, setDebug] = useState<DebugResponse | null>(null);

  function setHeader(i: number, patch: Partial<{ key: string; value: string }>) {
    onChange({ ...value, headers: value.headers.map((h, idx) => (idx === i ? { ...h, ...patch } : h)) });
  }
  function addHeader() {
    onChange({ ...value, headers: [...value.headers, { key: "", value: "" }] });
  }
  function removeHeader(i: number) {
    onChange({ ...value, headers: value.headers.filter((_, idx) => idx !== i) });
  }

  async function runDebug() {
    if (!value.url) return;
    setDebugging(true);
    setDebug(null);
    const headers: Record<string, string> = {};
    for (const h of value.headers) if (h.key) headers[h.key] = h.value;
    try {
      setDebug(
        await api.debugRequest({
          method: value.method,
          url: value.url,
          headers,
          body: value.body || undefined,
          insecure_skip_verify: value.insecureSkipVerify || undefined,
        })
      );
    } catch (e: any) {
      setDebug({
        status: 0,
        status_text: "",
        latency_ms: 0,
        headers: {},
        body: "",
        body_truncated: false,
        recv_bytes: 0,
        error: e.message,
      });
    } finally {
      setDebugging(false);
    }
  }

  // Live preview of how the configured assertions judge the debug response.
  const statusPass =
    debug && !debug.error
      ? value.expectStatus
        ? debug.status === value.expectStatus
        : debug.status < 400
      : null;
  const bodyPass =
    debug && !debug.error && value.bodyContains ? debug.body.includes(value.bodyContains) : null;

  return (
    <div style={{ border: "1px solid var(--border)", borderRadius: 8, padding: 14 }}>
      <div className="row">
        <div>
          <label>{t("http.method")}</label>
          <select value={value.method} onChange={(e) => onChange({ ...value, method: e.target.value })}>
            {METHODS.map((m) => (
              <option key={m}>{m}</option>
            ))}
          </select>
        </div>
        <div style={{ flex: 1 }}>
          <label>{t("http.url")}</label>
          <input
            value={value.url}
            onChange={(e) => onChange({ ...value, url: e.target.value })}
            placeholder="http://echo:8088/"
            style={{ width: "100%" }}
          />
        </div>
        <button type="button" className="secondary" onClick={runDebug} disabled={!value.url || debugging}>
          {debugging ? t("debug.sending") : `▶ ${t("debug.send")}`}
        </button>
      </div>

      <label>{t("http.headers")}</label>
      {value.headers.map((h, i) => (
        <div className="row" key={i} style={{ marginBottom: 6 }}>
          <input
            placeholder="Header"
            value={h.key}
            onChange={(e) => setHeader(i, { key: e.target.value })}
            style={{ width: 220 }}
          />
          <input
            placeholder="Value"
            value={h.value}
            onChange={(e) => setHeader(i, { value: e.target.value })}
            style={{ flex: 1 }}
          />
          <button type="button" className="secondary" onClick={() => removeHeader(i)}>
            {t("ramp.remove")}
          </button>
        </div>
      ))}
      <button type="button" className="secondary" onClick={addHeader}>
        + {t("http.addHeader")}
      </button>

      <label>{t("http.body")}</label>
      <textarea rows={4} value={value.body} onChange={(e) => onChange({ ...value, body: e.target.value })} />

      <label>
        {t("assert.title")}
        <Help tip={t("assert.help")} />
      </label>
      <div className="row">
        <div>
          <label style={{ margin: 0 }}>{t("assert.status")}</label>
          <input
            type="number"
            min={0}
            value={value.expectStatus || ""}
            placeholder={t("assert.statusPh")}
            onChange={(e) => onChange({ ...value, expectStatus: parseInt(e.target.value || "0", 10) })}
            style={{ width: 150 }}
          />
        </div>
        <div style={{ flex: 1 }}>
          <label style={{ margin: 0 }}>{t("assert.bodyContains")}</label>
          <input
            value={value.bodyContains}
            placeholder={t("assert.bodyPh")}
            onChange={(e) => onChange({ ...value, bodyContains: e.target.value })}
            style={{ width: "100%" }}
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

      {debug && (
        <div
          style={{
            marginTop: 12,
            border: "1px solid var(--border-strong)",
            borderRadius: 8,
            padding: 12,
            background: "var(--panel-2)",
          }}
        >
          {debug.error ? (
            <div className="error" style={{ margin: 0 }}>
              {t("debug.failed")}: {debug.error}
            </div>
          ) : (
            <>
              <div className="row" style={{ alignItems: "center", marginBottom: 8 }}>
                <span className={`badge ${debug.status < 400 ? "completed" : "failed"}`}>
                  {debug.status} {debug.status_text}
                </span>
                <span className="muted" style={{ fontFamily: "var(--font-mono)" }}>
                  {debug.latency_ms.toFixed(1)} ms · {formatBytes(debug.recv_bytes)}
                </span>
                {statusPass !== null && (
                  <span style={{ color: statusPass ? "var(--green)" : "var(--red)" }}>
                    {statusPass ? "✓" : "✗"} {t("assert.status")}
                  </span>
                )}
                {bodyPass !== null && (
                  <span style={{ color: bodyPass ? "var(--green)" : "var(--red)" }}>
                    {bodyPass ? "✓" : "✗"} {t("assert.bodyContains")}
                  </span>
                )}
              </div>
              <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>
                {t("debug.respBody")}
                {debug.body_truncated ? ` (${t("debug.truncated")})` : ""}
              </div>
              <pre
                style={{
                  margin: 0,
                  maxHeight: 240,
                  overflow: "auto",
                  whiteSpace: "pre-wrap",
                  wordBreak: "break-all",
                  fontSize: 12,
                }}
              >
                {debug.body || t("log.bodyEmpty")}
              </pre>
            </>
          )}
        </div>
      )}
    </div>
  );
}

function formatBytes(n: number): string {
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + " MB";
  if (n >= 1 << 10) return (n / (1 << 10)).toFixed(1) + " KB";
  return n + " B";
}
