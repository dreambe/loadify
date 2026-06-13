CREATE TABLE IF NOT EXISTS audit_log (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  ts timestamptz NOT NULL DEFAULT now(),
  user_id uuid,
  user_name text NOT NULL DEFAULT '',
  method text NOT NULL,
  path text NOT NULL,
  status int NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log (ts DESC)
