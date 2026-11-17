-- Sprint 0.1: tenant isolation primitives, tenants, accounts.
-- Runs as app_migrator, which therefore owns every object created here.

-- Transaction-scoped tenant binding. The third argument (true) of set_config
-- makes the setting local to the current transaction, so it can never leak
-- across pooled connections. SECURITY INVOKER (the default) is intentional.
CREATE FUNCTION set_tenant(t uuid) RETURNS void
LANGUAGE sql VOLATILE SECURITY INVOKER AS
$$ SELECT set_config('app.tenant_id', t::text, true) $$;

-- missing_ok = true: returns NULL instead of raising when the setting was
-- never defined; nullif covers the empty string left after a tx ends.
CREATE FUNCTION current_tenant() RETURNS uuid
LANGUAGE sql STABLE SECURITY INVOKER AS
$$ SELECT nullif(current_setting('app.tenant_id', true), '')::uuid $$;

CREATE TABLE tenants (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenants FORCE ROW LEVEL SECURITY;
CREATE POLICY tenants_isolation ON tenants
    USING (id = current_tenant())
    WITH CHECK (id = current_tenant());

CREATE TABLE accounts (
    id         uuid NOT NULL DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants (id),
    name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    domain     text,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id),
    UNIQUE (tenant_id, id)
);
CREATE INDEX accounts_tenant_created_idx ON accounts (tenant_id, created_at);
CREATE INDEX accounts_tenant_domain_idx ON accounts (tenant_id, lower(domain));
ALTER TABLE accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE accounts FORCE ROW LEVEL SECURITY;
CREATE POLICY accounts_isolation ON accounts
    USING (tenant_id = current_tenant())
    WITH CHECK (tenant_id = current_tenant());

GRANT USAGE ON SCHEMA public TO app_user;
GRANT SELECT ON tenants TO app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON accounts TO app_user;
