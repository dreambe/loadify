"use client";

import { useState } from "react";
import { api, type DebugResponse } from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import Help from "./Help";
import Icon from "./Icon";
import JsonExplorer from "./JsonExplorer";

// Assert mirrors the backend plan.HTTPAssert: one per-request check.
export interface Assert {
  source: "status" | "body" | "json";
  path: string;
  op: string;
  value: string;
}

// HttpRequest is the structured form of an HTTP/HTTPS plan, kept in component
// state and serialized into the plan JSON the API expects.
export interface HttpRequest {
  method: string;
  url: string;
  params: { key: string; value: string }[];
  headers: { key: string; value: string }[];
  body: string;
  asserts: Assert[];
  insecureSkipVerify: boolean;
  followRedirects: boolean;
  cookieJar: boolean;
  traceHeader: boolean;
  clientCertPEM: string;
  clientKeyPEM: string;
}

export const emptyHttpRequest: HttpRequest = {
  method: "GET",
  url: "",
  params: [],
  headers: [],
  body: "",
  asserts: [{ source: "status", path: "", op: "eq", value: "200" }],
  insecureSkipVerify: false,
  followRedirects: false,
  cookieJar: false,
  traceHeader: false,
  clientCertPEM: "",
  clientKeyPEM: "",
};

const OPS = ["eq", "ne", "gt", "lt", "gte", "lte", "contains", "exists"];

// toPlan converts the form into the backend plan object.
export function httpRequestToPlan(protocol: string, r: HttpRequest): unknown {
  const headers: Record<string, string> = {};
  for (const h of r.headers) if (h.key) headers[h.key] = h.value;
  const params = r.params.filter((p) => p.key);
  const asserts = r.asserts
    .filter((a) => a.op === "exists" || a.value !== "" || a.source === "body")
    .map((a) => ({
      source: a.source,
      ...(a.source === "json" ? { path: a.path } : {}),
      op: a.op,
      ...(a.op !== "exists" ? { value: a.value } : {}),
    }));
  return {
    protocol,
    http: {
      method: r.method,
      url: r.url,
      ...(params.length ? { params } : {}),
      ...(Object.keys(headers).length ? { headers } : {}),
      ...(r.body ? { body: r.body } : {}),
      ...(asserts.length ? { asserts } : {}),
      ...(r.insecureSkipVerify ? { insecure_skip_verify: true } : {}),
      ...(r.followRedirects ? { follow_redirects: true } : {}),
      ...(r.cookieJar ? { cookie_jar: true } : {}),
      ...(r.traceHeader ? { trace_header: true } : {}),
      ...(r.clientCertPEM ? { client_cert_pem: r.clientCertPEM } : {}),
      ...(r.clientKeyPEM ? { client_key_pem: r.clientKeyPEM } : {}),
    },
  };
}

// planToHttpRequest rebuilds the form state from a stored plan (edit / copy).
// Legacy expect_status / body_contains fields become assertion rows.
export function planToHttpRequest(plan: any): HttpRequest {
  const h = plan?.http ?? {};
  const asserts: Assert[] = (h.asserts ?? []).map((a: any) => ({
    source: a.source ?? "status",
    path: a.path ?? "",
    op: a.op ?? "eq",
    value: a.value ?? "",
  }));
  if (h.expect_status) {
    asserts.push({ source: "status", path: "", op: "eq", value: String(h.expect_status) });
  }
  if (h.body_contains) {
    asserts.push({ source: "body", path: "", op: "contains", value: h.body_contains });
  }
  return {
    method: h.method || "GET",
    url: h.url || "",
    params: (h.params ?? []).map((p: any) => ({ key: p.key ?? "", value: String(p.value ?? "") })),
    headers: Object.entries(h.headers ?? {}).map(([key, value]) => ({
      key,
      value: String(value),
    })),
    body: h.body || "",
    asserts,
    insecureSkipVerify: !!h.insecure_skip_verify,
    followRedirects: !!h.follow_redirects,
    cookieJar: !!h.cookie_jar,
    traceHeader: !!h.trace_header,
    clientCertPEM: h.client_cert_pem || "",
    clientKeyPEM: h.client_key_pem || "",
  };
}

// evalAssertPreview mirrors the backend evaluation so the builder can show
// each assertion's verdict against the latest debug response.
function evalAssertPreview(a: Assert, dr: DebugResponse): { ok: boolean; got: string } | null {
  if (dr.error) return null;
  let actual: unknown;
  if (a.source === "status") actual = dr.status;
  else if (a.source === "body") actual = dr.body;
  else {
    try {
      let cur: any = JSON.parse(dr.body);
      for (const seg of a.path.split(".")) {
        if (seg === "" || cur == null) return { ok: false, got: "(missing)" };
        cur = Array.isArray(cur) ? cur[parseInt(seg, 10)] : cur[seg];
        if (cur === undefined) return { ok: a.op === "ne", got: "(missing)" };
      }
      actual = cur;
    } catch {
      return { ok: false, got: "(not json)" };
    }
  }
  const got = typeof actual === "string" ? actual : JSON.stringify(actual);
  const short = got.length > 40 ? got.slice(0, 40) + "…" : got;
  if (a.op === "exists") return { ok: actual !== undefined, got: short };
  if (a.op === "contains") return { ok: got.includes(a.value), got: short };
  if (a.op === "eq" || a.op === "ne") {
    let eq: boolean;
    if (typeof actual === "number") eq = actual === parseFloat(a.value);
    else if (typeof actual === "boolean") eq = String(actual) === a.value.trim();
    else eq = got === a.value;
    return { ok: a.op === "eq" ? eq : !eq, got: short };
  }
  const af = typeof actual === "number" ? actual : parseFloat(got);
  const wf = parseFloat(a.value);
  if (Number.isNaN(af) || Number.isNaN(wf)) return { ok: false, got: short };
  const cmp = { gt: af > wf, lt: af < wf, gte: af >= wf, lte: af <= wf }[a.op];
  return { ok: !!cmp, got: short };
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
  const [respTree, setRespTree] = useState(true);

  function setHeader(i: number, patch: Partial<{ key: string; value: string }>) {
    onChange({ ...value, headers: value.headers.map((h, idx) => (idx === i ? { ...h, ...patch } : h)) });
  }
  function setAssert(i: number, patch: Partial<Assert>) {
    onChange({ ...value, asserts: value.asserts.map((a, idx) => (idx === i ? { ...a, ...patch } : a)) });
  }

  // pickAssert turns a clicked response field into a pre-filled assertion row.
  // Scalars seed an eq check against the sampled value; objects/arrays default
  // to exists. The path matches the runtime evaluator exactly.
  function pickAssert(path: string, _leafKey: string, sample: unknown) {
    const scalar = sample === null || typeof sample !== "object";
    const row: Assert = scalar
      ? { source: "json", path, op: "eq", value: sample === null ? "" : String(sample) }
      : { source: "json", path, op: "exists", value: "" };
    onChange({ ...value, asserts: [...value.asserts, row] });
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

  return (
    <div className="builder">
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
          <label className="req">{t("http.url")}</label>
          <input
            value={value.url}
            onChange={(e) => onChange({ ...value, url: e.target.value })}
            placeholder="https://api.example.com/v1/ping"
            style={{ width: "100%" }}
            required
          />
        </div>
        <button type="button" className="secondary" onClick={runDebug} disabled={!value.url || debugging}>
          {debugging ? t("debug.sending") : <><Icon name="play" /> {t("debug.send")}</>}
        </button>
      </div>

      <label>{t("http.params")}</label>
      {value.params.map((p, i) => (
        <div className="row" key={i} style={{ marginBottom: 6 }}>
          <input
            placeholder={t("kv.key")}
            value={p.key}
            onChange={(e) => onChange({ ...value, params: value.params.map((x, idx) => (idx === i ? { ...x, key: e.target.value } : x)) })}
            style={{ width: 220 }}
          />
          <input
            placeholder={t("kv.value")}
            value={p.value}
            onChange={(e) => onChange({ ...value, params: value.params.map((x, idx) => (idx === i ? { ...x, value: e.target.value } : x)) })}
            style={{ flex: 1 }}
          />
          <button
            type="button"
            className="secondary"
            onClick={() => onChange({ ...value, params: value.params.filter((_, idx) => idx !== i) })}
          >
            {t("ramp.remove")}
          </button>
        </div>
      ))}
      <button
        type="button"
        className="secondary"
        onClick={() => onChange({ ...value, params: [...value.params, { key: "", value: "" }] })}
      >
        + {t("http.addParam")}
      </button>

      <label>{t("http.headers")}</label>
      {value.headers.map((h, i) => (
        <div className="row" key={i} style={{ marginBottom: 6 }}>
          <input
            placeholder={t("kv.key")}
            value={h.key}
            onChange={(e) => setHeader(i, { key: e.target.value })}
            style={{ width: 220 }}
          />
          <input
            placeholder={t("kv.value")}
            value={h.value}
            onChange={(e) => setHeader(i, { value: e.target.value })}
            style={{ flex: 1 }}
          />
          <button
            type="button"
            className="secondary"
            onClick={() => onChange({ ...value, headers: value.headers.filter((_, idx) => idx !== i) })}
          >
            {t("ramp.remove")}
          </button>
        </div>
      ))}
      <button
        type="button"
        className="secondary"
        onClick={() => onChange({ ...value, headers: [...value.headers, { key: "", value: "" }] })}
      >
        + {t("http.addHeader")}
      </button>

      <label>{t("http.body")}</label>
      <textarea rows={4} value={value.body} onChange={(e) => onChange({ ...value, body: e.target.value })} />

      <label>
        {t("assert.title")}
        <Help tip={t("assert.help")} />
      </label>
      {value.asserts.map((a, i) => {
        const verdict = debug && !debug.error ? evalAssertPreview(a, debug) : null;
        return (
          <div className="row" key={i} style={{ marginBottom: 6, alignItems: "center" }}>
            <select
              value={a.source}
              onChange={(e) => setAssert(i, { source: e.target.value as Assert["source"] })}
              style={{ width: 130 }}
            >
              <option value="status">{t("assert.srcStatus")}</option>
              <option value="body">{t("assert.srcBody")}</option>
              <option value="json">{t("assert.srcJson")}</option>
            </select>
            {a.source === "json" && (
              <input
                placeholder={t("assert.pathPh")}
                value={a.path}
                onChange={(e) => setAssert(i, { path: e.target.value })}
                style={{ width: 200, fontFamily: "var(--font-mono)" }}
              />
            )}
            <select value={a.op} onChange={(e) => setAssert(i, { op: e.target.value })} style={{ width: 110 }}>
              {OPS.map((op) => (
                <option key={op} value={op}>
                  {t(`assert.op.${op}`)}
                </option>
              ))}
            </select>
            {a.op !== "exists" && (
              <input
                placeholder={t("assert.valuePh")}
                value={a.value}
                onChange={(e) => setAssert(i, { value: e.target.value })}
                style={{ flex: 1, fontFamily: "var(--font-mono)" }}
              />
            )}
            {verdict && (
              <span
                style={{ color: verdict.ok ? "var(--green)" : "var(--red)", fontSize: 12, whiteSpace: "nowrap" }}
                title={verdict.got}
              >
                {verdict.ok ? "✓" : `✗ ${verdict.got}`}
              </span>
            )}
            <button
              type="button"
              className="secondary"
              onClick={() => onChange({ ...value, asserts: value.asserts.filter((_, idx) => idx !== i) })}
            >
              {t("ramp.remove")}
            </button>
          </div>
        );
      })}
      <button
        type="button"
        className="secondary"
        onClick={() =>
          onChange({
            ...value,
            asserts: [...value.asserts, { source: "json", path: "", op: "eq", value: "" }],
          })
        }
      >
        + {t("assert.add")}
      </button>

      <label style={{ display: "block", marginTop: 12, color: "var(--muted)", fontSize: 12, textTransform: "uppercase", letterSpacing: "0.08em" }}>
        {t("http.advanced")}
      </label>
      {(
        [
          ["followRedirects", "http.followRedirects"],
          ["cookieJar", "http.cookieJar"],
          ["traceHeader", "http.traceHeader"],
          ["insecureSkipVerify", "http.insecure"],
        ] as const
      ).map(([k, label]) => (
        <label key={k} style={{ display: "flex", gap: 6, alignItems: "center", marginTop: 6 }}>
          <input
            type="checkbox"
            checked={value[k]}
            onChange={(e) => onChange({ ...value, [k]: e.target.checked })}
          />
          {t(label)}
        </label>
      ))}
      <div className="row" style={{ marginTop: 8 }}>
        <div style={{ flex: 1 }}>
          <label style={{ fontSize: 12 }}>{t("http.clientCert")}</label>
          <textarea
            rows={3}
            value={value.clientCertPEM}
            onChange={(e) => onChange({ ...value, clientCertPEM: e.target.value })}
            placeholder="-----BEGIN CERTIFICATE-----"
            style={{ width: "100%", fontFamily: "var(--font-mono)", fontSize: 12 }}
          />
        </div>
        <div style={{ flex: 1 }}>
          <label style={{ fontSize: 12 }}>{t("http.clientKey")}</label>
          <textarea
            rows={3}
            value={value.clientKeyPEM}
            onChange={(e) => onChange({ ...value, clientKeyPEM: e.target.value })}
            placeholder="-----BEGIN PRIVATE KEY-----"
            style={{ width: "100%", fontFamily: "var(--font-mono)", fontSize: 12 }}
          />
        </div>
      </div>

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
              </div>
              <div className="row" style={{ alignItems: "center", justifyContent: "space-between", marginBottom: 4 }}>
                <span className="muted" style={{ fontSize: 12 }}>
                  {t("debug.respBody")}
                  {debug.body_truncated ? ` (${t("debug.truncated")})` : ""}
                </span>
                {isJson(debug.body) && (
                  <span style={{ display: "flex", gap: 4 }}>
                    <button
                      type="button"
                      className={respTree ? "" : "secondary"}
                      style={{ padding: "2px 10px", fontSize: 12 }}
                      onClick={() => setRespTree(true)}
                    >
                      {t("json.viewTree")}
                    </button>
                    <button
                      type="button"
                      className={respTree ? "secondary" : ""}
                      style={{ padding: "2px 10px", fontSize: 12 }}
                      onClick={() => setRespTree(false)}
                    >
                      {t("json.viewRaw")}
                    </button>
                  </span>
                )}
              </div>
              {respTree && isJson(debug.body) ? (
                <>
                  <div className="muted" style={{ fontSize: 11, marginBottom: 6 }}>
                    {t("json.pickHint")}
                  </div>
                  <JsonExplorer body={debug.body} mode="assert" onPick={pickAssert} />
                </>
              ) : (
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
                  {prettyBody(debug.body) || t("log.bodyEmpty")}
                </pre>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}

// isJson reports whether the body parses as JSON (gates the tree view).
function isJson(s: string): boolean {
  try {
    JSON.parse(s);
    return true;
  } catch {
    return false;
  }
}

// prettyBody re-indents JSON bodies for readability; other content untouched.
function prettyBody(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}

function formatBytes(n: number): string {
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + " MB";
  if (n >= 1 << 10) return (n / (1 << 10)).toFixed(1) + " KB";
  return n + " B";
}
