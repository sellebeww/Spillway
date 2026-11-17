-- +goose Up
-- Public pages resolve a site by slug alone, without knowing the organization, so the slug of a
-- public site must be unique across all organizations. Private sites keep per-organization slugs.
DROP INDEX sites_public_slug_idx;
CREATE UNIQUE INDEX sites_public_slug_uq ON sites (slug) WHERE is_public;

-- +goose Down
DROP INDEX sites_public_slug_uq;
CREATE INDEX sites_public_slug_idx ON sites (slug) WHERE is_public;
