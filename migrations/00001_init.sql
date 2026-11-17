-- +goose Up
CREATE EXTENSION IF NOT EXISTS timescaledb;
CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE schema_info (
    id         smallint PRIMARY KEY CHECK (id = 1),
    created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO schema_info (id) VALUES (1);

-- +goose Down
DROP TABLE schema_info;
-- Extensions are intentionally kept: later objects may depend on them.
