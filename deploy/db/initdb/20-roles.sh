#!/bin/bash
# Creates the least-privilege roles on a fresh data directory. The postgres
# image runs this once, as the bootstrap superuser (POSTGRES_USER), after the
# postgis image's own 10_postgis.sh; it never runs again on an existing
# volume (use deploy/db/adopt-roles.sql there).
#
#   tp_owner   owns the database, the public schema and every object in it;
#              runs migrations, ingest and devseed
#   tp_app     the runtime (app and the admin CLI); grants come from
#              migrations/00008_privileges.sql
#   tp_backup  pg_dump, via pg_read_all_data
#
# Passwords come from db.env (docs/operations.md, "Database roles").
set -euo pipefail
: "${TP_OWNER_PASSWORD:?set TP_OWNER_PASSWORD in db.env}"
: "${TP_APP_PASSWORD:?set TP_APP_PASSWORD in db.env}"
: "${TP_BACKUP_PASSWORD:?set TP_BACKUP_PASSWORD in db.env}"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -v owner_password="$TP_OWNER_PASSWORD" -v app_password="$TP_APP_PASSWORD" \
  -v backup_password="$TP_BACKUP_PASSWORD" -v db="$POSTGRES_DB" <<'SQL'
SELECT format('CREATE ROLE tp_owner LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %L', :'owner_password') \gexec
SELECT format('CREATE ROLE tp_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %L', :'app_password') \gexec
SELECT format('CREATE ROLE tp_backup LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %L', :'backup_password') \gexec
GRANT pg_read_all_data TO tp_backup;
-- In Postgres 15+ the public schema belongs to pg_database_owner, so this
-- makes tp_owner its owner too.
ALTER DATABASE :"db" OWNER TO tp_owner;
-- Extensions need a superuser; migration 00001 then finds them present.
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
SQL
