package changeset

import (
	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/season"
)

type Bars struct {
	Fixture             string
	Before, After       [12]season.Status
	HadBefore, HasAfter bool
}

type IngredientPreview struct {
	Slug, Name              string
	ScoreBefore, ScoreAfter *float64 // nil when no fixture lists the ingredient
	Bars                    []Bars
}

type Preview struct {
	Diff          Diff
	Before, After calibration.Report
	Delta         float64
	NeedsOverride bool
	Items         []IngredientPreview
}

// BuildPreview scores both snapshots and draws before/after bars for every
// changed ingredient at every fixture location.
func BuildPreview(cur, proposed *catalog.Snapshot, cases []calibration.Case) Preview {
	d := ComputeDiff(cur, proposed)
	before, after := calibration.Score(cur, cases), calibration.Score(proposed, cases)
	p := Preview{
		Diff: d, Before: before, After: after,
		Delta: after.Score - before.Score, NeedsOverride: calibration.ScoreDropped(before.Score, after.Score),
	}
	for _, slug := range d.Slugs() {
		b, hadB := cur.Get(slug)
		a, hasA := proposed.Get(slug)
		ip := IngredientPreview{Slug: slug, Name: a.Name}
		if !hasA {
			ip.Name = b.Name
		}
		if v, ok := before.PerIngredient[slug]; ok {
			ip.ScoreBefore = &v
		}
		if v, ok := after.PerIngredient[slug]; ok {
			ip.ScoreAfter = &v
		}
		for _, c := range cases {
			bars := Bars{Fixture: c.Fixture.Name, HadBefore: hadB, HasAfter: hasA}
			if hadB {
				bars.Before, _, _ = season.Year(c.Input, b)
			}
			if hasA {
				bars.After, _, _ = season.Year(c.Input, a)
			}
			ip.Bars = append(ip.Bars, bars)
		}
		p.Items = append(p.Items, ip)
	}
	return p
}
