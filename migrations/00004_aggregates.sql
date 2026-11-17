-- +goose NO TRANSACTION
-- +goose Up
-- Continuous aggregates cannot be created inside a transaction block.
-- Only readings with quality_score >= 0.5 (the default min_quality) are aggregated (docs/SPEC.md 5.6).

CREATE MATERIALIZED VIEW readings_1m WITH (timescaledb.continuous) AS
SELECT device_id,
       time_bucket(INTERVAL '1 minute', ts) AS bucket,
       min(level_cm)                        AS min_level,
       sum(level_cm)                        AS sum_level,
       sum(level_cm) / count(*)             AS avg_level,
       max(level_cm)                        AS max_level,
       count(*)                             AS n
FROM readings
WHERE quality_score >= 0.5
GROUP BY device_id, time_bucket(INTERVAL '1 minute', ts)
WITH NO DATA;

CREATE MATERIALIZED VIEW readings_1h WITH (timescaledb.continuous) AS
SELECT device_id,
       time_bucket(INTERVAL '1 hour', bucket) AS bucket,
       min(min_level)                         AS min_level,
       sum(sum_level)                         AS sum_level,
       sum(sum_level) / sum(n)                AS avg_level,
       max(max_level)                         AS max_level,
       sum(n)                                 AS n
FROM readings_1m
GROUP BY device_id, time_bucket(INTERVAL '1 hour', bucket)
WITH NO DATA;

CREATE MATERIALIZED VIEW readings_1d WITH (timescaledb.continuous) AS
SELECT device_id,
       time_bucket(INTERVAL '1 day', bucket) AS bucket,
       min(min_level)                        AS min_level,
       sum(sum_level)                        AS sum_level,
       sum(sum_level) / sum(n)               AS avg_level,
       max(max_level)                        AS max_level,
       sum(n)                                AS n
FROM readings_1h
GROUP BY device_id, time_bucket(INTERVAL '1 day', bucket)
WITH NO DATA;

-- Since TimescaleDB 2.13 real-time aggregation is OFF by default; turn it on explicitly so
-- queries also see data newer than the last materialization.
ALTER MATERIALIZED VIEW readings_1m SET (timescaledb.materialized_only = false);
ALTER MATERIALIZED VIEW readings_1h SET (timescaledb.materialized_only = false);
ALTER MATERIALIZED VIEW readings_1d SET (timescaledb.materialized_only = false);

-- Refresh windows cover the 24 h late-data horizon and stay well inside raw retention.
SELECT add_continuous_aggregate_policy('readings_1m',
    start_offset => INTERVAL '3 days', end_offset => INTERVAL '1 minute', schedule_interval => INTERVAL '1 minute');
SELECT add_continuous_aggregate_policy('readings_1h',
    start_offset => INTERVAL '7 days', end_offset => INTERVAL '1 hour', schedule_interval => INTERVAL '5 minutes');
SELECT add_continuous_aggregate_policy('readings_1d',
    start_offset => INTERVAL '30 days', end_offset => INTERVAL '1 day', schedule_interval => INTERVAL '1 hour');

-- +goose Down
DROP MATERIALIZED VIEW readings_1d;
DROP MATERIALIZED VIEW readings_1h;
DROP MATERIALIZED VIEW readings_1m;
