-- +goose Up
SET search_path TO auth;

-- API Key 限速与启用/禁用支持。
-- rate_limit_rpm: 每分钟请求数上限（0 = 不限）
-- rate_limit_daily: 每日请求数上限（0 = 不限）
-- enabled: 可逆的启用/禁用开关（与 revoked_at 永久吊销区分）
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS rate_limit_rpm   INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS rate_limit_daily  BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS description      TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS total_requests   BIGINT NOT NULL DEFAULT 0;

COMMENT ON COLUMN api_keys.rate_limit_rpm IS '每分钟请求数上限，0 表示不限速';
COMMENT ON COLUMN api_keys.rate_limit_daily IS '每日请求数上限，0 表示不限';
COMMENT ON COLUMN api_keys.enabled IS '可逆禁用开关；false 时请求被拒但不影响 revoked_at';
COMMENT ON COLUMN api_keys.description IS '用户备注/描述';
COMMENT ON COLUMN api_keys.total_requests IS '累计请求总数（异步更新，非精确）';

-- +goose Down
SET search_path TO auth;

ALTER TABLE api_keys
    DROP COLUMN IF EXISTS rate_limit_rpm,
    DROP COLUMN IF EXISTS rate_limit_daily,
    DROP COLUMN IF EXISTS enabled,
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS total_requests;
