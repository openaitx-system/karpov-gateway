-- +goose Up
SET search_path TO auth;

-- 旧 API Keys 没有 plan_id 字段或为空，统一回填为 free
UPDATE api_keys SET plan_id = 'free' WHERE plan_id IS NULL OR plan_id = '';

-- +goose Down
-- 无需回退
