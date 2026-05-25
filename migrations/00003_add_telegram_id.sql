-- +goose Up
ALTER TABLE users ADD COLUMN IF NOT EXISTS telegram_id BIGINT UNIQUE;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS telegram_id;
