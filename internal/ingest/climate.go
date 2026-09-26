package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"

	"thymeandplace/internal/climate"
	"thymeandplace/internal/store"
)

type ClimateCheck struct {
	Name                       string
	Lat, Lng                   float64
	AnnualMeanC, JulyMinusJanC float64
}

// Climate is TerraClimate 1991–2020 (Abatzoglou et al., 2018), CC0.
type Climate struct {
	SourceList []Source
	Checks     []ClimateCheck
	ToleranceC float64
}

// DefaultClimateBase is the THREDDS HTTP file server for the TerraClimate
// 1991–2020 climatologies (verified 2026-09-25). Override with CLIMATE_SOURCE_BASE.
const DefaultClimateBase = "http://thredds.northwestknowledge.net:8080/thredds/fileServer/TERRACLIMATE_ALL/climatology"

func NewClimate(base string) *Climate {
	if base == "" {
		base = DefaultClimateBase
	}
	var src []Source
	for _, v := range []string{"tmin", "tmax", "ppt"} {
		f := "TerraClimate_19912020_" + v + ".nc"
		src = append(src, Source{URL: strings.TrimRight(base, "/") + "/" + f, File: f})
	}
	return &Climate{
		SourceList: src,
		ToleranceC: 4,
		Checks: []ClimateCheck{
			{"London", 51.51, -0.13, 11.3, 13}, {"Moscow", 55.76, 37.62, 5.8, 25},
			{"Yakutsk", 62.03, 129.73, -8.8, 58}, {"Anchorage", 61.22, -149.90, 3.0, 23},
			{"Cairo", 30.04, 31.24, 22.0, 14}, {"Delhi", 28.61, 77.21, 25.0, 17},
			{"Singapore", 1.35, 103.82, 27.5, 0}, {"Nairobi", -1.29, 36.82, 18.0, 0},
			{"Sydney", -33.87, 151.21, 18.3, -10}, {"Buenos Aires", -34.60, -58.38, 18.0, -13},
			{"Cape Town", -33.92, 18.42, 16.8, -8}, {"Ushuaia", -54.80, -68.30, 5.8, -8},
		},
	}
}

func (*Climate) Name() string   { return "climate" }
func (*Climate) View() string   { return "climate_cell" }
func (*Climate) Prefix() string { return "climate_cell_v" }
func (*Climate) License() string {
	return "CC0 1.0 — TerraClimate (Abatzoglou et al., 2018); citation requested"
}
func (c *Climate) Sources() []Source { return c.SourceList }

// Transform copies the output of the Python transform container.
func (*Climate) Transform(_ context.Context, dir string, w io.Writer) error {
	f, err := os.Open(filepath.Join(dir, "out", "climate.csv"))
	if errors.Is(err, os.ErrNotExist) {
		return &ExternalTransformError{Cmd: "docker compose --profile tools run --rm transform /work/climate/" + filepath.Base(dir)}
	}
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func (*Climate) CreateSQL(t string) string {
	return fmt.Sprintf(`CREATE TABLE %s (
  cell_id   integer PRIMARY KEY,
  tmin_c    real[12] NOT NULL,
  tmax_c    real[12] NOT NULL,
  precip_mm real[12] NOT NULL
)`, t)
}

func (*Climate) Load(ctx context.Context, tx pgx.Tx, t string, r io.Reader) error {
	if !store.ValidIdent(t) {
		return fmt.Errorf("invalid table name %q", t)
	}
	_, err := tx.Conn().PgConn().CopyFrom(ctx, r,
		"COPY "+t+" (cell_id, tmin_c, tmax_c, precip_mm) FROM STDIN WITH (FORMAT csv, HEADER true)")
	return err
}

func (c *Climate) Check(ctx context.Context, db store.DBTX, t string) []string {
	if !store.ValidIdent(t) {
		return []string{fmt.Sprintf("invalid table name %q", t)}
	}
	var fails []string
	var bad int
	err := db.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s c WHERE
		c.cell_id < 0 OR c.cell_id >= 1036800
		OR cardinality(c.tmin_c) <> 12 OR cardinality(c.tmax_c) <> 12 OR cardinality(c.precip_mm) <> 12
		OR EXISTS (SELECT 1 FROM unnest(c.tmin_c, c.tmax_c, c.precip_mm) AS u(lo, hi, p)
		           WHERE lo IS NULL OR hi IS NULL OR p IS NULL
		              OR lo < -80 OR lo > 60 OR hi < -80 OR hi > 60 OR lo > hi OR p < 0)`, t)).Scan(&bad)
	if err != nil {
		return []string{"plausibility query: " + err.Error()}
	}
	if bad > 0 {
		fails = append(fails, fmt.Sprintf("%d cells with implausible values", bad))
	}
	for _, chk := range c.Checks {
		if msg := c.spotCheck(ctx, db, t, chk); msg != "" {
			fails = append(fails, msg)
		}
	}
	return fails
}

func (c *Climate) spotCheck(ctx context.Context, db store.DBTX, t string, chk ClimateCheck) string {
	own := climate.CellID(chk.Lat, chk.Lng)
	ids := []int32{int32(own)}
	for _, id := range climate.NeighborCells(own) {
		ids = append(ids, int32(id))
	}
	rows, err := store.CellsIn(ctx, db, t, ids)
	if err != nil {
		return fmt.Sprintf("spot check %s: %v", chk.Name, err)
	}
	cells, err := climate.CellsToNormals(rows)
	if err != nil {
		return fmt.Sprintf("spot check %s: %v", chk.Name, err)
	}
	id, ok := climate.PickNearest(chk.Lat, chk.Lng, own, cells)
	if !ok {
		return fmt.Sprintf("spot check %s: no land cell", chk.Name)
	}
	n := cells[id]
	var annual float64
	for m := 0; m < 12; m++ {
		annual += n.Mean(m) / 12
	}
	seasonal := n.Mean(6) - n.Mean(0)
	switch {
	case math.Abs(annual-chk.AnnualMeanC) > c.ToleranceC:
		return fmt.Sprintf("spot check %s: annual mean %.1f °C, expected %.1f ± %.1f", chk.Name, annual, chk.AnnualMeanC, c.ToleranceC)
	case math.Abs(chk.JulyMinusJanC) < 4 && math.Abs(seasonal) > 4:
		return fmt.Sprintf("spot check %s: July−January %.1f °C, expected near 0", chk.Name, seasonal)
	case math.Abs(chk.JulyMinusJanC) >= 4 && (seasonal*chk.JulyMinusJanC <= 0 || math.Abs(seasonal) < math.Abs(chk.JulyMinusJanC)/2):
		return fmt.Sprintf("spot check %s: July−January %.1f °C, expected about %.1f", chk.Name, seasonal, chk.JulyMinusJanC)
	}
	return ""
}
