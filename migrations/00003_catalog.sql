-- +goose Up
CREATE TYPE ingredient_category AS ENUM ('fruit','vegetable','herb','mushroom','seafood');
CREATE TYPE ingredient_kind AS ENUM
  ('annual','perennial_fruit','cultivated_year_round','wild_mushroom','seafood');
CREATE TYPE frost_tolerance AS ENUM ('tender','hardy');

-- Per-kind CHECKs include explicit IS NOT NULL: without them a NULL makes
-- the CHECK evaluate to NULL, which Postgres treats as passing.
CREATE TABLE ingredient (
  slug       text PRIMARY KEY CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
  name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  category   ingredient_category NOT NULL,
  kind       ingredient_kind NOT NULL,
  frost                   frost_tolerance,
  grow_temp_min_c         real,
  grow_temp_max_c         real,
  months_to_harvest       smallint,
  storage_months          smallint NOT NULL DEFAULT 0 CHECK (storage_months BETWEEN 0 AND 9),
  chill_months_min        smallint,
  warmup_temp_c           real,
  months_bloom_to_harvest smallint,
  harvest_span_months     smallint,
  fruiting_temp_min_c     real,
  fruiting_temp_max_c     real,
  min_precip_mm           real,
  tip_choose text NOT NULL,
  tip_store  text NOT NULL,
  note       text NOT NULL DEFAULT '',
  updated_at timestamptz NOT NULL DEFAULT now(),

  CHECK ((kind = 'seafood') = (category = 'seafood')),
  CHECK (kind <> 'wild_mushroom' OR category = 'mushroom'),
  CHECK (kind <> 'annual' OR (
    frost IS NOT NULL
    AND grow_temp_min_c IS NOT NULL AND grow_temp_max_c IS NOT NULL
    AND grow_temp_max_c > grow_temp_min_c
    AND months_to_harvest IS NOT NULL AND months_to_harvest BETWEEN 1 AND 11)),
  CHECK (kind <> 'perennial_fruit' OR (
    frost IS NOT NULL
    AND chill_months_min IS NOT NULL AND chill_months_min BETWEEN 0 AND 12
    AND warmup_temp_c IS NOT NULL
    AND months_bloom_to_harvest IS NOT NULL AND months_bloom_to_harvest BETWEEN 1 AND 11
    AND harvest_span_months IS NOT NULL AND harvest_span_months BETWEEN 1 AND 12)),
  CHECK (kind <> 'wild_mushroom' OR (
    fruiting_temp_min_c IS NOT NULL AND fruiting_temp_max_c IS NOT NULL
    AND fruiting_temp_max_c > fruiting_temp_min_c
    AND min_precip_mm IS NOT NULL AND min_precip_mm >= 0))
);

CREATE TABLE ingredient_pairing (
  slug       text REFERENCES ingredient ON DELETE CASCADE,
  pairs_with text REFERENCES ingredient ON DELETE CASCADE,
  PRIMARY KEY (slug, pairs_with),
  CHECK (slug <> pairs_with)
);

CREATE TABLE seafood_region (
  slug         text REFERENCES ingredient ON DELETE CASCADE,
  ecoregion_id integer NOT NULL,
  months       smallint[] NOT NULL CHECK (cardinality(months) BETWEEN 1 AND 12
                                          AND months <@ ARRAY[1,2,3,4,5,6,7,8,9,10,11,12]::smallint[]),
  peak_months  smallint[] NOT NULL DEFAULT '{}' CHECK (peak_months <@ months),
  PRIMARY KEY (slug, ecoregion_id)
);

CREATE TABLE catalog_meta (
  id      boolean PRIMARY KEY DEFAULT true CHECK (id),
  version bigint NOT NULL DEFAULT 0
);
INSERT INTO catalog_meta DEFAULT VALUES;

-- +goose Down
DROP TABLE catalog_meta;
DROP TABLE seafood_region;
DROP TABLE ingredient_pairing;
DROP TABLE ingredient;
DROP TYPE frost_tolerance;
DROP TYPE ingredient_kind;
DROP TYPE ingredient_category;
