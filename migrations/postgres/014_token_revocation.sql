-- creds_changed_at lets us revoke already-issued JWTs: any token whose iat
-- predates this timestamp is rejected. Bumped on disable, password reset and
-- role change so those take effect immediately instead of at token expiry.
ALTER TABLE users ADD COLUMN IF NOT EXISTS creds_changed_at timestamptz NOT NULL DEFAULT now()
