DROP TABLE IF EXISTS activities, opportunities, stages, pipelines, contacts;
DROP TRIGGER IF EXISTS accounts_updated ON accounts;
ALTER TABLE accounts DROP COLUMN IF EXISTS industry, DROP COLUMN IF EXISTS employee_count,
    DROP COLUMN IF EXISTS website, DROP COLUMN IF EXISTS owner_id,
    DROP COLUMN IF EXISTS last_contact_at, DROP COLUMN IF EXISTS updated_at;
