"use client";

import { useI18n } from "@/lib/i18n";

// HttpRequest is the structured form of an HTTP/HTTPS plan, kept in component
// state and serialized into the plan JSON the API expects.
export interface HttpRequest {
  method: string;
  url: string;
  headers: { key: string; value: string }[];
  body: string;
  expectStatus: number;
}

export const emptyHttpRequest: HttpRequest = {
  method: "GET",
  url: "",
  headers: [],
  body: "",
  expectStatus: 0,
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
    },
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

  function setHeader(i: number, patch: Partial<{ key: string; value: string }>) {
    onChange({ ...value, headers: value.headers.map((h, idx) => (idx === i ? { ...h, ...patch } : h)) });
  }
  function addHeader() {
    onChange({ ...value, headers: [...value.headers, { key: "", value: "" }] });
  }
  function removeHeader(i: number) {
    onChange({ ...value, headers: value.headers.filter((_, idx) => idx !== i) });
  }

  return (
    <div style={{ border: "1px solid var(--border)", borderRadius: 6, padding: 12 }}>
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
        <div>
          <label>{t("http.expectStatus")}</label>
          <input
            type="number"
            min={0}
            value={value.expectStatus}
            onChange={(e) => onChange({ ...value, expectStatus: parseInt(e.target.value || "0", 10) })}
            style={{ width: 90 }}
          />
        </div>
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
    </div>
  );
}
