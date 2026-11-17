#!/usr/bin/env bash
# Static checks (CLAUDE.md "Static checks"). Extended by every sprint.
set -u
fail=0
check() { # name, command that must print nothing
  out=$(eval "$2"); if [ -n "$out" ]; then echo "FAIL: $1"; echo "$out"; fail=1; else echo "ok:   $1"; fi
}
check "no pgxpool outside internal/db" \
  "grep -rl pgxpool --include=*.go . | grep -v '^./internal/db/'"
check "no SET SESSION in code or SQL" \
  "grep -rniE 'set[[:space:]]+session' --include=*.go --include=*.sql --include=*.py --include=*.yml --include=Makefile . | grep -v '^./scripts/' | grep -v '^./promts/'"
check "no set_config(..., false)" \
  "grep -rniE \"set_config\\([^)]*,[[:space:]]*false\\)\" --include=*.go --include=*.sql . | grep -v '^./promts/'"
check "Redis client only used inside internal/ratelimit (keys are always t:{tenant}: or global: prefixed)" \
  "grep -rl 'redis/go-redis' --include=*.go . | grep -v -E '^./internal/ratelimit/|_test.go'"
check "Python: DB access only through tenant_tx (no raw pool.connection outside db.py)" \
  "grep -rn 'pool.connection()' --include=*.py agents | grep -v -E 'agents/pyworker/db.py|/tests/'"
check "Kafka client imports only in internal/outbox, internal/events, cmd/relay" \
  "grep -rl 'twmb/franz-go' --include=*.go . | grep -v -E '^./(internal/outbox|internal/events|cmd/relay)/'"
check "app services reach Postgres only through PgBouncer (migrate connects directly)" \
  "grep -nE 'DATABASE_URL:.*@postgres:' deploy/docker-compose.yml | grep -v ferriskey"
check "no tenant_id filter in repositories (RLS supplies it)" \
  "grep -rnE \"(WHERE|AND)[[:space:]]+[a-z_.]*tenant_id[[:space:]]*=[[:space:]]*([$][0-9]|'|current_tenant)\" --include=*.go internal cmd | grep -v '_test.go'"
check "schema lint: RLS forced, UNIQUE(tenant_id,id), composite FKs, listed cross-tenant policies only" \
  "docker compose --env-file deploy/.env -f deploy/docker-compose.yml exec -T postgres psql -U postgres -d ratify -XAt -f - < scripts/rls-check.sql"
check "compose limits, pins, budget" "python3 scripts/budget.py | grep -E '^(FAIL|  )'"
exit $fail
