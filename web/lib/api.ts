"use client";

import { clearSession, getToken } from "./auth";
import type { AuditEntry, Capacity, DrillSample, Environment, Run, Schedule, SeriesPoint, TargetMetrics, TestDefinition, TrendPoint, User, WorkerInfo } from "./types";

// Default to SAME-ORIGIN (relative /api): the app is meant to run behind a
// reverse proxy that serves the web UI and routes /api to apisrv, so it works on
// any domain with no rebuild and never accidentally calls the *viewer's*
// localhost. Set NEXT_PUBLIC_API_BASE to an absolute URL only when the API is on
// a different origin (local dev / bare docker-compose without a proxy).
export const API_BASE = process.env.NEXT_PUBLIC_API_BASE || "";

class APIError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

// shareToken, when set (a public report/run share link), authorizes read
// requests via ?share= and suppresses the login redirect, so a shared link can
// drive the real run page with no session.
let shareToken = "";
export function setShareToken(t: string) {
  shareToken = t;
}

async function req<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  const token = getToken();
  let url = `${API_BASE}${path}`;
  if (token) {
    headers.set("Authorization", `Bearer ${token}`);
  } else if (shareToken) {
    url += (path.includes("?") ? "&" : "?") + "share=" + encodeURIComponent(shareToken);
  }
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const res = await fetch(url, { ...init, headers });
  if (res.status === 401) {
    // In share mode there's no session to clear and nowhere to log in.
    if (!shareToken) {
      clearSession();
      if (typeof window !== "undefined") window.location.href = "/login";
    }
    throw new APIError(401, "unauthorized");
  }
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const j = await res.json();
      msg = j.error || msg;
    } catch {
      /* ignore */
    }
    throw new APIError(res.status, msg);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

// reqList guards against Go marshaling empty (nil) slices as JSON null: list
// endpoints always resolve to an array so callers can map/length safely.
async function reqList<T>(path: string, init: RequestInit = {}): Promise<T[]> {
  return (await req<T[] | null>(path, init)) ?? [];
}

export interface LoginResponse {
  token: string;
  user: User;
}

// DebugResponse is one ad-hoc request fired from the test builder.
export interface DebugResponse {
  status: number;
  status_text: string;
  latency_ms: number;
  headers: Record<string, string>;
  body: string;
  body_truncated: boolean;
  recv_bytes: number;
  error?: string;
}

// DebugScenarioStep is one step's resolved request/response from a chained
// scenario debug run.
export interface DebugScenarioStep {
  group: string;
  method: string;
  url: string; // resolved after {{var}} interpolation + query params
  req_body?: string; // resolved request body (what was actually sent)
  status: number;
  ok: boolean;
  error_kind?: string;
  latency_ms: number;
  body: string;
}

export interface DebugScenarioResponse {
  steps: DebugScenarioStep[];
  error?: string;
}

export const api = {
  login: (email: string, password: string) =>
    req<LoginResponse>("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ email, password }),
    }),
  me: () => req<User>("/api/v1/auth/me"),
  authConfig: () => req<{ feishu_enabled: boolean }>("/api/v1/auth/config"),
  feishuLoginURL: () => `${API_BASE}/api/v1/auth/feishu/login`,

  listTests: () => reqList<TestDefinition>("/api/v1/tests"),
  getTest: (id: string) => req<TestDefinition>(`/api/v1/tests/${id}`),
  createTest: (body: {
    name: string;
    protocol: string;
    plan: unknown;
    ramp: unknown;
    script?: string;
    thresholds?: unknown;
    dataset?: unknown;
    tags?: string[];
  }) => req<{ id: string }>("/api/v1/tests", { method: "POST", body: JSON.stringify(body) }),
  updateTest: (
    id: string,
    body: {
      name: string;
      protocol: string;
      plan: unknown;
      ramp: unknown;
      script?: string;
      thresholds?: unknown;
      dataset?: unknown;
      tags?: string[];
    }
  ) => req<void>(`/api/v1/tests/${id}`, { method: "PUT", body: JSON.stringify(body) }),
  deleteTest: (id: string) => req<void>(`/api/v1/tests/${id}`, { method: "DELETE" }),
  debugRequest: (body: {
    method: string;
    url: string;
    headers?: Record<string, string>;
    body?: string;
    insecure_skip_verify?: boolean;
  }) => req<DebugResponse>("/api/v1/tests/debug", { method: "POST", body: JSON.stringify(body) }),
  debugScenario: (steps: unknown[]) =>
    req<DebugScenarioResponse>("/api/v1/tests/debug-scenario", {
      method: "POST",
      body: JSON.stringify({ steps }),
    }),
  testTrend: (id: string, n = 20) => reqList<TrendPoint>(`/api/v1/tests/${id}/trend?n=${n}`),
  setBaseline: (testId: string, runId: string) =>
    req<void>(`/api/v1/tests/${testId}/baseline`, { method: "POST", body: JSON.stringify({ run_id: runId }) }),
  clearBaseline: (testId: string) =>
    req<void>(`/api/v1/tests/${testId}/baseline`, { method: "POST", body: JSON.stringify({ run_id: "" }) }),
  importTest: (format: string, content: string) =>
    req<{ name: string; protocol: string; plan: unknown }>("/api/v1/tests/import", {
      method: "POST",
      body: JSON.stringify({ format, content }),
    }),

  listRuns: (limit?: number) => reqList<Run>(`/api/v1/runs${limit ? `?limit=${limit}` : ""}`),
  // Whole-table run aggregates for the dashboard KPIs (correct beyond the
  // capped runs list). pass_rate is null when no finished run has a verdict.
  runStats: () =>
    req<{ total: number; running: number; last24h: number; scored: number; passed: number; pass_rate: number | null }>(
      "/api/v1/runs/stats"
    ),
  getRun: (id: string) => req<Run>(`/api/v1/runs/${id}`),
  startRun: (testId: string, desiredWorkers: number, name = "", environmentId = "") =>
    req<{ run_id: string; status: string }>("/api/v1/runs", {
      method: "POST",
      body: JSON.stringify({
        test_id: testId,
        desired_workers: desiredWorkers,
        name,
        environment_id: environmentId || undefined,
      }),
    }),
  stopRun: (id: string) =>
    req<{ run_id: string; status: string }>(`/api/v1/runs/${id}/stop`, { method: "POST" }),
  deleteRun: (id: string) => req<void>(`/api/v1/runs/${id}`, { method: "DELETE" }),
  shareRun: (id: string) =>
    req<{ token: string; expires_at: string }>(`/api/v1/runs/${id}/share`, { method: "POST" }),
  // Persistent CLI/agent token (Feishu-style): getApiToken returns the current
  // one (minted lazily on first read); resetApiToken rotates it.
  getApiToken: () => req<{ exists: boolean }>(`/api/v1/auth/token`),
  resetApiToken: () => req<{ token: string }>(`/api/v1/auth/token`, { method: "POST" }),
  runSeries: (id: string, group = "*", res = 1) =>
    reqList<SeriesPoint>(`/api/v1/runs/${id}/series?group=${encodeURIComponent(group)}&res=${res}`),
  targetMetrics: (id: string) => req<TargetMetrics>(`/api/v1/runs/${id}/target-metrics`),
  // Discover target services (label values, default job) + available labels from
  // the operator's Prometheus, to populate the test editor's dropdowns.
  promServices: (label = "job") =>
    req<{ enabled: boolean; label: string; labels: string[]; values: string[] }>(
      `/api/v1/prometheus/services?label=${encodeURIComponent(label)}`
    ),
  runSamples: (
    id: string,
    filter: { status_class?: string; error_kind?: string; group?: string; limit?: number; from_ms?: number; to_ms?: number } = {}
  ) => {
    const q = new URLSearchParams();
    if (filter.status_class) q.set("status_class", filter.status_class);
    if (filter.error_kind) q.set("error_kind", filter.error_kind);
    if (filter.group) q.set("group", filter.group);
    if (filter.limit) q.set("limit", String(filter.limit));
    if (filter.from_ms) q.set("from_ms", String(Math.round(filter.from_ms)));
    if (filter.to_ms) q.set("to_ms", String(Math.round(filter.to_ms)));
    return req<{ sampled: boolean; samples: DrillSample[] }>(`/api/v1/runs/${id}/samples?${q.toString()}`);
  },

  listEnvironments: () => reqList<Environment>("/api/v1/environments"),
  createEnvironment: (name: string, vars: Record<string, string>) =>
    req<{ id: string }>("/api/v1/environments", { method: "POST", body: JSON.stringify({ name, vars }) }),
  updateEnvironment: (id: string, name: string, vars: Record<string, string>) =>
    req<void>(`/api/v1/environments/${id}`, { method: "PUT", body: JSON.stringify({ name, vars }) }),
  deleteEnvironment: (id: string) => req<void>(`/api/v1/environments/${id}`, { method: "DELETE" }),

  listWorkers: () => reqList<WorkerInfo>("/api/v1/workers"),
  getCapacity: () => req<Capacity>("/api/v1/capacity"),

  listSchedules: () => reqList<Schedule>("/api/v1/schedules"),
  createSchedule: (testId: string, intervalMinutes: number, desiredWorkers: number) =>
    req<{ id: string }>("/api/v1/schedules", {
      method: "POST",
      body: JSON.stringify({ test_id: testId, interval_minutes: intervalMinutes, desired_workers: desiredWorkers }),
    }),
  setScheduleEnabled: (id: string, enabled: boolean) =>
    req<{ enabled: boolean }>(`/api/v1/schedules/${id}/enabled?enabled=${enabled}`, { method: "POST" }),
  updateSchedule: (id: string, intervalMinutes: number, desiredWorkers: number) =>
    req<void>(`/api/v1/schedules/${id}`, {
      method: "PUT",
      body: JSON.stringify({ interval_minutes: intervalMinutes, desired_workers: desiredWorkers }),
    }),
  deleteSchedule: (id: string) => req<void>(`/api/v1/schedules/${id}`, { method: "DELETE" }),

  listAudit: (limit = 200) => reqList<AuditEntry>(`/api/v1/audit?limit=${limit}`),

  listUsers: () => reqList<User>("/api/v1/users"),
  createUser: (body: { email: string; name: string; role: string; password: string }) =>
    req<User>("/api/v1/users", { method: "POST", body: JSON.stringify(body) }),
  updateUser: (id: string, body: { role?: string; password?: string; disabled?: boolean }) =>
    req<User>(`/api/v1/users/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  deleteUser: (id: string) => req<void>(`/api/v1/users/${id}`, { method: "DELETE" }),
  changePassword: (oldPassword: string, newPassword: string) =>
    req<void>("/api/v1/auth/password", {
      method: "POST",
      body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
    }),
  getWebhooks: () => req<{ webhook_urls: string[] }>("/api/v1/auth/webhooks"),
  setWebhooks: (urls: string[]) =>
    req<{ webhook_urls: string[] }>("/api/v1/auth/webhooks", {
      method: "PUT",
      body: JSON.stringify({ webhook_urls: urls }),
    }),
};

// reportURL builds the HTML report link (token via query param for a plain
// link / new tab); falls back to the share token so the report opens from a
// public share link too.
export function reportURL(runId: string): string {
  return `${API_BASE}/api/v1/runs/${runId}/report.html?${authParam()}`;
}

// shareRunURL builds the public (no-login) link to the real, interactive run
// page, carrying the share token so its read requests are authorized.
export function shareRunURL(runId: string, shareToken: string): string {
  const origin = typeof window !== "undefined" ? window.location.origin : "";
  return `${origin}/runs/${runId}?share=${encodeURIComponent(shareToken)}`;
}

// authParam returns the query auth for plain links / WS handshakes that can't
// set headers: the session token when signed in, else the run share token (so a
// shared link's CSV download and live stream are authorized just like its reads).
function authParam(): string {
  const token = getToken();
  if (token) return `token=${encodeURIComponent(token)}`;
  if (shareToken) return `share=${encodeURIComponent(shareToken)}`;
  return "token=";
}

// liveSocketURL builds the WebSocket URL for a run's live stream, carrying the
// JWT (or share token) as a query param (browsers cannot set headers on the WS
// handshake).
export function liveSocketURL(runId: string): string {
  // A WebSocket URL must be absolute. With an absolute API_BASE, swap http→ws;
  // with same-origin (empty), derive ws(s) + host from the page location.
  let base = API_BASE;
  if (base) {
    base = base.replace(/^http/, "ws");
  } else if (typeof window !== "undefined") {
    base = `${window.location.protocol === "https:" ? "wss:" : "ws:"}//${window.location.host}`;
  }
  return `${base}/api/v1/runs/${runId}/live?${authParam()}`;
}

// exportCSVURL builds the CSV download link for a run (token via query param,
// since a plain <a download> can't set headers). Falls back to the share token
// so CSV export works from a public share link too.
export function exportCSVURL(runId: string): string {
  return `${API_BASE}/api/v1/runs/${runId}/export.csv?${authParam()}`;
}
