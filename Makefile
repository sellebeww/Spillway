# Copyright (c) 2026 Vassel Goleyu. MIT License.
COMPOSE := docker compose --env-file .env -f deploy/compose/docker-compose.dev.yml

# Explicit list: "./..." would also scan Go files vendored inside web/node_modules.
GOPKGS := ./cmd/... ./internal/... ./migrations/... ./tools/...

.PHONY: smoke verify dev-up dev-down migrate web-dev web-e2e

verify:
	go vet $(GOPKGS)
	golangci-lint run $(GOPKGS)
	go test -race $(GOPKGS)
	cd web && npm run check && npm run lint && npm test && npm run build

dev-up:
	@test -f .env || cp .env.example .env
	$(COMPOSE) up -d --wait

dev-down:
	$(COMPOSE) down

migrate:
	@set -a; . ./.env; set +a; go run ./cmd/spillway migrate

# Web dev server on :3000 with the same environment as the API (needs make dev-up and the API running).
web-dev:
	@set -a; . ./.env; set +a; cd web && npm run dev

# Playwright smoke tests against a mock API and mock OIDC provider (needs: npx playwright install chromium).
web-e2e:
	cd web && npm run test:e2e

# Production compose smoke test over plain HTTP (needs deploy/compose/.env.prod, see docs/DEPLOY.md).
smoke:
	deploy/scripts/smoke.sh
