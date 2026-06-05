export type Role = "viewer" | "operator" | "admin";

export interface User {
  id: string;
  email: string;
  name: string;
  role: Role;
}

export interface TestDefinition {
  id: string;
  name: string;
  protocol: string;
  plan: unknown;
  ramp: unknown;
  script?: string;
  created_at: string;
}

export interface Run {
  id: string;
  test_def_id: string;
  status: string;
  desired_workers: number;
  started_at?: string;
  ended_at?: string;
  summary?: unknown;
  created_at: string;
}

export interface WorkerInfo {
  worker_id: string;
  region: string;
  status: string;
  active_vus: number;
  last_seen_unix_ms: number;
}

export interface LiveTick {
  run_id: string;
  ts_unix_ms: number;
  rps: number;
  error_rate: number;
  active_vus: number;
  p50_ms: number;
  p90_ms: number;
  p95_ms: number;
  p99_ms: number;
  groups?: Record<string, GroupTick>;
}

export interface GroupTick {
  rps: number;
  error_rate: number;
  p50_ms: number;
  p90_ms: number;
  p95_ms: number;
  p99_ms: number;
}

export interface SeriesPoint {
  ts: string;
  rps: number;
  error_rate: number;
  p50_ms: number;
  p90_ms: number;
  p95_ms: number;
  p99_ms: number;
}
