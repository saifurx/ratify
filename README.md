# Ratify

Native CRM + AI sales optimization platform. Architecture rules live in
[CLAUDE.md](CLAUDE.md). This is Sprint 0.1: tenant isolation, auth, migrations.

## Run

Requires Docker (Go runs inside containers; no local Go needed).

```bash
make up              # creates deploy/.env, builds, starts postgres, redis, migrate, api
make seed            # prints TENANT_ID=<uuid>
make test            # unit + tenant isolation gate (as app_user; seeds as app_migrator)
make security-check  # static checks (also runs in CI)
make budget          # sums compose mem_limit, fails above 13.5 GiB
make down | make clean
```

Try it (dev mode trusts headers):

```bash
curl -XPOST localhost:8080/api/accounts -H "X-Tenant-Id: $TENANT_ID" -d '{"name":"Acme"}'
curl localhost:8080/api/accounts -H "X-Tenant-Id: <another uuid>"   # {"items":[]}
```

## Tenant binding: why transaction scope

`set_tenant(uuid)` wraps `set_config('app.tenant_id', $1, true)`; the `true`
makes it **transaction-local**. The API sits behind connection pools (and,
from Sprint 1.4a, PgBouncer in transaction mode), so a session-scoped setting
would stay on the connection and leak one tenant's context into the next
request. **Session-level settings (`SET SESSION`, `set_config(..., false)`) are
forbidden for every setting**; use `SET LOCAL` / `set_config(..., true)` inside
the transaction. `make security-check` greps for violations.
`current_tenant()` uses `current_setting('app.tenant_id', true)`, so it
returns NULL (never an error) when unset, and RLS then matches zero rows.

All DB access goes through `db.TenantTx.WithTenant`; only `internal/db`
imports pgxpool. The API refuses to boot as superuser, as a table owner, as a
BYPASSRLS role or as `app_migrator`.

## Auth

- Dev mode: `OIDC_ISSUER` empty **and** `ENV != production` → trusts
  `X-Tenant-Id` / `X-User-Id`. `ENV=production` without `OIDC_ISSUER` is a boot error.
- OIDC: set `OIDC_ISSUER` and `OIDC_CLIENT_ID`; tokens must carry a `tenant_id`
  claim (UUID). Missing or invalid claim → 403.
- FerrisKey (profile `oidc`): set `FERRISKEY_IMAGE` in `deploy/.env` to a
  **verified, pinned** image tag, override default admin credentials, then
  `docker compose --profile oidc up -d ferriskey`. It is default-on in Sprint 1.3.

## Dev credentials

`deploy/initdb/01-roles.sql` creates dev-only role passwords; never reuse them.
