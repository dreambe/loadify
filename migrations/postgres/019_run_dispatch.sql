-- Durable admission queue: the exact StartRun payload (marshaled proto) for a
-- pending/queued/running run, so a restarted coordinator's lost in-memory queue
-- can be replayed from Postgres (the source of truth). Cleared when the run
-- reaches a terminal state.
ALTER TABLE runs ADD COLUMN IF NOT EXISTS dispatch_payload bytea;
