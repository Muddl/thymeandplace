package ingest

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"

	"thymeandplace/internal/store"
)

type PlaceCheck struct{ Query, Country string }

// geonamesUnpinned is why the GeoNames sources carry no sha256 pin.
const geonamesUnpinned = "GeoNames regenerates its dumps at fixed, unversioned URLs"

// Places is the GeoNames cities1000 dataset.
type Places struct {
	SourceList []Source
	SpotChecks []PlaceCheck
}

func NewPlaces() *Places {
	return &Places{
		SourceList: []Source{
			{URL: "https://download.geonames.org/export/dump/cities1000.zip", File: "cities1000.zip", Unpinned: geonamesUnpinned},
			{URL: "https://download.geonames.org/export/dump/admin1CodesASCII.txt", File: "admin1CodesASCII.txt", Unpinned: geonamesUnpinned},
		},
		SpotChecks: []PlaceCheck{
			{"London", "GB"}, {"Paris", "FR"}, {"Tokyo", "JP"}, {"Sydney", "AU"}, {"Cairo", "EG"},
			{"Lagos", "NG"}, {"Lima", "PE"}, {"Toronto", "CA"}, {"Mumbai", "IN"}, {"Moscow", "RU"},
			{"Sao Paulo", "BR"}, {"Reykjavik", "IS"},
		},
	}
}

func (*Places) Name() string   { return "places" }
func (*Places) View() string   { return "place" }
func (*Places) Prefix() string { return "place_v" }
func (*Places) License() string {
	return "CC BY 4.0 — GeoNames (https://www.geonames.org); attribution required"
}
func (*Places) Attribution() []string {
	return []string{"GeoNames, licensed under CC BY 4.0"}
}
func (p *Places) Sources() []Source { return p.SourceList }

func (*Places) Transform(_ context.Context, dir string, w io.Writer) error {
	admin1, err := readAdmin1(filepath.Join(dir, "raw", "admin1CodesASCII.txt"))
	if err != nil {
		return err
	}
	zr, err := zip.OpenReader(filepath.Join(dir, "raw", "cities1000.zip"))
	if err != nil {
		return err
	}
	defer zr.Close()
	var txt *zip.File
	for _, f := range zr.File {
		if f.Name == "cities1000.txt" {
			txt = f
		}
	}
	if txt == nil {
		return fmt.Errorf("cities1000.txt not found in zip")
	}
	rc, err := txt.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"place_id", "name", "ascii_name", "admin1", "country", "population", "lat", "lng"})
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 15 {
			return fmt.Errorf("cities1000.txt line %d: %d fields", line, len(f))
		}
		ascii := f[2]
		if ascii == "" {
			ascii = f[1]
		}
		pop := f[14]
		if pop == "" {
			pop = "0"
		}
		country := f[8]
		if err := cw.Write([]string{f[0], f[1], ascii, admin1[country+"."+f[10]], country, pop, f[4], f[5]}); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

func readAdmin1(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), "\t")
		if len(parts) >= 2 {
			out[parts[0]] = parts[1]
		}
	}
	return out, sc.Err()
}

func (*Places) CreateSQL(t string) string {
	return fmt.Sprintf(`CREATE TABLE %[1]s (
  place_id   integer PRIMARY KEY,
  name       text NOT NULL,
  ascii_name text NOT NULL,
  admin1     text,
  country    text NOT NULL,
  population integer NOT NULL,
  geom       geography(Point, 4326) NOT NULL
);
CREATE INDEX ON %[1]s USING gin (ascii_name gin_trgm_ops);
CREATE INDEX ON %[1]s USING gin (name gin_trgm_ops);
CREATE INDEX ON %[1]s USING gist (geom);`, t)
}

func (*Places) Load(ctx context.Context, tx pgx.Tx, t string, r io.Reader) error {
	if !store.ValidIdent(t) {
		return fmt.Errorf("invalid table name %q", t)
	}
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE place_stage (place_id int, name text, ascii_name text,
		admin1 text, country text, population bigint, lat float8, lng float8) ON COMMIT DROP`); err != nil {
		return err
	}
	if err := copyStage(ctx, tx, "place_stage", r); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s
		SELECT place_id, name, ascii_name, nullif(admin1, ''), country, least(population, 2147483647)::int,
		       ST_SetSRID(ST_MakePoint(lng, lat), 4326)::geography
		FROM place_stage`, t))
	return err
}

func (p *Places) Check(ctx context.Context, db store.DBTX, t string) []string {
	if !store.ValidIdent(t) {
		return []string{fmt.Sprintf("invalid table name %q", t)}
	}
	var fails []string
	var bad int
	if err := db.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE population < 0 OR name = '' OR country = ''`, t)).Scan(&bad); err != nil {
		return []string{"plausibility query: " + err.Error()}
	}
	if bad > 0 {
		fails = append(fails, fmt.Sprintf("%d implausible rows", bad))
	}
	for _, c := range p.SpotChecks {
		rows, err := store.SearchPlaces(ctx, db, t, c.Query, 1)
		switch {
		case err != nil:
			fails = append(fails, fmt.Sprintf("spot check %q: %v", c.Query, err))
		case len(rows) == 0:
			fails = append(fails, fmt.Sprintf("spot check %q: no match", c.Query))
		case rows[0].Country != c.Country:
			fails = append(fails, fmt.Sprintf("spot check %q: top result in %s, want %s", c.Query, rows[0].Country, c.Country))
		}
	}
	return fails
}
