-- +goose Up
SET search_path TO pool;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS credentials (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider        TEXT NOT NULL,
    label           TEXT,
    payload_enc     BYTEA NOT NULL,
    capabilities    TEXT[] NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'active'
                     CHECK (status IN ('active','disabled','banned','refreshing')),
    health_score    NUMERIC(5,4) NOT NULL DEFAULT 1.0,
    last_used_at    TIMESTAMPTZ,
    last_failed_at  TIMESTAMPTZ,
    fail_count      INT NOT NULL DEFAULT 0,
    cooldown_until  TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS credentials_provider_status_idx
    ON credentials (provider, status, cooldown_until)
    WHERE status = 'active';

CREATE TABLE IF NOT EXISTS credential_health_records (
    id             BIGSERIAL PRIMARY KEY,
    credential_id  UUID NOT NULL REFERENCES credentials(id) ON DELETE CASCADE,
    ts             TIMESTAMPTZ NOT NULL DEFAULT now(),
    latency_ms     INT,
    ok             BOOLEAN NOT NULL,
    error          TEXT
);
CREATE INDEX IF NOT EXISTS chr_cred_time_idx ON credential_health_records (credential_id, ts DESC);

CREATE TABLE IF NOT EXISTS provider_endpoint_weights (
    provider     TEXT NOT NULL,
    endpoint     TEXT NOT NULL,
    weight       INT NOT NULL DEFAULT 1,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, endpoint)
);

INSERT INTO provider_endpoint_weights (provider, endpoint, weight) VALUES
    ('qqmusic','GetSong',1),
    ('qqmusic','SearchSongs',1),
    ('qqmusic','GetSongURL',3),
    ('qqmusic','GetLyric',1),
    ('qqmusic','GetAlbum',1),
    ('qqmusic','GetArtist',1),
    ('qqmusic','GetPlaylist',1)
ON CONFLICT (provider, endpoint) DO NOTHING;

-- +goose Down
SET search_path TO pool;

DROP TABLE IF EXISTS provider_endpoint_weights;
DROP TABLE IF EXISTS credential_health_records;
DROP TABLE IF EXISTS credentials;
