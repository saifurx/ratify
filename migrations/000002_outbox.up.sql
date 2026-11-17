-- Sprint 0.2: transactional outbox + consumer dedupe table.
-- Delivery is AT-LEAST-ONCE; consumers dedupe on event_id (= outbox.id).

CREATE TABLE outbox (
    id               uuid PRIMARY KEY,
    seq              bigint GENERATED ALWAYS AS IDENTITY,  -- strict per-key ordering
    tenant_id        uuid NOT NULL REFERENCES tenants (id),
    topic            text NOT NULL,
    key              text,
    payload          jsonb NOT NULL,
    headers          jsonb,
    created_at       timestamptz NOT NULL DEFAULT now(),
    dispatched_at    timestamptz,
    attempts         int NOT NULL DEFAULT 0,
    last_error       text,
    dead_lettered_at timestamptz,
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),   -- retry backoff
    UNIQUE (tenant_id, id)
);
CREATE INDEX outbox_pending_idx ON outbox (seq)
    WHERE dispatched_at IS NULL AND dead_lettered_at IS NULL;
CREATE INDEX outbox_pending_key_idx ON outbox (key, seq)
    WHERE dispatched_at IS NULL AND dead_lettered_at IS NULL;
CREATE INDEX outbox_tenant_created_idx ON outbox (tenant_id, created_at);

ALTER TABLE outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbox FORCE ROW LEVEL SECURITY;

CREATE POLICY outbox_tenant ON outbox TO app_user
    USING (tenant_id = current_tenant())
    WITH CHECK (tenant_id = current_tenant());

-- The ONLY cross-tenant policy in this sprint (CLAUDE.md "Cross-tenant
-- system roles"): the relay must read every tenant's pending rows.
CREATE POLICY outbox_relay ON outbox TO relay_user
    USING (true) WITH CHECK (true);

GRANT SELECT, INSERT ON outbox TO app_user;
GRANT USAGE ON SCHEMA public TO relay_user;
GRANT SELECT ON outbox TO relay_user;
GRANT UPDATE (dispatched_at, attempts, last_error, dead_lettered_at, next_attempt_at)
    ON outbox TO relay_user;

CREATE TABLE processed_events (
    tenant_id    uuid NOT NULL REFERENCES tenants (id),
    consumer     text NOT NULL,
    event_id     uuid NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, consumer, event_id)
);
ALTER TABLE processed_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE processed_events FORCE ROW LEVEL SECURITY;
CREATE POLICY processed_events_isolation ON processed_events
    USING (tenant_id = current_tenant())
    WITH CHECK (tenant_id = current_tenant());
GRANT SELECT, INSERT ON processed_events TO app_user;
