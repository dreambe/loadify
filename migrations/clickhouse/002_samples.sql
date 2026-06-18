CREATE TABLE IF NOT EXISTS samples (
  run_id String,
  ts DateTime64(3),
  `group` LowCardinality(String),
  protocol LowCardinality(String),
  status_class LowCardinality(String),
  status Int32,
  ok UInt8,
  error_kind LowCardinality(String),
  method LowCardinality(String),
  url String,
  latency_us Int64,
  recv_bytes Int64,
  resp_body String,
  req_body String
) ENGINE = MergeTree
ORDER BY (run_id, ts)
TTL toDateTime(ts) + INTERVAL 7 DAY
