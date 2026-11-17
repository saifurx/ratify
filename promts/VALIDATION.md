# Ratify sprint prompts: validation report

> **Status (2026-11-18):** all findings below have been applied to the prompt files. Originals are in `_originals/`. A new sprint `2.0` (Temporal, LLM gateway, secrets) was added; run order is now 0.1 → 0.1b → 0.2 → 1.1 → 1.2 → 1.3 → 1.4 → 2.0 → 2.1 → 2.2 → 2.3 → 3.1 → 3.2 → 3.3 → 4.1 → 4.2 → 5.1 → 5.2.

Scope: 0.md (CLAUDE.md), 0.1, 0.1b, 0.2, 1.1–1.4, 2.1–2.3, 3.1–3.3, 4.1, 4.2, 5.1, 5.2 (18 files).
Severity: **CRITICAL** = the sprint can't pass as written, or the design is broken. **HIGH** = a contradiction or gap that will cause rework or a compliance or security hole. **MED** = needs clarifying or tightening.

---

## A. Cross-cutting issues (fix in CLAUDE.md / sequencing)

| # | Sev | Issue | Fix |
|---|-----|-------|-----|
| A1 | CRITICAL | Every prompt says "Read CLAUDE.md", but the file is named `0.md`. | Save it as `CLAUDE.md` at the repo root. |
| A2 | CRITICAL | **Temporal is used in 2.1 (sequences) and 2.3 (calendar sync) but isn't deployed until 3.1.** | Move "deploy Temporal (+ its persistence DB, namespaces, queues)" into 2.1. Leave 3.1 with only the agent task queues. |
| A3 | CRITICAL | **The decision gate is referenced in 2.1 (AI draft "goes through the gate as READ") but is only built in 3.2.** | In 2.1, stub the draft endpoint behind a feature flag, or move the draft feature to 3.2. |
| A4 | CRITICAL | **Background workers vs FORCE RLS.** relay_user (0.2), retention jobs (4.1), warmup auto-pause (2.2), MAPE/forecast jobs (4.2), embedding backfill (3.3), and Temporal workers all read across tenants. With FORCE RLS and `current_tenant()` = NULL they see **zero rows**, so the 0.2 relay will never dispatch anything. | Add a "cross-tenant system roles" rule to CLAUDE.md. Either (a) the worker iterates tenants and calls `set_tenant()` per tx, or (b) a dedicated role gets a narrow, role-specific policy (e.g. `TO relay_user USING (true)` on outbox only). Never use BYPASSRLS on shared roles. |
| A5 | CRITICAL | **Memory budget.** CLAUDE.md says limits already sum to ~12.7 GB of 16 GB, with "do not add services without trimming". Later sprints add Kafka (2 GB), relay, Temporal + DB + UI, Postal (needs **MariaDB**, not Postgres), the decision gate, Next.js, Python agent/enrichment/scoring/ASR workers, Prometheus, Grafana, headless Chromium, FerrisKey, and an HNSW index on 4 tables. No sprint assigns limits. | Add a memory-budget table to CLAUDE.md (service → mem_limit/cpus per phase). Require each sprint to update it and to fail if the sum is over 14 GB. |
| A6 | HIGH | **t3.xlarge is burstable** (CPU credits). Load tests (1.4), CPU-only Whisper + pyannote (4.1) and XGBoost training will drain credits, after which the instance throttles to ~40% of a vCPU. | State "T3 Unlimited" or move to m6i/m7i.xlarge. Document this in CLAUDE.md. |
| A7 | HIGH | **What counts as an "AI write"?** Non-negotiable #2 says *no agent writes to CRM, no exceptions*. Yet these AI outputs are written without the gate: the lead score (1.2), the meeting prep brief (2.3, which says "no decision gate needed yet"), the methodology_assessment (3.3), embeddings (3.3), and the call Activity + `last_contact_at` (4.1). In 3.2 the lead-scoring agent is "read-only" but recomputes and stores a score. | Define two classes in CLAUDE.md. **Derived annotations** (scores, embeddings, briefs, assessments) are written to AI-owned columns or tables, version-stamped, and audit-logged. **Business mutations** (CRM records, outreach, quotes) always go through the gate. |
| A8 | HIGH | **Users, teams and in-tenant authorization don't exist.** `owner_id` is used everywhere, 1.3 needs rep/manager/admin plus "team", 2.3 has `team_id`, and 5.1 needs sales_manager/finance/legal. No sprint creates users/teams tables or enforces owner/team scope in the **API**. RLS only enforces the tenant, so "rep A can't see rep B's account" (1.3) would be UI-only. | Add users, teams, and roles in 1.1 (synced from FerrisKey claims). Enforce owner/team scope in the service layer (or a second RLS predicate on `app.user_id` / `app.role`). Use one role vocabulary everywhere. |
| A9 | HIGH | **No LLM or embedding provider is specified.** Used in 2.1, 2.3, 3.x, 4.1 and 5.1. `vector(1536)` implies OpenAI. 5.2 wants "LLM cost per tenant per day", but nothing meters it. | Add an LLM gateway section: provider and model, data-residency/PII policy for sending CRM data out, a timeout/fallback policy, and per-call cost metering to a table. |
| A10 | HIGH | **Backups are never created.** 1.4 restores "latest pg_dump from S3" and 5.2 restores an "EBS snapshot", but no sprint schedules pg_dump→S3 or a DLM snapshot policy. | Add both to 1.4 deliverables, with RPO targets. |
| A11 | HIGH | **PgBouncer appears only in the 5.2 checklist**, so it's never deployed. | Either deploy it (transaction mode) in 0.1/1.4 and test `set_tenant` through it, or remove it from the checklist. |
| A12 | MED | Some acceptance criteria are human or ops tasks an agent can't do, yet the prompts say "commit when green": live inbox placement (2.2), a real calendar (2.3), a staging quote-to-signature flow (5.1), "DR drill by someone who didn't write the script", "every runbook executed by a non-author", and "checklist 100%" (5.2). | Split each prompt into **agent acceptance** (automatable) and **ops exit gate** (a human checklist). |
| A13 | MED | Pinning is inconsistent. CLAUDE.md wants pinned versions, but 2.2 uses `postal:latest`. | Pin every image (Postal: `ghcr.io/postalserver/postal:<ver>`). |
| A14 | MED | CLAUDE.md's project layout misses `cmd/import`, `scripts/`, `docs/`, `internal/decisiongate`, `internal/toolbus` and `internal/skills`. 5.2 cites "migrations before app container, per CLAUDE.md", but CLAUDE.md never says that. | Update the layout and add a Deploy-order rule. |
| A15 | MED | Cross-tenant tables. The "global" unsubscribe list (2.2), the public `tenant_token`/booking-slug lookup (1.2, 2.3) and the Redis enrichment cache (1.2) are cross-tenant by nature. | Specify non-RLS tables or SECURITY DEFINER lookup functions explicitly, and prefix tenant-scoped Redis keys with the tenant id. |

---

## B. Per-sprint findings

### 0.1: Tenant isolation scaffold
- **HIGH** The acceptance test "grep `pgxpool` only hits internal/db and httpx.go" will fail, because `cmd/api/main.go` has to construct the pool. → Allow `cmd/`, or move pool construction into `internal/db.NewPool`.
- MED `AssertNotPrivileged` lives in `httpx.go` (HTTP helpers). It fits better in `internal/db`.
- MED `make test` doesn't say which role it connects as. → State that tests run as `app_user` and seed as `app_migrator`.

### 0.1b: Audit
- Good prompt. **MED** Add checks for: the API pool role ≠ table owner (a table owner bypasses grants), `set_tenant` is SECURITY INVOKER, and `current_tenant()` uses `current_setting('app.tenant_id', true)` (the missing_ok form) so it returns NULL instead of raising.

### 0.2: Kafka + outbox
- **CRITICAL** The relay can't see outbox rows under FORCE RLS (see A4).
- **CRITICAL** The exactly-once claim is false. A Postgres tx and a Kafka tx can't commit atomically. If the relay crashes *after* the Kafka commit but *before* the PG commit, the message is re-published on restart. An idempotent producer only dedupes retries within one producer session. → Put `outbox.id` in a Kafka header as the event id, have consumers use `isolation.level=read_committed` and dedupe on event id, and reword the acceptance test as "effectively-once at the consumer".
- **HIGH** The grants are inconsistent: `app_user` gets UPDATE on dispatched_at/attempts/last_error "because the relay uses these", but the relay runs as `relay_user`. → Drop app_user's UPDATE.
- **HIGH** Poison rows: rows over 10 attempts stay `dispatched_at IS NULL` and get re-read forever. → Filter `attempts < 10`, add a dead-letter state, and define per-key ordering behaviour.
- MED The grep test "no `kafka` outside outbox/relay" will hit config, compose, Makefile and tests. → Restrict it to Go imports in `internal/**` handlers.
- MED Typo "boker". Kafka 4.0.0 is fine, but say "pin" explicitly.

### 1.1: CRM entities
- **CRITICAL** **FKs bypass RLS.** A contact or opportunity can reference another tenant's `account_id`, and the FK check will pass. → Use composite FKs `(tenant_id, account_id) REFERENCES accounts(tenant_id, id)` plus a UNIQUE(tenant_id, id) on parents.
- **HIGH** Pipeline is "one per tenant" yet has `is_default bool`, which is contradictory. → Allow many pipelines with one partial-unique default.
- **HIGH** `owner_id` has no users table (A8). `last_contact_at` (used in 4.1) isn't defined. `citext` needs `CREATE EXTENSION citext`.
- MED GraphQL adds a second API surface and a new dependency. Confirm it's wanted in Phase 1.

### 1.2: Leads, consent, enrichment, scoring
- **HIGH** Returning `{"status":"suppressed"}` reveals suppression status to the sender (an enumeration leak) and contradicts "don't reveal why". → Return a response identical to success.
- **HIGH** The consent model is inconsistent. Conversion requires `consent_status='granted'`, but the lead has a `lawful_basis` field and 1.4 import accepts any lawful_basis (e.g. legitimate interest for B2B). → Define one rule: what lawful bases permit conversion and outreach.
- **HIGH** Fuzzy dedupe on (domain, name) merges *different emails* and unions their consent ledgers. Consent captured from email X would then apply to email Y. → Merge consent per email address only.
- MED The 5-second end-to-end target includes a cold crawl that respects robots.txt and rate limits, which isn't realistic. → Enrich asynchronously, and measure the 5 s budget without enrichment, or on a cache hit.
- MED "Domain age" needs WHOIS/RDAP. Say whether that counts as a "third-party API".
- MED `first_captured_at` is referenced but isn't in the model. The score endpoint sits under the Go `/api/v1/internal` path but is served by Python. Clarify routing and auth.
- MED XGBoost trained on synthetic data produces meaningless scores. Label it a "v0 heuristic" in the UI.

### 1.3: Next.js UI
- **HIGH** Rep, manager and admin visibility needs backend authorization (A8). The prompt says "don't add endpoints". Clarify that authz work is allowed and required.
- **HIGH** Full-text search needs tsvector columns plus a search endpoint, which means backend work.
- MED The FerrisKey tenant_id claim mapping (realm per tenant? a custom claim?) isn't defined.

### 1.4: Hardening
- **HIGH** As specified, the anomaly detector must look up the tenant of every id in every response. Under RLS that lookup sees nothing, and with privilege it's slow and itself a bypass. → Require every DTO to carry `tenant_id` and compare it in the middleware, with no DB lookup.
- **HIGH** Backups aren't created anywhere (A10).
- MED "Run the load test in CI nightly on t3.xlarge" needs CI infra that isn't defined. 10 leads/s × 5 min also triggers 3,000 crawls.
- MED The import tool doesn't say which role it connects as, or whether it emits outbox events or only audit events.

### 2.1: Sequence engine
- **CRITICAL** Temporal isn't deployed yet (A2), and the gate doesn't exist yet (A3).
- **HIGH** Throttle behaviour contradicts 2.2. 2.1 says "limit hit → reschedule", but 2.2 says "cap exceeded → REJECT, not defer". → Define both: the rate limit (per min/hour) reschedules; the daily warmup cap rejects and marks the enrollment with a visible status.
- **HIGH** There's no consent or `do_not_contact` check at enrollment or send (non-negotiable 5).
- MED "1-hour delays run in ~2 hours in test mode" contradicts the delay divisor. → Use the Temporal time-skipping test env.
- MED No sprint builds the consumer of `ratify.sequence.step` (2.2 never names the sender service).

### 2.2: Postal / deliverability
- **CRITICAL** **Unsubscribe compliance is missing.** Gmail and Yahoo bulk-sender rules (since Feb 2024) require the one-click `List-Unsubscribe` + `List-Unsubscribe-Post` headers (RFC 8058) and an unsubscribe endpoint. Neither is in any sprint.
- **HIGH** EC2 blocks outbound port 25 by default. You must request removal from AWS, and set reverse DNS (PTR) for the Elastic IP. Neither is mentioned.
- **HIGH** SES/Postmark failover sends from different IPs and infrastructure. That bypasses Postal's warmup state and needs separate DKIM/SPF alignment. → Either drop failover or treat it as a separately warmed stream.
- **HIGH** The auto-pause threshold at warmup volume is far too sensitive. At 100/day, one complaint in 7 days is 0.14%, so the domain pauses. → Add a minimum-sample rule (e.g. ≥1,000 sends in the window).
- **HIGH** The send gate checks suppression, unsubscribe and cap, but not contact `consent_status` / `do_not_contact`.
- MED Gmail provides no complaint FBL (only Postmaster Tools). Inbound replies need MX records for the reply domain. No sprint implements the DMARC p=none→quarantine→reject progression. DKIM "encrypted at rest" has no key-management choice (KMS?).
- MED "Real send reaches the inbox" can't be guaranteed in-sprint for a new domain. Make it an ops gate (A12).

### 2.3: Calendar / booking
- **HIGH** The `availability` table has no `tenant_id`, which violates the RLS rule. The `meeting` table is used but never defined.
- **HIGH** Hourly sync can't prevent double-booking. → Do a live free/busy query at booking time, plus a DB exclusion constraint (`tstzrange` + `EXCLUDE USING gist`) on meetings per user.
- **HIGH** The prep brief is an AI write marked "no gate needed" (A7).
- MED Google's calendar scopes need OAuth app verification (weeks of lead time). The CalDAV/ICS fallback is scope creep. Add DST test cases. `team_id` needs teams (A8).

### 3.1: Agent runtime
- **CRITICAL** **LangGraph inside a Temporal workflow breaks determinism.** Workflow code can't run LangGraph's executor. → Choose explicitly: (a) the workflow drives the graph loop itself, and each node is an Activity (LangGraph is then only a definition/router), or (b) the whole graph runs in one Activity (you lose per-node durability). The test "no duplicate tool invocations after crash" also needs idempotency keys per tool call, because Activities are at-least-once.
- **HIGH** The `agent_worker` DB role contradicts "tools are HTTP calls to the Go API". The Python runtime never touches the DB. → State that the Go API uses an `agent_worker` pool for service-JWT requests.
- **HIGH** `draft_email` is listed as a tool and called a write in the tests ("saved to sequence"), but 2.1 calls drafting a READ. → Split it into `draft_email` (read) and `save_draft_to_sequence` (write).
- MED Service JWT issuance (FerrisKey client credentials?) and how the tenant claim gets into it aren't specified.

### 3.2: Decision gate
- **HIGH** Internal contradiction: item 3 says NBA Accept "goes through the gate as a write by the rep", but item 4 says Accept is "a normal rep write, not an agent write". Pick one.
- **HIGH** Immutability: "no UPDATE/DELETE grants" doesn't stop the **table owner**. → Make the owner a NOLOGIN role and add a `BEFORE UPDATE OR DELETE` trigger that raises. Also add RLS on `tenant_id`.
- **HIGH** The `input_hash` canonicalization (canonical JSON form) is unspecified, so token matching will be flaky. The token signing key and its rotation are unspecified.
- MED "agent_worker role must have the tool's permission" conflates a DB role with app permissions. The gate service's own DB role isn't defined. "Human-approved" exists (3.3) but there's no approval queue/UI anywhere.
- MED The 3.2 rule stubs ("Phase 5 fills margin/discount") overlap with the separate CPQ rules engine in 5.1. Define the relationship.

### 3.3: Skills, Agent Studio, pgvector
- **HIGH** `hnsw.iterative_scan` is a **session GUC**. Setting it per session conflicts with CLAUDE.md's "never SET SESSION" rule under pooling. → Use `SET LOCAL` inside TenantTx. It's also an option, not a "requirement" of pgvector 0.8.
- **HIGH** "BANT runs on lead stage changes", but leads have no stage in 1.2.
- **HIGH** MEDDPICC triggers on the stage names Discovery, Proposal and Negotiation, but stages are tenant-configurable. → Add a stage→methodology-phase mapping.
- MED "Skills invoked by the decision gate" turns a deterministic rule engine into an orchestrator. Prefer agents or activities only. The embedding provider and its data egress aren't specified (A9). HNSW memory on 4 tables needs to go into the budget (A5).

### 4.1: Calls / ASR
- **HIGH** On CPU, faster-whisper plus pyannote won't do 10 minutes of audio in under 3 minutes on 4 burstable vCPUs alongside everything else, and needs about 3–6 GB of RAM. → Use hosted ASR, a GPU sidecar, or relax the target.
- **HIGH** The audio is kept unredacted for 90 days. If callers read card numbers aloud, that puts the system in PCI scope. → Shorten audio retention or gate it by consent and purpose.
- MED The LLM summary must use the *redacted* transcript. Zoom, Teams and Meet webhooks each need an app registration, which is scope creep. → Start with upload only. `last_contact_at` isn't defined (1.1). The Activity write is AI-generated (A7).

### 4.2: Forecasting
- **HIGH** The unique constraint `tenant+date+dimension` must include `dimension_value`. Otherwise only one rep snapshot per day is possible.
- **HIGH** A pooled cross-tenant model learns from other tenants' data, which conflicts with tenant isolation (and needs a cross-tenant reader). → Make it opt-in per tenant, or limit it to a single-enterprise deployment, and document that.
- MED "MAPE < 20% on synthetic data" is a self-fulfilling or flaky test. → Assert the pipeline runs and that MAPE is computed correctly; benchmark separately. Also block UPDATE on snapshots via grants or a trigger, not only the unique constraint.

### 5.1: CPQ
- **CRITICAL** "Approval if margin < Y%" needs product **cost**, which no table has. → Add `cost_cents` to `price_book_entry` or `product`.
- **HIGH** `price_book_entry` and `quote_line` have no `tenant_id`, which violates the RLS rule (and the composite-FK fix from 1.1).
- **HIGH** Native e-sign: legal validity varies by jurisdiction (ESIGN/UETA, eIDAS, India IT Act §3A). → Add consent-to-e-sign, signer identity verification, and a tamper-evident audit certificate, or integrate a provider.
- MED `tax_code` exists but there's no tax computation. The approver roles (sales_manager/finance/legal) aren't in the 1.3 role set. Headless Chromium needs to go into the memory budget.

### 5.2: Hardening / handover
- **HIGH** Several checklist items cite things never built: PgBouncer (A11), LLM cost per tenant (A9), and "migrations before app per CLAUDE.md" (A14).
- **HIGH** Human-only steps are mixed into an agent prompt (A12).
- MED "Warmup ≥4 weeks before pilot" is fine, but align it to CLAUDE.md's 4–6 weeks. Check that FerrisKey actually supports admin 2FA and session timeout before making them hard criteria. Add **gate down = fail closed** explicitly to INCIDENT.md.

---

## C. Suggested additions to CLAUDE.md
1. Cross-tenant system-role policy (A4).
2. Memory/CPU budget table, updated per sprint (A5, A6).
3. AI write classes: derived annotations vs business mutations (A7).
4. Users, teams and roles model, plus in-tenant authz rule (A8).
5. LLM/embedding provider, data egress, and cost metering (A9).
6. Composite tenant FKs rule: every child table has `tenant_id`, and FKs include it (1.1, 2.3, 5.1).
7. `SET LOCAL` only, for *any* GUC (3.3).
8. Event ids plus consumer-side dedupe (0.2).
9. Deploy order (migrate → app) and the backup/RPO policy (A10, A14).
10. Agent acceptance vs ops exit-gate split (A12).

## D. Suggested re-sequencing
- 0.1 → 0.1b → 0.2 (with A4 fix) → **1.1 (+ users/teams/authz)** → 1.2 → 1.3 → 1.4 (+ backups, PgBouncer)
- **2.0 (new): Temporal deploy + LLM gateway + memory budget**
- 2.1 (draft endpoint stubbed) → 2.2 (+ List-Unsubscribe, port 25/PTR) → 2.3
- 3.1 → 3.2 (enable the 2.1 draft through the gate) → 3.3 → 4.1 → 4.2 → 5.1 → 5.2
