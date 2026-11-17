#!/bin/sh
# Copyright (c) 2026 Vassel Goleyu. MIT License.
# Runs once on first start of an empty data volume: a separate database and role for Keycloak.
set -eu
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres <<SQL
CREATE ROLE keycloak LOGIN PASSWORD '${KEYCLOAK_DB_PASSWORD}';
CREATE DATABASE keycloak OWNER keycloak;
SQL
