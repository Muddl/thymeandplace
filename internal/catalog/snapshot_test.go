package catalog_test

import (
	"errors"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
)

func mustSnap(t *testing.T, items []catalog.Ingredient) *catalog.Snapshot {
	t.Helper()
	s, err := catalog.NewSnapshot(7, items)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	return s
}

func TestNewSnapshotSortsAndIndexes(t *testing.T) {
	s := mustSnap(t, catalogtest.SmallCatalog())
	if s.Version() != 7 || s.Len() != 11 {
		t.Fatalf("version/len = %d/%d", s.Version(), s.Len())
	}
	all := s.All()
	for i := 1; i < len(all); i++ {
		if all[i-1].Slug >= all[i].Slug {
			t.Fatalf("All() not sorted: %q before %q", all[i-1].Slug, all[i].Slug)
		}
	}
	if got, ok := s.Get("kale"); !ok || got.Name != "Kale" {
		t.Fatalf("Get(kale) = %v, %v", got, ok)
	}
	if _, ok := s.Get("nope"); ok {
		t.Fatal("Get(nope) should miss")
	}
}

func TestNewSnapshotRejects(t *testing.T) {
	dup := append(catalogtest.SmallCatalog(), catalogtest.Hardy())
	badPair := catalogtest.SmallCatalog()
	badPair[0].Pairings = []string{"unicorn"}
	invalid := catalogtest.SmallCatalog()
	invalid[0].Name = ""

	for name, items := range map[string][]catalog.Ingredient{"duplicate": dup, "unknown pairing": badPair, "invalid item": invalid} {
		t.Run(name, func(t *testing.T) {
			_, err := catalog.NewSnapshot(1, items)
			var ve *catalog.ValidationError
			if !errors.As(err, &ve) || len(ve.Problems) == 0 {
				t.Fatalf("want ValidationError, got %v", err)
			}
		})
	}
}

// Review Focus 4: the snapshot is immutable from the outside.
func TestSnapshotImmutable(t *testing.T) {
	items := catalogtest.SmallCatalog()
	s := mustSnap(t, items)

	items[0].Name = "CHANGED"
	items[0].Pairings[0] = "CHANGED"
	all := s.All()
	all[0].Name = "CHANGED TOO"
	all[0].Regions = nil

	for _, ing := range s.All() {
		if ing.Name == "CHANGED" || ing.Name == "CHANGED TOO" {
			t.Fatalf("snapshot mutated through caller slice: %q", ing.Slug)
		}
	}
	tomato, _ := s.Get("tomato")
	if tomato.Pairings[0] != "basil" {
		t.Fatalf("pairings mutated: %v", tomato.Pairings)
	}
	g, _ := s.Get("tomato")
	g.Pairings[0] = "CHANGED"
	again, _ := s.Get("tomato")
	if again.Pairings[0] != "basil" {
		t.Fatal("Get returned shared pairings slice")
	}
}

func TestWithUpsertAndDelete(t *testing.T) {
	s := mustSnap(t, catalogtest.SmallCatalog())

	changed := catalogtest.Hardy()
	changed.Name = "Curly kale"
	added := catalogtest.Hardy()
	added.Slug, added.Name = "chard", "Chard"

	p, err := s.With([]catalog.Ingredient{changed, added}, []string{"basil"})
	if err != nil {
		t.Fatalf("With: %v", err)
	}
	if p.Version() != s.Version() {
		t.Fatal("With must keep version")
	}
	if k, _ := p.Get("kale"); k.Name != "Curly kale" {
		t.Fatalf("kale not updated: %q", k.Name)
	}
	if _, ok := p.Get("chard"); !ok {
		t.Fatal("chard not added")
	}
	if _, ok := p.Get("basil"); ok {
		t.Fatal("basil not deleted")
	}
	// tomato paired with basil; the cascade strips it.
	if tm, _ := p.Get("tomato"); len(tm.Pairings) != 0 {
		t.Fatalf("pairing to deleted slug kept: %v", tm.Pairings)
	}
	// original untouched
	if _, ok := s.Get("basil"); !ok {
		t.Fatal("With mutated the receiver")
	}
}

func TestWithRejects(t *testing.T) {
	s := mustSnap(t, catalogtest.SmallCatalog())
	if _, err := s.With(nil, []string{"ghost"}); err == nil {
		t.Fatal("deleting an unknown slug must fail")
	}
	bad := catalogtest.Annual()
	bad.Pairings = []string{"ghost"}
	if _, err := s.With([]catalog.Ingredient{bad}, nil); err == nil {
		t.Fatal("pairing to unknown slug must fail")
	}
	// A pairing to an ingredient added in the same change is fine.
	newA := catalogtest.Hardy()
	newA.Slug, newA.Pairings = "chard", []string{"leek"}
	newB := catalogtest.Hardy()
	newB.Slug = "leek"
	if _, err := s.With([]catalog.Ingredient{newA, newB}, nil); err != nil {
		t.Fatalf("pairing within changeset rejected: %v", err)
	}
}
