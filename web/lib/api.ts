"use client";

import { clearSession, getToken } from "./auth";
import type { Run, Schedule, SeriesPoint, TestDefinition, User, WorkerInfo } from "./types";

export const API_BASE =
  process.env.NEXT_PUBLIC_API_BASE || "http://localhost:8080";

class APIError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function req<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  const token = getToken();
  if (token) headers.set("Authorization", `Bearer ${token}`);
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const res = await fetch(`${API_BASE}${path}`, { ...init, headers });
  if (res.status === 401) {
    clearSession();
    if (typeof window !== "undefined") window.location.href = "/login";
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

export interface LoginResponse {
  token: string;
  user: User;
}

export const api = {
  login: (email: string, password: string) =>
    req<LoginResponse>("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ email, password }),
    }),
  me: () => req<User>("/api/v1/auth/me"),
  feishuLoginURL: () => `${API_BASE}/api/v1/auth/feishu/login`,

  listTests: () => req<TestDefinition[]>("/api/v1/tests"),
  getTest: (id: string) => req<TestDefinition>(`/api/v1/tests/${id}`),
  createTest: (body: {
    name: string;
    protocol: string;
    plan: unknown;
    ramp: unknown;
    script?: string;
    thresholds?: unknown;
    dataset?: unknown;
  }) => req<{ id: string }>("/api/v1/tests", { method: "POST", body: JSON.stringify(body) }),

  listRuns: () => req<Run[]>("/api/v1/runs"),
  getRun: (id: string) => req<Run>(`/api/v1/runs/${id}`),
  startRun: (testId: string, desiredWorkers: number) =>
    req<{ run_id: string; status: string }>("/api/v1/runs", {
      method: "POST",
      body: JSON.stringify({ test_id: testId, desired_workers: desiredWorkers }),
    }),
  stopRun: (id: string) =>
    req<{ run_id: string; status: string }>(`/api/v1/runs/${id}/stop`, { method: "POST" }),
  runSeries: (id: string, group = "*", res = 1) =>
    req<SeriesPoint[]>(`/api/v1/runs/${id}/series?group=${encodeURIComponent(group)}&res=${res}`),

  listWorkers: () => req<WorkerInfo[]>("/api/v1/workers"),

  listSchedules: () => req<Schedule[]>("/api/v1/schedules"),
  createSchedule: (testId: string, intervalMinutes: number, desiredWorkers: number) =>
    req<{ id: string }>("/api/v1/schedules", {
      method: "POST",
      body: JSON.stringify({ test_id: testId, interval_minutes: intervalMinutes, desired_workers: desiredWorkers }),
    }),
  setScheduleEnabled: (id: string, enabled: boolean) =>
    req<{ enabled: boolean }>(`/api/v1/schedules/${id}/enabled?enabled=${enabled}`, { method: "POST" }),

  listUsers: () => req<User[]>("/api/v1/users"),
  createUser: (body: { email: string; name: string; role: string; password: string }) =>
    req<User>("/api/v1/users", { method: "POST", body: JSON.stringify(body) }),
};

// liveSocketURL builds the WebSocket URL for a run's live stream, carrying the
// JWT as a query param (browsers cannot set headers on the WS handshake).
export function liveSocketURL(runId: string): string {
  const base = API_BASE.replace(/^http/, "ws");
  const token = getToken() || "";
  return `${base}/api/v1/runs/${runId}/live?token=${encodeURIComponent(token)}`;
}

// exportCSVURL builds the CSV download link for a run (token via query param,
// since a plain <a download> can't set headers).
export function exportCSVURL(runId: string): string {
  const token = getToken() || "";
  return `${API_BASE}/api/v1/runs/${runId}/export.csv?token=${encodeURIComponent(token)}`;
}
