# Ratify

Native CRM + AI sales optimization platform. Architecture rules live in
[CLAUDE.md](CLAUDE.md). Sprints so far: 0.1 tenant isolation, auth, migrations; 0.2 Kafka + transactional outbox; 1.4a PgBouncer + metrics skeleton; 1.1 users/teams/authz + CRM API.

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
H=(-H "X-Tenant-Id: $TENANT_ID" -H "X-User-Id: me" -H "X-Role: admin")
curl -XPOST localhost:8080/api/v1/accounts "${H[@]}" -d '{"name":"Acme"}'
curl localhost:8080/api/v1/accounts "${H[@]}"
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

## Kafka and the outbox (Sprint 0.2)

- Apache Kafka 4.0.0, KRaft single broker (RF=1, heap `-Xms512m -Xmx1024m`,
  `mem_limit` 2g). Not Redpanda (license).
- **Handlers never call Kafka.** They call `outbox.Append(ctx, tx, topic, key,
  payload)` inside their own transaction, so an event exists iff the business
  write commits. The relay (`cmd/relay`, role `relay_user`) publishes rows.
- **Delivery is at-least-once.** A Postgres and a Kafka transaction cannot
  commit atomically; a crash between the Kafka commit and the Postgres commit
  republishes the row (a duplicate on the topic is expected). Every message
  carries `event_id` (= `outbox.id`), `tenant_id`, `event_type`, `occurred_at`
  headers. Consumers use `internal/events`, which reads `read_committed` and
  dedupes on `event_id` (`processed_events`) in the handler's transaction, which
  gives exactly-once *processing*. Never claim exactly-once publication.
- The relay uses a stable `transactional.id` per instance
  (`RELAY_INSTANCE_ID`); a restarted instance fences and aborts its
  predecessor's open Kafka transaction.
- Cross-tenant access is one role-specific policy (`outbox_relay`, only on
  `outbox`); `relay_user` can read no other table, and may only update the
  dispatch bookkeeping columns. No BYPASSRLS anywhere.
- Retries use exponential backoff. A row failing 10 times is dead-lettered
  (`dead_lettered_at`, `outbox_dead_letter` on stderr) and never blocks other
  keys. Kafka being unreachable does not charge attempts: rows wait (the outbox
  buffers) and the relay resumes. Rows with the same key publish in `seq` order.
- `make test` stops the compose relay while tests run (so it cannot race the
  test relays), restarts it afterwards, and prints a `docker stats` snapshot of
  Kafka and the relay. Fresh role changes require `make clean && make up`
  (initdb only runs on an empty volume).

## PgBouncer and metrics (Sprint 1.4a)

- PgBouncer (transaction pooling, `edoburu/pgbouncer:v1.24.1-p1`) fronts
  Postgres for the API and relay; **migrations connect directly**. Clients are
  authenticated through `auth_query` (`pgbouncer.user_lookup`, role
  `pgbouncer_auth`), so new roles need no PgBouncer change. The test suite
  (`DATABASE_URL`, `RELAY_URL`) also runs through it.
- `tests/pgbouncer_test.go`: 20 goroutines alternating 4 tenants over a
  2-connection pool (`ratify_small`), plus unbound queries on reused
  connections; no request ever sees another tenant's rows. Negative control: a
  session-scoped binding (`set_config(..., false)`) through the same pooler
  leaks the tenant to the next client, which is why it is forbidden.
- `/metrics` on the API (Prometheus; request count, latency histogram, 5xx by
  route, Go/process stats); Prometheus on `127.0.0.1:9090` scrapes it. Labels
  carry no tenant, user or email. Outbox lag, dead letters, cross-tenant and
  backup metrics plus Grafana arrive in 1.4b.
- `make init` appends variables added to `deploy/.env.example` to an existing
  `deploy/.env` (never overwrites).

## CRM API and authorization (Sprint 1.1)

REST under `/api/v1` (accounts, contacts, opportunities, activities, pipelines
and stages, users, teams) plus a read-only GraphQL account-360 query at
`POST /api/v1/graphql`. Lists use opaque keyset cursors (`?limit=&cursor=`);
the cursor is HMAC-signed with `CURSOR_SECRET`, binds the tenant into the
signature without encoding it, and is rejected under any other tenant.
Filters: `owner_id` (accounts, contacts, opportunities), `status`
(opportunities), plus `account_id`/`contact_id`/`opportunity_id`/`type` where
they apply.

**Two layers, kept separate.** RLS is the tenant boundary only. In-tenant
visibility is `authz.Scope(ctx)`, applied in the service layer to every
list/get/update/delete as an *owner* filter (never a tenant filter):

| role | visibility | writes |
|------|-----------|--------|
| rep | `owner_id = self` | own records |
| manager | members of the teams they manage (+ self) | those records |
| admin | whole tenant | yes (pipelines, teams are admin-only) |
| finance, legal | whole tenant | no |
| agent, system (service principals) | whole tenant | no (until a later sprint) |

An out-of-scope record is a 404, never a 403 (existence is not revealed).
Users are upserted from token claims on the first authenticated request
(dev mode: `X-User-Id` + `X-Role`; an unknown role or missing subject is 403,
never a default grant). Every FK between tenant tables is composite, so a row
can never reference another tenant's record. Domain events
(`contact.created/updated`, `opportunity.created/stage_changed`,
`activity.logged`, `account.created`) go through the outbox in the same
transaction; `audit_log` is append-only. Public tenant lookups
(`resolve_tenant_token`, `resolve_tenant_slug`) are SECURITY DEFINER functions
that return only the tenant id.

`make security-check` also lints the live schema (RLS enabled and forced,
`UNIQUE (tenant_id, id)`, composite FKs, only the two documented cross-tenant
policies, no BYPASSRLS) and rejects tenant filters in repositories.
