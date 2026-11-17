#!/bin/sh
# Copyright (c) 2026 Vassel Goleyu. MIT License.
# Smoke test of the production compose stack on this machine, over plain HTTP (DOMAIN=localhost).
# Checks: readyz, one simulator message through Caddy, the public site API and page, /metrics hidden.
# Usage: deploy/scripts/smoke.sh            (leaves the stack running; "down -v" to remove it)
set -eu
cd "$(dirname "$0")/../.."
ENVF=deploy/compose/.env.prod
LOCAL="http://127.0.0.1:${SPILLWAY_LOCAL_PORT:-8080}"   # loopback port of the API (override when 8080 is taken)
export SPILLWAY_LOCAL_PORT="${SPILLWAY_LOCAL_PORT:-8080}"
[ -f "$ENVF" ] || { echo "create $ENVF first (copy .env.prod.example; DOMAIN=localhost PUBLIC_SCHEME=http)"; exit 2; }
COMPOSE="docker compose --env-file $ENVF -f deploy/compose/docker-compose.prod.yml"
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT

# No --wait: Keycloak's first start can take minutes and is checked last, separately.
$COMPOSE up -d --build
echo "1. readyz"
for i in $(seq 1 90); do curl -fsS "$LOCAL/readyz" 2>/dev/null && break; sleep 2; done
curl -fsS "$LOCAL/readyz"; echo
for i in $(seq 1 60); do curl -fsS -o /dev/null http://localhost/ 2>/dev/null && break; sleep 2; done

echo "2. seed dev fixture and send one message through Caddy"
$COMPOSE run --rm -T -e SPILLWAY_ENV=dev spillway seed-dev | tee "$WORK/seed.txt" >/dev/null
SECRET="$(sed -n 's/^device=dev-001 .*secret=//p' "$WORK/seed.txt")"
[ -n "$SECRET" ] || { echo "seed-dev printed no secret for dev-001 (already seeded? use down -v)"; exit 1; }
printf '[{"device":"dev-001","secret":"%s"}]' "$SECRET" > "$WORK/devices.json"
go run ./cmd/simulator -devices-file "$WORK/devices.json" -http http://localhost/v1/ingest -scenario steady -interval 1s -duration 1s -report - > "$WORK/report.json"
grep -q '"unique_confirmed": 1' "$WORK/report.json" || { cat "$WORK/report.json"; echo "message not confirmed"; exit 1; }
sleep 2

echo "3. public API shows the reading"
curl -fsS http://localhost/v1/public/sites/kali-contoh | tee "$WORK/site.json" | grep -q '"last_reading"\|"level_cm"' || { cat "$WORK/site.json"; echo "no reading"; exit 1; }

echo "4. public page"
curl -fsS http://localhost/s/kali-contoh | grep -qi 'kali' || { echo "page lacks site name"; exit 1; }

echo "5. /metrics is not public"
[ "$(curl -s -o /dev/null -w '%{http_code}' http://localhost/metrics)" = 404 ] || { echo "/metrics reachable through Caddy"; exit 1; }

echo "5b. stateful services come back after a restart (a restarted broker once crash-looped on its password file)"
$COMPOSE restart mosquitto postgres >/dev/null
for i in $(seq 1 60); do curl -fsS "$LOCAL/readyz" >/dev/null 2>&1 && break; [ "$i" = 60 ] && { echo "readyz did not recover after restarting mosquitto and postgres"; exit 1; }; sleep 2; done
go run ./cmd/simulator -devices-file "$WORK/devices.json" -http http://localhost/v1/ingest -scenario steady -interval 1s -duration 1s -report - > "$WORK/report2.json"
grep -q '"unique_confirmed": 1' "$WORK/report2.json" || { cat "$WORK/report2.json"; echo "no ingest after restart"; exit 1; }

echo "6. keycloak realm imported with placeholders substituted (first start can take minutes)"
for i in $(seq 1 180); do
  curl -fsS -H 'Host: auth.localhost' http://localhost/realms/spillway/.well-known/openid-configuration 2>/dev/null | grep -q 'auth.localhost' && break
  [ "$i" = 180 ] && { echo "realm discovery failed after 15 minutes"; exit 1; }
  sleep 5
done
echo "SMOKE OK"
