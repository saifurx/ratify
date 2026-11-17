-- The default text-search parser keeps "a@b.test" and "b.test" as single
-- tokens, which defeats word and prefix search. Normalise every non
-- alphanumeric run to a space before tokenising.
DROP INDEX IF EXISTS accounts_search_idx, contacts_search_idx, opportunities_search_idx;
ALTER TABLE accounts      DROP COLUMN search;
ALTER TABLE contacts      DROP COLUMN search;
ALTER TABLE opportunities DROP COLUMN search;

ALTER TABLE accounts ADD COLUMN search tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', regexp_replace(lower(
        coalesce(name, '') || ' ' || coalesce(domain, '') || ' ' || coalesce(industry, '') || ' ' || coalesce(website, '')),
        '[^[:alnum:]]+', ' ', 'g'))) STORED;
ALTER TABLE contacts ADD COLUMN search tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', regexp_replace(lower(
        coalesce(first_name, '') || ' ' || coalesce(last_name, '') || ' ' || coalesce(email::text, '') || ' ' ||
        coalesce(title, '') || ' ' || coalesce(phone, '')),
        '[^[:alnum:]]+', ' ', 'g'))) STORED;
ALTER TABLE opportunities ADD COLUMN search tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', regexp_replace(lower(coalesce(name, '')), '[^[:alnum:]]+', ' ', 'g'))) STORED;

CREATE INDEX accounts_search_idx      ON accounts      USING gin (tenant_id, search);
CREATE INDEX contacts_search_idx      ON contacts      USING gin (tenant_id, search);
CREATE INDEX opportunities_search_idx ON opportunities USING gin (tenant_id, search);
