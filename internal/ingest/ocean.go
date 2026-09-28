package ingest

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"thymeandplace/internal/store"
)

type Point struct {
	Name     string
	Lat, Lng float64
}

// Ocean is Marine Ecoregions of the World (Spalding et al., 2007), served as
// GeoJSON by the Esri Oceans MEOW feature service, clipped to water using
// Natural Earth's 1:50m land polygons (coastal MEOW polygons run well inland).
type Ocean struct {
	Source             Source
	Land               Source
	MinCount, MaxCount int
	Coastal, Inland    []Point
}

// DefaultOceanURL returns all 232 MEOW polygons as GeoJSON in WGS84 in one
// request (item 74b6ac5c8fc24dcb8abaad6428a5dfa4). Override with OCEAN_SOURCE_URL.
const DefaultOceanURL = "https://services.arcgis.com/bDAhvQYMG4WL8O5o/arcgis/rest/services/MEOW/FeatureServer/0/query" +
	"?where=1%3D1&outFields=ECO_CODE_X,ECOREGION&outSR=4326&f=geojson"

// DefaultLandURL is Natural Earth's 1:50m land polygon layer (public domain),
// used to subtract land from every ecoregion. Override with OCEAN_LAND_URL.
const DefaultLandURL = "https://raw.githubusercontent.com/nvkelso/natural-earth-vector/master/geojson/ne_50m_land.geojson"

func NewOcean(url string) *Ocean {
	if url == "" {
		url = DefaultOceanURL
	}
	return &Ocean{
		Source:   Source{URL: url, File: "meow.geojson"},
		Land:     Source{URL: DefaultLandURL, File: "ne_50m_land.geojson"},
		MinCount: 225, MaxCount: 240, // MEOW has 232 ecoregions
		Coastal: []Point{
			{"Lisbon", 38.72, -9.14}, {"Boston", 42.36, -71.06}, {"Tokyo", 35.68, 139.77},
			{"Sydney", -33.87, 151.21}, {"Cape Town", -33.92, 18.42}, {"Valparaíso", -33.05, -71.62},
			{"Mumbai", 18.94, 72.84}, {"Reykjavik", 64.15, -21.94}, {"Vancouver", 49.28, -123.12},
			{"Singapore", 1.29, 103.85},
		},
		Inland: []Point{
			{"Madrid", 40.42, -3.70}, {"Denver", 39.74, -104.99}, {"Ulaanbaatar", 47.92, 106.92}, {"Bamako", 12.64, -8.0},
		},
	}
}

func (*Ocean) Name() string   { return "ocean" }
func (*Ocean) View() string   { return "ocean_ecoregion" }
func (*Ocean) Prefix() string { return "ocean_ecoregion_v" }

// License carries the LicenseUnresolved marker until the MEOW terms are
// confirmed and recorded in docs/reference-data-licenses.md; until then no
// ocean version can be approved or go live. The resolution commit replaces
// this text with the confirmed terms (docs/superpowers/specs/
// 2026-09-27-reference-data-provenance-design.md §4.2).
func (*Ocean) License() string {
	return LicenseUnresolved + " Marine Ecoregions of the World (Spalding et al., 2007) via Esri ArcGIS Online item " +
		"74b6ac5c8fc24dcb8abaad6428a5dfa4; terms not yet confirmed (docs/reference-data-licenses.md)" +
		"; land mask: Natural Earth 1:50m land (recorded as public domain)"
}

// Attribution is the citation only, with no licensing claim, until the MEOW
// terms are confirmed and say what attribution they require.
func (*Ocean) Attribution() []string {
	return []string{
		"Marine Ecoregions of the World (Spalding, M.D. et al., 2007, BioScience)",
		"Natural Earth 1:50m land polygons (public domain)",
	}
}
func (o *Ocean) Sources() []Source { return []Source{o.Source, o.Land} }

type geoFeatureCollection struct {
	Type     string `json:"type"`
	Features []struct {
		Properties map[string]any  `json:"properties"`
		Geometry   json.RawMessage `json:"geometry"`
	} `json:"features"`
	ExceededTransferLimit bool `json:"exceededTransferLimit"`
	Properties            struct {
		ExceededTransferLimit bool `json:"exceededTransferLimit"`
	} `json:"properties"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// geomType reports the GeoJSON geometry's "type" field.
func geomType(raw json.RawMessage) (string, error) {
	var g struct {
		Type string `json:"type"`
	}
	err := json.Unmarshal(raw, &g)
	return g.Type, err
}

func compactGeoJSON(raw json.RawMessage) (string, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Transform reads both raw files and writes ONE CSV with header
// kind,ecoregion_id,name,geojson: kind "eco" for each MEOW ecoregion polygon
// (Task 5's checks unchanged) and kind "land" (ecoregion_id 0, empty name)
// for every Natural Earth Polygon/MultiPolygon feature, used at Load time to
// clip the ecoregions down to water.
func (o *Ocean) Transform(_ context.Context, dir string, w io.Writer) error {
	b, err := os.ReadFile(filepath.Join(dir, "raw", o.Source.File))
	if err != nil {
		return err
	}
	var fc geoFeatureCollection
	if err := json.Unmarshal(b, &fc); err != nil {
		return fmt.Errorf("not GeoJSON: %w", err)
	}
	switch {
	case fc.Error != nil:
		return fmt.Errorf("feature service error %d: %s", fc.Error.Code, fc.Error.Message)
	case fc.Type != "FeatureCollection":
		return fmt.Errorf("expected a FeatureCollection, got %q", fc.Type)
	case fc.ExceededTransferLimit || fc.Properties.ExceededTransferLimit:
		return errors.New("feature service truncated the result (exceededTransferLimit); page the query with resultOffset")
	case len(fc.Features) == 0:
		return errors.New("no features in the response")
	}

	lb, err := os.ReadFile(filepath.Join(dir, "raw", o.Land.File))
	if err != nil {
		return err
	}
	var land geoFeatureCollection
	if err := json.Unmarshal(lb, &land); err != nil {
		return fmt.Errorf("land file not GeoJSON: %w", err)
	}
	if land.Type != "FeatureCollection" {
		return fmt.Errorf("land file: expected a FeatureCollection, got %q", land.Type)
	}

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"kind", "ecoregion_id", "name", "geojson"})
	for i, f := range fc.Features {
		code, ok := f.Properties["ECO_CODE_X"].(float64)
		if !ok || code <= 0 {
			continue // no usable ecoregion code
		}
		gt, err := geomType(f.Geometry)
		if err != nil || (gt != "Polygon" && gt != "MultiPolygon") {
			return fmt.Errorf("feature %d (ECO_CODE_X %v): geometry must be Polygon or MultiPolygon, got %q", i, code, gt)
		}
		name, _ := f.Properties["ECOREGION"].(string)
		compact, err := compactGeoJSON(f.Geometry)
		if err != nil {
			return err
		}
		if err := cw.Write([]string{"eco", strconv.Itoa(int(code)), strings.TrimSpace(name), compact}); err != nil {
			return err
		}
	}

	var landPolys int
	for _, f := range land.Features {
		gt, err := geomType(f.Geometry)
		if err != nil || (gt != "Polygon" && gt != "MultiPolygon") {
			continue // skip non-polygon land features (e.g. points, lines)
		}
		compact, err := compactGeoJSON(f.Geometry)
		if err != nil {
			return err
		}
		if err := cw.Write([]string{"land", "0", "", compact}); err != nil {
			return err
		}
		landPolys++
	}
	if landPolys == 0 {
		return errors.New("land file: no Polygon/MultiPolygon features")
	}

	cw.Flush()
	return cw.Error()
}

func (*Ocean) CreateSQL(t string) string {
	return fmt.Sprintf(`CREATE TABLE %[1]s (
  ecoregion_id integer PRIMARY KEY,
  name         text NOT NULL,
  geom         geography(MultiPolygon, 4326) NOT NULL
);
CREATE INDEX ON %[1]s USING gist (geom);`, t)
}

func (*Ocean) Load(ctx context.Context, tx pgx.Tx, t string, r io.Reader) error {
	if !store.ValidIdent(t) {
		return fmt.Errorf("invalid table name %q", t)
	}
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE ocean_stage (kind text, ecoregion_id int, name text, geojson text) ON COMMIT DROP`); err != nil {
		return err
	}
	if err := copyStage(ctx, tx, "ocean_stage", r); err != nil {
		return err
	}
	return clipToWater(ctx, tx, t)
}

// clipToWater builds a subdivided, GiST-indexed land table from the staged
// Natural Earth polygons, then inserts each ecoregion's water-only geometry
// (its per-part-valid union minus any intersecting land) into t. An
// ecoregion left with no water after clipping is skipped, not inserted.
func clipToWater(ctx context.Context, tx pgx.Tx, t string) error {
	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE ocean_land ON COMMIT DROP AS
		SELECT ST_Subdivide(ST_MakeValid(ST_SetSRID(ST_GeomFromGeoJSON(geojson), 4326)), 256) AS g
		FROM ocean_stage WHERE kind = 'land'`); err != nil {
		return fmt.Errorf("build land table: %w", err)
	}
	if _, err := tx.Exec(ctx, `CREATE INDEX ON ocean_land USING gist (g)`); err != nil {
		return fmt.Errorf("index land table: %w", err)
	}
	_, err := tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s
		SELECT ecoregion_id, name, ST_Multi(clipped)::geography
		FROM (
			SELECT e.ecoregion_id, e.name,
			       ST_CollectionExtract(ST_MakeValid(ST_Difference(e.eco,
			         coalesce((SELECT ST_Union(l.g) FROM ocean_land l WHERE ST_Intersects(l.g, e.eco)),
			                  ST_SetSRID('GEOMETRYCOLLECTION EMPTY'::geometry, 4326))
			       )), 3) AS clipped
			FROM (
				SELECT ecoregion_id, min(name) AS name,
				       ST_Union(ST_MakeValid(ST_SetSRID(ST_GeomFromGeoJSON(geojson), 4326))) AS eco
				FROM ocean_stage
				WHERE kind = 'eco'
				GROUP BY ecoregion_id
			) e
		) c
		WHERE clipped IS NOT NULL AND NOT ST_IsEmpty(clipped)`, t))
	return err
}

func (o *Ocean) Check(ctx context.Context, db store.DBTX, t string) []string {
	if !store.ValidIdent(t) {
		return []string{fmt.Sprintf("invalid table name %q", t)}
	}
	var fails []string
	var n int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM "+t).Scan(&n); err != nil {
		return []string{"count: " + err.Error()}
	}
	if n < o.MinCount || n > o.MaxCount {
		fails = append(fails, fmt.Sprintf("%d ecoregions, expected %d–%d", n, o.MinCount, o.MaxCount))
	}
	for _, pt := range o.Coastal {
		ids, err := store.NearbyEcoregions(ctx, db, t, pt.Lat, pt.Lng, 100_000)
		if err != nil || len(ids) == 0 {
			fails = append(fails, fmt.Sprintf("spot check %s: no ecoregion within 100 km (%v)", pt.Name, err))
		}
	}
	for _, pt := range o.Inland {
		ids, err := store.NearbyEcoregions(ctx, db, t, pt.Lat, pt.Lng, 100_000)
		if err != nil || len(ids) > 0 {
			fails = append(fails, fmt.Sprintf("spot check %s: expected inland, got %v (%v)", pt.Name, ids, err))
		}
	}
	rows, err := db.Query(ctx, fmt.Sprintf(`SELECT DISTINCT r.ecoregion_id FROM seafood_region r
		WHERE NOT EXISTS (SELECT 1 FROM %s e WHERE e.ecoregion_id = r.ecoregion_id) ORDER BY 1`, t))
	if err != nil {
		return append(fails, "catalog check: "+err.Error())
	}
	defer rows.Close()
	var missing []string
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err == nil {
			missing = append(missing, strconv.Itoa(int(id)))
		}
	}
	if len(missing) > 0 {
		fails = append(fails, "catalog seafood regions reference missing ecoregions: "+strings.Join(missing, ", "))
	}
	return fails
}
