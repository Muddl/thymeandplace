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
