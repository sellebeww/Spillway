-- +goose Up
-- Raw readings. No org_id column and no foreign key on purpose: this is the hot, high-volume
-- table. Tenant scoping always goes through devices (id, org_id); inserts verify the device
-- belongs to the caller's organization in the same statement.
CREATE TABLE readings (
    device_id     uuid NOT NULL,
    ts            timestamptz NOT NULL,
    sid           text NOT NULL,
    fc            bigint NOT NULL CHECK (fc BETWEEN 0 AND 4294967295),
    received_at   timestamptz NOT NULL,
    raw_value     integer NOT NULL,           -- sensor-to-water distance, mm
    level_cm      double precision NOT NULL,  -- water level above datum
    quality_flags text[] NOT NULL,
    quality_score real NOT NULL CHECK (quality_score BETWEEN 0 AND 1),
    late          boolean NOT NULL DEFAULT false,
    batt_v        real
);

-- Daily chunks; the unique index includes the partitioning column ts as required.
SELECT create_hypertable('readings', by_range('ts', INTERVAL '1 day'));
CREATE UNIQUE INDEX readings_dedup_uq ON readings (device_id, ts, sid, fc);

-- Compress chunks older than 7 days (docs/SPEC.md 5.6).
ALTER TABLE readings SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'device_id',
    timescaledb.compress_orderby = 'ts DESC'
);
SELECT add_compression_policy('readings', INTERVAL '7 days');

-- Raw retention, default 90 days. `spillway migrate` re-applies the configured value.
SELECT add_retention_policy('readings', INTERVAL '90 days');

-- +goose Down
DROP TABLE readings;
