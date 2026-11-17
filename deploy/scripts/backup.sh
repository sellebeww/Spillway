#!/bin/sh
# Copyright (c) 2026 Vassel Goleyu. MIT License.
# Logical backup of the spillway and keycloak databases (custom format, compressed).
# Usage: deploy/scripts/backup.sh [backup-dir]     Cron example in docs/DEPLOY.md.
# Keep SPILLWAY_SECRET_KEY somewhere else: a dump without it cannot decrypt device secrets.
set -eu
cd "$(dirname "$0")/../.."
DIR="${1:-${BACKUP_DIR:-./backups}}"
KEEP_DAYS="${KEEP_DAYS:-14}"
COMPOSE="docker compose --env-file deploy/compose/.env.prod -f deploy/compose/docker-compose.prod.yml"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$DIR"
for db in spillway keycloak; do
  out="$DIR/$db-$STAMP.dump"
  $COMPOSE exec -T postgres pg_dump -U spillway -Fc "$db" > "$out.tmp"
  mv "$out.tmp" "$out"
  echo "wrote $out ($(wc -c < "$out") bytes)"
done
find "$DIR" -name '*.dump' -mtime "+$KEEP_DAYS" -delete
