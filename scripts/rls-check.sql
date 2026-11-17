-- Schema lint over the migrated database (CLAUDE.md "Data model rules").
-- Every row returned is a violation; the script prints nothing when clean.
WITH tenant_tables AS (
  SELECT c.oid, c.relname, c.relrowsecurity, c.relforcerowsecurity
    FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = 'public'
    JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped
   WHERE c.relkind = 'r'
)
SELECT 'RLS not enabled+forced: ' || relname FROM tenant_tables
 WHERE NOT (relrowsecurity AND relforcerowsecurity)
UNION ALL
SELECT 'missing UNIQUE (tenant_id, id): ' || t.relname FROM tenant_tables t
 WHERE EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = t.oid AND a.attname = 'id' AND NOT a.attisdropped)
   AND NOT EXISTS (
     SELECT 1 FROM pg_constraint k
      WHERE k.conrelid = t.oid AND k.contype IN ('u','p') AND cardinality(k.conkey) = 2
        AND (SELECT array_agg(attname::text ORDER BY attname) FROM pg_attribute
              WHERE attrelid = t.oid AND attnum = ANY (k.conkey)) = ARRAY['id','tenant_id'])
UNION ALL
SELECT 'non-composite FK (bypasses RLS): ' || t.relname || '.' || k.conname
  FROM tenant_tables t
  JOIN pg_constraint k ON k.conrelid = t.oid AND k.contype = 'f'
  JOIN tenant_tables r ON r.oid = k.confrelid
 WHERE NOT EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = t.oid AND a.attname = 'tenant_id' AND a.attnum = ANY (k.conkey))
UNION ALL
SELECT 'policy without current_tenant(): ' || p.tablename || '.' || p.policyname
  FROM pg_policies p
 WHERE p.schemaname = 'public'
   AND coalesce(p.qual, '') NOT LIKE '%current_tenant()%'
   -- the ONLY cross-tenant policies (CLAUDE.md "Cross-tenant system roles")
   AND p.policyname NOT IN ('outbox_relay', 'tenants_resolver')
UNION ALL
SELECT 'policy without WITH CHECK current_tenant(): ' || p.tablename || '.' || p.policyname
  FROM pg_policies p
 WHERE p.schemaname = 'public' AND p.policyname NOT IN ('outbox_relay', 'tenants_resolver')
   AND p.cmd IN ('ALL', 'INSERT', 'UPDATE') AND coalesce(p.with_check, '') NOT LIKE '%current_tenant()%'
UNION ALL
SELECT 'role with BYPASSRLS: ' || rolname FROM pg_roles WHERE rolbypassrls AND rolname <> 'postgres'
UNION ALL
SELECT 'LOGIN role owns an RLS table: ' || c.relname || ' (' || r.rolname || ')'
  FROM tenant_tables c JOIN pg_class pc ON pc.oid = c.oid JOIN pg_roles r ON r.oid = pc.relowner
 WHERE r.rolcanlogin AND r.rolname <> 'app_migrator';
