-- +goose Up
-- Rules apply to a site, but the engine evaluates one device at a time, so an alert event belongs
-- to a (rule, device) pair: "one open event per rule" holds for each device. With one device per
-- site this is exactly the rule from docs/SPEC.md 5.4. Events created without a device (phase 3
-- tests, manual rows) keep the old per-rule behaviour through the COALESCE.
ALTER TABLE alert_events ADD COLUMN device_id uuid;
ALTER TABLE alert_events ADD COLUMN reminder_count integer NOT NULL DEFAULT 0;
-- Counts every notified decision of the event; it is the outbox seq, so a second ESCALATED (or
-- DEESCALATED) of the same event is a different notification, not a duplicate of the first.
ALTER TABLE alert_events ADD COLUMN notify_seq integer NOT NULL DEFAULT 0;
ALTER TABLE alert_events ADD CONSTRAINT alert_events_device_fk
    FOREIGN KEY (org_id, device_id) REFERENCES devices (org_id, id);
DROP INDEX alert_events_one_open_per_rule;
CREATE UNIQUE INDEX alert_events_one_open_per_rule_device ON alert_events
    (rule_id, COALESCE(device_id, '00000000-0000-0000-0000-000000000000'::uuid)) WHERE state = 'OPEN';
CREATE INDEX alert_events_open_idx ON alert_events (org_id) WHERE state = 'OPEN';

-- Persistent long-polling offset of the Telegram bot (global, not tenant data).
CREATE TABLE bot_state (
    name       text PRIMARY KEY,
    value      bigint NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE bot_state;
DROP INDEX alert_events_open_idx;
DROP INDEX alert_events_one_open_per_rule_device;
CREATE UNIQUE INDEX alert_events_one_open_per_rule ON alert_events (rule_id) WHERE state = 'OPEN';
ALTER TABLE alert_events DROP CONSTRAINT alert_events_device_fk;
ALTER TABLE alert_events DROP COLUMN notify_seq;
ALTER TABLE alert_events DROP COLUMN reminder_count;
ALTER TABLE alert_events DROP COLUMN device_id;
