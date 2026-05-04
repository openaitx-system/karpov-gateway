-- +goose Up
SET search_path TO auth;

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS plan_id TEXT NOT NULL DEFAULT 'free';

COMMENT ON COLUMN api_keys.plan_id IS 'API Key 绑定的套餐 ID（free/basic/pro/enterprise）';

-- +goose Down
SET search_path TO auth;

ALTER TABLE api_keys DROP COLUMN IF EXISTS plan_id;
