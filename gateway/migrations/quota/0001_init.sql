-- +goose Up
SET search_path TO quota;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS plans (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code              TEXT NOT NULL UNIQUE,
    name              TEXT NOT NULL,
    price_cents       BIGINT NOT NULL DEFAULT 0,
    currency          TEXT NOT NULL DEFAULT 'CNY',
    period            TEXT NOT NULL DEFAULT 'monthly'
                       CHECK (period IN ('monthly','yearly')),
    provider_quotas   JSONB NOT NULL DEFAULT '{}'::jsonb,
    endpoint_weights  JSONB NOT NULL DEFAULT '{}'::jsonb,
    soft_limit_pct    INT NOT NULL DEFAULT 80
                       CHECK (soft_limit_pct BETWEEN 0 AND 100),
    qps               INT NOT NULL DEFAULT 5,
    is_active         BOOLEAN NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS subscriptions (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                UUID NOT NULL,
    plan_id                UUID NOT NULL REFERENCES plans(id),
    status                 TEXT NOT NULL DEFAULT 'active'
                            CHECK (status IN ('active','past_due','canceled','expired')),
    current_period_start   TIMESTAMPTZ NOT NULL,
    current_period_end     TIMESTAMPTZ NOT NULL,
    cancel_at_period_end   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS subs_user_active_idx ON subscriptions (user_id, status);

CREATE TABLE IF NOT EXISTS quota_rules (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    provider        TEXT NOT NULL,
    monthly_limit   BIGINT,
    daily_limit     BIGINT,
    soft_limit_pct  INT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, provider)
);

CREATE TABLE IF NOT EXISTS usage_events (
    id          UUID NOT NULL DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    provider    TEXT NOT NULL,
    endpoint    TEXT NOT NULL,
    weight      INT NOT NULL DEFAULT 1,
    status      TEXT NOT NULL,
    ts          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, ts)
) PARTITION BY RANGE (ts);

-- +goose StatementBegin
DO $$
DECLARE
    m DATE := date_trunc('month', now())::DATE;
    i INT;
    name TEXT;
    start_ts DATE;
    end_ts DATE;
BEGIN
    FOR i IN 0..8 LOOP
        start_ts := (m + (i || ' month')::INTERVAL)::DATE;
        end_ts := (m + ((i+1) || ' month')::INTERVAL)::DATE;
        name := format('usage_events_%s', to_char(start_ts, 'YYYY_MM'));
        EXECUTE format(
          'CREATE TABLE IF NOT EXISTS quota.%I PARTITION OF quota.usage_events FOR VALUES FROM (%L) TO (%L)',
          name, start_ts, end_ts
        );
    END LOOP;
END$$;
-- +goose StatementEnd

CREATE INDEX IF NOT EXISTS usage_events_user_time_idx ON usage_events (user_id, ts DESC);

CREATE TABLE IF NOT EXISTS usage_aggregates_daily (
    user_id     UUID NOT NULL,
    provider    TEXT NOT NULL,
    date        DATE NOT NULL,
    count       BIGINT NOT NULL DEFAULT 0,
    weight_sum  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, provider, date)
);

CREATE TABLE IF NOT EXISTS usage_aggregates_monthly (
    user_id      UUID NOT NULL,
    provider     TEXT NOT NULL,
    year_month   CHAR(7) NOT NULL,
    count        BIGINT NOT NULL DEFAULT 0,
    weight_sum   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, provider, year_month)
);

-- +goose Down
SET search_path TO quota;

DROP TABLE IF EXISTS usage_aggregates_monthly;
DROP TABLE IF EXISTS usage_aggregates_daily;
DROP TABLE IF EXISTS usage_events CASCADE;
DROP TABLE IF EXISTS quota_rules;
DROP TABLE IF EXISTS subscriptions;
DROP TABLE IF EXISTS plans;
