package changeset

import (
	"reflect"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
)

func TestComputeDiff(t *testing.T) {
	cur := snap(t)
	tomato := catalogtest.Annual()
	tomato.Pairings = []string{"basil"}
	tomato.MonthsToHarvest = catalogtest.P(4)
	chard := catalogtest.Hardy()
	chard.Slug, chard.Name = "chard", "Chard"
	proposed, err := cur.With([]catalog.Ingredient{tomato, chard}, []string{"banana"})
	if err != nil {
		t.Fatal(err)
	}
	d := ComputeDiff(cur, proposed)

	if len(d.Added) != 1 || d.Added[0].Slug != "chard" || len(d.Deleted) != 1 || d.Deleted[0].Slug != "banana" {
		t.Fatalf("added=%v deleted=%v", d.Added, d.Deleted)
	}
	if len(d.Changed) != 1 || d.Changed[0].Slug != "tomato" ||
		!reflect.DeepEqual(d.Changed[0].Fields, []FieldChange{{"months_to_harvest", "3", "4"}}) {
		t.Fatalf("changed = %+v", d.Changed)
	}
	if d.Unchanged != 9 {
		t.Fatalf("unchanged = %d, want 9", d.Unchanged)
	}
	if !reflect.DeepEqual(d.Slugs(), []string{"banana", "chard", "tomato"}) {
		t.Fatalf("slugs = %v", d.Slugs())
	}
}

func TestDiffShowsCascadedPairings(t *testing.T) {
	cur := snap(t)
	proposed, _ := cur.With(nil, []string{"basil"})
	d := ComputeDiff(cur, proposed)
	if len(d.Changed) != 1 || d.Changed[0].Slug != "tomato" || d.Changed[0].Fields[0] != (FieldChange{"pairings", "basil", ""}) {
		t.Fatalf("changed = %+v", d.Changed)
	}
}

func TestFieldsFormatting(t *testing.T) {
	f := map[string]string{}
	for _, fv := range Fields(catalogtest.Seafood()) {
		f[fv.Field] = fv.Value
	}
	if f["regions"] != "25: Jan–Apr (peak Feb–Mar)" || f["grow_temp_min_c"] != "" || f["kind"] != "seafood" {
		t.Fatalf("fields = %v", f)
	}
}

func TestDiffEmpty(t *testing.T) {
	cur := snap(t)
	if d := ComputeDiff(cur, cur); !d.Empty() || d.Unchanged != cur.Len() {
		t.Fatalf("diff = %+v", d)
	}
}
