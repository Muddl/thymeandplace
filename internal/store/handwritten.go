package store

// Hand-written queries. sqlc cannot model PostGIS or pg_trgm functions, and
// validation must query versioned tables by name. Table names are checked
// against ValidIdent; every value is a bind parameter.

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var identRE = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// ValidIdent reports whether name is a safe unquoted SQL identifier.
func ValidIdent(name string) bool { return identRE.MatchString(name) }

func withTable(sql, table string) (string, error) {
	if !ValidIdent(table) {
		return "", fmt.Errorf("invalid table name %q", table)
	}
	return strings.ReplaceAll(sql, "{table}", table), nil
}

type CellRow struct {
	CellID   int32
	TminC    []float32
	TmaxC    []float32
	PrecipMM []float32
}

func CellsIn(ctx context.Context, db DBTX, table string, ids []int32) ([]CellRow, error) {
	sql, err := withTable(`SELECT cell_id, tmin_c, tmax_c, precip_mm FROM {table} WHERE cell_id = ANY($1)`, table)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, sql, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CellRow
	for rows.Next() {
		var r CellRow
		if err := rows.Scan(&r.CellID, &r.TminC, &r.TmaxC, &r.PrecipMM); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func NearbyEcoregions(ctx context.Context, db DBTX, table string, lat, lng, meters float64) ([]int32, error) {
	sql, err := withTable(`SELECT ecoregion_id FROM {table}
		WHERE ST_DWithin(geom, ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography, $3)
		ORDER BY ecoregion_id`, table)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, sql, lat, lng, meters)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int32
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

type PlaceRow struct {
	PlaceID    int32
	Name       string
	Admin1     *string
	Country    string
	Population int32
	Lat, Lng   float64
	DistanceM  float64
}

const placeCols = `place_id, name, admin1, country, population, ST_Y(geom::geometry), ST_X(geom::geometry)`

func scanPlaces(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}) ([]PlaceRow, error) {
	defer rows.Close()
	var out []PlaceRow
	for rows.Next() {
		var p PlaceRow
		if err := rows.Scan(&p.PlaceID, &p.Name, &p.Admin1, &p.Country, &p.Population, &p.Lat, &p.Lng, &p.DistanceM); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func SearchPlaces(ctx context.Context, db DBTX, table, q string, limit int) ([]PlaceRow, error) {
	sql, err := withTable(`SELECT `+placeCols+`, 0::float8 FROM {table}
		WHERE ascii_name % $1 OR name % $1
		ORDER BY greatest(similarity(ascii_name, $1), similarity(name, $1)) DESC, population DESC, place_id
		LIMIT $2`, table)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, sql, q, limit)
	if err != nil {
		return nil, err
	}
	return scanPlaces(rows)
}

func NearestPlace(ctx context.Context, db DBTX, table string, lat, lng float64) (PlaceRow, bool, error) {
	sql, err := withTable(`SELECT `+placeCols+`,
		ST_Distance(geom, ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography) FROM {table}
		ORDER BY geom <-> ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography LIMIT 1`, table)
	if err != nil {
		return PlaceRow{}, false, err
	}
	rows, err := db.Query(ctx, sql, lat, lng)
	if err != nil {
		return PlaceRow{}, false, err
	}
	ps, err := scanPlaces(rows)
	if err != nil || len(ps) == 0 {
		return PlaceRow{}, false, err
	}
	return ps[0], true, nil
}

func PlaceByID(ctx context.Context, db DBTX, table string, id int32) (PlaceRow, bool, error) {
	sql, err := withTable(`SELECT `+placeCols+`, 0::float8 FROM {table} WHERE place_id = $1`, table)
	if err != nil {
		return PlaceRow{}, false, err
	}
	rows, err := db.Query(ctx, sql, id)
	if err != nil {
		return PlaceRow{}, false, err
	}
	ps, err := scanPlaces(rows)
	if err != nil || len(ps) == 0 {
		return PlaceRow{}, false, err
	}
	return ps[0], true, nil
}
