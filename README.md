# Ratify

Native CRM + AI sales optimization platform. Architecture rules live in
[CLAUDE.md](CLAUDE.md). Sprints so far: 0.1 tenant isolation, auth, migrations; 0.2 Kafka + transactional outbox; 1.4a PgBouncer + metrics skeleton; 1.1 users/teams/authz + CRM API; 1.2 leads, consent, enrichment, scoring v0; 1.3 Next.js UI.

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

## Leads, consent, enrichment, scoring (Sprint 1.2)

**Hosted form.** `POST /api/v1/inbound/leads?tenant_token=<public token>` (no
auth). The tenant is resolved from the token by a SECURITY DEFINER function
(unknown token: 404). JSON body: `email` (required), `first_name`, `last_name`,
`company`, `domain`, `source`, `consent` (boolean); UTM params come from the
query string, the referrer from the `Referer` header; IP and user agent go to
the consent ledger. It sets a first-party `ratify_sid` cookie (no third-party
tracking). Rate limits: per IP (global) and per tenant, per tenant and IP, in
Redis with `t:{tenant_id}:` / `global:` keys (`INBOUND_RATE_*` env); Redis down
means 503 (fail closed). The response is always `202 {"status":"received"}`.

**Suppression and consent.** Suppression (email or domain) is checked
synchronously before any write; a suppressed submission returns exactly the
same status, body and cookie and stores nothing: no lead, no ledger row, no
event, no log line (only the `ratify_inbound_leads_total{outcome}` counter).
Dedupe: same email merges into the existing lead (first_captured_at kept,
consent event appended). Same company domain + name under a different email is
only flagged `possible_duplicate_of`; consent never transfers between
addresses. `POST /api/v1/leads/{id}/convert` requires `consent.CanContact`
(403 with a reason code otherwise) and creates account, contact and
opportunity in one transaction. Admins manage `/api/v1/suppressions`
(add/list only; suppression is permanent).

**Enrichment and scoring (Python `pyworker`).** Consumes `lead.created` /
`lead.updated`, enriches the company domain asynchronously (a lead is usable
immediately), then writes a versioned `lead_scores` row and emits `lead.scored`.
It runs as the `ml_worker` DB role (tenant-scoped, column-level grants, no
BYPASSRLS) through PgBouncer.
- Crawler: native, robots.txt first, per-host rate limit (Redis), 7-day cache at
  `global:enrich:{domain}` (public data, cross-tenant by design), only public IPs
  (SSRF guard, connects to the validated address, never follows off-site
  redirects). RDAP is used for domain age; no paid enrichment APIs.
  Provenance (HTML hash + parsed fields) goes to `enrichment_raw`.
- Scoring v0: XGBoost trained on a documented synthetic dataset
  (`agents/pyworker/model.py`: seed 7, n=4000). `model_version` is
  `synthetic-v0-<hash>`, so the UI must label the score **preliminary**. Same
  (features, model_version) gives the same score and reasons (SHAP-style top
  contributions). The internal `POST /score` (port 8090, `PYWORKER_SERVICE_TOKEN`)
  is not routed through the public API and publishes no host port.
- Measured, not asserted: cold crawl of a public site (robots.txt + page over
  TLS) took ~120-150 ms, RDAP ~0.7-0.9 s. With a cache hit, `lead.created` was
  dispatched ~30 ms and `lead.scored` ~60 ms after the form POST locally.
- `make test` also runs the Python tests (`make test-py`) inside the worker
  image; test lead domains use `.test` so no test ever crawls a real site.

## Web UI (Sprint 1.3)

Next.js 15 (App Router, TypeScript, Tailwind, shadcn-style components) in
`web/`, served on <http://localhost:3000>.

```bash
make up          # includes web and ferriskey
make seed-demo   # 2 tenants, every role, 2 teams each; 100 accounts, 500 contacts, 200 deals per tenant
open http://localhost:3000   # demo login: pick a seeded user (development only)
make test-web    # typecheck + Vitest
make test-ui     # Playwright end-to-end (builds web, runs a Playwright container on the compose network)
```

Screens: login/logout, dashboard (pipeline value by stage, my open deals,
recent activity), accounts and contacts (list + detail with contacts,
opportunities and activity tabs), pipeline board (drag a card to change stage;
a stage menu is the keyboard fallback), activity composer (notes and call logs
only), lead inbox (scores labelled "preliminary" for synthetic models; Convert
is disabled with the reason when `consent.CanContact` is false), global search
(`GET /api/v1/search`, PostgreSQL full-text, scoped like the lists) and an
admin-only settings placeholder. Every mutation goes through the API; errors
render as toasts with the API's own message.

**Authentication.** Auth.js v5 with a custom OIDC provider for FerrisKey
(`ghcr.io/ferriskey/ferriskey-api:0.10.2`, pinned, default-on in Compose; the
admin console is opt-in with `--profile ops`). The default `admin/admin` is
replaced by `FERRISKEY_ADMIN_*` in `deploy/.env`.
- Claim mapping: **one realm per tenant**. Tokens must carry `tenant_id` (the
  Ratify tenant UUID, as a custom claim/protocol mapper) and `roles` (any of
  `rep, manager, admin, finance, legal`). The BFF forwards the ID token; the API
  derives the tenant from `tenant_id` only. Set `OIDC_ISSUER`, `OIDC_CLIENT_ID`
  and `OIDC_CLIENT_SECRET` for the web app and `OIDC_ISSUER`/`OIDC_CLIENT_ID`
  for the API (same client). **Not verified end to end in this sprint**: FerrisKey's
  documentation does not describe how to add custom claims, so creating the
  `tenant_id`/`roles` mapper in a realm is an operator step to validate against
  a real realm; the e2e suite exercises the UI through dev login.
- Never client-supplied tenant: enforced by the BFF (see CLAUDE.md) and covered by
  an e2e test that injects an `X-Tenant-Id` header, a `tenant_id` query and body.
- Dev login is compiled in only as a code path gated on `OIDC_ISSUER == ""` and
  `APP_ENV != production`; with `APP_ENV=production` and no issuer the web app
  refuses to start.
