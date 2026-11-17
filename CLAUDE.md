# Ratify

Native CRM + AI sales optimization platform. Single-enterprise deployment
target: one EC2 instance, Docker Compose, ~10k records/month. "Tenants" are
isolated business units inside that enterprise and are treated as fully
untrusted with respect to each other.

## Architecture (locked — do not propose alternatives without flagging)

- Go modular monolith for CRM, sequencing, CPQ. Python for ML, agents, ASR.
- PostgreSQL 17 with **RLS**. Tenant binding is transaction-scoped via
  `set_tenant(uuid)` which uses `set_config('app.tenant_id', $1, true)`.
  `current_tenant()` uses `current_setting('app.tenant_id', true)` so it
  returns NULL (never errors) when unset.
  NEVER use `SET SESSION` for ANY setting (tenant, user, role,
  hnsw.iterative_scan, statement_timeout, ...). Use `SET LOCAL` /
  `set_config(..., true)` inside the transaction — session settings leak
  across pooled connections.
- All DB access goes through `internal/db.TenantTx.WithTenant`. Handlers
  must never call pgxpool directly; only `internal/db` constructs pools
  (`db.NewPool(ctx, role, url)`). `role` is a typed value from an
  allow-list in internal/db (`RoleApp`, later `RoleRelay`, `RoleAgentWorker`,
  `RoleGate`, `RoleMLWorker`, ...). Every binary boots with
  `AssertNotPrivileged(role)`: it refuses superuser, table owners,
  BYPASSRLS roles and `app_migrator`, and checks the connected role equals
  the expected one. Tests needing a raw pool use `db.RawPoolForTest`
  behind the `dbtest` build tag, inside internal/db.
- PgBouncer in transaction mode sits between every app service and
  Postgres (built right after Sprint 0.2 as "1.4a"; from then on all tests
  run through it). Migrations connect directly.
- Apache Kafka 4.x KRaft (single broker, RF=1, heap capped 1 GB). NOT Redpanda
  (BSL license conflict). Outbox pattern for any paid side effect (email
  send, SMS, enrichment call). Delivery is at-least-once; every event
  carries `event_id` (= outbox.id) in its headers and every consumer MUST
  dedupe on it and read with `isolation.level=read_committed`.
- FerrisKey for OIDC (pinned version, default admin/admin overridden). NOT
  Keycloak. Dev mode: X-Tenant-Id / X-User-Id headers only when OIDC_ISSUER
  is empty AND ENV != production.
- Temporal (self-hosted, deployed in Sprint 2.0) for every durable
  workflow. LangGraph only for graph DEFINITION in agents: the Temporal
  workflow drives the graph loop and executes each node as an Activity.
  LangGraph's own executor never runs inside workflow code (it is not
  deterministic). No LangGraph checkpointer — durability is Temporal's.
- Decision gate is a SEPARATE Go service. Every business mutation proposed
  by an AI passes through it (see "AI write classes"). Decision records are
  append-only and immutable.
- Postal MTA for email (pinned image tag; needs its own MariaDB). Synchronous
  suppression + consent check on the send path. Warmup enforced (start
  100-200/day, double every 2-3 days over 4-6 weeks). DMARC p=none →
  quarantine → reject. Never send bulk above warmup cap.
- pgvector 0.8+; iterative scans enabled per transaction with
  `SET LOCAL hnsw.iterative_scan = relaxed_order`. Escalation trigger to
  Qdrant: filtered recall misses target under tenant-scoped queries OR HNSW
  memory pushes instance into swap. NOT a fixed vector count.
- LLM gateway (Sprint 2.0): ONE Go package `internal/llm` and one Python
  module `agents/llm.py` that all LLM and embedding calls go through. It
  records provider, model, tokens, cost, tenant_id, purpose to `llm_usage`.
  Inputs are PII-minimised; no direct provider SDK calls elsewhere.
  Provider and model are configured in env and documented in README.

## Data model rules

- Every tenant-owned table has `tenant_id uuid NOT NULL`, ENABLE + FORCE RLS,
  a policy using `current_tenant()` (USING + WITH CHECK), and indexes led by
  tenant_id. This includes child/line tables (quote_line, price_book_entry,
  availability, ...).
- Every parent table has `UNIQUE (tenant_id, id)`. Every FK between tenant
  tables is composite: `(tenant_id, x_id) REFERENCES x(tenant_id, id)`.
  Plain FKs bypass RLS and allow cross-tenant references.
- No repository query contains `WHERE tenant_id` — RLS supplies it.
- Users/teams/roles live in `users`, `teams`, `team_members` (Sprint 1.1),
  synced from FerrisKey claims. Role vocabulary (the only one):
  `rep`, `manager`, `admin`, `finance`, `legal`, plus service principals
  `agent`, `system`. In-tenant visibility (rep sees own records, manager sees
  team, admin sees tenant) is enforced in the service layer via
  `authz.Scope(ctx)`, and covered by tests. RLS = tenant boundary only.

## Shared platform tables (create once, reuse; do not reinvent per sprint)

- `audit_log` + `audit.Write(ctx, tx, ...)` (Sprint 1.1). Every "audit-logged"
  statement in any sprint means this.
- `ai_annotations` (Sprint 1.1): derived AI output, with `model_version`
  and `produced_by`.
- `notifications` + `notify.Send` (Sprint 2.0).
- `internal/blob` (S3, SSE-KMS, per-tenant prefix) and `internal/secrets`
  (Sprint 2.0). Responses of tenant-owned records embed `httpx.TenantOwned`
  (carries `tenant_id`) from the first handler on (Sprint 1.1).

## Migration number ranges

Sprints may run in parallel worktrees, so each owns a range: 0.x = 0001-0099,
1.x = 0100-0199, 2.x = 0200-0299, 3.x = 0300-0399, 4.x = 0400-0499,
5.x = 0500-0599. Never edit a shipped migration.

## Cross-tenant system roles

Some workers legitimately operate across tenants (outbox relay, retention,
warmup monitor, forecasting, embedding backfill, Temporal workers). Rules:
- Preferred: iterate tenants (via `list_tenant_ids()` SECURITY DEFINER) and
  run each unit of work in its own `set_tenant()` transaction.
- Allowed only when per-tenant iteration is impossible: a dedicated LOGIN
  role with a role-specific policy on exactly the table(s) it needs
  (e.g. `CREATE POLICY outbox_relay ON outbox TO relay_user USING (true)`).
- Never grant BYPASSRLS. Never reuse app_user for system work.
- The complete list of cross-tenant policies (enforced by `make security-check`):
  `outbox_relay` (relay_user, outbox only) and `tenants_resolver`
  (tenant_resolver NOLOGIN, id/slug/token columns, used only by the
  SECURITY DEFINER lookup functions). Role memberships that exist only to
  hand over ownership use `INHERIT FALSE` so policies are not inherited.
- Python ML/batch jobs that need the database (lead scoring 1.2, embedding
  backfill 3.3, forecasting 4.2) connect as the dedicated `ml_worker` role
  (created in 1.2): not an owner, no BYPASSRLS, through PgBouncer, per-table
  grants, `set_tenant()` per transaction. The `agents` runtime (3.1) has NO
  database credentials at all; it only calls the Go API.
- Public lookups (tenant_token → tenant, booking slug → tenant) go through
  narrow SECURITY DEFINER functions that return only the tenant id.
- Truly global data (global unsubscribe list, enrichment cache) lives in
  explicitly non-tenant tables named `global_*`, documented as such. Redis
  keys for tenant data are prefixed `t:{tenant_id}:`.

## AI write classes

1. **Derived annotations** — scores, embeddings, prep briefs, methodology
   assessments, call summaries, forecasts. Written only to AI-owned columns
   or tables (e.g. `lead_scores`, `*_embedding`, `ai_annotations`), always
   with `model_version` + `produced_by`, and audit-logged. No gate needed;
   they never change business state.
2. **Business mutations** — creating/updating CRM records, enrolling or
   sending outreach, creating quotes, changing stages. If an AI proposes
   it, it MUST pass the decision gate and carry a gate token. If a human
   performs it (including "Accept" on an AI recommendation), it is a normal
   authenticated user write AND a decision record is written linking the
   recommendation to the human's choice.
Until the gate exists (Sprint 3.2), AI-proposed business mutations are
disabled, not stubbed open.

## Non-negotiables

1. **Tenant isolation** — RLS is the safety net, not the primary gate.
   Every query must have tenant context. Missing context = error, not
   "see everything".
2. **Decision gate** — no agent performs a business mutation or sends
   outreach without passing the gate. No exceptions, even in tests.
3. **Outbox pattern** — paid side effects go through outbox. If it can
   cost money or send an email, it needs the outbox.
4. **Resource limits** — every Compose service has explicit mem_limit and
   cpus, and appears in the budget table below.
5. **Fail closed** — missing tenant, missing consent, missing suppression
   check, missing decision record, gate unreachable: all fail the request,
   not degrade.

## Resource budget

Target instance: **m7i.xlarge (4 vCPU, 16 GB, non-burstable)**. If a
t3.xlarge is used it MUST run in T3 Unlimited mode (load tests and ASR
exhaust CPU credits otherwise). Sum of mem_limit must stay ≤ 13.5 GB (leaves ~2.5 GB for OS and Postgres page cache).
Every sprint that adds or changes a service updates this table and
`make budget` (sums compose mem_limit, fails above 13.5 GB).

| Service            | Sprint | mem_limit | cpus |
|--------------------|--------|-----------|------|
| postgres           | 0.1    | 4g        | 1.5  |
| redis              | 0.1    | 256m      | 0.25 |
| migrate (one-shot) | 0.1    | 64m       | 0.25 |
| api                | 0.1    | 512m      | 1.0  |
| kafka              | 0.2    | 2g        | 1.0  |
| relay              | 0.2    | 256m      | 0.25 |
| pyworker (enrich+score) | 1.2 | 768m    | 0.5  |
| web (Next.js)      | 1.3    | 512m      | 0.5  |
| pgbouncer          | 1.4a   | 64m       | 0.25 |
| prometheus         | 1.4a   | 256m      | 0.15 |
| grafana            | 1.4b   | 256m      | 0.10 |
| temporal (+ui)     | 2.0    | 768m      | 0.5  |
| worker (Go temporal host) | 2.0 | 256m  | 0.25 |
| sender (outbox → Postal)  | 2.2 | 256m    | 0.25 |
| postal + mariadb   | 2.2    | 1g        | 0.5  |
| decisiongate       | 3.2    | 256m      | 0.25 |
| agents             | 3.1    | 768m      | 0.5  |
| asr (local only; does NOT fit this instance — use hosted ASR) | 4.1 | n/a | |
| chromium (pdf)     | 5.1    | 512m      | 0.25 |
| ferriskey (oidc)   | 1.3    | 512m      | 0.25 |

Adjust values as measured; never exceed the cap without trimming elsewhere.

## Static checks (`make security-check`, run in CI on every PR)

Born in Sprint 0.1 and extended by every sprint: no pgxpool outside
internal/db; no `SET SESSION` / `set_config(..., false)`; compose services
have mem_limit, cpus and pinned image tags; later: Kafka imports confined,
SQL lint for RLS/composite FKs, no provider SDK outside internal/llm, etc.

## Testing discipline

- Every sprint ends with a passing test suite and a green `make security-check`. Tests run as `app_user`
  (through PgBouncer once it exists); seeding runs as `app_migrator`.
- The tenant isolation test (`tests/tenant_isolation_test.go`) is a
  REQUIRED gate. It must pass before any business logic.
- Cross-tenant attempts are tested, not assumed away.
- AI business mutations are tested via the decision gate, never mocked past it.
- Tests assert behaviour, not ML quality. Model quality is a documented
  benchmark, not a pass/fail unit test.

## Acceptance vs ops gates

Each sprint prompt has:
- **Agent acceptance** — automatable; must be green before committing.
- **Ops exit gate** — needs a human, real infrastructure, or elapsed time
  (inbox placement, real calendars, DR drill by a non-author). Claude
  prepares scripts and checklists in docs/ops/ and does NOT claim these
  are done.

## Project layout

    .github/workflows/  CI (tests + security-check)
    cmd/api/            Go API entrypoint
    cmd/relay/          Outbox relay (0.2)
    cmd/import/         CSV import tool (1.4)
    cmd/worker/         Go Temporal worker host (2.0)
    cmd/sender/         Outbox consumer → Postal (2.2)
    cmd/decisiongate/   Decision gate service (3.2)
    internal/           Go modules (compiler enforces boundaries)
      db/ tenant/ authz/ platform/ outbox/ llm/ toolbus/ decisiongate/ skills/ ...
    migrations/         SQL migrations (golang-migrate format; never edit a
                        shipped migration — add a new one)
    tests/              Integration tests, RLS gate
    web/                Next.js app (1.3)
    agents/             Python agent runtime and ML workers
    deploy/             Docker Compose, Dockerfiles, initdb, grafana
    scripts/            restore-test.sh, warmup-check.sh, backup.sh
    docs/               runbooks; docs/ops/ for human exit-gate checklists

## Deploy order and backups

- Deploy: run migrations (as app_migrator) → wait for success → roll app
  containers. Migrations must be backward compatible with the previous app
  version (expand/contract).
- Backups (Sprint 1.4): nightly pg_dump → S3 (encrypted, 30-day retention)
  and daily EBS snapshots via DLM (7 daily, 4 weekly). Target RPO 24h,
  RTO 2h unless the enterprise agrees otherwise.
- Secrets (DKIM keys, OAuth refresh tokens, JWT signing keys, LLM keys) are
  encrypted with a KMS-managed data key; never stored plaintext.

## When in doubt

Ask. Do not invent architecture. If a task seems to require violating a
non-negotiable above, flag it before proceeding.
