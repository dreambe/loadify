-- Persistent personal API token (CLI / AI agent). Like a Feishu app secret:
-- generated once, viewable any time in settings, invalidated only on reset.
ALTER TABLE users ADD COLUMN IF NOT EXISTS api_token text;
CREATE UNIQUE INDEX IF NOT EXISTS users_api_token_idx ON users (api_token) WHERE api_token IS NOT NULL;
