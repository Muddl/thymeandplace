package calibrun

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/season"
)

func TestDriftRecordsAndWarns(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	fx, err := calibration.LoadFixtures(os.DirFS("../../testdata/calibration"))
	if err != nil {
		t.Fatal(err)
	}
	n := climatetest.Temperate()
	cases := []calibration.Case{{Fixture: fx[0], Input: season.Input{Normals: &n}}}

	good, _ := catalog.NewSnapshot(1, catalogtest.SmallCatalog())
	worse := func() *catalog.Snapshot {
		tomato := catalogtest.Annual()
		tomato.Pairings = []string{"basil"}
		tomato.MonthsToHarvest = catalogtest.P(1)
		s, _ := good.With([]catalog.Ingredient{tomato}, nil)
		return s
	}()

	var logs bytes.Buffer
	cur := good
	d := &Drift{
		Pool:        pool,
		Snapshot:    func() *catalog.Snapshot { return cur },
		Cases:       func(context.Context) ([]calibration.Case, error) { return cases, nil },
		DataVersion: func() string { return "abc123" },
		Log:         slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Fatal("first run must not warn")
	}
	cur = worse
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "calibration score dropped") {
		t.Fatalf("no drift warning in:\n%s", logs.String())
	}
	var runs int
	var dv string
	_ = pool.QueryRow(ctx, `SELECT count(*), max(data_version) FROM calibration_run`).Scan(&runs, &dv)
	if runs != 2 || dv != "abc123" {
		t.Fatalf("runs=%d dv=%q", runs, dv)
	}
}

func TestDriftNeedsSnapshot(t *testing.T) {
	d := &Drift{Pool: dbtest.New(t), Snapshot: func() *catalog.Snapshot { return nil }}
	if _, err := d.RunOnce(context.Background()); err == nil {
		t.Fatal("want error without a snapshot")
	}
}
