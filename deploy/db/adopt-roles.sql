-- One-time adoption of the least-privilege roles for a database created
-- before them (docs/operations.md, "Adopting an existing database"). Run it
-- together with backup/own-objects.sql, as the bootstrap superuser, before
-- deploying the release that adds migrations/00008_privileges.sql. From
-- /opt/thymeandplace/deploy, once db.env has the three TP_*_PASSWORD values
-- and the db container has been recreated with it:
--
--   cat db/adopt-roles.sql backup/own-objects.sql | docker compose exec -T db sh -c \
--     'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -v owner_password="$TP_OWNER_PASSWORD" \
--      -v app_password="$TP_APP_PASSWORD" -v backup_password="$TP_BACKUP_PASSWORD" -f -'
--
-- It is idempotent: it creates each role if missing (or resets its
-- password); own-objects.sql then hands the database and every non-extension
-- object in the public schema to tp_owner. REASSIGN OWNED can't be used: the
-- bootstrap superuser also owns the system catalogs.

SELECT format(CASE WHEN EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tp_owner')
  THEN 'ALTER ROLE tp_owner LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %L'
  ELSE 'CREATE ROLE tp_owner LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %L' END, :'owner_password') \gexec
SELECT format(CASE WHEN EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tp_app')
  THEN 'ALTER ROLE tp_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %L'
  ELSE 'CREATE ROLE tp_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %L' END, :'app_password') \gexec
SELECT format(CASE WHEN EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tp_backup')
  THEN 'ALTER ROLE tp_backup LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %L'
  ELSE 'CREATE ROLE tp_backup LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD %L' END, :'backup_password') \gexec
GRANT pg_read_all_data TO tp_backup;
