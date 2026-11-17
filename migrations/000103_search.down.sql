DROP INDEX IF EXISTS opportunities_search_idx, contacts_search_idx, accounts_search_idx;
ALTER TABLE opportunities DROP COLUMN IF EXISTS search;
ALTER TABLE contacts DROP COLUMN IF EXISTS search;
ALTER TABLE accounts DROP COLUMN IF EXISTS search;
