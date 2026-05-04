-- +goose Up
SET search_path TO auth;

CREATE TABLE IF NOT EXISTS oauth_identities (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider            TEXT NOT NULL,
    provider_sub        TEXT NOT NULL,
    provider_login      TEXT,
    provider_email      CITEXT,
    provider_name       TEXT,
    provider_avatar     TEXT,
    trust_level         INT,
    scopes              TEXT[] NOT NULL DEFAULT '{}',
    access_token_enc    BYTEA,
    refresh_token_enc   BYTEA,
    expires_at          TIMESTAMPTZ,
    raw_profile         JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at       TIMESTAMPTZ,
    CONSTRAINT oauth_identities_provider_sub_uniq UNIQUE (provider, provider_sub),
    CONSTRAINT oauth_identities_user_provider_uniq UNIQUE (user_id, provider)
);
CREATE INDEX IF NOT EXISTS oauth_identities_user_idx ON oauth_identities (user_id);
CREATE INDEX IF NOT EXISTS oauth_identities_email_idx ON oauth_identities (provider_email)
    WHERE provider_email IS NOT NULL;

-- +goose Down
SET search_path TO auth;
DROP TABLE IF EXISTS oauth_identities;
