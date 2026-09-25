package store_test

import (
	"context"
	"testing"

	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/store"
)

func TestReferenceSchema(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	for _, view := range []string{"climate_cell", "ocean_ecoregion", "place"} {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+view).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: n=%d err=%v", view, n, err)
		}
	}
	q := store.New(pool)
	ids, err := q.ListLiveDatasetIDs(ctx)
	if err != nil || len(ids) != 0 {
		t.Fatalf("ids=%v err=%v", ids, err)
	}

	ins := `INSERT INTO dataset_versions (dataset, table_name, source_url, source_checksum, license, is_live)
	        VALUES ('places','place_v1','u','c','CC BY 4.0', $1) RETURNING id`
	var live int64
	if err := pool.QueryRow(ctx, ins, true).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, ins, true); err == nil {
		t.Fatal("second live row for the same dataset must be rejected")
	}
	ids, _ = q.ListLiveDatasetIDs(ctx)
	if len(ids) != 1 || ids[0] != live {
		t.Fatalf("ids = %v, want [%d]", ids, live)
	}
}
