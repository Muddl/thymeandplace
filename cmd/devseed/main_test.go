package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/store"
)

func TestRunRefusesWithoutDevseedAllow(t *testing.T) {
	getenv := func(string) string { return "" }
	var stdout, stderr bytes.Buffer

	code := run(getenv, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout should be untouched, got %q", stdout.String())
	}
	const want = "devseed seeds synthetic reference data and resets the sample catalog; set DEVSEED_ALLOW=1 only for local development or end-to-end tests"
	if got := strings.TrimSpace(stderr.String()); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestRunRefusesWhenDevseedAllowIsNotExactlyOne(t *testing.T) {
	for _, v := range []string{"0", "true", "yes", " 1", "1 "} {
		getenv := func(k string) string {
			if k == "DEVSEED_ALLOW" {
				return v
			}
			return ""
		}
		var stdout, stderr bytes.Buffer

		code := run(getenv, &stdout, &stderr)

		if code != 2 {
			t.Fatalf("DEVSEED_ALLOW=%q: exit code = %d, want 2", v, code)
		}
		if !strings.Contains(stderr.String(), "DEVSEED_ALLOW=1") {
			t.Fatalf("DEVSEED_ALLOW=%q: stderr missing guidance: %q", v, stderr.String())
		}
	}
}

// TestRunRefusalIsCheckedBeforeDatabaseURL confirms the guard fires even when
// DATABASE_URL is unset/invalid, i.e. the refusal happens before any attempt
// to connect or migrate.
func TestRunRefusalIsCheckedBeforeDatabaseURL(t *testing.T) {
	getenv := func(k string) string {
		if k == "DATABASE_URL" {
			return "postgres://not-a-real-host-xyz/thymeandplace"
		}
		return ""
	}
	var stdout, stderr bytes.Buffer

	code := run(getenv, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (should refuse before touching DATABASE_URL)", code)
	}
}

func TestSeedRemovesIngredientsNotInSmallCatalog(t *testing.T) {
	ctx := context.Background()
	admin := dbtest.New(t)
	url := dbtest.URLAs(admin, dbtest.Owner)
	if err := seed(ctx, url); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	owner := dbtest.As(t, admin, dbtest.Owner)
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT set_config('app.actor', 'devseed', true)`); err != nil {
		t.Fatal(err)
	}
	chard := catalog.Ingredient{
		Slug: "chard", Name: "Chard", Category: catalog.CategoryVegetable, Kind: catalog.KindYearRound,
		TipChoose: "Glossy leaves.", TipStore: "Fridge, wrapped.",
		Pairings: []string{"kale"},
	}
	if err := catalog.Write(ctx, store.New(tx), []catalog.Ingredient{chard}, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if err := seed(ctx, url); err != nil {
		t.Fatalf("second seed: %v", err)
	}

	snap, err := catalog.Load(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Get("chard"); ok {
		t.Fatal("chard still present after second seed")
	}
	want := catalogtest.SmallCatalog()
	if snap.Len() != len(want) {
		t.Fatalf("catalog len = %d, want %d", snap.Len(), len(want))
	}
	for _, ing := range want {
		if _, ok := snap.Get(ing.Slug); !ok {
			t.Errorf("missing %s after second seed", ing.Slug)
		}
	}
}
