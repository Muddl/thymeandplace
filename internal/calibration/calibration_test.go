package calibration

import (
	"math"
	"os"
	"testing"
	"testing/fstest"

	calibfixtures "thymeandplace/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/season"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-3 }

func TestJaccard(t *testing.T) {
	var none, all [12]bool
	for i := range all {
		all[i] = true
	}
	half := [12]bool{true, true, true, true, true, true}
	if Jaccard(none, none) != 1 {
		t.Fatal("two empty sets must score 1")
	}
	if Jaccard(all, none) != 0 {
		t.Fatal("disjoint must score 0")
	}
	if !near(Jaccard(all, half), 0.5) {
		t.Fatalf("all vs half = %v", Jaccard(all, half))
	}
}

func TestLoadFixtures(t *testing.T) {
	fx, err := LoadFixtures(os.DirFS("../../testdata/calibration"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fx) != 1 || fx[0].ID != "temperate-test" || len(fx[0].Months["apple"]) != 4 {
		t.Fatalf("got %+v", fx)
	}
}

func TestLoadFixturesRejectsBadFiles(t *testing.T) {
	bad := map[string]string{
		"missing source": `{"id":"a","name":"A","lat":1,"lng":1,"months":{}}`,
		"bad month":      `{"id":"a","name":"A","lat":1,"lng":1,"source":"s","months":{"x":[13]}}`,
		"bad lat":        `{"id":"a","name":"A","lat":91,"lng":1,"source":"s","months":{}}`,
		"unknown field":  `{"id":"a","name":"A","lat":1,"lng":1,"source":"s","months":{},"extra":1}`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			fsys := fstest.MapFS{"f.json": {Data: []byte(body)}}
			if _, err := LoadFixtures(fsys); err == nil {
				t.Fatal("want error")
			}
		})
	}
	dup := fstest.MapFS{
		"a.json": {Data: []byte(`{"id":"x","name":"A","lat":1,"lng":1,"source":"s","months":{}}`)},
		"b.json": {Data: []byte(`{"id":"x","name":"B","lat":1,"lng":1,"source":"s","months":{}}`)},
	}
	if _, err := LoadFixtures(dup); err == nil {
		t.Fatal("duplicate IDs must fail")
	}
}

func TestScore(t *testing.T) {
	fx, err := LoadFixtures(os.DirFS("../../testdata/calibration"))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := catalog.NewSnapshot(1, catalogtest.SmallCatalog())
	if err != nil {
		t.Fatal(err)
	}
	n := climatetest.Temperate()
	rep := Score(snap, []Case{{Fixture: fx[0], Input: season.Input{Normals: &n}}})

	if rep.Compared != 3 {
		t.Fatalf("Compared = %d, want 3", rep.Compared)
	}
	if !near(rep.Score, (1+4.0/6+0)/3) {
		t.Fatalf("Score = %v", rep.Score)
	}
	if !near(rep.PerIngredient["tomato"], 1) || !near(rep.PerIngredient["apple"], 4.0/6) || !near(rep.PerIngredient["banana"], 0) {
		t.Fatalf("PerIngredient = %v", rep.PerIngredient)
	}
	if _, ok := rep.PerIngredient["not-in-catalog"]; ok {
		t.Fatal("slug missing from catalog must be skipped")
	}
	if !near(rep.PerFixture["temperate-test"], rep.Score) {
		t.Fatalf("PerFixture = %v", rep.PerFixture)
	}
}

func TestScoreEmpty(t *testing.T) {
	snap, _ := catalog.NewSnapshot(1, nil)
	if rep := Score(snap, nil); rep.Compared != 0 || rep.Score != 0 {
		t.Fatalf("got %+v", rep)
	}
}

func TestEmbeddedFixturesLoad(t *testing.T) {
	if _, err := LoadFixtures(calibfixtures.FS()); err != nil {
		t.Fatalf("embedded fixtures invalid: %v", err)
	}
}

// A drop of exactly OverrideThreshold is allowed; anything more needs an
// override. The epsilon absorbs float noise in a decimal 0.02.
func TestScoreDropped(t *testing.T) {
	for _, tc := range []struct {
		before, after float64
		want          bool
	}{
		{0.50, 0.48, false},
		{0.50, 0.4799, true},
		{0.671, 0.651, false}, // 0.02 in decimal, 0.020000000000000018 in float64
		{0.5, 0.5, false},
		{0.5, 0.6, false},
	} {
		if got := ScoreDropped(tc.before, tc.after); got != tc.want {
			t.Errorf("ScoreDropped(%v, %v) = %v, want %v", tc.before, tc.after, got, tc.want)
		}
	}
}
