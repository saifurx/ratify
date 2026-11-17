COMPOSE = docker compose --env-file deploy/.env -f deploy/docker-compose.yml
GO_IMAGE = golang:1.25.14-alpine
NET = ratify_default
include deploy/.env.example
-include deploy/.env
export

GO = docker run --rm --network $(NET) -v $(CURDIR):/src -w /src \
  -v ratify-gomod:/go/pkg/mod -v ratify-gocache:/root/.cache/go-build \
  -e CGO_ENABLED=0 \
  -e DATABASE_URL=postgres://app_user:$(APP_USER_PASSWORD)@postgres:5432/ratify?sslmode=disable \
  -e MIGRATOR_URL=postgres://app_migrator:$(MIGRATOR_PASSWORD)@postgres:5432/ratify?sslmode=disable \
  -e RELAY_URL=postgres://relay_user:$(RELAY_USER_PASSWORD)@postgres:5432/ratify?sslmode=disable \
  -e KAFKA_BROKERS=kafka:9092 \
  -e ADMIN_URL=postgres://postgres:$(POSTGRES_PASSWORD)@postgres:5432/ratify?sslmode=disable \
  $(GO_IMAGE)

.PHONY: init up down migrate test psql seed clean budget security-check tidy
init:
	@test -f deploy/.env || { cp deploy/.env.example deploy/.env; echo "created deploy/.env"; }

up: init
	$(COMPOSE) up -d --build --wait

down:
	$(COMPOSE) down

migrate: init
	$(COMPOSE) run --rm migrate

tidy:
	$(GO) go mod tidy

# Runs unit + isolation tests as app_user (seeding as app_migrator).
# The compose relay is stopped during tests so it cannot race the test relays.
test: init
	@$(COMPOSE) stop relay >/dev/null 2>&1 || true
	@$(GO) go test -count=1 -p 1 -tags dbtest ./... ; rc=$$?; \
	  $(COMPOSE) start relay >/dev/null 2>&1 || true; \
	  echo "--- kafka resource snapshot (heap capped by -Xmx1024m) ---"; \
	  docker stats --no-stream --format '{{.Name}} mem={{.MemUsage}} cpu={{.CPUPerc}}' ratify-kafka-1 ratify-relay-1 2>/dev/null || true; \
	  exit $$rc

psql:
	$(COMPOSE) exec postgres psql -U app_user -d ratify

seed: init
	@./scripts/seed.sh

budget: init
	@python3 scripts/budget.py

security-check: init
	@./scripts/security-check.sh

clean:
	$(COMPOSE) down -v --remove-orphans
