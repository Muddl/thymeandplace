-- +goose Up
-- Least-privilege runtime (docs/superpowers/specs/2026-09-27-least-privilege-deployment-design.md).
-- Runs as tp_owner, which owns every object here. The roles themselves are
-- cluster-wide and are created outside migrations: by
-- deploy/db/initdb/20-roles.sh on a fresh volume, or deploy/db/adopt-roles.sql
-- for an existing database.

-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tp_app')
     OR NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tp_backup') THEN
    RAISE EXCEPTION 'roles tp_app and tp_backup are missing: create them with deploy/db/adopt-roles.sql (existing database) or deploy/db/initdb/20-roles.sh (fresh volume) before migrating';
  END IF;
END $$;
-- +goose StatementEnd

-- Catalog: the app edits it; history and the version counter are written
-- only by the triggers below.
GRANT SELECT, INSERT, UPDATE, DELETE ON ingredient, ingredient_pairing, seafood_region TO tp_app;
GRANT SELECT ON catalog_meta TO tp_app;
-- SELECT ... FOR UPDATE (Apply's catalog lock) needs UPDATE on some column;
-- id is CHECK (id), so it can only ever be true.
GRANT UPDATE (id) ON catalog_meta TO tp_app;
GRANT SELECT ON catalog_history TO tp_app;

-- Changesets and calibration.
GRANT SELECT, INSERT, DELETE ON pending_changeset TO tp_app;
GRANT UPDATE (id) ON pending_changeset TO tp_app; -- for GetPendingChangesetForUpdate's FOR UPDATE
GRANT SELECT, INSERT ON calibration_run TO tp_app;

-- Reference data (read through the views, which check their owner's rights
-- on the versioned tables) and migration state (RequireMigrated).
GRANT SELECT ON dataset_versions, goose_db_version TO tp_app;
GRANT SELECT ON climate_cell, ocean_ecoregion, place TO tp_app;

-- Admin. The login log is append-only for the app; old rows go only through
-- prune_admin_login_log.
GRANT SELECT, INSERT, UPDATE, DELETE ON admin_passkey, admin_session, admin_enrollment_token TO tp_app;
GRANT SELECT, INSERT, UPDATE ON admin_backup TO tp_app;
GRANT SELECT, INSERT ON admin_login_log TO tp_app;

GRANT USAGE ON SEQUENCE pending_changeset_id_seq, calibration_run_id_seq, admin_login_log_id_seq TO tp_app;

-- The history and version triggers run as their owner (tp_owner), so the app
-- needs no write access to catalog_history or catalog_meta.version.
ALTER FUNCTION catalog_history_row() SECURITY DEFINER SET search_path = public, pg_temp;
ALTER FUNCTION catalog_bump_version() SECURITY DEFINER SET search_path = public, pg_temp;
REVOKE ALL ON FUNCTION catalog_history_row(), catalog_bump_version() FROM PUBLIC;

-- The only way the app can delete login-log rows: past a retention of at
-- least 30 days, so a compromised app can't erase the recent audit trail.
-- +goose StatementBegin
CREATE FUNCTION prune_admin_login_log(retention interval) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n bigint;
BEGIN
  IF retention IS NULL OR retention < interval '30 days' THEN
    RAISE EXCEPTION 'admin_login_log retention must be at least 30 days, got %', retention;
  END IF;
  DELETE FROM admin_login_log WHERE at < now() - retention;
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION prune_admin_login_log(interval) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION prune_admin_login_log(interval) TO tp_app;

-- Versioned reference tables that ingest creates later are readable by the
-- app with no extra grant (it reads them through the views, but a view on a
-- new table must not fail for want of one).
ALTER DEFAULT PRIVILEGES FOR ROLE tp_owner IN SCHEMA public GRANT SELECT ON TABLES TO tp_app;

-- +goose Down
ALTER DEFAULT PRIVILEGES FOR ROLE tp_owner IN SCHEMA public REVOKE SELECT ON TABLES FROM tp_app;
DROP FUNCTION prune_admin_login_log(interval);
GRANT EXECUTE ON FUNCTION catalog_history_row(), catalog_bump_version() TO PUBLIC;
ALTER FUNCTION catalog_bump_version() SECURITY INVOKER RESET search_path;
ALTER FUNCTION catalog_history_row() SECURITY INVOKER RESET search_path;
REVOKE USAGE ON SEQUENCE pending_changeset_id_seq, calibration_run_id_seq, admin_login_log_id_seq FROM tp_app;
REVOKE ALL ON admin_passkey, admin_session, admin_enrollment_token, admin_backup, admin_login_log FROM tp_app;
REVOKE ALL ON dataset_versions, goose_db_version, climate_cell, ocean_ecoregion, place FROM tp_app;
REVOKE ALL ON pending_changeset, calibration_run FROM tp_app;
REVOKE ALL ON ingredient, ingredient_pairing, seafood_region, catalog_meta, catalog_history FROM tp_app;
