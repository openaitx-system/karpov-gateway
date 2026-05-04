-- +goose Up
SET search_path TO billing;

ALTER TABLE billing.orders DROP CONSTRAINT IF EXISTS orders_status_check;
ALTER TABLE billing.orders ADD CONSTRAINT orders_status_check
    CHECK (status IN ('pending','paying','paid','completed','failed','expired','canceled','refunded'));

ALTER TABLE billing.refunds
    ADD COLUMN IF NOT EXISTS refund_type TEXT NOT NULL DEFAULT 'full'
        CHECK (refund_type IN ('full', 'partial')),
    ADD COLUMN IF NOT EXISTS operator_id UUID,
    ADD COLUMN IF NOT EXISTS operator_note TEXT NOT NULL DEFAULT '';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION billing.on_refund_completed()
RETURNS TRIGGER AS $$
DECLARE
    v_order RECORD;
    v_active_paid_count INT;
BEGIN
    IF NEW.status <> 'completed' OR OLD.status = 'completed' THEN
        RETURN NEW;
    END IF;

    SELECT * INTO v_order FROM billing.orders WHERE id = NEW.order_id;
    IF NOT FOUND THEN
        RETURN NEW;
    END IF;

    UPDATE billing.orders SET status = 'refunded' WHERE id = NEW.order_id;

    IF NEW.refund_type = 'full' THEN
        SELECT COUNT(*) INTO v_active_paid_count
        FROM billing.orders
        WHERE user_id = v_order.user_id
          AND status = 'completed'
          AND id <> NEW.order_id;

        IF v_active_paid_count = 0 THEN
            UPDATE auth.api_keys
            SET plan_id = 'free'
            WHERE user_id = v_order.user_id
              AND revoked_at IS NULL
              AND enabled = TRUE;

            INSERT INTO quota.quota_rules (user_id, provider, daily_limit, monthly_limit, soft_limit_pct)
            VALUES
                (v_order.user_id, 'qqmusic', billing.get_plan_daily_limit('free'), billing.get_plan_monthly_limit('free'), 80),
                (v_order.user_id, 'netease', billing.get_plan_daily_limit('free'), billing.get_plan_monthly_limit('free'), 80)
            ON CONFLICT (user_id, provider)
            DO UPDATE SET
                daily_limit = EXCLUDED.daily_limit,
                monthly_limit = EXCLUDED.monthly_limit,
                updated_at = now();
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS trg_refund_completed ON billing.refunds;
CREATE TRIGGER trg_refund_completed
    AFTER UPDATE OF status ON billing.refunds
    FOR EACH ROW
    WHEN (NEW.status = 'completed' AND OLD.status <> 'completed')
    EXECUTE FUNCTION billing.on_refund_completed();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION quota.on_subscription_expired()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.status NOT IN ('expired', 'canceled') THEN
        RETURN NEW;
    END IF;
    IF OLD.status IN ('expired', 'canceled') THEN
        RETURN NEW;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM quota.subscriptions
        WHERE user_id = NEW.user_id
          AND status = 'active'
          AND id <> NEW.id
    ) THEN
        UPDATE auth.api_keys
        SET plan_id = 'free'
        WHERE user_id = NEW.user_id
          AND revoked_at IS NULL
          AND enabled = TRUE;

        INSERT INTO quota.quota_rules (user_id, provider, daily_limit, monthly_limit, soft_limit_pct)
        VALUES
            (NEW.user_id, 'qqmusic', billing.get_plan_daily_limit('free'), billing.get_plan_monthly_limit('free'), 80),
            (NEW.user_id, 'netease', billing.get_plan_daily_limit('free'), billing.get_plan_monthly_limit('free'), 80)
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

DROP TRIGGER IF EXISTS trg_subscription_expired ON quota.subscriptions;
CREATE TRIGGER trg_subscription_expired
    AFTER UPDATE OF status ON quota.subscriptions
    FOR EACH ROW
    WHEN (NEW.status IN ('expired', 'canceled') AND OLD.status NOT IN ('expired', 'canceled'))
    EXECUTE FUNCTION quota.on_subscription_expired();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION quota.sweep_expired_subscriptions()
RETURNS INT AS $$
DECLARE
    affected INT;
BEGIN
    UPDATE quota.subscriptions
    SET status = 'expired', updated_at = now()
    WHERE status = 'active'
      AND current_period_end < now();
    GET DIAGNOSTICS affected = ROW_COUNT;
    RETURN affected;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS trg_subscription_expired ON quota.subscriptions;
DROP FUNCTION IF EXISTS quota.on_subscription_expired();
DROP FUNCTION IF EXISTS quota.sweep_expired_subscriptions();
DROP TRIGGER IF EXISTS trg_refund_completed ON billing.refunds;
DROP FUNCTION IF EXISTS billing.on_refund_completed();

ALTER TABLE billing.refunds
    DROP COLUMN IF EXISTS refund_type,
    DROP COLUMN IF EXISTS operator_id,
    DROP COLUMN IF EXISTS operator_note;

ALTER TABLE billing.orders DROP CONSTRAINT IF EXISTS orders_status_check;
ALTER TABLE billing.orders ADD CONSTRAINT orders_status_check
    CHECK (status IN ('pending','paying','paid','completed','failed','expired','canceled'));
