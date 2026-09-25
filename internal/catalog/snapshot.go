package catalog

import (
	"fmt"
	"slices"
	"sort"
)

// Snapshot is an immutable, validated view of the whole catalog.
// It is safe for concurrent use.
type Snapshot struct {
	version int64
	items   []Ingredient // sorted by slug; never handed out without cloning
	index   map[string]int
}

// NewSnapshot validates items and builds a snapshot. It deep-copies items.
func NewSnapshot(version int64, items []Ingredient) (*Snapshot, error) {
	cp := make([]Ingredient, len(items))
	for i, it := range items {
		cp[i] = clone(it)
	}
	sort.Slice(cp, func(i, j int) bool { return cp[i].Slug < cp[j].Slug })

	var problems []Problem
	index := make(map[string]int, len(cp))
	for i, it := range cp {
		problems = append(problems, it.Validate()...)
		if _, dup := index[it.Slug]; dup {
			problems = append(problems, Problem{Slug: it.Slug, Field: "slug", Message: "is used by more than one ingredient"})
		}
		index[it.Slug] = i
	}
	for _, it := range cp {
		for _, p := range it.Pairings {
			if _, ok := index[p]; !ok {
				problems = append(problems, Problem{Slug: it.Slug, Field: "pairings", Message: fmt.Sprintf("refers to unknown ingredient %q", p)})
			}
		}
	}
	if len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	return &Snapshot{version: version, items: cp, index: index}, nil
}

func (s *Snapshot) Version() int64 { return s.version }
func (s *Snapshot) Len() int       { return len(s.items) }

// Get returns a copy of one ingredient.
func (s *Snapshot) Get(slug string) (Ingredient, bool) {
	i, ok := s.index[slug]
	if !ok {
		return Ingredient{}, false
	}
	return clone(s.items[i]), true
}

// All returns copies of every ingredient, sorted by slug.
func (s *Snapshot) All() []Ingredient {
	out := make([]Ingredient, len(s.items))
	for i, it := range s.items {
		out[i] = clone(it)
	}
	return out
}

// With returns the catalog that would result from applying upserts and
// deletes. Deleting strips pairings that point at the deleted slug, as the
// database's ON DELETE CASCADE does.
func (s *Snapshot) With(upserts []Ingredient, deletes []string) (*Snapshot, error) {
	next := make(map[string]Ingredient, len(s.items))
	for _, it := range s.items {
		next[it.Slug] = clone(it)
	}
	var problems []Problem
	deleted := map[string]bool{}
	for _, slug := range deletes {
		if _, ok := next[slug]; !ok {
			problems = append(problems, Problem{Slug: slug, Field: "slug", Message: "does not exist"})
			continue
		}
		delete(next, slug)
		deleted[slug] = true
	}
	for slug, it := range next {
		it.Pairings = slices.DeleteFunc(it.Pairings, func(p string) bool { return deleted[p] })
		next[slug] = it
	}
	for _, u := range upserts {
		next[u.Slug] = clone(u)
	}
	if len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	items := make([]Ingredient, 0, len(next))
	for _, it := range next {
		items = append(items, it)
	}
	return NewSnapshot(s.version, items)
}

func clone(it Ingredient) Ingredient {
	it.Pairings = slices.Clone(it.Pairings)
	if it.Regions != nil {
		rs := make([]SeafoodRegion, len(it.Regions))
		for i, r := range it.Regions {
			rs[i] = SeafoodRegion{EcoregionID: r.EcoregionID, Months: slices.Clone(r.Months), PeakMonths: slices.Clone(r.PeakMonths)}
		}
		it.Regions = rs
	}
	it.GrowTempMinC = clonePtr(it.GrowTempMinC)
	it.GrowTempMaxC = clonePtr(it.GrowTempMaxC)
	it.MonthsToHarvest = clonePtr(it.MonthsToHarvest)
	it.ChillMonthsMin = clonePtr(it.ChillMonthsMin)
	it.WarmupTempC = clonePtr(it.WarmupTempC)
	it.MonthsBloomToHarvest = clonePtr(it.MonthsBloomToHarvest)
	it.HarvestSpanMonths = clonePtr(it.HarvestSpanMonths)
	it.FruitingTempMinC = clonePtr(it.FruitingTempMinC)
	it.FruitingTempMaxC = clonePtr(it.FruitingTempMaxC)
	it.MinPrecipMM = clonePtr(it.MinPrecipMM)
	return it
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
