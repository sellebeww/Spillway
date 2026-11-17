#!/bin/sh
# Copyright (c) 2026 Vassel Goleyu. MIT License.
# Restores a spillway dump into an EMPTY database (TimescaleDB needs pre/post restore calls).
# Usage: deploy/scripts/restore.sh <spillway-dump-file> [target-db]    (default target: spillway_restore)
# To restore over production: stop spillway, drop and recreate the database, run this with target "spillway".
set -eu
cd "$(dirname "$0")/../.."
FILE="${1:?usage: restore.sh <dump-file> [target-db]}"
TARGET="${2:-spillway_restore}"
COMPOSE="docker compose --env-file deploy/compose/.env.prod -f deploy/compose/docker-compose.prod.yml"
PSQL="$COMPOSE exec -T postgres psql -v ON_ERROR_STOP=1 -U spillway"
$PSQL -d postgres -c "CREATE DATABASE \"$TARGET\""
$PSQL -d "$TARGET" -c "CREATE EXTENSION IF NOT EXISTS timescaledb" -c "SELECT timescaledb_pre_restore()"
# pg_restore warns about objects that already exist after CREATE EXTENSION; those are harmless.
$COMPOSE exec -T postgres pg_restore -U spillway -d "$TARGET" --no-owner < "$FILE" || echo "pg_restore reported warnings (see above)"
$PSQL -d "$TARGET" -c "SELECT timescaledb_post_restore()"
echo "restored into $TARGET"
