# Ratify: optimized implementation plan

Source: all 20 files in `promts/` (CLAUDE.md = `0.md`, sprints 0.1 → 5.2, VALIDATION.md). `_originals/` was ignored.
The prompts are already well hardened (VALIDATION.md fixes are applied). This plan does not redesign anything. It re-orders, splits, parallelizes and closes the gaps that are still left between sprints.

---

## 1. Remaining gaps between prompts (fix before the sprint that hits them)

| # | Gap | Where it bites | Fix (put in the earliest sprint shown) |
|---|-----|----------------|----------------------------------------|
| G1 | **`audit_log` is never created.** "audit-logged" appears in 2.2, 3.2, 3.3, 4.1, 5.1 and CLAUDE.md. | Every later sprint invents its own table. | Add one `audit_log` table + `audit.Write(ctx, tx, ...)` helper in **1.1**. |
| G2 | **`ai_annotations` is first defined in 2.3**, but 3.2/3.3/4.1/5.1 also use it. | Migration ordering and ownership collisions. | Create it in **1.1** (or 2.0) next to `audit_log`, with `model_version`, `produced_by`. |
| G3 | **No notifications mechanism.** 2.2 ("alert/notification to sequence owner"), 5.1 (escalation) and 3.3 need one. | Each sprint invents its own. | Minimal `notifications` table + `notify.Send(ctx, tx, user, kind, payload)` + bell in the UI. Build in **2.0**, UI in 2.2. |
| G4 | **Python workers need a DB role but 3.1 says the Python runtime has none.** 1.2 pyworker scoring, 3.3 embedding backfill, 4.2 per-tenant forecasting and "runs under set_tenant" all need DB access. | Contradiction resolved ad hoc. | Decide in **1.2**: a dedicated `ml_worker` LOGIN role (non-owner, no BYPASSRLS, through PgBouncer, `set_config(...,true)` per tenant tx, grants per table). The `agents` service (3.1) stays DB-less. Add it to CLAUDE.md. |
| G5 | **`internal/db` has one pool type** but later sprints need relay_user (0.2), agent_worker (3.1), gate_user (3.2), ml_worker, temporal workers. `AssertNotPrivileged` hard-rejects table owners, so each binary needs its expected role. | Four sprints modify the boot check. | In **0.1** make `NewPool(role Role)` and `AssertNotPrivileged(expected Role)` take a role from an allow-list. Nothing outside `internal/db` ever builds a pool. |
| G6 | **0.1's grep acceptance vs tests.** The 4th isolation subtest ("raw query outside wrapper") needs a raw pool, but `grep -rl pgxpool ... | grep -v internal/db` must be empty. | Expose `db.RawPoolForTest` under a `//go:build dbtest` tag inside `internal/db`. Tests import that, never pgxpool. |
| G7 | **`lead.updated` is consumed by 3.3 but never emitted** (1.2 emits created/scored/converted only). | BANT trigger never fires. | Add `lead.updated` in 1.2's event list. |
| G8 | **Lead scoring exists twice**: 1.2 pyworker XGBoost endpoint and 3.2 "Lead Scoring Agent". | Two scoring paths, two model-version stores. | 3.2's agent should only call the 1.2 scorer through the `score_lead` tool, then write `lead_scores`. No second model. |
| G9 | **Tenant slug / token columns undefined.** 1.2 needs `tenant_token`, 2.3 needs `tenant_slug` (`/book/{tenant_slug}/...`). | Retro-migration of `tenants`. | Add `slug` (unique) and `public_token` to `tenants` in **1.1**, with the two SECURITY DEFINER resolvers. |
| G10 | **Shared S3 code** is needed in 1.4 (backups), 4.1 (audio), 5.1 (PDFs). | Three separate SSE-KMS implementations. | One `internal/blob` package (SSE-KMS, per-tenant prefix, presigned URLs) built in **2.0** with `internal/secrets`. 1.4's backup script is shell and stays independent. |
| G11 | **Budget table is missing services.** `cmd/sender` (2.2), `cmd/worker` (2.0 Go Temporal host), the `ml_worker` job runner, and the k6/backup one-shots are absent. | `make budget` passes while reality does not. | Add rows: sender 256m/0.25, worker 256m/0.25. See §4 for the arithmetic. |
| G12 | **Transactional confirmation emails (2.3) are "exempt from the warmup cap"**, but the cap in 2.2 is per `sending_domain`. | Confirmation emails either count against the cap or bypass it ad hoc. | In 2.2 make `stream` ('transactional' \| 'outbound') a column on `sending_domain`; cap and List-Unsubscribe apply only to `outbound`. Transactional still passes suppression/consent checks. |
| G13 | **CI is introduced only in 1.4** (nightly load). Static checks promised in 5.2 (`make security-check`) run only at the end. | Violations (SET SESSION, pgxpool, SDK imports, plain FKs) accumulate for 5 phases and become a big FAIL list in 5.2. | **Shift left**: GitHub Actions with Postgres/Kafka service containers in **0.1**, and a growing `make security-check` that each sprint extends (see §3). 5.2 then just completes and documents it. |
| G14 | **DTO `tenant_id` base type** is demanded in 1.4 for the anomaly detector. | Retrofitting every DTO from 1.1–1.3 in 1.4. | Define `httpx.TenantOwned` (embedded `tenant_id`) in **1.1** and use it from the first handler. |
| G15 | **Migration numbering collisions** if sprints run in parallel (000002, 000003...). | Merge conflicts, wrong order. | Reserve ranges: 0.x = 0001-0099, 1.x = 0100-0199, 2.x = 0200-0299, 3.x = 0300-0399, 4.x = 0400-0499, 5.x = 0500-0599. CLAUDE.md update. |
| G16 | **Bound the Postgres connection through PgBouncer from the start.** PgBouncer lands in 1.4, so 1.1-1.3 tests never exercise transaction pooling, and any GUC/session leak is found late. | Late discovery of pooling bugs. | Move PgBouncer (and the pool_size=2 concurrency test) to right after 0.2. See §3 (Step 4). |
| G17 | **Local ASR cannot fit the budget** (≥ 4 GB on top of ~12.9 GB committed = ~17 GB). | 4.1 "decision required first" has only one viable answer. | Pre-decide: **hosted ASR** is the default and the plan. Keep the `Transcriber` interface; local ASR requires a separate larger instance. |

---

## 2. Dependency graph (real, not just numeric order)

```
0.1 ─ 0.1b ─ 0.2 ─┬─ PgBouncer(1.4a) ─┐
                  └─ 1.1 ─┬─ 1.2 ─────┼─ 1.3 (UI)  ─┐
                          │           └─ 1.4b       │
                          └─ [Phase-1 exit gate: restore passes]
2.0 (Temporal, LLM, secrets, blob, notify) needs only 1.1 + 1.4a
   ├─ 2.1 ─ 2.2 ─┐                 (2.2 has the longest lead time → start early)
   ├─ 2.3 ───────┤   needs 2.0 + 2.2's sender for confirm email
   ├─ 3.1 ─ 3.2 ─ 3.3
   ├─ 4.1  (needs 2.0, 1.1; independent of 3.x)
   └─ 4.2  (needs 2.0, 1.1; independent of 3.x)
5.1 needs 3.2 (gate hook) + 2.2 (email) + 2.3's e-sign mail path
5.2 last
```

Genuinely independent pairs (can be built in parallel worktrees):

- 1.3 (UI) ∥ 1.2 tail and 1.4b (after 1.1's API is stable)
- 2.0 ∥ 1.3/1.4b (no shared files except Compose + CLAUDE.md budget table)
- 2.3 ∥ 2.1/2.2 (until the confirmation-email hookup)
- 3.1 → 3.2 → 3.3 ∥ 4.1 ∥ 4.2

---

## 3. Optimized sequence (22 work units instead of 18 monolithic prompts)

Rules for parallel work: separate git worktrees; one writer per file set; **serialize merges of `docker-compose.yml`, `Makefile`, CLAUDE.md budget table and `migrations/` ranges** (G15). Run `make budget` after every merge.

### Phase 0: Foundation (serial)
1. **0.1** with G5/G6 baked in; add GitHub Actions + `make security-check` v0 (grep checks: no `pgxpool` outside `internal/db`, no `SET SESSION`/`set_config(...,false)`, every compose service has `mem_limit`+`cpus`).
2. **0.1b** audit: keep it, but run as a 15-minute checklist pass (most items are already tests). Output: PASS/FAIL table.
3. **0.2** Kafka + outbox (+ `security-check` additions: Kafka client imports only in outbox/events/relay).

### Phase 1: CRM core
4. **1.4a (pulled forward)**: PgBouncer + pool_size=2 isolation test, Prometheus `/metrics` skeleton. From here every test runs through PgBouncer. *(Backups and the rest of 1.4 stay later.)*
5. **1.1** CRM + users/teams/authz, **plus** G1, G2, G9, G14 (audit_log, ai_annotations, tenant slug/token, `TenantOwned` DTO base). Decide GraphQL now: it is the only extra API surface and dependency in Phase 1; keep it for the single account-360 query as specified.
6. **1.2** leads/consent/enrichment/scoring, plus G4 (`ml_worker` role), G7 (`lead.updated`).
7. **1.3** UI. Can start as soon as 1.1's REST + authz tests are green; consume the OpenAPI spec so UI work does not wait for 1.2.
8. **1.4b**: backups, DR runbook + `restore-test.sh`, cross-tenant detector, k6, import tool, Grafana. **Phase-1 exit gate: restore-test passes** (as specified).

### Phase 2: Outreach (the deliverability clock is the critical path)
9. **Week 1, in parallel with Phase 0 (human tasks, no code):** buy/choose sending domains, request AWS port-25 removal, Elastic IP + PTR, Google OAuth verification + Microsoft app registration, LLM provider DPA, hosted-ASR provider DPA. Put these into `docs/ops/` now so they can start immediately; they have weeks of lead time.
10. **2.0** platform: Temporal, LLM gateway, secrets, **plus** `internal/blob` (G10) and `notifications` (G3). Test through PgBouncer; add temporal + worker rows to the budget (G11).
11. **2.1** sequences. Stub the AI draft behind a flag exactly as designed; it is a READ.
12. **2.2a** sender + send gate + unsubscribe (items 1-4 and tests for them). Everything that can lose reputation lands first.
13. **2.2b** warmup engine, feedback loops, reply detection, warmup-check script. **Start real warmup as soon as 2.2a+b pass on staging**, not at the end of Phase 5.
14. **2.3** calendar/booking (independent of 2.1/2.2 until the confirmation email). Uses G12's transactional stream.

### Phase 3: Agents (serial inside; parallel with Phase 4)
15. **3.1** agent runtime. Ordering inside: tool bus + `agent_worker` DB role first, then Python worker + `AgentRunWorkflow`.
16. **3.2a** decision gate service, token (JCS shared vectors in Go and Python first), immutable `decision_records`. This is the Phase-3 exit item; do not start 3.2b until the immutability and token tests pass.
17. **3.2b** the three agents, approval inbox, Accept/reject, enable `save_draft_to_sequence` (G8: scoring agent reuses the 1.2 scorer).
18. **3.3** skills + Agent Studio + pgvector. Budget the HNSW index. Do the recall benchmark in the first commit, because filtered recall decides whether Qdrant is needed.

### Phase 4: Intelligence (parallel with 3.x once 2.0 exists)
19. **4.1** calls/ASR with hosted transcriber (G17). Presidio redaction before anything is stored or sent to an LLM.
20. **4.2** forecasting. Per-tenant models only; pooled training stays off.

### Phase 5
21. **5.1a** pricing, quote, rules engine (`internal/cpq/rules`, also the gate's `BusinessRule`), Temporal approval. **5.1b** proposal PDF + e-sign (chromium `pdf` service, `docs/ESIGN.md`).
22. **5.2** finalize `make security-check` (most checks already exist from G13), runbooks, dashboards, restore RTO/RPO, `PILOT_CHECKLIST.md` (left blank for humans).

---

## 4. Resource arithmetic (CLAUDE.md budget is tighter than it looks)

| Item | Value |
|------|-------|
| Sum of `mem_limit` in the CLAUDE.md table (excluding ASR) | **12.56 GiB** (12,864 MB) |
| Missing rows: sender + Go worker (256m each) | + 0.5 GiB → **13.06 GiB** |
| Cap in CLAUDE.md | 14 GiB, which leaves **~2 GiB for the OS, page cache and spikes** on a 16 GiB host |
| Sum of `cpus` caps | **7.75 vCPU** committed against 4 vCPU (limits are caps, but contention is real during load tests) |
| Local ASR (≥ 4g) | Does not fit. Use hosted. |

Recommendations: (a) make `make budget` fail above **13.5 GiB** rather than 14 so Postgres page cache survives; (b) set Postgres `shared_buffers` ≈ 1 GiB, `effective_cache_size` ≈ 4 GiB, `max_connections` small (PgBouncer fronts it), and size Temporal/Kafka JVM heaps explicitly; (c) record a `docker stats` snapshot in every sprint that adds a service (0.2 already does).

---

## 5. Splitting oversized sprints (faster review, safer commits)

| Sprint | Why | Split |
|--------|-----|-------|
| 1.4 | PgBouncer is needed 4 sprints earlier; the rest is hardening | 1.4a (early) / 1.4b |
| 2.2 | Highest risk; ~9 deliverables | 2.2a send gate + unsubscribe + sender; 2.2b warmup, FBL webhooks, reply detection |
| 3.2 | The gate is the architectural centrepiece; agents are not | 3.2a gate + records + tokens + immutability; 3.2b agents + inbox + accept/reject |
| 5.1 | CPQ + PDF + e-sign are three subsystems | 5.1a pricing/rules/approval; 5.1b proposal/e-sign |

Each split piece keeps the original "agent acceptance" subset that applies to it, and commits separately.

---

## 6. Static checks to add incrementally (replaces one big 5.2 step)

`make security-check` is born in 0.1 and extended per sprint; CI runs it on every PR.

| Added in | Check |
|----------|-------|
| 0.1 | no `pgxpool` outside `internal/db`; no `SET SESSION` / `set_config(..., false)`; compose has `mem_limit`+`cpus`+pinned image tag |
| 0.2 | Kafka client imports only in `internal/outbox`, `internal/events`, `cmd/relay` |
| 1.1 | SQL lint: every `CREATE TABLE` with `tenant_id` has ENABLE+FORCE RLS, `UNIQUE (tenant_id,id)`, and composite FKs; no `WHERE tenant_id` in repositories |
| 1.4b | DTO coverage test (`tenant_id` present); `cross_tenant_leak_attempt` test |
| 2.0 | no provider SDK imports outside `internal/llm` / `agents/llm.py` |
| 3.2a | immutability tests for `gate.decision_records`; only listed cross-tenant policies exist |
| 4.2 | immutability test for `forecast_snapshot` |
| 5.2 | gitleaks, govulncheck, pip-audit, npm audit, FerrisKey config checks |

---

## 7. Process optimizations for running the prompts

1. **Update CLAUDE.md before Sprint 0.1:** save as `CLAUDE.md` (A1), add G4 (`ml_worker`), G5 (role-aware `NewPool`), G11 (budget rows), G15 (migration ranges). All later prompts reference it, so this is the highest-leverage edit.
2. **Keep each prompt's "Agent acceptance" and "Ops exit gate" as the definition of done.** Never mark ops gates complete; prepare `docs/ops/*` only.
3. **One session per work unit** with a fresh context; each prompt is self-contained and CLAUDE.md carries the rules. Use higher effort for 0.1, 2.2a, 3.2a and 5.1b (security- and compliance-critical); standard effort for UI, import tool, dashboards, runbooks.
4. **Gate between phases** with a human check: Phase 1 exit = restore passes; Phase 2 exit = 2.2 and 2.3 ops gates; Phase 3 exit = 3.2a immutability and token tests.
5. **Seed once, reuse:** `make seed-demo` (1.3) should be extended by later sprints (sequences, calls, quotes) instead of each creating its own fixtures. Semantic-search, load, and forecast benchmarks all use it.
6. **Fake-by-default in tests:** `LLM_PROVIDER=fake`, mocked Postal/Google/Microsoft, Temporal time-skipping. No test depends on wall-clock time or an external service; real-service checks are ops gates.

---

## 8. Critical path and what to start today

Critical path = **domain/DNS + AWS port-25 + Postal warmup (4-6 weeks)** and **Google/Microsoft OAuth verification (weeks)**. The code sprints are shorter than these waits. So:

- Day 1: start every human task listed in step 9 above.
- Engineering order: 0.1 → 0.1b → 0.2 → 1.4a → 1.1 → 1.2 → (1.3 ∥ 1.4b) → 2.0 → 2.1 → 2.2a/b → start warmup → (2.3 ∥ 3.1…3.3 ∥ 4.1 ∥ 4.2) → 5.1 → 5.2.
- The pilot cannot start before warmup finishes, so any slip in 2.2 slips the pilot by the same amount; protect 2.2 and its ops prerequisites above all else.
