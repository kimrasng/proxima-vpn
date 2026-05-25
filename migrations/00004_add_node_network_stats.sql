-- +goose Up
ALTER TABLE nodes
    ADD COLUMN network_in  FLOAT,
    ADD COLUMN network_out FLOAT;

-- +goose Down
ALTER TABLE nodes
    DROP COLUMN network_in,
    DROP COLUMN network_out;
