-- Sprint 1.1 (range 0100-0199). Shared platform tables + identity.
CREATE EXTENSION IF NOT EXISTS citext;

CREATE FUNCTION set_updated_at() RETURNS trigger LANGUAGE plpgsql AS
$$ BEGIN NEW.updated_at = now(); RETURN NEW; END $$;

-- ---- tenant public identifiers + narrow cross-tenant lookups -------------
ALTER TABLE tenants
    ADD COLUMN slug         citext UNIQUE,
    ADD COLUMN public_token text   UNIQUE
        DEFAULT replace(gen_random_uuid()::text, '-', '') || replace(gen_random_uuid()::text, '-', '');

CREATE POLICY tenants_resolver ON tenants TO tenant_resolver USING (true);
GRANT USAGE ON SCHEMA public TO tenant_resolver;
GRANT SELECT (id, slug, public_token) ON tenants TO tenant_resolver;

CREATE FUNCTION resolve_tenant_token(tok text) RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS
$$ SELECT id FROM public.tenants WHERE public_token = tok $$;
CREATE FUNCTION resolve_tenant_slug(s text) RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS
$$ SELECT id FROM public.tenants WHERE slug = s::citext $$;
ALTER FUNCTION resolve_tenant_token(text) OWNER TO tenant_resolver;
ALTER FUNCTION resolve_tenant_slug(text) OWNER TO tenant_resolver;
REVOKE ALL ON FUNCTION resolve_tenant_token(text), resolve_tenant_slug(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION resolve_tenant_token(text), resolve_tenant_slug(text) TO app_user;

-- ---- users, teams ---------------------------------------------------------
CREATE TABLE users (
    id               uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    tenant_id        uuid NOT NULL REFERENCES tenants (id),
    external_subject text NOT NULL,                     -- FerrisKey `sub`
    email            citext,
    display_name     text,
    role             text NOT NULL CHECK (role IN ('rep','manager','admin','finance','legal')),
    active           boolean NOT NULL DEFAULT true,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, external_subject)
);
CREATE TABLE teams (
    id              uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    tenant_id       uuid NOT NULL REFERENCES tenants (id),
    name            text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    manager_user_id uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, name),
    FOREIGN KEY (tenant_id, manager_user_id) REFERENCES users (tenant_id, id)
);
CREATE TABLE team_members (
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    team_id   uuid NOT NULL,
    user_id   uuid NOT NULL,
    PRIMARY KEY (tenant_id, team_id, user_id),
    FOREIGN KEY (tenant_id, team_id) REFERENCES teams (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);
CREATE INDEX team_members_user_idx ON team_members (tenant_id, user_id);
CREATE INDEX teams_manager_idx ON teams (tenant_id, manager_user_id);
CREATE TRIGGER users_updated BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER teams_updated BEFORE UPDATE ON teams FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---- audit_log (append-only) and ai_annotations --------------------------
CREATE TABLE audit_log (
    id           uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    tenant_id    uuid NOT NULL REFERENCES tenants (id),
    actor        text NOT NULL,            -- user id, 'ai:<producer>@<version>' or 'system'
    action       text NOT NULL,
    subject_type text NOT NULL,
    subject_id   uuid,
    before       jsonb,
    after        jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id)
);
CREATE INDEX audit_log_subject_idx ON audit_log (tenant_id, subject_type, subject_id, created_at);
CREATE FUNCTION forbid_mutation() RETURNS trigger LANGUAGE plpgsql AS
$$ BEGIN RAISE EXCEPTION '% on % is not allowed (append-only)', TG_OP, TG_TABLE_NAME USING ERRCODE = '42501'; END $$;
CREATE TRIGGER audit_log_append_only BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

CREATE TABLE ai_annotations (
    id            uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    tenant_id     uuid NOT NULL REFERENCES tenants (id),
    subject_type  text NOT NULL,
    subject_id    uuid NOT NULL,
    kind          text NOT NULL,
    content       jsonb NOT NULL,
    model_version text NOT NULL,
    produced_by   text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id)
);
CREATE INDEX ai_annotations_subject_idx ON ai_annotations (tenant_id, subject_type, subject_id, kind, created_at);

DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['users','teams','team_members','audit_log','ai_annotations'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY %I ON %I USING (tenant_id = current_tenant()) WITH CHECK (tenant_id = current_tenant())', t || '_isolation', t);
  END LOOP;
END $$;

GRANT SELECT, INSERT, UPDATE ON users TO app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON teams, team_members TO app_user;
GRANT SELECT, INSERT ON audit_log, ai_annotations TO app_user;
