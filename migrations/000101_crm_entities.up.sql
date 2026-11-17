-- Sprint 1.1: CRM entities. Every FK between tenant tables is COMPOSITE
-- (tenant_id, x_id) -> x(tenant_id, id): plain FKs bypass RLS.
ALTER TABLE accounts
    ADD COLUMN industry        text,
    ADD COLUMN employee_count  int CHECK (employee_count IS NULL OR employee_count >= 0),
    ADD COLUMN website         text,
    ADD COLUMN owner_id        uuid,
    ADD COLUMN last_contact_at timestamptz,
    ADD COLUMN updated_at      timestamptz NOT NULL DEFAULT now(),
    ADD FOREIGN KEY (tenant_id, owner_id) REFERENCES users (tenant_id, id);
CREATE INDEX accounts_tenant_owner_idx ON accounts (tenant_id, owner_id, created_at DESC);
CREATE TRIGGER accounts_updated BEFORE UPDATE ON accounts FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE contacts (
    id                  uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    tenant_id           uuid NOT NULL REFERENCES tenants (id),
    first_name          text NOT NULL DEFAULT '',
    last_name           text NOT NULL DEFAULT '',
    email               citext,
    phone               text,
    title               text,
    account_id          uuid,
    owner_id            uuid,
    do_not_contact      boolean NOT NULL DEFAULT false,
    consent_status      text NOT NULL DEFAULT 'none' CHECK (consent_status IN ('none','granted','revoked')),
    lawful_basis        text CHECK (lawful_basis IN ('consent','legitimate_interest','contract')),
    consent_source      text,
    consent_captured_at timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, account_id) REFERENCES accounts (tenant_id, id),
    FOREIGN KEY (tenant_id, owner_id)   REFERENCES users (tenant_id, id)
);
CREATE INDEX contacts_tenant_created_idx ON contacts (tenant_id, created_at DESC, id DESC);
CREATE INDEX contacts_tenant_owner_idx   ON contacts (tenant_id, owner_id, created_at DESC);
CREATE INDEX contacts_tenant_account_idx ON contacts (tenant_id, account_id);
CREATE INDEX contacts_tenant_email_idx   ON contacts (tenant_id, email);

CREATE TABLE pipelines (
    id         uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    tenant_id  uuid NOT NULL REFERENCES tenants (id),
    name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id)
);
-- A tenant may have many pipelines but exactly one default.
CREATE UNIQUE INDEX pipelines_one_default ON pipelines (tenant_id) WHERE is_default;

CREATE TABLE stages (
    id          uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    tenant_id   uuid NOT NULL REFERENCES tenants (id),
    pipeline_id uuid NOT NULL,
    name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    ordinal     int NOT NULL,
    -- tenant-independent mapping later sprints rely on; names are free text
    phase       text NOT NULL CHECK (phase IN ('prospecting','discovery','proposal','negotiation','closed_won','closed_lost')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, pipeline_id, id),
    UNIQUE (tenant_id, pipeline_id, ordinal) DEFERRABLE INITIALLY IMMEDIATE,
    FOREIGN KEY (tenant_id, pipeline_id) REFERENCES pipelines (tenant_id, id)
);

CREATE TABLE opportunities (
    id              uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    tenant_id       uuid NOT NULL REFERENCES tenants (id),
    name            text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    account_id      uuid NOT NULL,
    pipeline_id     uuid NOT NULL,
    stage_id        uuid NOT NULL,
    amount_cents    bigint NOT NULL DEFAULT 0 CHECK (amount_cents >= 0),
    currency        text NOT NULL DEFAULT 'USD' CHECK (currency ~ '^[A-Z]{3}$'),
    close_date      date,
    owner_id        uuid,
    status          text NOT NULL DEFAULT 'open' CHECK (status IN ('open','won','lost')),
    last_contact_at timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, account_id) REFERENCES accounts (tenant_id, id),
    -- the stage must belong to the opportunity's pipeline
    FOREIGN KEY (tenant_id, pipeline_id, stage_id) REFERENCES stages (tenant_id, pipeline_id, id),
    FOREIGN KEY (tenant_id, owner_id) REFERENCES users (tenant_id, id)
);
CREATE INDEX opportunities_tenant_created_idx ON opportunities (tenant_id, created_at DESC, id DESC);
CREATE INDEX opportunities_tenant_owner_idx   ON opportunities (tenant_id, owner_id, status, created_at DESC);
CREATE INDEX opportunities_tenant_account_idx ON opportunities (tenant_id, account_id, status);

CREATE TABLE activities (
    id             uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    tenant_id      uuid NOT NULL REFERENCES tenants (id),
    type           text NOT NULL CHECK (type IN ('note','email_sent','email_received','call','meeting','task')),
    subject        text NOT NULL DEFAULT '',
    body           text NOT NULL DEFAULT '',
    occurred_at    timestamptz NOT NULL DEFAULT now(),
    contact_id     uuid,
    opportunity_id uuid,
    account_id     uuid NOT NULL,
    metadata       jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_by     text NOT NULL,         -- user id or 'ai:<producer>@<version>'
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id, account_id)     REFERENCES accounts (tenant_id, id),
    FOREIGN KEY (tenant_id, contact_id)     REFERENCES contacts (tenant_id, id),
    FOREIGN KEY (tenant_id, opportunity_id) REFERENCES opportunities (tenant_id, id)
);
CREATE INDEX activities_tenant_account_idx ON activities (tenant_id, account_id, occurred_at DESC);
CREATE INDEX activities_tenant_created_idx ON activities (tenant_id, created_at DESC, id DESC);

DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['contacts','pipelines','stages','opportunities','activities'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY %I ON %I USING (tenant_id = current_tenant()) WITH CHECK (tenant_id = current_tenant())', t || '_isolation', t);
    EXECUTE format('CREATE TRIGGER %I BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION set_updated_at()', t || '_updated', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO app_user', t);
  END LOOP;
END $$;
