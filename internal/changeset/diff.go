package changeset

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/season"
)

type FieldValue struct{ Field, Value string }

func fstr(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

func istr(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}

func monthSet(ms []int) (s [12]bool) {
	for _, m := range ms {
		if m >= 1 && m <= 12 {
			s[m-1] = true
		}
	}
	return
}

// Fields flattens an ingredient into display order. Empty string = not set.
func Fields(ing catalog.Ingredient) []FieldValue {
	ing = catalog.Normalize(ing)
	var regions []string
	for _, r := range ing.Regions {
		s := fmt.Sprintf("%d: %s", r.EcoregionID, season.FormatMonths(monthSet(r.Months)))
		if len(r.PeakMonths) > 0 {
			s += " (peak " + season.FormatMonths(monthSet(r.PeakMonths)) + ")"
		}
		regions = append(regions, s)
	}
	return []FieldValue{
		{"slug", ing.Slug}, {"name", ing.Name}, {"category", string(ing.Category)}, {"kind", string(ing.Kind)},
		{"frost", string(ing.Frost)},
		{"grow_temp_min_c", fstr(ing.GrowTempMinC)}, {"grow_temp_max_c", fstr(ing.GrowTempMaxC)},
		{"months_to_harvest", istr(ing.MonthsToHarvest)}, {"storage_months", strconv.Itoa(ing.StorageMonths)},
		{"chill_months_min", istr(ing.ChillMonthsMin)}, {"warmup_temp_c", fstr(ing.WarmupTempC)},
		{"months_bloom_to_harvest", istr(ing.MonthsBloomToHarvest)}, {"harvest_span_months", istr(ing.HarvestSpanMonths)},
		{"fruiting_temp_min_c", fstr(ing.FruitingTempMinC)}, {"fruiting_temp_max_c", fstr(ing.FruitingTempMaxC)},
		{"min_precip_mm", fstr(ing.MinPrecipMM)},
		{"tip_choose", ing.TipChoose}, {"tip_store", ing.TipStore}, {"note", ing.Note},
		{"pairings", strings.Join(ing.Pairings, ", ")}, {"regions", strings.Join(regions, "; ")},
	}
}

type FieldChange struct{ Field, Before, After string }

type ItemChange struct {
	Slug, Name string
	Fields     []FieldChange
}

type Diff struct {
	Added     []catalog.Ingredient
	Changed   []ItemChange
	Deleted   []catalog.Ingredient
	Unchanged int
}

func (d Diff) Empty() bool { return len(d.Added)+len(d.Changed)+len(d.Deleted) == 0 }

// Slugs lists every added, changed or deleted slug, sorted.
func (d Diff) Slugs() []string {
	var out []string
	for _, a := range d.Added {
		out = append(out, a.Slug)
	}
	for _, c := range d.Changed {
		out = append(out, c.Slug)
	}
	for _, x := range d.Deleted {
		out = append(out, x.Slug)
	}
	sort.Strings(out)
	return out
}

// ComputeDiff compares every ingredient in the two snapshots, so changes the
// database would cascade (pairings to a deleted slug) are shown too.
func ComputeDiff(cur, proposed *catalog.Snapshot) Diff {
	var d Diff
	for _, before := range cur.All() {
		after, ok := proposed.Get(before.Slug)
		if !ok {
			d.Deleted = append(d.Deleted, before)
			continue
		}
		var changes []FieldChange
		bf, af := Fields(before), Fields(after)
		for i := range bf {
			if bf[i].Value != af[i].Value {
				changes = append(changes, FieldChange{bf[i].Field, bf[i].Value, af[i].Value})
			}
		}
		if len(changes) == 0 {
			d.Unchanged++
			continue
		}
		d.Changed = append(d.Changed, ItemChange{Slug: after.Slug, Name: after.Name, Fields: changes})
	}
	for _, after := range proposed.All() {
		if _, ok := cur.Get(after.Slug); !ok {
			d.Added = append(d.Added, after)
		}
	}
	return d
}
