package ingest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"thymeandplace/internal/climate"
	"thymeandplace/internal/dbtest"
)

func arr(vals [12]float64) string {
	parts := make([]string, 12)
	for i, v := range vals {
		parts[i] = fmt.Sprintf("%.2f", v)
	}
	return `"{` + strings.Join(parts, ",") + `}"`
}

func climateCSV(rows map[int][3][12]float64) string {
	var b strings.Builder
	b.WriteString("cell_id,tmin_c,tmax_c,precip_mm\n")
	for id, r := range rows {
		fmt.Fprintf(&b, "%d,%s,%s,%s\n", id, arr(r[0]), arr(r[1]), arr(r[2]))
	}
	return b.String()
}

func flat(v float64) (out [12]float64) {
	for i := range out {
		out[i] = v
	}
	return
}

func TestClimateExternalTransform(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	base := serveFiles(t, map[string][]byte{"a_tmin.nc": []byte("x"), "a_tmax.nc": []byte("x"), "a_ppt.nc": []byte("x")})
	ds := &Climate{SourceList: []Source{{base + "/a_tmin.nc", "a_tmin.nc"}, {base + "/a_tmax.nc", "a_tmax.nc"}, {base + "/a_ppt.nc", "a_ppt.nc"}}}
	p := newPipeline(t, pool, ds)
	v, err := p.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = p.Transform(ctx, v)
	var ext *ExternalTransformError
	if !errors.As(err, &ext) || !strings.Contains(ext.Cmd, fmt.Sprintf("/work/climate/v%d", v)) {
		t.Fatalf("want external transform error, got %v", err)
	}

	// Once the container has written its output, transform uploads it.
	out := filepath.Join(p.versionDir(v), "out")
	_ = os.MkdirAll(out, 0o755)
	london := climate.CellID(51.5, -0.1)
	csv := climateCSV(map[int][3][12]float64{london: {flat(7), flat(15), flat(50)}})
	if err := os.WriteFile(filepath.Join(out, "climate.csv"), []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Transform(ctx, v); err != nil {
		t.Fatalf("transform with output: %v", err)
	}
	if err := p.Load(ctx, v); err != nil {
		t.Fatalf("load: %v", err)
	}
	dv, _ := p.version(ctx, v)
	var got float32
	if err := pool.QueryRow(ctx, "SELECT tmax_c[1] FROM "+dv.TableName+" WHERE cell_id = $1", london).Scan(&got); err != nil || got != 15 {
		t.Fatalf("tmax = %v %v", got, err)
	}
}

func TestClimateCheck(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	ds := &Climate{
		Checks:     []ClimateCheck{{Name: "London", Lat: 51.5, Lng: -0.1, AnnualMeanC: 11, JulyMinusJanC: 13}},
		ToleranceC: 4,
	}
	london := climate.CellID(51.5, -0.1)
	good := [3][12]float64{
		{2, 2, 4, 5, 8, 11, 13, 13, 11, 8, 5, 3},
		{8, 9, 12, 15, 18, 21, 23, 23, 20, 16, 11, 9},
		flat(50),
	}
	badPrecip := good
	badPrecip[2][3] = -1
	flipped := [3][12]float64{good[0], good[1], good[2]}
	for m := 0; m < 12; m++ { // southern-hemisphere pattern at a northern point
		flipped[0][m], flipped[1][m] = good[0][(m+6)%12], good[1][(m+6)%12]
	}

	cases := []struct {
		name string
		rows map[int][3][12]float64
		want string
	}{
		{"good", map[int][3][12]float64{london: good}, ""},
		{"negative precip", map[int][3][12]float64{london: badPrecip}, "implausible"},
		{"hemisphere flipped", map[int][3][12]float64{london: flipped}, "London"},
		{"missing cell", map[int][3][12]float64{london + 7: good}, "no land cell"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			table := fmt.Sprintf("climate_cell_vtest%d", i)
			tx, _ := pool.Begin(ctx)
			if _, err := tx.Exec(ctx, ds.CreateSQL(table)); err != nil {
				t.Fatal(err)
			}
			if err := ds.Load(ctx, tx, table, strings.NewReader(climateCSV(c.rows))); err != nil {
				t.Fatal(err)
			}
			_ = tx.Commit(ctx)
			fails := ds.Check(ctx, pool, table)
			if c.want == "" && len(fails) != 0 {
				t.Fatalf("unexpected failures %v", fails)
			}
			if c.want != "" && (len(fails) == 0 || !strings.Contains(strings.Join(fails, ";"), c.want)) {
				t.Fatalf("want failure containing %q, got %v", c.want, fails)
			}
		})
	}
}
