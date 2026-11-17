-- +goose Up
-- Every business table carries org_id. Child tables reference their parent with a
-- composite (org_id, id) foreign key, so a row can never point at another tenant's parent.

CREATE TABLE organizations (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug       text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
    name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sites (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES organizations (id),
    slug       text NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
    name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    location   geography(Point, 4326) NOT NULL,
    tz         text NOT NULL DEFAULT 'Asia/Jakarta',
    is_public  boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug),
    UNIQUE (org_id, id)
);
CREATE INDEX sites_location_gix ON sites USING gist (location);
-- Public pages resolve a site by slug without knowing the org.
CREATE INDEX sites_public_slug_idx ON sites (slug) WHERE is_public;

CREATE TABLE devices (
    id                  uuid PRIMARY KEY,
    org_id              uuid NOT NULL,
    site_id             uuid NOT NULL,
    device_key          text NOT NULL UNIQUE CHECK (device_key ~ '^[A-Za-z0-9_-]{1,64}$'),
    name                text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    secret_enc          bytea NOT NULL,           -- versioned AES-GCM blob, never plaintext
    expected_interval_s integer NOT NULL DEFAULT 60 CHECK (expected_interval_s BETWEEN 1 AND 86400),
    low_batt_v          real NOT NULL DEFAULT 3.3 CHECK (low_batt_v > 0 AND low_batt_v <= 20),
    last_seen_at        timestamptz,
    last_status         text NOT NULL DEFAULT 'OFFLINE' CHECK (last_status IN ('ONLINE', 'DELAYED', 'OFFLINE')),
    created_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES sites (org_id, id)
);
CREATE INDEX devices_org_site_idx ON devices (org_id, site_id);

CREATE TABLE users (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       uuid NOT NULL REFERENCES organizations (id),
    oidc_sub     text NOT NULL,
    email        text,
    display_name text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, oidc_sub),
    UNIQUE (org_id, id)
);

CREATE TABLE calibrations (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       uuid NOT NULL,
    device_id    uuid NOT NULL,
    version      integer NOT NULL CHECK (version >= 1),
    offset_cm    double precision NOT NULL,
    min_valid_mm integer NOT NULL DEFAULT 250 CHECK (min_valid_mm >= 0),
    max_valid_mm integer NOT NULL DEFAULT 4500,
    valid_from   timestamptz NOT NULL,
    note         text NOT NULL DEFAULT '',
    created_by   uuid,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CHECK (min_valid_mm < max_valid_mm),
    UNIQUE (device_id, version),
    FOREIGN KEY (org_id, device_id) REFERENCES devices (org_id, id),
    FOREIGN KEY (org_id, created_by) REFERENCES users (org_id, id)
);
CREATE INDEX calibrations_device_valid_from_idx ON calibrations (device_id, valid_from DESC, version DESC);

CREATE TABLE alert_rules (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL,
    site_id     uuid NOT NULL,
    type        text NOT NULL CHECK (type IN ('THRESHOLD', 'RATE_OF_RISE')),
    params      jsonb NOT NULL,
    cooldown_s  integer NOT NULL DEFAULT 1800 CHECK (cooldown_s >= 0),
    min_quality real NOT NULL DEFAULT 0.5 CHECK (min_quality BETWEEN 0 AND 1),
    enabled     boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES sites (org_id, id)
);
CREATE INDEX alert_rules_org_site_idx ON alert_rules (org_id, site_id);

CREATE TABLE alert_events (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           uuid NOT NULL,
    rule_id          uuid NOT NULL,
    state            text NOT NULL CHECK (state IN ('OPEN', 'CLOSED')),
    severity         smallint NOT NULL CHECK (severity BETWEEN 1 AND 3),
    opened_at        timestamptz NOT NULL,
    closed_at        timestamptz,
    peak_level       double precision,
    peak_rate        double precision,
    last_notified_at timestamptz,
    UNIQUE (org_id, id),
    CHECK ((state = 'CLOSED') = (closed_at IS NOT NULL)),
    FOREIGN KEY (org_id, rule_id) REFERENCES alert_rules (org_id, id)
);
-- At most one open event per rule.
CREATE UNIQUE INDEX alert_events_one_open_per_rule ON alert_events (rule_id) WHERE state = 'OPEN';
CREATE INDEX alert_events_org_rule_opened_idx ON alert_events (org_id, rule_id, opened_at DESC);

CREATE TABLE channels (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES organizations (id),
    kind       text NOT NULL CHECK (kind IN ('telegram')),
    chat_id    text NOT NULL CHECK (length(chat_id) BETWEEN 1 AND 64),
    is_active  boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, kind, chat_id),
    UNIQUE (org_id, id)
);

CREATE TABLE subscriptions (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL,
    site_id    uuid NOT NULL,
    channel_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (site_id, channel_id),
    FOREIGN KEY (org_id, site_id) REFERENCES sites (org_id, id),
    FOREIGN KEY (org_id, channel_id) REFERENCES channels (org_id, id)
);
CREATE INDEX subscriptions_channel_idx ON subscriptions (channel_id);

-- Deep-link tokens: only the SHA-256 of the token is stored.
CREATE TABLE subscription_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL,
    site_id    uuid NOT NULL,
    token_hash bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_by uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (org_id, site_id) REFERENCES sites (org_id, id),
    FOREIGN KEY (org_id, created_by) REFERENCES users (org_id, id)
);

-- One row per (event, kind, channel, seq). seq is 0 except for repeated REMINDERs.
CREATE TABLE notifications_outbox (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id          uuid NOT NULL,
    alert_event_id  uuid NOT NULL,
    kind            text NOT NULL CHECK (kind IN ('OPENED', 'ESCALATED', 'DEESCALATED', 'CLOSED', 'REMINDER')),
    seq             integer NOT NULL DEFAULT 0 CHECK (seq >= 0),
    channel_id      uuid NOT NULL,
    status          text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'SENT', 'FAILED')),
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text,
    payload         jsonb NOT NULL DEFAULT '{}',
    created_at      timestamptz NOT NULL DEFAULT now(),
    sent_at         timestamptz,
    UNIQUE (alert_event_id, kind, channel_id, seq),
    FOREIGN KEY (org_id, alert_event_id) REFERENCES alert_events (org_id, id),
    FOREIGN KEY (org_id, channel_id) REFERENCES channels (org_id, id)
);
CREATE INDEX outbox_due_idx ON notifications_outbox (next_attempt_at) WHERE status = 'PENDING';

CREATE TABLE memberships (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL,
    user_id    uuid NOT NULL,
    role       text NOT NULL CHECK (role IN ('admin', 'operator')),
    site_id    uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((role = 'operator') = (site_id IS NOT NULL)),
    FOREIGN KEY (org_id, user_id) REFERENCES users (org_id, id),
    FOREIGN KEY (org_id, site_id) REFERENCES sites (org_id, id)
);
CREATE UNIQUE INDEX memberships_admin_uq ON memberships (user_id) WHERE role = 'admin';
CREATE UNIQUE INDEX memberships_operator_uq ON memberships (user_id, site_id) WHERE role = 'operator';
CREATE INDEX memberships_org_site_idx ON memberships (org_id, site_id);

CREATE TABLE audit_log (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id        uuid NOT NULL REFERENCES organizations (id),
    at            timestamptz NOT NULL DEFAULT now(),
    actor_user_id uuid,
    actor_label   text NOT NULL,
    action        text NOT NULL,
    entity_type   text NOT NULL,
    entity_id     text NOT NULL,
    before        jsonb,
    after         jsonb
);
CREATE INDEX audit_log_org_at_idx ON audit_log (org_id, at DESC);
CREATE INDEX audit_log_entity_idx ON audit_log (org_id, entity_type, entity_id);

-- Calibrations and the audit log are append-only.
-- +goose StatementBegin
CREATE FUNCTION spillway_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME USING ERRCODE = 'restrict_violation';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER calibrations_append_only BEFORE UPDATE OR DELETE ON calibrations
    FOR EACH ROW EXECUTE FUNCTION spillway_append_only();
CREATE TRIGGER audit_log_append_only BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION spillway_append_only();

-- +goose Down
DROP TABLE audit_log, memberships, notifications_outbox, subscription_tokens, subscriptions,
    channels, alert_events, alert_rules, calibrations, users, devices, sites, organizations;
DROP FUNCTION spillway_append_only();
