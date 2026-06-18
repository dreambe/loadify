CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS test_definitions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  protocol text NOT NULL,
  plan_json jsonb NOT NULL,
  ramp_json jsonb,
  script_js text,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS runs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  test_def_id uuid NOT NULL REFERENCES test_definitions(id),
  status text NOT NULL DEFAULT 'pending',
  desired_workers int NOT NULL DEFAULT 0,
  started_at timestamptz,
  ended_at timestamptz,
  summary jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_runs_created ON runs (created_at DESC)
