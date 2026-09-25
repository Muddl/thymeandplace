package ingest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"thymeandplace/internal/dbtest"
)

// geonamesLine builds one 19-column cities1000.txt row.
func geonamesLine(id int, name, ascii string, lat, lng float64, country, admin1 string, pop int) string {
	f := make([]string, 19)
	f[0], f[1], f[2] = fmt.Sprint(id), name, ascii
	f[4], f[5], f[6], f[7] = fmt.Sprint(lat), fmt.Sprint(lng), "P", "PPL"
	f[8], f[10], f[14] = country, admin1, fmt.Sprint(pop)
	return strings.Join(f, "\t")
}

// placesSource returns a Places dataset served from a local HTTP server with n filler rows.
func placesSource(t *testing.T, n int) *Places {
	lines := []string{
		geonamesLine(1, "London", "London", 51.5085, -0.1257, "GB", "ENG", 8961989),
		geonamesLine(2, "London", "London", 42.9834, -81.233, "CA", "08", 346765),
		geonamesLine(3, "Paris", "Paris", 48.8534, 2.3488, "FR", "11", 2138551),
		geonamesLine(4, "Zürich", "Zurich", 47.3667, 8.55, "CH", "ZH", 341730),
	}
	for i := 0; i < n; i++ {
		lines = append(lines, geonamesLine(100+i, fmt.Sprintf("Town %d", i), fmt.Sprintf("Town %d", i), float64(i%80), float64(i%170), "ZZ", "", 100))
	}
	admin1 := "GB.ENG\tEngland\tEngland\t6269131\nCA.08\tOntario\tOntario\t6093943\nFR.11\tÎle-de-France\tIle-de-France\t3012874\n"
	base := serveFiles(t, map[string][]byte{
		"cities1000.zip":       zipOf(t, map[string][]byte{"cities1000.txt": []byte(strings.Join(lines, "\n") + "\n")}),
		"admin1CodesASCII.txt": []byte(admin1),
	})
	return &Places{
		SourceList: []Source{{base + "/cities1000.zip", "cities1000.zip"}, {base + "/admin1CodesASCII.txt", "admin1CodesASCII.txt"}},
		SpotChecks: []PlaceCheck{{"London", "GB"}, {"Paris", "FR"}},
	}
}

func TestPlacesFetchTransformLoad(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	p := newPipeline(t, pool, placesSource(t, 20))

	v, err := p.Fetch(ctx)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	dv, _ := p.version(ctx, v)
	if dv.TableName != fmt.Sprintf("place_v%d", v) || !strings.HasPrefix(dv.SourceChecksum, "sha256:cities1000.zip=") ||
		dv.LicenseReviewed || !strings.Contains(dv.License, "CC BY 4.0") {
		t.Fatalf("version row = %+v", dv)
	}
	if ok, _ := p.Blob.Exists(ctx, rawKey("places", v, "cities1000.zip")); !ok {
		t.Fatal("raw file not uploaded")
	}

	if err := p.Transform(ctx, v); err != nil {
		t.Fatalf("transform: %v", err)
	}
	// Review Focus 4: transform twice is a no-op.
	if err := p.Transform(ctx, v); err != nil {
		t.Fatalf("second transform: %v", err)
	}
	if err := p.Load(ctx, v); err != nil {
		t.Fatalf("load: %v", err)
	}
	// Review Focus 4: load twice rebuilds cleanly.
	if err := p.Load(ctx, v); err != nil {
		t.Fatalf("second load: %v", err)
	}
	var n int
	var admin1 string
	if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*), max(admin1) FILTER (WHERE place_id = 1) FROM %s`, dv.TableName)).Scan(&n, &admin1); err != nil {
		t.Fatal(err)
	}
	if n != 24 || admin1 != "England" {
		t.Fatalf("rows=%d admin1=%q", n, admin1)
	}
	if fails := p.DS.Check(ctx, pool, dv.TableName); len(fails) != 0 {
		t.Fatalf("check: %v", fails)
	}
}

func TestPlacesCheckReportsSpotFailures(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	ds := placesSource(t, 5)
	ds.SpotChecks = append(ds.SpotChecks, PlaceCheck{"Atlantis", "XX"})
	p := newPipeline(t, pool, ds)
	v, _ := p.Fetch(ctx)
	_ = p.Transform(ctx, v)
	if err := p.Load(ctx, v); err != nil {
		t.Fatal(err)
	}
	dv, _ := p.version(ctx, v)
	fails := ds.Check(ctx, pool, dv.TableName)
	if len(fails) != 1 || !strings.Contains(fails[0], "Atlantis") {
		t.Fatalf("fails = %v", fails)
	}
}
