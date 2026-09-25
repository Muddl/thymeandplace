package ingest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/store"
)

func rect(x0, y0, x1, y1 float64) [][2]float64 {
	return [][2]float64{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}, {x0, y0}}
}

func polygon(rings ...[][2]float64) map[string]any {
	return map[string]any{"type": "Polygon", "coordinates": rings}
}

func feature(id any, name string, geom map[string]any) map[string]any {
	return map[string]any{"type": "Feature", "properties": map[string]any{"ECO_CODE_X": id, "ECOREGION": name}, "geometry": geom}
}

func geoJSON(t *testing.T, doc map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func collection(features ...map[string]any) map[string]any {
	return map[string]any{"type": "FeatureCollection", "features": features}
}

// landGeoJSON builds a Natural Earth-shaped FeatureCollection out of land
// polygons (properties are irrelevant for land features).
func landGeoJSON(t *testing.T, polys ...map[string]any) []byte {
	t.Helper()
	feats := make([]map[string]any, len(polys))
	for i, p := range polys {
		feats[i] = feature(nil, "", p)
	}
	return geoJSON(t, collection(feats...))
}

// emptyLand is a land file whose single polygon is far from every test
// ecoregion, so clipping never changes the result.
func emptyLand(t *testing.T) []byte {
	return landGeoJSON(t, polygon(rect(170, 80, 171, 81)))
}

func testOcean(t *testing.T, body, land []byte) *Ocean {
	base := serveFiles(t, map[string][]byte{"meow.geojson": body, "ne_50m_land.geojson": land})
	return &Ocean{
		Source:   Source{URL: base + "/meow.geojson", File: "meow.geojson"},
		Land:     Source{URL: base + "/ne_50m_land.geojson", File: "ne_50m_land.geojson"},
		MinCount: 2, MaxCount: 2,
		Coastal: []Point{{"lisbon", 38.7167, -9.1333}},
		Inland:  []Point{{"madrid", 40.4165, -3.7026}},
	}
}

func goodMEOW(t *testing.T) []byte {
	return geoJSON(t, collection(
		feature(30, "Iberian", polygon(rect(-10.5, 36.5, -9.4, 43.5))),
		feature(25, "North Sea", polygon(rect(0, 51, 8, 58), rect(2, 53, 3, 54))), // outer ring + hole
		feature(25, "North Sea", polygon(rect(8, 55, 9, 56))),                     // second part, same id
		feature(nil, "No code", polygon(rect(-40, -10, -20, 10))),                 // skipped
	))
}

func loadOcean(t *testing.T, p *Pipeline) (int64, error) {
	t.Helper()
	ctx := context.Background()
	v, err := p.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Transform(ctx, v); err != nil {
		return v, err
	}
	return v, p.Load(ctx, v)
}

func TestOceanPipeline(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	ds := testOcean(t, goodMEOW(t), emptyLand(t))
	p := newPipeline(t, pool, ds)
	v, err := loadOcean(t, p)
	if err != nil {
		t.Fatalf("transform/load: %v", err)
	}
	dv, _ := p.version(ctx, v)
	var n int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM "+dv.TableName).Scan(&n)
	if n != 2 {
		t.Fatalf("ecoregions = %d, want 2 (null code skipped, parts merged)", n)
	}
	if ids, _ := store.NearbyEcoregions(ctx, pool, dv.TableName, 55.5, 8.5, 0); len(ids) != 1 || ids[0] != 25 {
		t.Fatalf("second part: %v", ids)
	}
	if ids, _ := store.NearbyEcoregions(ctx, pool, dv.TableName, 53.5, 2.5, 0); len(ids) != 0 {
		t.Fatalf("hole treated as inside: %v", ids)
	}
	if fails := ds.Check(ctx, pool, dv.TableName); len(fails) != 0 {
		t.Fatalf("check: %v", fails)
	}
}

func TestOceanRejectsBadResponses(t *testing.T) {
	cases := map[string][]byte{
		"arcgis error":  []byte(`{"error":{"code":400,"message":"Invalid query"}}`),
		"truncated":     geoJSON(t, map[string]any{"type": "FeatureCollection", "features": []any{}, "properties": map[string]any{"exceededTransferLimit": true}}),
		"no features":   geoJSON(t, collection()),
		"not a polygon": geoJSON(t, collection(feature(7, "Point", map[string]any{"type": "Point", "coordinates": []float64{1, 2}}))),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			pool := dbtest.New(t)
			if _, err := loadOcean(t, newPipeline(t, pool, testOcean(t, body, emptyLand(t)))); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestOceanRejectsBadLand(t *testing.T) {
	cases := map[string][]byte{
		"not a feature collection": geoJSON(t, map[string]any{"type": "Polygon", "coordinates": []any{}}),
		"zero polygon features":    geoJSON(t, collection(feature(nil, "", map[string]any{"type": "Point", "coordinates": []float64{1, 2}}))),
	}
	for name, land := range cases {
		t.Run(name, func(t *testing.T) {
			pool := dbtest.New(t)
			if _, err := loadOcean(t, newPipeline(t, pool, testOcean(t, goodMEOW(t), land))); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestOceanClipsLand(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	eco := feature(50, "Land-Water", polygon(rect(-10, 40, 0, 50)))
	land := polygon(rect(-10, 40, -5, 50)) // covers the west half of the ecoregion
	ds := testOcean(t, geoJSON(t, collection(eco)), landGeoJSON(t, land))
	ds.MinCount, ds.MaxCount = 1, 1
	p := newPipeline(t, pool, ds)
	v, err := loadOcean(t, p)
	if err != nil {
		t.Fatalf("transform/load: %v", err)
	}
	dv, _ := p.version(ctx, v)

	// Deep in the land part, >100 km from the remaining water: no match.
	if ids, _ := store.NearbyEcoregions(ctx, pool, dv.TableName, 45, -9.9, 100_000); len(ids) != 0 {
		t.Fatalf("deep land point matched: %v", ids)
	}
	// Inside the remaining water part: matches.
	if ids, _ := store.NearbyEcoregions(ctx, pool, dv.TableName, 45, -2.5, 0); len(ids) != 1 || ids[0] != 50 {
		t.Fatalf("water point: %v", ids)
	}
	// On land, within 100 km of the water part: still matches.
	if ids, _ := store.NearbyEcoregions(ctx, pool, dv.TableName, 45, -5.5, 100_000); len(ids) != 1 || ids[0] != 50 {
		t.Fatalf("near-shore land point: %v", ids)
	}
}

func TestOceanSkipsFullyLandEcoregion(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	eco := feature(60, "All Land", polygon(rect(20, 40, 21, 41)))
	land := polygon(rect(19, 39, 22, 42)) // fully covers the ecoregion
	ds := testOcean(t, geoJSON(t, collection(eco)), landGeoJSON(t, land))
	ds.MinCount, ds.MaxCount = 0, 0
	p := newPipeline(t, pool, ds)
	v, err := loadOcean(t, p)
	if err != nil {
		t.Fatalf("transform/load: %v", err)
	}
	dv, _ := p.version(ctx, v)
	var n int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM "+dv.TableName).Scan(&n)
	if n != 0 {
		t.Fatalf("ecoregions = %d, want 0 (fully covered by land, skipped not failed)", n)
	}
}

func TestOceanCheckCatalogEcoregions(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	ds := testOcean(t, goodMEOW(t), emptyLand(t))
	p := newPipeline(t, pool, ds)
	v, err := loadOcean(t, p)
	if err != nil {
		t.Fatal(err)
	}
	dv, _ := p.version(ctx, v)

	cod := catalogtest.Seafood()
	cod.Regions = []catalog.SeafoodRegion{{EcoregionID: 77, Months: []int{1}}}
	tx, _ := pool.Begin(ctx)
	if err := catalog.Write(ctx, store.New(tx), []catalog.Ingredient{cod}, nil); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)

	fails := ds.Check(ctx, pool, dv.TableName)
	if len(fails) != 1 || !strings.Contains(fails[0], "77") {
		t.Fatalf("fails = %v", fails)
	}
}
