-- DEV PASSWORDS ONLY. Production roles/passwords are provisioned out of band
-- (see CLAUDE.md "Secrets"). No role here has BYPASSRLS, and only
-- app_migrator may create objects (it owns every table).
CREATE ROLE app_migrator LOGIN PASSWORD 'app_migrator_dev' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
CREATE ROLE app_user     LOGIN PASSWORD 'app_user_dev'     NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
CREATE ROLE ferriskey_user LOGIN PASSWORD 'ferriskey_dev'  NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;

CREATE DATABASE ferriskey OWNER ferriskey_user;

REVOKE ALL ON DATABASE ratify FROM PUBLIC;
GRANT CONNECT ON DATABASE ratify TO app_user, app_migrator;

REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE, CREATE ON SCHEMA public TO app_migrator;
-- app_user gets USAGE on the schema from the migration; it never gets CREATE.
