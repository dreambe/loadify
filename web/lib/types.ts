export type Role = "viewer" | "operator" | "admin";

export interface User {
  id: string;
  email: string;
  name: string;
  role: Role;
  avatar_url?: string;
  disabled?: boolean;
  created_at?: string;
  last_login_at?: string;
}

export interface TestDefinition {
  id: string;
  name: string;
  protocol: string;
  plan: unknown;
  ramp: unknown;
  script?: string;
  thresholds?: Threshold[];
  dataset?: unknown;
  tags?: string[];
  created_by?: string;
  creator_name?: string;
  created_at: string;
}

export interface Run {
  id: string;
  test_def_id: string;
  name: string;
  status: string;
  desired_workers: number;
  started_at?: string;
  ended_at?: string;
  summary?: RunSummary;
  created_by?: string;
  creator_name?: string;
  source?: string; // "manual" | "schedule"
  test_snapshot?: any;
  created_at: string;
}

export interface RunSummary {
  total_requests?: number;
  summary?: {
    error_rate?: number;
    p50_ms?: number;
    p90_ms?: number;
    p95_ms?: number;
    p99_ms?: number;
  };
  passed?: boolean;
  checks?: ThresholdCheck[];
  auto_stopped?: boolean;
  reason?: string;
  metrics_degraded?: boolean;
  metrics_error?: string;
  regressed?: boolean;
  baseline?: {
    run_id: string;
    p95_ms: number;
    p95_delta_pct: number;
    error_rate: number;
    total_requests: number;
  };
}

// TrendPoint is one run's compact metrics for a test's trend chart.
export interface TrendPoint {
  run_id: string;
  name: string;
  status: string;
  ended_at?: string;
  metrics: {
    total: number;
    error_rate: number;
    p50_ms: number;
    p90_ms: number;
    p95_ms: number;
    p99_ms: number;
  };
}

export interface Threshold {
  metric: string;
  op: string;
  value: number;
}

export interface ThresholdCheck extends Threshold {
  actual: number;
  ok: boolean;
}

export interface Schedule {
  id: string;
  test_def_id: string;
  interval_minutes: number;
  desired_workers: number;
  enabled: boolean;
  next_run_at: string;
  last_run_id?: string;
  created_by?: string;
  creator_name?: string;
}

export interface WorkerInfo {
  worker_id: string;
  region: string;
  status: string;
  active_vus: number;
  last_seen_unix_ms: number;
  cpu_pct?: number;
  mem_bytes?: number;
  cpu_cores?: number;
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
  samples?: LogSample[];
}

export interface LogSample {
  ts_unix_ms: number;
  group: string;
  method?: string;
  url?: string;
  status: number;
  ok: boolean;
  latency_ms: number;
  ttfb_ms: number;
  sent_bytes: number;
  recv_bytes: number;
  error_kind?: string;
  req_body?: string;
  resp_body?: string;
}

export interface GroupTick {
  rps: number;
  error_rate: number;
  p50_ms: number;
  p90_ms: number;
  p95_ms: number;
  p99_ms: number;
}

// Environment is a user-defined named set of variables for {{KEY}} substitution.
export interface Environment {
  id: string;
  name: string;
  vars: Record<string, string>;
  created_by?: string;
  creator_name?: string;
  created_at: string;
}

// DrillSample is one persisted sampled request for post-run error drill-down.
export interface DrillSample {
  ts: string;
  group: string;
  protocol: string;
  status_class: string;
  status: number;
  ok: boolean;
  error_kind: string;
  method: string;
  url: string;
  latency_us: number;
  recv_bytes: number;
  req_body: string;
  resp_body: string;
}

// AuditEntry is one recorded mutating action (who did what, when, outcome).
export interface AuditEntry {
  id: string;
  ts: string;
  user_id?: string;
  user_name: string;
  method: string;
  path: string;
  status: number;
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
