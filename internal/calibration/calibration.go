// Package calibration scores a catalog snapshot against published seasonal
// guides using mean per-ingredient Jaccard overlap.
package calibration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/season"
)

// Fixture is one location's months per ingredient from a published guide.
type Fixture struct {
	ID     string           `json:"id"`
	Name   string           `json:"name"`
	Lat    float64          `json:"lat"`
	Lng    float64          `json:"lng"`
	Source string           `json:"source"`
	Months map[string][]int `json:"months"`
}

// LoadFixtures reads and validates every *.json file at the root of fsys.
func LoadFixtures(fsys fs.FS) ([]Fixture, error) {
	names, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return nil, err
	}
	seen := map[string]string{}
	var out []Fixture
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		var f Fixture
		if err := dec.Decode(&f); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if err := f.validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if other, dup := seen[f.ID]; dup {
			return nil, fmt.Errorf("%s: id %q already used by %s", name, f.ID, other)
		}
		seen[f.ID] = path.Base(name)
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f Fixture) validate() error {
	switch {
	case f.ID == "" || f.Name == "":
		return fmt.Errorf("id and name are required")
	case f.Source == "":
		return fmt.Errorf("source is required")
	case f.Lat < -90 || f.Lat > 90 || f.Lng < -180 || f.Lng > 180:
		return fmt.Errorf("lat/lng out of range")
	}
	for slug, months := range f.Months {
		for _, m := range months {
			if m < 1 || m > 12 {
				return fmt.Errorf("%s: month %d out of range", slug, m)
			}
		}
	}
	return nil
}

// Case pairs a fixture with the engine input for its location.
type Case struct {
	Fixture Fixture
	Input   season.Input
}

// Report is the result of scoring one snapshot.
type Report struct {
	Score         float64            // mean over all compared (fixture, ingredient) pairs
	Compared      int                // number of pairs compared
	PerIngredient map[string]float64 // mean per ingredient across fixtures
	PerFixture    map[string]float64 // mean per fixture across ingredients
}

// Score compares engine months with guide months for every ingredient
// present in both the snapshot and a fixture.
func Score(snap *catalog.Snapshot, cases []Case) Report {
	rep := Report{PerIngredient: map[string]float64{}, PerFixture: map[string]float64{}}
	ingSum, ingN := map[string]float64{}, map[string]int{}
	var total float64

	for _, c := range cases {
		slugs := make([]string, 0, len(c.Fixture.Months))
		for s := range c.Fixture.Months {
			slugs = append(slugs, s)
		}
		sort.Strings(slugs)

		var fSum float64
		fN := 0
		for _, slug := range slugs {
			ing, ok := snap.Get(slug)
			if !ok {
				continue
			}
			months, _, _ := season.Year(c.Input, ing)
			var engine, guide [12]bool
			for m, s := range months {
				engine[m] = s != season.Out
			}
			for _, m := range c.Fixture.Months[slug] {
				guide[m-1] = true
			}
			j := Jaccard(engine, guide)
			fSum += j
			fN++
			ingSum[slug] += j
			ingN[slug]++
			total += j
			rep.Compared++
		}
		if fN > 0 {
			rep.PerFixture[c.Fixture.ID] = fSum / float64(fN)
		}
	}
	for slug, sum := range ingSum {
		rep.PerIngredient[slug] = sum / float64(ingN[slug])
	}
	if rep.Compared > 0 {
		rep.Score = total / float64(rep.Compared)
	}
	return rep
}

// Jaccard is |a ∩ b| / |a ∪ b|; two empty sets score 1.
func Jaccard(a, b [12]bool) float64 {
	inter, union := 0, 0
	for i := 0; i < 12; i++ {
		if a[i] && b[i] {
			inter++
		}
		if a[i] || b[i] {
			union++
		}
	}
	if union == 0 {
		return 1
	}
	return float64(inter) / float64(union)
}

// OverrideThreshold is the largest calibration score drop allowed without an
// explicit override, both for catalog edits (changeset) and in the daily
// drift check (calibrun).
const OverrideThreshold = 0.02

// ScoreDropped reports a drop of more than OverrideThreshold. The epsilon
// keeps a drop of exactly 0.02 (in decimal) from tripping on float noise.
func ScoreDropped(before, after float64) bool {
	return before-after > OverrideThreshold+1e-9
}
