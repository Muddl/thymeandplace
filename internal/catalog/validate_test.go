package catalog_test

import (
	"strings"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
)

func TestValidBuildersPass(t *testing.T) {
	for _, ing := range []catalog.Ingredient{
		catalogtest.Annual(), catalogtest.Hardy(), catalogtest.Perennial(), catalogtest.YearRound(),
		catalogtest.WildMushroom(), catalogtest.Seafood(), catalogtest.Mackerel(),
	} {
		if p := ing.Validate(); len(p) != 0 {
			t.Errorf("%s: unexpected problems %v", ing.Slug, p)
		}
	}
}

func TestInvalidExamplesRejected(t *testing.T) {
	for _, ex := range catalogtest.InvalidExamples() {
		t.Run(ex.Name, func(t *testing.T) {
			problems := ex.Ingredient.Validate()
			for _, p := range problems {
				if p.Field == ex.Field {
					return
				}
			}
			t.Fatalf("want a problem on %q, got %v", ex.Field, problems)
		})
	}
}

func TestAppOnlyRules(t *testing.T) {
	cases := []struct {
		name  string
		ing   catalog.Ingredient
		field string
	}{
		{"unknown category", func() catalog.Ingredient { i := catalogtest.Annual(); i.Category = "grain"; return i }(), "category"},
		{"unknown kind", func() catalog.Ingredient { i := catalogtest.Annual(); i.Kind = "tree"; return i }(), "kind"},
		{"unknown frost", func() catalog.Ingredient { i := catalogtest.Annual(); i.Frost = "brr"; return i }(), "frost"},
		{"seafood without regions", func() catalog.Ingredient { i := catalogtest.Seafood(); i.Regions = nil; return i }(), "regions"},
		{"regions on non-seafood", func() catalog.Ingredient {
			i := catalogtest.Annual()
			i.Regions = catalogtest.Seafood().Regions
			return i
		}(), "regions"},
		{"smallint overflow", func() catalog.Ingredient { i := catalogtest.YearRound(); i.MonthsToHarvest = catalogtest.P(40000); return i }(), "months_to_harvest"},
		// The DB write path uses ON CONFLICT for links, so duplicates are app-only rules.
		{"duplicate pairing", func() catalog.Ingredient { i := catalogtest.Annual(); i.Pairings = []string{"basil", "basil"}; return i }(), "pairings"},
		{"duplicate ecoregion", func() catalog.Ingredient {
			i := catalogtest.Seafood()
			i.Regions = append(i.Regions, catalog.SeafoodRegion{EcoregionID: 25, Months: []int{5}})
			return i
		}(), "regions[1].ecoregion_id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, p := range c.ing.Validate() {
				if p.Field == c.field {
					return
				}
			}
			t.Fatalf("want problem on %q, got %v", c.field, c.ing.Validate())
		})
	}
}

func TestNameLengthCountsCharacters(t *testing.T) {
	i := catalogtest.Annual()
	i.Name = strings.Repeat("é", 80) // 160 bytes, 80 characters
	if p := i.Validate(); len(p) != 0 {
		t.Fatalf("80-character name rejected: %v", p)
	}
}

func TestProblemsCarrySlug(t *testing.T) {
	i := catalogtest.Annual()
	i.Name = ""
	p := i.Validate()
	if len(p) == 0 || p[0].Slug != "tomato" {
		t.Fatalf("problem should carry slug, got %v", p)
	}
}

func TestValidationErrorMessage(t *testing.T) {
	err := &catalog.ValidationError{Problems: []catalog.Problem{{Slug: "a", Field: "name", Message: "is required"}}}
	if !strings.Contains(err.Error(), "a: name is required") {
		t.Fatalf("Error() = %q", err.Error())
	}
}
