DROP TABLE IF EXISTS ai_annotations, audit_log, team_members, teams, users;
DROP FUNCTION IF EXISTS forbid_mutation();
DROP FUNCTION IF EXISTS resolve_tenant_slug(text);
DROP FUNCTION IF EXISTS resolve_tenant_token(text);
DROP POLICY IF EXISTS tenants_resolver ON tenants;
ALTER TABLE tenants DROP COLUMN IF EXISTS public_token, DROP COLUMN IF EXISTS slug;
DROP FUNCTION IF EXISTS set_updated_at();
