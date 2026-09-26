package admin

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
)

func hasField(ps []catalog.Problem, field string) bool {
	for _, p := range ps {
		if p.Field == field {
			return true
		}
	}
	return false
}

func TestParseAnnual(t *testing.T) {
	v := url.Values{
		"slug": {"tomato"}, "name": {"Tomato"}, "category": {"vegetable"}, "kind": {"annual"},
		"frost": {"tender"}, "grow_temp_min_c": {"16"}, "grow_temp_max_c": {"30.5"}, "months_to_harvest": {"3"},
		"storage_months": {"0"}, "tip_choose": {"c"}, "tip_store": {"s"}, "note": {""},
		"pairings": {" basil, garlic ,, olive-oil "},
	}
	ing, ps := parseIngredientForm(v)
	if len(ps) != 0 {
		t.Fatalf("problems: %v", ps)
	}
	if *ing.GrowTempMaxC != 30.5 || *ing.MonthsToHarvest != 3 || ing.StorageMonths != 0 || ing.ChillMonthsMin != nil ||
		!reflect.DeepEqual(ing.Pairings, []string{"basil", "garlic", "olive-oil"}) {
		t.Fatalf("ing = %+v", ing)
	}
}

// Review Focus 2.
func TestParseNumbers(t *testing.T) {
	base := url.Values{"slug": {"x"}, "name": {"X"}, "category": {"vegetable"}, "kind": {"annual"}, "storage_months": {""}}
	for _, bad := range []string{"abc", "1e400", "NaN", "Inf", "-Inf"} {
		v := cloneValues(base)
		v.Set("grow_temp_min_c", bad)
		if _, ps := parseIngredientForm(v); !hasField(ps, "grow_temp_min_c") {
			t.Fatalf("%q accepted", bad)
		}
	}
	v := cloneValues(base)
	v.Set("months_to_harvest", "2.5")
	if _, ps := parseIngredientForm(v); !hasField(ps, "months_to_harvest") {
		t.Fatal("fractional month accepted")
	}
	v = cloneValues(base)
	v.Set("grow_temp_min_c", "0")
	ing, ps := parseIngredientForm(v)
	if len(ps) != 0 || ing.GrowTempMinC == nil || *ing.GrowTempMinC != 0 || ing.GrowTempMaxC != nil || ing.StorageMonths != 0 {
		t.Fatalf("zero/blank handling: %+v %v", ing, ps)
	}
	// Ruling: a whitespace-only numeric field is treated the same as blank
	// (unset), not as a parse error.
	v = cloneValues(base)
	v.Set("grow_temp_min_c", "   ")
	ing, ps = parseIngredientForm(v)
	if len(ps) != 0 || ing.GrowTempMinC != nil {
		t.Fatalf("whitespace-only field not treated as blank: %+v %v", ing, ps)
	}
}

func TestRegionsCRLF(t *testing.T) {
	rs, ps := parseRegions("25: 1,2,3,4 | peak 2,3\r\n30: 10,11\r\n")
	if len(ps) != 0 || len(rs) != 2 || rs[0].EcoregionID != 25 || rs[1].EcoregionID != 30 ||
		!reflect.DeepEqual(rs[1].Months, []int{10, 11}) {
		t.Fatalf("rs=%+v ps=%v", rs, ps)
	}
}

func TestRegionsOversizedEcoregionID(t *testing.T) {
	_, ps := parseRegions(strings.Repeat("9", 40) + ": 1,2")
	if !hasField(ps, "regions[0].months") {
		t.Fatal("40-digit ecoregion ID accepted")
	}
}

func TestRegionsRoundTrip(t *testing.T) {
	rs, ps := parseRegions("25: 1,2,3,4 | peak 2,3\n\n 30 : 10, 11 \n")
	if len(ps) != 0 || len(rs) != 2 || rs[0].EcoregionID != 25 || !reflect.DeepEqual(rs[0].PeakMonths, []int{2, 3}) || rs[1].PeakMonths != nil {
		t.Fatalf("rs=%+v ps=%v", rs, ps)
	}
	if got := formatRegions(rs); got != "25: 1,2,3,4 | peak 2,3\n30: 10,11" {
		t.Fatalf("format = %q", got)
	}
	for _, bad := range []string{"25 1,2", "x: 1", "25: a", "25: 1 | top 2"} {
		if _, ps := parseRegions(bad); !hasField(ps, "regions[0].months") {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestFormValuesRoundTrip(t *testing.T) {
	for _, ing := range catalogtest.SmallCatalog() {
		v := url.Values{}
		for k, val := range formValues(ing) {
			v.Set(k, val)
		}
		got, ps := parseIngredientForm(v)
		if len(ps) != 0 {
			t.Fatalf("%s: %v", ing.Slug, ps)
		}
		if !reflect.DeepEqual(catalog.Normalize(got), catalog.Normalize(ing)) {
			t.Fatalf("%s:\n got %+v\nwant %+v", ing.Slug, catalog.Normalize(got), catalog.Normalize(ing))
		}
	}
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
}
