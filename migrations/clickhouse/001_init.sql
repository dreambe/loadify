CREATE TABLE IF NOT EXISTS rollup_1s (
  run_id String,
  ts DateTime,
  `group` LowCardinality(String),
  protocol LowCardinality(String),
  status_class LowCardinality(String),
  count UInt64,
  errors UInt64,
  sent_bytes UInt64,
  recv_bytes UInt64,
  p50_ms Float64,
  p90_ms Float64,
  p95_ms Float64,
  p99_ms Float64,
  max_ms Float64,
  hist String
) ENGINE = MergeTree
ORDER BY (run_id, `group`, status_class, ts)
