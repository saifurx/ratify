-- Sprint 1.2: inbound leads, consent ledger, suppression, enrichment, scores.

CREATE TABLE leads (
    id                       uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    tenant_id                uuid NOT NULL REFERENCES tenants (id),
    email                    citext NOT NULL,
    first_name               text NOT NULL DEFAULT '',
    last_name                text NOT NULL DEFAULT '',
    company                  text,
    domain                   text,
    source                   text NOT NULL DEFAULT 'web_form',
    source_detail            jsonb NOT NULL DEFAULT '{}'::jsonb,
    consent_status           text NOT NULL DEFAULT 'none' CHECK (consent_status IN ('none','granted','revoked')),
    lawful_basis             text CHECK (lawful_basis IN ('consent','legitimate_interest','contract')),
    consent_source           text,
    consent_captured_at      timestamptz,
    raw_payload              jsonb NOT NULL DEFAULT '{}'::jsonb,
    first_captured_at        timestamptz NOT NULL DEFAULT now(),
    possible_duplicate_of    uuid,
    converted_at             timestamptz,
    converted_account_id     uuid,
    converted_contact_id     uuid,
    converted_opportunity_id uuid,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    deleted_at               timestamptz,   -- soft delete only; leads are never hard-deleted
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, possible_duplicate_of)    REFERENCES leads (tenant_id, id),
    FOREIGN KEY (tenant_id, converted_account_id)     REFERENCES accounts (tenant_id, id),
    FOREIGN KEY (tenant_id, converted_contact_id)     REFERENCES contacts (tenant_id, id),
    FOREIGN KEY (tenant_id, converted_opportunity_id) REFERENCES opportunities (tenant_id, id)
);
CREATE UNIQUE INDEX leads_tenant_email_active ON leads (tenant_id, email) WHERE deleted_at IS NULL;
CREATE INDEX leads_tenant_created_idx ON leads (tenant_id, created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX leads_tenant_domain_idx  ON leads (tenant_id, lower(domain)) WHERE deleted_at IS NULL;
CREATE TRIGGER leads_updated BEFORE UPDATE ON leads FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Consent is keyed by EMAIL and append-only. status 'none' means "no consent
-- statement was made" and never overrides an earlier granted/revoked.
CREATE TABLE consent_ledger (
    id            uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    tenant_id     uuid NOT NULL REFERENCES tenants (id),
    email         citext NOT NULL,
    lead_id       uuid,
    contact_id    uuid,
    status        text NOT NULL CHECK (status IN ('none','granted','revoked')),
    lawful_basis  text CHECK (lawful_basis IN ('consent','legitimate_interest','contract')),
    source        text NOT NULL,
    captured_at   timestamptz NOT NULL DEFAULT now(),
    ip            text,
    user_agent    text,
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, lead_id)    REFERENCES leads (tenant_id, id),
    FOREIGN KEY (tenant_id, contact_id) REFERENCES contacts (tenant_id, id)
);
CREATE INDEX consent_ledger_email_idx ON consent_ledger (tenant_id, email, captured_at DESC, id DESC);
CREATE TRIGGER consent_ledger_append_only BEFORE UPDATE OR DELETE ON consent_ledger
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

CREATE TABLE suppression_list (
    id        uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    email     citext,
    domain    text,
    reason    text NOT NULL CHECK (reason IN ('hard_bounce','complaint','opt_out','manual')),
    added_by  text NOT NULL,
    added_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    CHECK ((email IS NOT NULL) <> (domain IS NOT NULL))
);
CREATE UNIQUE INDEX suppression_email_idx  ON suppression_list (tenant_id, email)       WHERE email IS NOT NULL;
CREATE UNIQUE INDEX suppression_domain_idx ON suppression_list (tenant_id, lower(domain)) WHERE domain IS NOT NULL;

-- Derived annotation (CLAUDE.md "AI write classes"): append-only, versioned,
-- reproducible from (features, model_version).
CREATE TABLE lead_scores (
    id            uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    tenant_id     uuid NOT NULL REFERENCES tenants (id),
    lead_id       uuid NOT NULL,
    score         int NOT NULL CHECK (score BETWEEN 0 AND 100),
    reasons       jsonb NOT NULL DEFAULT '[]'::jsonb,
    features      jsonb NOT NULL DEFAULT '{}'::jsonb,
    model_version text NOT NULL,
    produced_by   text NOT NULL,
    produced_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, lead_id) REFERENCES leads (tenant_id, id)
);
CREATE INDEX lead_scores_lead_idx ON lead_scores (tenant_id, lead_id, produced_at DESC);
CREATE TRIGGER lead_scores_append_only BEFORE UPDATE OR DELETE ON lead_scores
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

-- Provenance only: a hash of the fetched HTML plus the parsed fields; the raw
-- HTML itself is never stored.
CREATE TABLE enrichment_raw (
    id          uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    tenant_id   uuid NOT NULL REFERENCES tenants (id),
    lead_id     uuid NOT NULL,
    domain      text NOT NULL,
    status      text NOT NULL CHECK (status IN ('ok','blocked_robots','blocked_network','error','cached')),
    http_status int,
    html_sha256 text,
    parsed      jsonb NOT NULL DEFAULT '{}'::jsonb,
    fetched_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, lead_id) REFERENCES leads (tenant_id, id)
);
CREATE INDEX enrichment_raw_lead_idx ON enrichment_raw (tenant_id, lead_id, fetched_at DESC);

DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['leads','consent_ledger','suppression_list','lead_scores','enrichment_raw'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY %I ON %I USING (tenant_id = current_tenant()) WITH CHECK (tenant_id = current_tenant())', t || '_isolation', t);
  END LOOP;
END $$;

GRANT SELECT, INSERT, UPDATE ON leads TO app_user;          -- no DELETE: soft delete only
GRANT SELECT, INSERT ON consent_ledger, lead_scores, enrichment_raw TO app_user;
GRANT SELECT, INSERT, UPDATE ON suppression_list TO app_user;

-- ml_worker (tenant-scoped; NOT a cross-tenant role): least privilege.
GRANT USAGE ON SCHEMA public TO ml_worker;
GRANT SELECT ON leads TO ml_worker;
GRANT SELECT (id, tenant_id, domain) ON accounts TO ml_worker;
GRANT SELECT (id, tenant_id, account_id, status) ON opportunities TO ml_worker;
GRANT SELECT, INSERT ON enrichment_raw, lead_scores, processed_events TO ml_worker;
GRANT INSERT ON audit_log TO ml_worker;
GRANT INSERT ON outbox TO ml_worker;
CREATE POLICY outbox_ml_worker ON outbox TO ml_worker
    USING (tenant_id = current_tenant()) WITH CHECK (tenant_id = current_tenant());
