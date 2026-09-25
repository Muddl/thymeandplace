// Package catalogjson reads and writes the catalog import/export format.
package catalogjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/ocean"
)

const (
	Format   = "thymeandplace-catalog/v1"
	MaxBytes = 5 << 20
)

var (
	ErrTooLarge      = errors.New("catalog file is larger than 5 MB")
	ErrUnknownFormat = errors.New("unknown catalog format")
)

type Document struct {
	Format      string       `json:"format"`
	ExportedAt  time.Time    `json:"exported_at"`
	Ingredients []Ingredient `json:"ingredients"`
}

type Tips struct {
	Choose string `json:"choose"`
	Store  string `json:"store"`
}

type Region struct {
	EcoregionID int32 `json:"ecoregion_id"`
	Months      []int `json:"months"`
	PeakMonths  []int `json:"peak_months,omitempty"`
}

type Ingredient struct {
	Slug                 string     `json:"slug"`
	Name                 string     `json:"name"`
	Category             string     `json:"category"`
	Kind                 string     `json:"kind"`
	Frost                string     `json:"frost,omitempty"`
	GrowTempC            []*float64 `json:"grow_temp_c,omitempty"`
	MonthsToHarvest      *int       `json:"months_to_harvest,omitempty"`
	StorageMonths        int        `json:"storage_months"`
	ChillMonthsMin       *int       `json:"chill_months_min,omitempty"`
	WarmupTempC          *float64   `json:"warmup_temp_c,omitempty"`
	MonthsBloomToHarvest *int       `json:"months_bloom_to_harvest,omitempty"`
	HarvestSpanMonths    *int       `json:"harvest_span_months,omitempty"`
	FruitingTempC        []*float64 `json:"fruiting_temp_c,omitempty"`
	MinPrecipMM          *float64   `json:"min_precip_mm,omitempty"`
	Regions              []Region   `json:"regions,omitempty"`
	Tips                 Tips       `json:"tips"`
	Pairings             []string   `json:"pairings"`
	Note                 string     `json:"note"`
}

// Decode parses and structurally checks a catalog document. It does not
// validate ingredient rules; the changeset does that.
func Decode(r io.Reader) ([]catalog.Ingredient, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxBytes {
		return nil, ErrTooLarge
	}
	var head struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("not a valid JSON document: %w", err)
	}
	if head.Format != Format {
		return nil, fmt.Errorf("%w %q; expected %q", ErrUnknownFormat, head.Format, Format)
	}
	// Decode the document with ingredients left as raw JSON: decoding the
	// whole tree in one shot (as the old []Ingredient field did) reports a
	// type error, such as a non-integer months_to_harvest, with no item
	// index or slug. Each item below gets its own decoder instead, so the
	// error can be prefixed with which ingredient it came from.
	var doc struct {
		Format      string            `json:"format"`
		ExportedAt  time.Time         `json:"exported_at"`
		Ingredients []json.RawMessage `json:"ingredients"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid catalog file: %w", err)
	}
	if dec.More() {
		return nil, errors.New("invalid catalog file: unexpected data after the document")
	}
	out := make([]catalog.Ingredient, len(doc.Ingredients))
	for i, raw := range doc.Ingredients {
		slug := rawSlug(raw)
		var j Ingredient
		idec := json.NewDecoder(bytes.NewReader(raw))
		idec.DisallowUnknownFields()
		if err := idec.Decode(&j); err != nil {
			return nil, fmt.Errorf("ingredient %d (%q): %w", i+1, slug, err)
		}
		ing, err := j.toDomain()
		if err != nil {
			return nil, fmt.Errorf("ingredient %d (%q): %w", i+1, j.Slug, err)
		}
		out[i] = ing
	}
	return out, nil
}

// rawSlug best-effort extracts the slug field from a raw ingredient item, for
// use in error messages when the item otherwise fails to decode.
func rawSlug(raw json.RawMessage) string {
	var s struct {
		Slug string `json:"slug"`
	}
	_ = json.Unmarshal(raw, &s)
	return s.Slug
}

// Encode writes items as a catalog document.
func Encode(w io.Writer, items []catalog.Ingredient, exportedAt time.Time) error {
	doc := Document{Format: Format, ExportedAt: exportedAt.UTC(), Ingredients: make([]Ingredient, len(items))}
	for i, it := range items {
		doc.Ingredients[i] = fromDomain(catalog.Normalize(it))
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

func pair(p []*float64, field string) (*float64, *float64, error) {
	if p == nil {
		return nil, nil, nil
	}
	if len(p) != 2 {
		return nil, nil, fmt.Errorf("%s must have exactly 2 values [min, max]", field)
	}
	return p[0], p[1], nil
}

func (j Ingredient) toDomain() (catalog.Ingredient, error) {
	ing := catalog.Ingredient{
		Slug: j.Slug, Name: j.Name, Category: catalog.Category(j.Category), Kind: catalog.Kind(j.Kind),
		Frost: catalog.Frost(j.Frost), MonthsToHarvest: j.MonthsToHarvest, StorageMonths: j.StorageMonths,
		ChillMonthsMin: j.ChillMonthsMin, WarmupTempC: j.WarmupTempC,
		MonthsBloomToHarvest: j.MonthsBloomToHarvest, HarvestSpanMonths: j.HarvestSpanMonths,
		MinPrecipMM: j.MinPrecipMM, TipChoose: j.Tips.Choose, TipStore: j.Tips.Store,
		Pairings: j.Pairings, Note: j.Note,
	}
	var err error
	if ing.GrowTempMinC, ing.GrowTempMaxC, err = pair(j.GrowTempC, "grow_temp_c"); err != nil {
		return ing, err
	}
	if ing.FruitingTempMinC, ing.FruitingTempMaxC, err = pair(j.FruitingTempC, "fruiting_temp_c"); err != nil {
		return ing, err
	}
	for _, r := range j.Regions {
		ing.Regions = append(ing.Regions, catalog.SeafoodRegion{
			EcoregionID: ocean.EcoregionID(r.EcoregionID), Months: r.Months, PeakMonths: r.PeakMonths})
	}
	return ing, nil
}

func fromDomain(ing catalog.Ingredient) Ingredient {
	j := Ingredient{
		Slug: ing.Slug, Name: ing.Name, Category: string(ing.Category), Kind: string(ing.Kind),
		Frost: string(ing.Frost), MonthsToHarvest: ing.MonthsToHarvest, StorageMonths: ing.StorageMonths,
		ChillMonthsMin: ing.ChillMonthsMin, WarmupTempC: ing.WarmupTempC,
		MonthsBloomToHarvest: ing.MonthsBloomToHarvest, HarvestSpanMonths: ing.HarvestSpanMonths,
		MinPrecipMM: ing.MinPrecipMM, Tips: Tips{Choose: ing.TipChoose, Store: ing.TipStore},
		Pairings: append([]string{}, ing.Pairings...), Note: ing.Note,
	}
	if ing.GrowTempMinC != nil || ing.GrowTempMaxC != nil {
		j.GrowTempC = []*float64{ing.GrowTempMinC, ing.GrowTempMaxC}
	}
	if ing.FruitingTempMinC != nil || ing.FruitingTempMaxC != nil {
		j.FruitingTempC = []*float64{ing.FruitingTempMinC, ing.FruitingTempMaxC}
	}
	for _, r := range ing.Regions {
		j.Regions = append(j.Regions, Region{EcoregionID: int32(r.EcoregionID), Months: r.Months, PeakMonths: r.PeakMonths})
	}
	return j
}
