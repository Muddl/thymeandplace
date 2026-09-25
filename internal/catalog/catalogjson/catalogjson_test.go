package catalogjson

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
)

const specExample = `{
  "format": "thymeandplace-catalog/v1",
  "exported_at": "2026-09-24T12:00:00Z",
  "ingredients": [
    {
      "slug": "tomato", "name": "Tomato", "category": "vegetable", "kind": "annual",
      "frost": "tender", "grow_temp_c": [16, 30], "months_to_harvest": 3, "storage_months": 0,
      "tips": { "choose": "Heavy for its size, glossy, fragrant at the stem.",
                "store": "Stem-side down at room temperature; refrigerate only when fully ripe." },
      "pairings": ["basil", "olive-oil", "garlic"],
      "note": ""
    },
    {
      "slug": "atlantic-cod", "name": "Atlantic cod", "category": "seafood", "kind": "seafood",
      "regions": [{ "ecoregion_id": 25, "months": [1,2,3,4], "peak_months": [2,3] }],
      "tips": { "choose": "Firm, translucent flesh; clean sea smell.",
                "store": "Coldest part of the fridge; cook within 2 days." },
      "pairings": ["potato", "butter", "dill"]
    }
  ]
}`

func TestDecodeSpecExample(t *testing.T) {
	items, err := Decode(strings.NewReader(specExample))
	if err != nil {
		t.Fatal(err)
	}
	tom, cod := items[0], items[1]
	if tom.Kind != catalog.KindAnnual || *tom.GrowTempMinC != 16 || *tom.GrowTempMaxC != 30 || *tom.MonthsToHarvest != 3 ||
		tom.Frost != catalog.FrostTender || len(tom.Pairings) != 3 || tom.TipStore == "" {
		t.Fatalf("tomato = %+v", tom)
	}
	if cod.Regions[0].EcoregionID != 25 || !reflect.DeepEqual(cod.Regions[0].PeakMonths, []int{2, 3}) || cod.StorageMonths != 0 {
		t.Fatalf("cod = %+v", cod)
	}
}

// Review Focus 1: the codec round-trips without drift.
func TestRoundTrip(t *testing.T) {
	items := catalogtest.SmallCatalog()
	items[0].GrowTempMinC = catalogtest.P(16.1)
	var buf bytes.Buffer
	if err := Encode(&buf, items, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	got, err := Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	for i := range items {
		if !reflect.DeepEqual(catalog.Normalize(got[i]), catalog.Normalize(items[i])) {
			t.Fatalf("%s:\n got %+v\nwant %+v", items[i].Slug, catalog.Normalize(got[i]), catalog.Normalize(items[i]))
		}
	}
	var again bytes.Buffer // buf was consumed by Decode; re-encode to inspect the text
	_ = Encode(&again, items, time.Time{})
	if !strings.Contains(again.String(), `"pairings": []`) {
		t.Fatal("empty pairings must be written as []")
	}
}

func TestDecodeRejects(t *testing.T) {
	cases := map[string]string{
		"unknown format": `{"format":"other/v9","ingredients":[]}`,
		"missing format": `{"ingredients":[]}`,
		"unknown field":  `{"format":"thymeandplace-catalog/v1","ingredients":[{"slug":"a","colour":"red"}]}`,
		"bad pair":       `{"format":"thymeandplace-catalog/v1","ingredients":[{"slug":"a","grow_temp_c":[1,2,3]}]}`,
		"not json":       `nope`,
		"trailing data":  `{"format":"thymeandplace-catalog/v1","ingredients":[]} {}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(body)); err == nil {
				t.Fatal("want error")
			}
		})
	}
	_, err := Decode(strings.NewReader(`{"format":"x"}`))
	if !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("want ErrUnknownFormat, got %v", err)
	}
}

// Review finding M5: a type error inside one ingredient must name that
// ingredient's index and slug, not surface a bare encoding/json message.
func TestDecodeTypeErrorNamesIngredient(t *testing.T) {
	body := `{"format":"thymeandplace-catalog/v1","ingredients":[
	  {"slug":"a","name":"A","category":"vegetable","kind":"cultivated_year_round","storage_months":0,
	   "tips":{"choose":"c","store":"s"},"pairings":[]},
	  {"slug":"b","name":"B","category":"vegetable","kind":"annual","frost":"tender",
	   "months_to_harvest":3.5,"storage_months":0,"tips":{"choose":"c","store":"s"},"pairings":[]}]}`
	_, err := Decode(strings.NewReader(body))
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), `ingredient 2 ("b")`) {
		t.Fatalf("err = %v, want it to name ingredient 2 (%q)", err, "b")
	}
}

func TestDecodeSizeLimit(t *testing.T) {
	big := `{"format":"thymeandplace-catalog/v1","ingredients":[],"pad":"` + strings.Repeat("x", MaxBytes) + `"}`
	if _, err := Decode(strings.NewReader(big)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}
