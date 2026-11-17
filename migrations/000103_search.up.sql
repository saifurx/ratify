-- Sprint 1.3: PostgreSQL full-text search (no Elasticsearch). Generated
-- tsvector columns; GIN indexes are led by tenant_id via btree_gin.
CREATE EXTENSION IF NOT EXISTS btree_gin;

ALTER TABLE accounts ADD COLUMN search tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', coalesce(name, '') || ' ' || coalesce(domain, '') || ' ' ||
                          coalesce(industry, '') || ' ' || coalesce(website, ''))) STORED;
ALTER TABLE contacts ADD COLUMN search tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', coalesce(first_name, '') || ' ' || coalesce(last_name, '') || ' ' ||
                          coalesce(email::text, '') || ' ' || coalesce(title, '') || ' ' || coalesce(phone, ''))) STORED;
ALTER TABLE opportunities ADD COLUMN search tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', coalesce(name, ''))) STORED;

CREATE INDEX accounts_search_idx      ON accounts      USING gin (tenant_id, search);
CREATE INDEX contacts_search_idx      ON contacts      USING gin (tenant_id, search);
CREATE INDEX opportunities_search_idx ON opportunities USING gin (tenant_id, search);
