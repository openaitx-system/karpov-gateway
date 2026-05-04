-- +goose Up
SET search_path TO auth;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'user'
        CHECK (role IN ('user', 'admin', 'superadmin'));

CREATE INDEX IF NOT EXISTS users_role_idx ON users (role) WHERE role <> 'user';

-- +goose Down
SET search_path TO auth;

DROP INDEX IF EXISTS users_role_idx;
ALTER TABLE users DROP COLUMN IF EXISTS role;
