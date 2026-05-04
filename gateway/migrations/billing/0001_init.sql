-- +goose Up
SET search_path TO billing;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS orders (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID NOT NULL,
    plan_id            UUID NOT NULL,
    amount_cents       BIGINT NOT NULL,
    currency           TEXT NOT NULL DEFAULT 'CNY',
    status             TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending','paying','paid','completed','failed','expired','canceled')),
    payment_provider   TEXT NOT NULL,
    external_order_id  TEXT,
    idempotency_key    TEXT NOT NULL UNIQUE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at            TIMESTAMPTZ,
    expires_at         TIMESTAMPTZ NOT NULL,
    metadata           JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS orders_user_status_idx ON orders (user_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS orders_external_idx ON orders (payment_provider, external_order_id);

CREATE TABLE IF NOT EXISTS invoices (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    order_id    UUID NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    pdf_url     TEXT,
    issued_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS invoices_user_idx ON invoices (user_id, issued_at DESC);

CREATE TABLE IF NOT EXISTS transactions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        UUID NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    provider        TEXT NOT NULL,
    external_txn_id TEXT NOT NULL UNIQUE,
    amount_cents    BIGINT NOT NULL,
    currency        TEXT NOT NULL DEFAULT 'CNY',
    status          TEXT NOT NULL,
    raw_callback    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS refunds (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        UUID NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    amount_cents    BIGINT NOT NULL,
    reason          TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','processing','completed','failed')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS payment_callbacks (
    id              BIGSERIAL PRIMARY KEY,
    provider        TEXT NOT NULL,
    raw_body        BYTEA NOT NULL,
    headers         JSONB NOT NULL DEFAULT '{}'::jsonb,
    client_ip       INET,
    signature_ok    BOOLEAN NOT NULL DEFAULT FALSE,
    processed       BOOLEAN NOT NULL DEFAULT FALSE,
    error           TEXT,
    order_id        UUID,
    external_txn_id TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS payment_callbacks_time_idx ON payment_callbacks (created_at DESC);
CREATE INDEX IF NOT EXISTS payment_callbacks_order_idx ON payment_callbacks (order_id);

-- +goose Down
SET search_path TO billing;

DROP TABLE IF EXISTS payment_callbacks;
DROP TABLE IF EXISTS refunds;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS invoices;
DROP TABLE IF EXISTS orders;
