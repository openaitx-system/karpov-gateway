-- +goose Up
SET search_path TO auth;

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE IF NOT EXISTS users (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email              CITEXT,
    phone              TEXT,
    password_hash      TEXT NOT NULL,
    status             TEXT NOT NULL DEFAULT 'pending_email'
                          CHECK (status IN ('pending_email','active','locked','disabled')),
    role               TEXT NOT NULL DEFAULT 'user'
                          CHECK (role IN ('user','admin','superadmin')),
    totp_secret_enc    BYTEA,
    totp_enabled       BOOLEAN NOT NULL DEFAULT FALSE,
    failed_login_count INT NOT NULL DEFAULT 0,
    locked_until       TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at      TIMESTAMPTZ,
    last_login_ip      INET
);
CREATE UNIQUE INDEX IF NOT EXISTS users_email_uniq ON users (email) WHERE email IS NOT NULL;
CREATE INDEX IF NOT EXISTS users_role_idx ON users (role) WHERE role <> 'user';

CREATE TABLE IF NOT EXISTS sessions (
    sid         BYTEA PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ip          INET,
    user_agent  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions (user_id, expires_at DESC);

CREATE TABLE IF NOT EXISTS api_keys (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key_hash      TEXT NOT NULL,
    prefix        CHAR(8) NOT NULL,
    name          TEXT,
    scopes        TEXT[] NOT NULL DEFAULT '{}',
    ip_allowlist  CIDR[] NOT NULL DEFAULT '{}',
    last_used_at  TIMESTAMPTZ,
    expires_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS api_keys_user_idx ON api_keys (user_id, revoked_at);
CREATE INDEX IF NOT EXISTS api_keys_prefix_idx ON api_keys (prefix) WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS email_verifications (
    token       TEXT PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS password_resets (
    token         TEXT PRIMARY KEY,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at    TIMESTAMPTZ NOT NULL,
    used_at       TIMESTAMPTZ,
    requested_ip  INET
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    user_id     UUID,
    action      TEXT NOT NULL,
    ip          INET,
    user_agent  TEXT,
    metadata    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS audit_logs_user_time_idx ON audit_logs (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS audit_logs_action_time_idx ON audit_logs (action, created_at DESC);

-- +goose Down
SET search_path TO auth;

DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS password_resets;
DROP TABLE IF EXISTS email_verifications;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
