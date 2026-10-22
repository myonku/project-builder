ALTER TABLE users ADD COLUMN session_version INTEGER NOT NULL DEFAULT 0 CHECK (session_version >= 0);
CREATE TABLE password_reset_states (
 id UUID PRIMARY KEY, user_id UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
 last_self_service_reset_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE password_reset_tokens (
 id UUID PRIMARY KEY, user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 token_hash VARCHAR(64) NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
 requested_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL,
 consumed_at TIMESTAMPTZ, sent_at TIMESTAMPTZ, failed_at TIMESTAMPTZ, last_error VARCHAR(500),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK (expires_at > requested_at)
);
CREATE INDEX ix_password_reset_tokens_user_requested ON password_reset_tokens(user_id, requested_at DESC);
CREATE UNIQUE INDEX uq_password_reset_tokens_active_user ON password_reset_tokens(user_id) WHERE consumed_at IS NULL;
