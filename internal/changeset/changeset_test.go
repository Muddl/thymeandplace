package changeset

import (
	"context"
	"strings"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/ocean"
)

// fakeEco reports every ID in missing as absent.
type fakeEco struct{ missing map[ocean.EcoregionID]bool }

func (f fakeEco) Missing(_ context.Context, ids []ocean.EcoregionID) ([]ocean.EcoregionID, error) {
	var out []ocean.EcoregionID
	for _, id := range ids {
		if f.missing[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

func snap(t *testing.T) *catalog.Snapshot {
	t.Helper()
	s, err := catalog.NewSnapshot(5, catalogtest.SmallCatalog())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func hasProblem(ps []catalog.Problem, slug, field string) bool {
	for _, p := range ps {
		if p.Slug == slug && strings.HasPrefix(p.Field, field) {
			return true
		}
	}
	return false
}

func TestValidateOK(t *testing.T) {
	k := catalogtest.Hardy()
	k.Name = "Curly kale"
	proposed, problems, err := Validate(context.Background(), FormChangeset(k), snap(t), fakeEco{})
	if err != nil || len(problems) != 0 {
		t.Fatalf("problems=%v err=%v", problems, err)
	}
	if got, _ := proposed.Get("kale"); got.Name != "Curly kale" {
		t.Fatal("proposed snapshot not updated")
	}
}

func TestValidateProblems(t *testing.T) {
	bad := catalogtest.Annual()
	bad.Name = ""
	unknownPair := catalogtest.Hardy()
	unknownPair.Pairings = []string{"unicorn"}
	missingEco := catalogtest.Seafood()
	missingEco.Regions[0].EcoregionID = 999

	cases := []struct {
		name        string
		cs          Changeset
		slug, field string
	}{
		{"field rule", FormChangeset(bad), "tomato", "name"},
		{"unknown pairing", FormChangeset(unknownPair), "kale", "pairings"},
		{"missing ecoregion", FormChangeset(missingEco), "atlantic-cod", "regions[0].ecoregion_id"},
		{"delete unknown", DeleteChangeset("ghost"), "ghost", "slug"},
		{"save and delete", Changeset{Source: SourceImport, Upserts: []catalog.Ingredient{catalogtest.Hardy()}, Deletes: []string{"kale"}}, "kale", "slug"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eco := fakeEco{missing: map[ocean.EcoregionID]bool{999: true}}
			p, problems, err := Validate(context.Background(), c.cs, snap(t), eco)
			if err != nil || p != nil || !hasProblem(problems, c.slug, c.field) {
				t.Fatalf("snapshot=%v problems=%v err=%v", p, problems, err)
			}
		})
	}
}

// Review Focus 2: duplicate slugs in one import are a per-item problem.
func TestValidateDuplicateSlugInImport(t *testing.T) {
	file := `{"format":"thymeandplace-catalog/v1","ingredients":[
	  {"slug":"chard","name":"Chard","category":"vegetable","kind":"cultivated_year_round","tips":{"choose":"c","store":"s"},"pairings":[]},
	  {"slug":"chard","name":"Chard again","category":"vegetable","kind":"cultivated_year_round","tips":{"choose":"c","store":"s"},"pairings":[]}]}`
	cs, err := ImportChangeset(strings.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	p, problems, _ := Validate(context.Background(), cs, snap(t), fakeEco{})
	if p != nil || !hasProblem(problems, "chard", "slug") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestImportPairingToLaterItem(t *testing.T) {
	file := `{"format":"thymeandplace-catalog/v1","ingredients":[
	  {"slug":"chard","name":"Chard","category":"vegetable","kind":"cultivated_year_round","tips":{"choose":"c","store":"s"},"pairings":["leek"]},
	  {"slug":"leek","name":"Leek","category":"vegetable","kind":"cultivated_year_round","tips":{"choose":"c","store":"s"},"pairings":[]}]}`
	cs, err := ImportChangeset(strings.NewReader(file))
	if err != nil || cs.Source != SourceImport || len(cs.Deletes) != 0 {
		t.Fatalf("cs=%+v err=%v", cs, err)
	}
	if _, problems, _ := Validate(context.Background(), cs, snap(t), fakeEco{}); len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
}

// Review finding M1: an import with a real field that rounds to +Inf as
// float32 must be rejected here, before it can ever reach Export (which
// would otherwise fail the whole catalog with "json: unsupported value").
func TestImportNonFiniteRealFieldRejected(t *testing.T) {
	file := `{"format":"thymeandplace-catalog/v1","ingredients":[
	  {"slug":"tomato","name":"Tomato","category":"vegetable","kind":"annual","frost":"tender",
	   "grow_temp_c":[16,1e39],"months_to_harvest":3,"storage_months":0,
	   "tips":{"choose":"c","store":"s"},"pairings":[]}]}`
	cs, err := ImportChangeset(strings.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	_, problems, err := Validate(context.Background(), cs, snap(t), fakeEco{})
	if err != nil || !hasProblem(problems, "tomato", "grow_temp_max_c") {
		t.Fatalf("problems=%v err=%v", problems, err)
	}
}

// Review finding M4: Validate used to return on field problems before
// checking ecoregions, hiding a missing-ecoregion problem on a different
// item in the same change.
func TestValidateReportsFieldAndMissingEcoregionTogether(t *testing.T) {
	badName := catalogtest.Annual()
	badName.Name = "" // field problem on item A (tomato)
	missingEco := catalogtest.Seafood()
	missingEco.Regions[0].EcoregionID = 999 // missing ecoregion on item B (atlantic-cod)

	cs := Changeset{Source: SourceImport, Upserts: []catalog.Ingredient{badName, missingEco}}
	eco := fakeEco{missing: map[ocean.EcoregionID]bool{999: true}}
	p, problems, err := Validate(context.Background(), cs, snap(t), eco)
	if err != nil || p != nil {
		t.Fatalf("p=%v err=%v", p, err)
	}
	if !hasProblem(problems, "tomato", "name") {
		t.Fatalf("missing field problem: %v", problems)
	}
	if !hasProblem(problems, "atlantic-cod", "regions[0].ecoregion_id") {
		t.Fatalf("missing ecoregion problem: %v", problems)
	}
}

func TestUserMessage(t *testing.T) {
	if !strings.Contains(UserMessage(ErrStale), "Preview again") || UserMessage(nil) != "" {
		t.Fatal("UserMessage")
	}
}
