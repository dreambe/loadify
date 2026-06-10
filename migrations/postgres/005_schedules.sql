CREATE TABLE IF NOT EXISTS schedules (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  test_def_id uuid NOT NULL REFERENCES test_definitions(id),
  interval_minutes int NOT NULL,
  desired_workers int NOT NULL DEFAULT 0,
  enabled boolean NOT NULL DEFAULT true,
  next_run_at timestamptz NOT NULL DEFAULT now(),
  last_run_id uuid,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_schedules_due ON schedules (enabled, next_run_at)
