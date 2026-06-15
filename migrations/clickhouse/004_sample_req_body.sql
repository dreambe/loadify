-- Add the request-body snippet to persisted samples so run-result drill-down
-- can show exactly what was sent, not just the response. Idempotent for
-- deployments whose samples table predates the column.
ALTER TABLE samples ADD COLUMN IF NOT EXISTS req_body String AFTER resp_body
