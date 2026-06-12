ALTER TABLE users ADD COLUMN IF NOT EXISTS webhook_urls jsonb NOT NULL DEFAULT '[]';

ALTER TABLE test_definitions ADD COLUMN IF NOT EXISTS webhook_url text NOT NULL DEFAULT ''
