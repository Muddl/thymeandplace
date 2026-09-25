-- +goose Up
CREATE TABLE catalog_history (
  id         bigserial PRIMARY KEY,
  changed_at timestamptz NOT NULL DEFAULT now(),
  actor      text NOT NULL,
  change_id  bigint,
  action     text NOT NULL CHECK (action IN ('insert','update','delete')),
  table_name text NOT NULL,
  row_key    jsonb NOT NULL,
  before     jsonb,
  after      jsonb
);
CREATE INDEX catalog_history_slug ON catalog_history ((row_key->>'slug'), id DESC);

-- +goose StatementBegin
CREATE FUNCTION catalog_history_row() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  v_actor  text  := coalesce(nullif(current_setting('app.actor', true), ''), 'sql');
  v_change bigint := nullif(current_setting('app.change_id', true), '')::bigint;
  v_meta   text  := nullif(current_setting('app.meta', true), '');
  v_before jsonb;
  v_after  jsonb;
  v_row    jsonb;
  v_key    jsonb;
BEGIN
  IF TG_OP IN ('UPDATE', 'DELETE') THEN v_before := to_jsonb(OLD); END IF;
  IF TG_OP IN ('INSERT', 'UPDATE') THEN v_after := to_jsonb(NEW); END IF;
  v_row := coalesce(v_after, v_before);
  v_key := CASE TG_TABLE_NAME
    WHEN 'ingredient' THEN jsonb_build_object('slug', v_row->'slug')
    WHEN 'ingredient_pairing' THEN jsonb_build_object('slug', v_row->'slug', 'pairs_with', v_row->'pairs_with')
    ELSE jsonb_build_object('slug', v_row->'slug', 'ecoregion_id', v_row->'ecoregion_id')
  END;
  IF v_meta IS NOT NULL THEN
    v_after := coalesce(v_after, '{}'::jsonb) || jsonb_build_object('_meta', v_meta::jsonb);
  END IF;
  INSERT INTO catalog_history (actor, change_id, action, table_name, row_key, before, after)
  VALUES (v_actor, v_change, lower(TG_OP), TG_TABLE_NAME, v_key, v_before, v_after);
  RETURN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION catalog_bump_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  UPDATE catalog_meta SET version = version + 1;
  PERFORM pg_notify('data_version_changed', 'catalog');
  RETURN NULL;
END $$;
-- +goose StatementEnd

CREATE TRIGGER ingredient_history AFTER INSERT OR UPDATE OR DELETE ON ingredient
  FOR EACH ROW EXECUTE FUNCTION catalog_history_row();
CREATE TRIGGER ingredient_pairing_history AFTER INSERT OR UPDATE OR DELETE ON ingredient_pairing
  FOR EACH ROW EXECUTE FUNCTION catalog_history_row();
CREATE TRIGGER seafood_region_history AFTER INSERT OR UPDATE OR DELETE ON seafood_region
  FOR EACH ROW EXECUTE FUNCTION catalog_history_row();

CREATE TRIGGER ingredient_version AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON ingredient
  FOR EACH STATEMENT EXECUTE FUNCTION catalog_bump_version();
CREATE TRIGGER ingredient_pairing_version AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON ingredient_pairing
  FOR EACH STATEMENT EXECUTE FUNCTION catalog_bump_version();
CREATE TRIGGER seafood_region_version AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON seafood_region
  FOR EACH STATEMENT EXECUTE FUNCTION catalog_bump_version();

-- +goose Down
DROP TRIGGER seafood_region_version ON seafood_region;
DROP TRIGGER ingredient_pairing_version ON ingredient_pairing;
DROP TRIGGER ingredient_version ON ingredient;
DROP TRIGGER seafood_region_history ON seafood_region;
DROP TRIGGER ingredient_pairing_history ON ingredient_pairing;
DROP TRIGGER ingredient_history ON ingredient;
DROP FUNCTION catalog_bump_version();
DROP FUNCTION catalog_history_row();
DROP TABLE catalog_history;
