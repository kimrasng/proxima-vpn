-- +goose Up
ALTER TABLE users ADD COLUMN IF NOT EXISTS traffic_reset_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS traffic_reset_at;
