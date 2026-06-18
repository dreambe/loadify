ALTER TABLE test_definitions ADD COLUMN IF NOT EXISTS baseline_run_id uuid REFERENCES runs(id) ON DELETE SET NULL
