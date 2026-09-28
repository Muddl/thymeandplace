package ingest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"thymeandplace/internal/dbtest"
)

// unresolvedPlaces is a places-shaped dataset whose license terms are not
// confirmed yet, like ocean's today.
type unresolvedPlaces struct{ *Places }

func (unresolvedPlaces) License() string { return LicenseUnresolved + " terms not yet confirmed" }

func TestApproveLicenseRefusesUnresolved(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	p := newPipeline(t, pool, unresolvedPlaces{placesSource(t, 20)})
	v := loadVersion(t, p)

	if _, err := p.ApproveLicense(ctx, v); err == nil || !strings.Contains(err.Error(), "license terms not confirmed") {
		t.Fatalf("approving an unresolved license: %v", err)
	}
	if dv, _ := p.version(ctx, v); dv.LicenseReviewed {
		t.Fatal("refused approval still marked the license reviewed")
	}

	// A review set by hand-written SQL must not let the version go live.
	if _, err := pool.Exec(ctx, `UPDATE dataset_versions SET license_reviewed = true WHERE id = $1`, v); err != nil {
		t.Fatal(err)
	}
	before := viewTarget(t, pool, "place")
	err := p.Validate(ctx, v)
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(err.Error(), "license unresolved") {
		t.Fatalf("validate: want license unresolved, got %v", err)
	}
	if err := p.Swap(ctx, v); err == nil || !strings.Contains(err.Error(), "license unresolved") {
		t.Fatalf("swap: want license unresolved, got %v", err)
	}
	if liveID(t, pool) != 0 || viewTarget(t, pool, "place") != before {
		t.Fatal("an unresolved license went live")
	}
}

// Every shipped dataset records a license and the credit lines the About
// page must carry; ocean stays unresolved until its terms are confirmed.
func TestDatasetsDeclareLicenseAndAttribution(t *testing.T) {
	for _, ds := range []Dataset{NewPlaces(), NewOcean(""), NewClimate("")} {
		if strings.TrimSpace(ds.License()) == "" {
			t.Errorf("%s: no license", ds.Name())
		}
		if len(ds.Attribution()) == 0 {
			t.Errorf("%s: no attribution", ds.Name())
		}
		for _, line := range ds.Attribution() {
			if strings.TrimSpace(line) == "" || strings.Contains(line, LicenseUnresolved) {
				t.Errorf("%s: bad attribution line %q", ds.Name(), line)
			}
		}
	}
	// The MEOW resolution commit removes this check along with the marker.
	if !strings.HasPrefix(NewOcean("").License(), LicenseUnresolved) {
		t.Error("ocean's MEOW terms are unconfirmed: its license must carry the UNRESOLVED marker (docs/reference-data-licenses.md)")
	}
}
