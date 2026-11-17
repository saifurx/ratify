#!/usr/bin/env bash
# Seeds one demo tenant as app_migrator. Prints TENANT_ID=<uuid>.
set -euo pipefail
T=$(uuidgen | tr 'A-Z' 'a-z')
docker compose --env-file deploy/.env -f deploy/docker-compose.yml exec -T postgres \
  psql -q -v ON_ERROR_STOP=1 -U app_migrator -d ratify -v t="$T" >/dev/null <<'SQL'
BEGIN;
SELECT set_tenant(:'t'::uuid);
INSERT INTO tenants (id, name) VALUES (:'t'::uuid, 'Demo Tenant');
INSERT INTO accounts (tenant_id, name, domain) VALUES (current_tenant(), 'Acme Corp', 'acme.example');
COMMIT;
SQL
echo "TENANT_ID=$T"
