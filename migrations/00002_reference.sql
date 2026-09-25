-- +goose Up
CREATE TABLE dataset_versions (
  id               bigserial PRIMARY KEY,
  dataset          text NOT NULL CHECK (dataset IN ('climate','ocean','places')),
  table_name       text NOT NULL,
  source_url       text NOT NULL,
  source_checksum  text NOT NULL,
  license          text NOT NULL,
  license_reviewed boolean NOT NULL DEFAULT false,
  loaded_at        timestamptz NOT NULL DEFAULT now(),
  is_live          boolean NOT NULL DEFAULT false,
  -- Set the first time a version goes live; rollback only returns to promoted versions.
  promoted_at      timestamptz
);
CREATE UNIQUE INDEX dataset_versions_one_live ON dataset_versions (dataset) WHERE is_live;

-- Version 0 tables are empty placeholders so the views exist from the start.
-- Ingestion (Plan 3) creates *_v{N} tables and repoints the views.
CREATE TABLE climate_cell_v0 (
  cell_id   integer PRIMARY KEY,
  tmin_c    real[12] NOT NULL,
  tmax_c    real[12] NOT NULL,
  precip_mm real[12] NOT NULL
);
CREATE VIEW climate_cell AS SELECT * FROM climate_cell_v0;

CREATE TABLE ocean_ecoregion_v0 (
  ecoregion_id integer PRIMARY KEY,
  name         text NOT NULL,
  geom         geography(MultiPolygon, 4326) NOT NULL
);
CREATE INDEX ON ocean_ecoregion_v0 USING gist (geom);
CREATE VIEW ocean_ecoregion AS SELECT * FROM ocean_ecoregion_v0;

CREATE TABLE place_v0 (
  place_id   integer PRIMARY KEY,
  name       text NOT NULL,
  ascii_name text NOT NULL,
  admin1     text,
  country    text NOT NULL,
  population integer NOT NULL,
  geom       geography(Point, 4326) NOT NULL
);
CREATE INDEX ON place_v0 USING gin (ascii_name gin_trgm_ops);
-- Not in spec §6.3: lets accented queries ("Zürich") match the native name.
CREATE INDEX ON place_v0 USING gin (name gin_trgm_ops);
CREATE INDEX ON place_v0 USING gist (geom);
CREATE VIEW place AS SELECT * FROM place_v0;

-- +goose Down
DROP VIEW place;
DROP TABLE place_v0;
DROP VIEW ocean_ecoregion;
DROP TABLE ocean_ecoregion_v0;
DROP VIEW climate_cell;
DROP TABLE climate_cell_v0;
DROP TABLE dataset_versions;
