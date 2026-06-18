-- Lightweight grouping for tests: free-form tags, filterable in the UI. A
-- pragmatic precursor to full project/workspace isolation (tracked in the
-- roadmap), delivering most of the "organize many tests" value at low risk.
ALTER TABLE test_definitions ADD COLUMN IF NOT EXISTS tags text[] NOT NULL DEFAULT '{}'
