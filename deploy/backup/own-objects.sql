-- Hands the application database to tp_owner: the database itself and every
-- object in the public schema that the connecting superuser owns, except
-- extension members (PostGIS's own tables, views and functions stay with the
-- superuser that installed them). Used by restore.sh after pg_restore
-- --no-owner (which leaves everything owned by the restoring superuser) and
-- by the one-time role adoption (docs/operations.md, "Adopting an existing
-- database"). Idempotent. Run with psql as the bootstrap superuser.
SELECT format('ALTER DATABASE %I OWNER TO tp_owner', current_database()) \gexec

DO $$
DECLARE
  me oid := (SELECT oid FROM pg_roles WHERE rolname = current_user);
  r record;
BEGIN
  -- Tables and views. A table's indexes and linked sequences move with it.
  FOR r IN
    SELECT c.oid::regclass AS obj, c.relkind FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p', 'v', 'm', 'f') AND c.relowner = me
      AND NOT EXISTS (SELECT 1 FROM pg_depend d
                      WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')
  LOOP
    EXECUTE format('ALTER %s %s OWNER TO tp_owner',
      CASE r.relkind WHEN 'v' THEN 'VIEW' WHEN 'm' THEN 'MATERIALIZED VIEW' WHEN 'f' THEN 'FOREIGN TABLE' ELSE 'TABLE' END,
      r.obj);
  END LOOP;
  -- Sequences not linked to a table.
  FOR r IN
    SELECT c.oid::regclass AS obj FROM pg_class c
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'public' AND c.relkind = 'S' AND c.relowner = me
      AND NOT EXISTS (SELECT 1 FROM pg_depend d
                      WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype IN ('e', 'a', 'i'))
  LOOP
    EXECUTE format('ALTER SEQUENCE %s OWNER TO tp_owner', r.obj);
  END LOOP;
  -- Functions (the catalog triggers' functions, prune_admin_login_log).
  FOR r IN
    SELECT p.oid::regprocedure AS fn FROM pg_proc p
    JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = 'public' AND p.proowner = me
      AND NOT EXISTS (SELECT 1 FROM pg_depend d
                      WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')
  LOOP
    EXECUTE format('ALTER ROUTINE %s OWNER TO tp_owner', r.fn);
  END LOOP;
  -- Enum and domain types (the catalog's ingredient_kind and friends).
  FOR r IN
    SELECT t.oid::regtype AS typ FROM pg_type t
    JOIN pg_namespace n ON n.oid = t.typnamespace
    WHERE n.nspname = 'public' AND t.typtype IN ('e', 'd') AND t.typowner = me
      AND NOT EXISTS (SELECT 1 FROM pg_depend d
                      WHERE d.classid = 'pg_type'::regclass AND d.objid = t.oid AND d.deptype = 'e')
  LOOP
    EXECUTE format('ALTER TYPE %s OWNER TO tp_owner', r.typ);
  END LOOP;
END $$;
