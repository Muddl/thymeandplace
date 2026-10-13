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
# The SQL is deploy/db/roles.sql, mounted at /roles.sql in compose.
set -euo pipefail
: "${TP_OWNER_PASSWORD:?set TP_OWNER_PASSWORD in db.env}"
: "${TP_APP_PASSWORD:?set TP_APP_PASSWORD in db.env}"
: "${TP_BACKUP_PASSWORD:?set TP_BACKUP_PASSWORD in db.env}"

roles_sql=/roles.sql
if [ ! -f "$roles_sql" ]; then
  roles_sql="$(cd "$(dirname "$0")" && pwd)/../roles.sql"
fi
if [ ! -f "$roles_sql" ]; then
  echo "deploy/db/roles.sql not found; mount it at /roles.sql" >&2
  exit 1
fi

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -v owner_password="$TP_OWNER_PASSWORD" -v app_password="$TP_APP_PASSWORD" \
  -v backup_password="$TP_BACKUP_PASSWORD" -v db="$POSTGRES_DB" \
  -f "$roles_sql"
