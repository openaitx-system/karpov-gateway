-- +goose Up
SET search_path TO billing;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION billing.get_plan_qps(p_plan_id TEXT)
RETURNS INT AS $$
BEGIN
    CASE p_plan_id
        WHEN 'free' THEN RETURN 5;
        WHEN 'basic' THEN RETURN 20;
        WHEN 'pro' THEN RETURN 50;
        WHEN 'enterprise' THEN RETURN 200;
        ELSE RETURN 5;
    END CASE;
END;
$$ LANGUAGE plpgsql IMMUTABLE;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION billing.get_plan_daily_limit(p_plan_id TEXT)
RETURNS BIGINT AS $$
BEGIN
    CASE p_plan_id
        WHEN 'free' THEN RETURN 100;
        WHEN 'basic' THEN RETURN 1000;
        WHEN 'pro' THEN RETURN 5000;
        WHEN 'enterprise' THEN RETURN 50000;
        ELSE RETURN 100;
    END CASE;
END;
$$ LANGUAGE plpgsql IMMUTABLE;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION billing.get_plan_monthly_limit(p_plan_id TEXT)
RETURNS BIGINT AS $$
BEGIN
    CASE p_plan_id
        WHEN 'free' THEN RETURN 1000;
        WHEN 'basic' THEN RETURN 10000;
        WHEN 'pro' THEN RETURN 100000;
        WHEN 'enterprise' THEN RETURN 1000000;
        ELSE RETURN 1000;
    END CASE;
END;
$$ LANGUAGE plpgsql IMMUTABLE;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION billing.on_order_completed()
RETURNS TRIGGER AS $$
DECLARE
    v_plan_id TEXT;
    v_user_id UUID;
BEGIN
    IF NEW.status <> 'completed' OR OLD.status = 'completed' THEN
        RETURN NEW;
    END IF;

    v_user_id := NEW.user_id;
    v_plan_id := NEW.plan_id::TEXT;

    UPDATE auth.api_keys
    SET plan_id = v_plan_id
    WHERE user_id = v_user_id
      AND revoked_at IS NULL
      AND enabled = TRUE;

    INSERT INTO quota.quota_rules (user_id, provider, daily_limit, monthly_limit, soft_limit_pct)
    VALUES
        (v_user_id, 'qqmusic', billing.get_plan_daily_limit(v_plan_id), billing.get_plan_monthly_limit(v_plan_id), 80),
        (v_user_id, 'netease', billing.get_plan_daily_limit(v_plan_id), billing.get_plan_monthly_limit(v_plan_id), 80)
    ON CONFLICT (user_id, provider)
    DO UPDATE SET
        daily_limit = EXCLUDED.daily_limit,
        monthly_limit = EXCLUDED.monthly_limit,
        updated_at = now();

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS trg_order_completed ON billing.orders;
CREATE TRIGGER trg_order_completed
    AFTER UPDATE OF status ON billing.orders
    FOR EACH ROW
    WHEN (NEW.status = 'completed' AND OLD.status <> 'completed')
    EXECUTE FUNCTION billing.on_order_completed();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION billing.sweep_expired_orders()
RETURNS INT AS $$
DECLARE
    affected INT;
BEGIN
    UPDATE billing.orders
    SET status = 'expired'
    WHERE status IN ('pending', 'paying')
      AND expires_at < now();
    GET DIAGNOSTICS affected = ROW_COUNT;
    RETURN affected;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION auth.on_apikey_plan_change()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.plan_id IS DISTINCT FROM OLD.plan_id THEN
        INSERT INTO quota.quota_rules (user_id, provider, daily_limit, monthly_limit, soft_limit_pct)
        VALUES
            (NEW.user_id, 'qqmusic', billing.get_plan_daily_limit(NEW.plan_id), billing.get_plan_monthly_limit(NEW.plan_id), 80),
            (NEW.user_id, 'netease', billing.get_plan_daily_limit(NEW.plan_id), billing.get_plan_monthly_limit(NEW.plan_id), 80)
        ON CONFLICT (user_id, provider)
        DO UPDATE SET
            daily_limit = EXCLUDED.daily_limit,
            monthly_limit = EXCLUDED.monthly_limit,
            updated_at = now();
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS trg_apikey_plan_change ON auth.api_keys;
CREATE TRIGGER trg_apikey_plan_change
    AFTER UPDATE OF plan_id ON auth.api_keys
    FOR EACH ROW
    WHEN (NEW.plan_id IS DISTINCT FROM OLD.plan_id)
    EXECUTE FUNCTION auth.on_apikey_plan_change();

-- +goose Down
DROP TRIGGER IF EXISTS trg_apikey_plan_change ON auth.api_keys;
DROP FUNCTION IF EXISTS auth.on_apikey_plan_change();
DROP TRIGGER IF EXISTS trg_order_completed ON billing.orders;
DROP FUNCTION IF EXISTS billing.on_order_completed();
DROP FUNCTION IF EXISTS billing.sweep_expired_orders();
DROP FUNCTION IF EXISTS billing.get_plan_monthly_limit(TEXT);
DROP FUNCTION IF EXISTS billing.get_plan_daily_limit(TEXT);
DROP FUNCTION IF EXISTS billing.get_plan_qps(TEXT);
