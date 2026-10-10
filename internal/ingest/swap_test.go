package ingest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/dbtest"
)

func viewTarget(t *testing.T, pool *pgxpool.Pool, view string) string {
	t.Helper()
	var def string
	if err := pool.QueryRow(context.Background(), `SELECT pg_get_viewdef($1::regclass)`, view).Scan(&def); err != nil {
		t.Fatal(err)
	}
	return def
}

func liveID(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	_ = pool.QueryRow(context.Background(), `SELECT coalesce(max(id), 0) FROM dataset_versions WHERE dataset = 'places' AND is_live`).Scan(&id)
	return id
}

func loadVersion(t *testing.T, p *Pipeline) int64 {
	t.Helper()
	ctx := context.Background()
	v, err := p.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Transform(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := p.Load(ctx, v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSwapRequiresReviewedLicense(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	p := newPipeline(t, pool, placesSource(t, 20))
	v := loadVersion(t, p)

	err := p.Swap(ctx, v)
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(err.Error(), "license") {
		t.Fatalf("want license failure, got %v", err)
	}
	if liveID(t, pool) != 0 {
		t.Fatal("unreviewed version went live")
	}
}

func TestSwapRollbackAndNotify(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	ds := placesSource(t, 20)
	p := newPipeline(t, pool, ds)

	conn, _ := pool.Acquire(ctx)
	defer conn.Release()
	_, _ = conn.Exec(ctx, "LISTEN data_version_changed")

	v1 := loadVersion(t, p)
	if _, err := p.ApproveLicense(ctx, v1); err != nil {
		t.Fatal(err)
	}
	if err := p.Swap(ctx, v1); err != nil {
		t.Fatalf("swap v1: %v", err)
	}
	if liveID(t, pool) != v1 || !strings.Contains(viewTarget(t, pool, "place"), "place_v") {
		t.Fatalf("live=%d view=%s", liveID(t, pool), viewTarget(t, pool, "place"))
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if n, err := conn.Conn().WaitForNotification(wctx); err != nil || n.Payload != "dataset" {
		t.Fatalf("notify: %+v %v", n, err)
	}

	// Same sources and license: the review is inherited.
	v2 := loadVersion(t, p)
	if dv, _ := p.version(ctx, v2); !dv.LicenseReviewed {
		t.Fatal("identical license/sources should inherit review")
	}
	if err := p.Swap(ctx, v2); err != nil {
		t.Fatalf("swap v2: %v", err)
	}
	// Review Focus 4: the live version cannot be reloaded.
	if err := p.Load(ctx, v2); err == nil || !strings.Contains(err.Error(), "live") {
		t.Fatalf("reloading live version: %v", err)
	}

	if err := p.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if liveID(t, pool) != v1 {
		t.Fatalf("after rollback live = %d, want %d", liveID(t, pool), v1)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM place`).Scan(&n)
	if n != 24 {
		t.Fatalf("view rows after rollback = %d", n)
	}
	if err := p.Rollback(ctx); err == nil {
		t.Fatal("rolling back past the first promoted version must fail")
	}
}

// Review Focus M3: rollback must not return to a version that was already
// rolled back away from. v1 -> swap v2 -> rollback (back to v1) -> swap v3 ->
// rollback must land on v1, not v2 (v2's promotion was already superseded).
func TestRollbackSkipsPreviouslyRolledBackVersion(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	p := newPipeline(t, pool, placesSource(t, 20))

	v1 := loadVersion(t, p)
	if _, err := p.ApproveLicense(ctx, v1); err != nil {
		t.Fatal(err)
	}
	if err := p.Swap(ctx, v1); err != nil {
		t.Fatalf("swap v1: %v", err)
	}

	v2 := loadVersion(t, p)
	if dv, _ := p.version(ctx, v2); !dv.LicenseReviewed {
		t.Fatal("identical license/sources should inherit review")
	}
	if err := p.Swap(ctx, v2); err != nil {
		t.Fatalf("swap v2: %v", err)
	}
	if err := p.Rollback(ctx); err != nil {
		t.Fatalf("rollback to v1: %v", err)
	}
	if liveID(t, pool) != v1 {
		t.Fatalf("after first rollback live = %d, want %d", liveID(t, pool), v1)
	}

	v3 := loadVersion(t, p)
	if dv, _ := p.version(ctx, v3); !dv.LicenseReviewed {
		t.Fatal("identical license/sources should inherit review")
	}
	if err := p.Swap(ctx, v3); err != nil {
		t.Fatalf("swap v3: %v", err)
	}
	if err := p.Rollback(ctx); err != nil {
		t.Fatalf("rollback to v1 (skipping superseded v2): %v", err)
	}
	if liveID(t, pool) != v1 {
		t.Fatalf("after second rollback live = %d, want v1 (%d), not v2 (%d)", liveID(t, pool), v1, v2)
	}
}

// Review Focus 5: failed validation leaves live data untouched.
func TestFailedValidationLeavesLiveUnchanged(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	p := newPipeline(t, pool, placesSource(t, 20))
	v1 := loadVersion(t, p)
	_, _ = p.ApproveLicense(ctx, v1)
	if err := p.Swap(ctx, v1); err != nil {
		t.Fatal(err)
	}
	before := viewTarget(t, pool, "place")

	p.DS = placesSource(t, 2) // 6 rows vs 24: outside ±5%
	v2 := loadVersion(t, p)
	_, _ = p.ApproveLicense(ctx, v2)
	err := p.Swap(ctx, v2)
	if err == nil || !strings.Contains(err.Error(), "row count") {
		t.Fatalf("want row-count failure, got %v", err)
	}
	if liveID(t, pool) != v1 || viewTarget(t, pool, "place") != before {
		t.Fatal("failed swap changed live data")
	}
}

// Spec §4.5: an approval covers exact bytes.
func TestReviewCarriesOverOnlyForSameBytes(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	files := map[string][]byte{"cities1000.zip": []byte("v1 zip"), "admin1CodesASCII.txt": []byte("v1 admin")}
	p := newPipeline(t, pool, pinnedPlaces(t, files, nil))
	reviewed := func(v int64) bool {
		t.Helper()
		dv, err := p.version(ctx, v)
		if err != nil {
			t.Fatal(err)
		}
		return dv.LicenseReviewed
	}

	v1, err := p.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ApproveLicense(ctx, v1); err != nil {
		t.Fatal(err)
	}
	files["admin1CodesASCII.txt"] = []byte("v2 admin, changed")
	v2, err := p.Fetch(ctx)
	if err != nil || reviewed(v2) {
		t.Fatalf("changed bytes must not inherit the review: v2=%d reviewed=%v err=%v", v2, reviewed(v2), err)
	}
	files["admin1CodesASCII.txt"] = []byte("v1 admin")
	v3, err := p.Fetch(ctx)
	if err != nil || !reviewed(v3) {
		t.Fatalf("v1's exact bytes again must inherit the review: v3=%d reviewed=%v err=%v", v3, reviewed(v3), err)
	}
}
