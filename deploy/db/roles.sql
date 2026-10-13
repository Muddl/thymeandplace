-- Least-privilege roles. Fresh volumes run this from
-- deploy/db/initdb/20-roles.sh; tests apply the same file to tp_template.
-- Callers bind owner_password, app_password, backup_password and db.

CREATE ROLE tp_owner LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD :'owner_password';
CREATE ROLE tp_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD :'app_password';
CREATE ROLE tp_backup LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD :'backup_password';
GRANT pg_read_all_data TO tp_backup;
-- In Postgres 15+ the public schema belongs to pg_database_owner, so this
-- makes tp_owner its owner too.
ALTER DATABASE :"db" OWNER TO tp_owner;
-- Extensions need a superuser; migration 00001 then finds them present.
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
