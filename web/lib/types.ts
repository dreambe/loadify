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
  baseline_run_id?: string | null;
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
  queue_position?: number; // 1-based, when status == "queued"
  queue_eta_ms?: number; // rough estimate until a slot frees
}

// Capacity is the cluster's admission headroom: whether a new run would start
// immediately or queue behind others.
export interface Capacity {
  max_runs: number;
  running: number;
  queue_depth: number;
  workers_total: number;
  workers_available: number;
  cpu_max_pct: number;
  mem_max_pct: number; // per-node memory protection threshold (0 = disabled)
  can_accept: boolean;
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
  // The load generator itself was a bottleneck: it dropped work or ran hot, so
  // results may reflect the generator's limits, not the target's.
  generator_saturated?: boolean;
  dropped_iterations?: number;
  dropped_metrics?: number;
  peak_cpu_pct?: number;
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
  mem_bytes?: number; // host memory used
  cpu_cores?: number;
  mem_total_bytes?: number; // host memory total
  net_rx_bps?: number;
  net_tx_bps?: number;
  net_rx_pps?: number;
  net_tx_pps?: number;
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

// Target-service metrics pulled from the operator's Prometheus for a run's
// window (CPU/mem/disk/net of the system-under-test), rendered natively on the
// run page next to loadify's load charts.
export interface TargetMetricPanel {
  key: string; // "cpu" | "mem" | "disk" | "net"
  unit: string;
  series: { label: string; points: { ts: number; v: number }[] }[];
}
export interface TargetMetrics {
  enabled: boolean;
  instance?: string;
  panels: TargetMetricPanel[];
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
