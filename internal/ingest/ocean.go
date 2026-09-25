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
// GeoJSON by the Esri Oceans MEOW feature service.
type Ocean struct {
	Source             Source
	MinCount, MaxCount int
	Coastal, Inland    []Point
}

// DefaultOceanURL returns all 232 MEOW polygons as GeoJSON in WGS84 in one
// request (item 74b6ac5c8fc24dcb8abaad6428a5dfa4). Override with OCEAN_SOURCE_URL.
const DefaultOceanURL = "https://services.arcgis.com/bDAhvQYMG4WL8O5o/arcgis/rest/services/MEOW/FeatureServer/0/query" +
	"?where=1%3D1&outFields=ECO_CODE_X,ECOREGION&outSR=4326&f=geojson"

func NewOcean(url string) *Ocean {
	if url == "" {
		url = DefaultOceanURL
	}
	return &Ocean{
		Source:   Source{URL: url, File: "meow.geojson"},
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
func (*Ocean) License() string {
	return "Marine Ecoregions of the World (Spalding et al., 2007, BioScience), via Esri Oceans ArcGIS Online item " +
		"74b6ac5c8fc24dcb8abaad6428a5dfa4. The item states no licence: confirm terms with the original publishers " +
		"(The Nature Conservancy / WWF) before approving"
}
func (o *Ocean) Sources() []Source { return []Source{o.Source} }

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

// Transform turns the GeoJSON FeatureCollection into CSV rows of
// (ecoregion_id, name, geojson geometry).
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
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"ecoregion_id", "name", "geojson"})
	for i, f := range fc.Features {
		code, ok := f.Properties["ECO_CODE_X"].(float64)
		if !ok || code <= 0 {
			continue // no usable ecoregion code
		}
		var g struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(f.Geometry, &g); err != nil || (g.Type != "Polygon" && g.Type != "MultiPolygon") {
			return fmt.Errorf("feature %d (ECO_CODE_X %v): geometry must be Polygon or MultiPolygon, got %q", i, code, g.Type)
		}
		name, _ := f.Properties["ECOREGION"].(string)
		var compact bytes.Buffer
		if err := json.Compact(&compact, f.Geometry); err != nil {
			return err
		}
		if err := cw.Write([]string{strconv.Itoa(int(code)), strings.TrimSpace(name), compact.String()}); err != nil {
			return err
		}
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
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE ocean_stage (ecoregion_id int, name text, geojson text) ON COMMIT DROP`); err != nil {
		return err
	}
	if err := copyStage(ctx, tx, "ocean_stage", r); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s
		SELECT ecoregion_id, min(name),
		       ST_Multi(ST_CollectionExtract(ST_MakeValid(ST_Union(ST_SetSRID(ST_GeomFromGeoJSON(geojson), 4326))), 3))::geography
		FROM ocean_stage GROUP BY ecoregion_id`, t))
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
