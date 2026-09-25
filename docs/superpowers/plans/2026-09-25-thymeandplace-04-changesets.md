# Thyme & Place — Plan 4: Changesets, Import/Export, Calibration Preview and Drift Check Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route every catalog edit (form save, delete, JSON import) through one changeset path — build → validate → preview (field diff, calibration before/after, sample-city season bars) → apply in one audited transaction — and add the daily calibration drift check.

**Architecture:** `catalogjson` converts between the public JSON format and domain ingredients. `changeset` is mostly pure: `Validate` produces the proposed snapshot or per-field problems, `ComputeDiff` compares snapshots field by field, and `BuildPreview` scores both snapshots and draws bars. `changeset.Service` adds persistence: it stores previews in `pending_changeset` and applies them under a row lock on `catalog_meta`, rejecting stale, expired, unconfirmed or un-overridden changes. `calibrun` resolves fixture locations to engine inputs and runs the drift job.

**Tech Stack:** Go ≥ 1.25, pgx v5, sqlc, `encoding/json`.

**Spec:** `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` (§9.2, §9.3, §9.4, §13.2 drift check, §13.3)

**Depends on:** Plan 1 (`catalog`, `season`, `calibration`, `catalogtest`, `climatetest`, `calibfixtures`), Plan 2 (`catalog.Write/Load`, triggers, `dbtest`, `store`), Plan 3 (`climate.Service`, `ocean.Service`, `dbtest.LoadReference`).

## Global Constraints

- JSON format id: `thymeandplace-catalog/v1`. Anything else is rejected. Maximum upload size: **5 MB** (5 × 2²⁰ bytes). Unknown JSON fields are rejected.
- Import is **upsert by slug only** and never deletes. For each ingredient in the file, `pairings` and `regions` replace the stored lists (a missing list means empty).
- Pending changesets expire after **1 hour**.
- Calibration override: required when the score drops by **more than 0.02**. An override is recorded in the history rows' `after._meta` as `{"override":true,"score_before":…,"score_after":…}`.
- Apply is one transaction that sets `app.actor`, `app.change_id` (= pending id) and `app.meta`, writes, and deletes the pending row. It is rejected when the catalog version moved since the preview: "The catalog changed since this preview. Preview again."
- Delete changesets require the typed slug to match exactly.
- Preview shows: added/changed/deleted with field-level before/after; unchanged items counted, not listed; overall score before/after and per changed ingredient; 12-month bars before/after for every fixture location.
- Drift check: daily, stores `(ran_at, data_version, score, compared)` in `calibration_run`, and logs at WARN when the score is more than 0.02 below the previous run.
- Commit after every task. Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Export, then re-import the same file unchanged**: the preview shows zero added, zero changed, and no score change, with no phantom diffs from float precision or list order. Pinned in Task 1 (codec) and Task 4 (service).
2. **An import file listing the same slug twice**: a per-item problem is reported and nothing is stored. Pinned in Task 2.
3. **Applying a preview after its hour is up**: the owner sees "This preview has expired or was already applied. Preview again.", not a 500. Pinned in Task 5.
4. **Double-clicking Apply** (the same pending id twice): the catalog changes once, and the second request gets the same friendly expired/applied message. Pinned in Task 5.
5. **The first import into an empty catalog**: nothing to compare, so no override is demanded, no division by zero, and the preview lists every item as added. Pinned in Task 4.

---

## File Structure

```
migrations/00005_changesets.sql          # pending_changeset, calibration_run
internal/store/queries/changeset.sql
internal/catalog/catalogjson/catalogjson.go
internal/catalog/catalogjson/catalogjson_test.go
internal/changeset/changeset.go          # Changeset, constructors, Validate, errors
internal/changeset/changeset_test.go
internal/changeset/diff.go               # FieldValue, ComputeDiff
internal/changeset/diff_test.go
internal/changeset/preview.go            # Preview, BuildPreview, NeedsOverride
internal/changeset/preview_test.go
internal/changeset/service.go            # Service: Preview, Reopen, Apply
internal/changeset/service_test.go
internal/calibrun/cases.go               # Cases resolver
internal/calibrun/cases_test.go
internal/calibrun/drift.go               # Drift job
internal/calibrun/drift_test.go
```

---

### Task 1: Catalog JSON codec

**Files:**
- Create: `internal/catalog/catalogjson/catalogjson.go`, `internal/catalog/catalogjson/catalogjson_test.go`

**Interfaces:**
- Consumes: `catalog.Ingredient`, `catalog.Normalize` (Plans 1–2).
- Produces:
  - `const catalogjson.Format = "thymeandplace-catalog/v1"`, `const catalogjson.MaxBytes = 5 << 20`
  - `var catalogjson.ErrTooLarge`, `var catalogjson.ErrUnknownFormat`
  - `func catalogjson.Decode(r io.Reader) ([]catalog.Ingredient, error)`
  - `func catalogjson.Encode(w io.Writer, items []catalog.Ingredient, exportedAt time.Time) error` (normalises every item; `pairings` is always written, `[]` when empty)

- [ ] **Step 1: Write the failing tests**

Create `internal/catalog/catalogjson/catalogjson_test.go`:

```go
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

func TestDecodeSizeLimit(t *testing.T) {
	big := `{"format":"thymeandplace-catalog/v1","ingredients":[],"pad":"` + strings.Repeat("x", MaxBytes) + `"}`
	if _, err := Decode(strings.NewReader(big)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/catalog/catalogjson/`
Expected: FAIL to compile: `undefined: Decode`.

- [ ] **Step 3: Implement the codec**

Create `internal/catalog/catalogjson/catalogjson.go`:

```go
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
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc Document
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid catalog file: %w", err)
	}
	if dec.More() {
		return nil, errors.New("invalid catalog file: unexpected data after the document")
	}
	out := make([]catalog.Ingredient, len(doc.Ingredients))
	for i, j := range doc.Ingredients {
		ing, err := j.toDomain()
		if err != nil {
			return nil, fmt.Errorf("ingredient %d (%q): %w", i+1, j.Slug, err)
		}
		out[i] = ing
	}
	return out, nil
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
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/catalog/catalogjson/ -count=1`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/catalog/catalogjson
git commit -m "feat(catalogjson): thymeandplace-catalog/v1 import/export codec

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Changeset model, validation and field diff

**Files:**
- Create: `internal/changeset/changeset.go`, `internal/changeset/changeset_test.go`, `internal/changeset/diff.go`, `internal/changeset/diff_test.go`

**Interfaces:**
- Consumes: `catalog.*`, `catalogjson.Decode`/`Encode`, `season.FormatMonths`, `ocean.EcoregionID`.
- Produces:
  - `const changeset.SourceForm = "form"`, `SourceDelete = "delete"`, `SourceImport = "import"`
  - `type changeset.Changeset struct { Source string; Upserts []catalog.Ingredient; Deletes []string }`
  - `func changeset.FormChangeset(ing catalog.Ingredient) Changeset`, `func DeleteChangeset(slug string) Changeset`, `func ImportChangeset(r io.Reader) (Changeset, error)`, `func Export(w io.Writer, snap *catalog.Snapshot, now time.Time) error`
  - `type changeset.EcoregionChecker interface { Missing(ctx context.Context, ids []ocean.EcoregionID) ([]ocean.EcoregionID, error) }` (`*ocean.Service` satisfies it)
  - `func changeset.Validate(ctx context.Context, cs Changeset, cur *catalog.Snapshot, eco EcoregionChecker) (*catalog.Snapshot, []catalog.Problem, error)`: the error is only for infrastructure failures
  - `type changeset.InvalidError struct { Problems []catalog.Problem }`
  - `var changeset.ErrStale, ErrNotFound, ErrOverrideRequired, ErrConfirmMismatch, ErrNotReady`; `func changeset.UserMessage(err error) string`
  - `type changeset.FieldValue struct { Field, Value string }`; `func changeset.Fields(ing catalog.Ingredient) []FieldValue`
  - `type changeset.FieldChange struct { Field, Before, After string }`; `type changeset.ItemChange struct { Slug, Name string; Fields []FieldChange }`
  - `type changeset.Diff struct { Added []catalog.Ingredient; Changed []ItemChange; Deleted []catalog.Ingredient; Unchanged int }` with `(Diff).Slugs() []string` (added + changed + deleted, sorted) and `(Diff).Empty() bool`
  - `func changeset.ComputeDiff(cur, proposed *catalog.Snapshot) Diff`

- [ ] **Step 1: Write the failing tests**

Create `internal/changeset/changeset_test.go`:

```go
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

func TestUserMessage(t *testing.T) {
	if !strings.Contains(UserMessage(ErrStale), "Preview again") || UserMessage(nil) != "" {
		t.Fatal("UserMessage")
	}
}
```

Create `internal/changeset/diff_test.go`:

```go
package changeset

import (
	"reflect"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
)

func TestComputeDiff(t *testing.T) {
	cur := snap(t)
	tomato := catalogtest.Annual()
	tomato.Pairings = []string{"basil"}
	tomato.MonthsToHarvest = catalogtest.P(4)
	chard := catalogtest.Hardy()
	chard.Slug, chard.Name = "chard", "Chard"
	proposed, err := cur.With([]catalog.Ingredient{tomato, chard}, []string{"banana"})
	if err != nil {
		t.Fatal(err)
	}
	d := ComputeDiff(cur, proposed)

	if len(d.Added) != 1 || d.Added[0].Slug != "chard" || len(d.Deleted) != 1 || d.Deleted[0].Slug != "banana" {
		t.Fatalf("added=%v deleted=%v", d.Added, d.Deleted)
	}
	if len(d.Changed) != 1 || d.Changed[0].Slug != "tomato" ||
		!reflect.DeepEqual(d.Changed[0].Fields, []FieldChange{{"months_to_harvest", "3", "4"}}) {
		t.Fatalf("changed = %+v", d.Changed)
	}
	if d.Unchanged != 9 {
		t.Fatalf("unchanged = %d, want 9", d.Unchanged)
	}
	if !reflect.DeepEqual(d.Slugs(), []string{"banana", "chard", "tomato"}) {
		t.Fatalf("slugs = %v", d.Slugs())
	}
}

func TestDiffShowsCascadedPairings(t *testing.T) {
	cur := snap(t)
	proposed, _ := cur.With(nil, []string{"basil"})
	d := ComputeDiff(cur, proposed)
	if len(d.Changed) != 1 || d.Changed[0].Slug != "tomato" || d.Changed[0].Fields[0] != (FieldChange{"pairings", "basil", ""}) {
		t.Fatalf("changed = %+v", d.Changed)
	}
}

func TestFieldsFormatting(t *testing.T) {
	f := map[string]string{}
	for _, fv := range Fields(catalogtest.Seafood()) {
		f[fv.Field] = fv.Value
	}
	if f["regions"] != "25: Jan–Apr (peak Feb–Mar)" || f["grow_temp_min_c"] != "" || f["kind"] != "seafood" {
		t.Fatalf("fields = %v", f)
	}
}

func TestDiffEmpty(t *testing.T) {
	cur := snap(t)
	if d := ComputeDiff(cur, cur); !d.Empty() || d.Unchanged != cur.Len() {
		t.Fatalf("diff = %+v", d)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/changeset/`
Expected: FAIL to compile: `undefined: Validate`.

- [ ] **Step 3: Implement the changeset model and validation**

Create `internal/changeset/changeset.go`:

```go
// Package changeset is the single path for catalog edits:
// build → validate → preview → apply.
package changeset

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogjson"
	"thymeandplace/internal/ocean"
)

const (
	SourceForm   = "form"
	SourceDelete = "delete"
	SourceImport = "import"
)

// Changeset is a set of upserts and deletes. It is stored as JSON in
// pending_changeset between preview and apply.
type Changeset struct {
	Source  string
	Upserts []catalog.Ingredient
	Deletes []string
}

func FormChangeset(ing catalog.Ingredient) Changeset {
	return Changeset{Source: SourceForm, Upserts: []catalog.Ingredient{ing}}
}

func DeleteChangeset(slug string) Changeset {
	return Changeset{Source: SourceDelete, Deletes: []string{slug}}
}

// ImportChangeset decodes a catalog file. Import never deletes.
func ImportChangeset(r io.Reader) (Changeset, error) {
	items, err := catalogjson.Decode(r)
	if err != nil {
		return Changeset{}, err
	}
	return Changeset{Source: SourceImport, Upserts: items}, nil
}

// Export writes the whole snapshot in the import format.
func Export(w io.Writer, snap *catalog.Snapshot, now time.Time) error {
	return catalogjson.Encode(w, snap.All(), now)
}

type EcoregionChecker interface {
	Missing(ctx context.Context, ids []ocean.EcoregionID) ([]ocean.EcoregionID, error)
}

var (
	ErrStale            = errors.New("changeset: catalog changed since preview")
	ErrNotFound         = errors.New("changeset: preview expired or already applied")
	ErrOverrideRequired = errors.New("changeset: calibration override required")
	ErrConfirmMismatch  = errors.New("changeset: delete confirmation does not match")
	ErrNotReady         = errors.New("changeset: catalog not loaded")
)

// InvalidError carries per-item, per-field problems. Nothing was stored.
type InvalidError struct{ Problems []catalog.Problem }

func (e *InvalidError) Error() string { return (&catalog.ValidationError{Problems: e.Problems}).Error() }

// UserMessage is the owner-facing text for a changeset error, or "".
func UserMessage(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrStale):
		return "The catalog changed since this preview. Preview again."
	case errors.Is(err, ErrNotFound):
		return "This preview has expired or was already applied. Preview again."
	case errors.Is(err, ErrOverrideRequired):
		return "The calibration score drops by more than 0.02. Tick “Apply despite lower calibration score” to apply anyway."
	case errors.Is(err, ErrConfirmMismatch):
		return "Type the ingredient's slug exactly to confirm the delete."
	}
	return ""
}

// Validate checks cs against cur and live ecoregions. It returns the
// proposed snapshot when there are no problems. err is non-nil only for
// infrastructure failures.
func Validate(ctx context.Context, cs Changeset, cur *catalog.Snapshot, eco EcoregionChecker) (*catalog.Snapshot, []catalog.Problem, error) {
	var problems []catalog.Problem
	seen := map[string]bool{}
	ups := make([]catalog.Ingredient, len(cs.Upserts))
	for i, u := range cs.Upserts {
		ups[i] = catalog.Normalize(u)
		problems = append(problems, ups[i].Validate()...)
		if seen[u.Slug] {
			problems = append(problems, catalog.Problem{Slug: u.Slug, Field: "slug", Message: "appears more than once in this change"})
		}
		seen[u.Slug] = true
	}
	for _, d := range cs.Deletes {
		if seen[d] {
			problems = append(problems, catalog.Problem{Slug: d, Field: "slug", Message: "cannot be both saved and deleted in one change"})
		}
	}
	if len(problems) > 0 {
		return nil, problems, nil
	}

	proposed, err := cur.With(ups, cs.Deletes)
	var ve *catalog.ValidationError
	if errors.As(err, &ve) {
		return nil, ve.Problems, nil
	}
	if err != nil {
		return nil, nil, err
	}

	type loc struct {
		slug string
		i    int
	}
	var ids []ocean.EcoregionID
	owners := map[ocean.EcoregionID][]loc{}
	for _, u := range ups {
		for i, r := range u.Regions {
			ids = append(ids, r.EcoregionID)
			owners[r.EcoregionID] = append(owners[r.EcoregionID], loc{u.Slug, i})
		}
	}
	if len(ids) > 0 {
		missing, err := eco.Missing(ctx, ids)
		if err != nil {
			return nil, nil, err
		}
		for _, m := range missing {
			for _, o := range owners[m] {
				problems = append(problems, catalog.Problem{
					Slug: o.slug, Field: fmt.Sprintf("regions[%d].ecoregion_id", o.i),
					Message: fmt.Sprintf("ecoregion %d does not exist in the live ocean data", m),
				})
			}
		}
	}
	if len(problems) > 0 {
		return nil, problems, nil
	}
	return proposed, nil, nil
}
```

- [ ] **Step 4: Implement the diff**

Create `internal/changeset/diff.go`:

```go
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
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/changeset/ -count=1`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/changeset
git commit -m "feat(changeset): validation against snapshot and ecoregions; field-level diff

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Calibration preview and fixture case resolver

**Files:**
- Create: `internal/changeset/preview.go`, `internal/changeset/preview_test.go`, `internal/calibrun/cases.go`, `internal/calibrun/cases_test.go`

**Interfaces:**
- Consumes: `calibration.Score`, `calibration.Case`, `calibration.Fixture`, `calibration.LoadFixtures`, `season.Year`, `climate.Lookup`, `ocean.EcoregionID`.
- Produces:
  - `const changeset.OverrideThreshold = 0.02`; `func changeset.NeedsOverride(before, after float64) bool`
  - `type changeset.Bars struct { Fixture string; Before, After [12]season.Status; HadBefore, HasAfter bool }`
  - `type changeset.IngredientPreview struct { Slug, Name string; ScoreBefore, ScoreAfter *float64; Bars []Bars }`
  - `type changeset.Preview struct { Diff Diff; Before, After calibration.Report; Delta float64; NeedsOverride bool; Items []IngredientPreview }`
  - `func changeset.BuildPreview(cur, proposed *catalog.Snapshot, cases []calibration.Case) Preview`
  - `type calibrun.NormalsFinder interface { Normals(ctx context.Context, lat, lng float64) (climate.Lookup, error) }`
  - `type calibrun.EcoregionFinder interface { Nearby(ctx context.Context, lat, lng float64) ([]ocean.EcoregionID, error) }`
  - `func calibrun.Cases(ctx context.Context, fixtures []calibration.Fixture, cl NormalsFinder, oc EcoregionFinder) ([]calibration.Case, error)`

- [ ] **Step 1: Write the failing preview tests**

Create `internal/changeset/preview_test.go`:

```go
package changeset

import (
	"math"
	"os"
	"testing"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/season"
)

func testCases(t *testing.T) []calibration.Case {
	t.Helper()
	fx, err := calibration.LoadFixtures(os.DirFS("../../testdata/calibration"))
	if err != nil {
		t.Fatal(err)
	}
	n := climatetest.Temperate()
	return []calibration.Case{{Fixture: fx[0], Input: season.Input{Normals: &n}}}
}

func TestPreviewScoreDropNeedsOverride(t *testing.T) {
	cur := snap(t)
	tomato := catalogtest.Annual()
	tomato.Pairings = []string{"basil"}
	tomato.MonthsToHarvest = catalogtest.P(1) // harvest May–Sep vs guide Jul–Sep: 3/5
	proposed, _ := cur.With([]catalog.Ingredient{tomato}, nil)

	p := BuildPreview(cur, proposed, testCases(t))
	if math.Abs(p.Before.Score-(1+4.0/6)/3) > 1e-9 || math.Abs(p.After.Score-(0.6+4.0/6)/3) > 1e-9 {
		t.Fatalf("scores %v → %v", p.Before.Score, p.After.Score)
	}
	if !p.NeedsOverride || p.Delta >= 0 {
		t.Fatalf("override=%v delta=%v", p.NeedsOverride, p.Delta)
	}
	if len(p.Items) != 1 || p.Items[0].Slug != "tomato" || *p.Items[0].ScoreBefore != 1 || math.Abs(*p.Items[0].ScoreAfter-0.6) > 1e-9 {
		t.Fatalf("items = %+v", p.Items)
	}
	b := p.Items[0].Bars[0]
	if b.Fixture != "Temperate test town" || b.Before[7] != season.Peak || b.After[6] != season.Peak || b.After[4] != season.InSeason {
		t.Fatalf("bars = %+v", b)
	}
}

func TestPreviewImprovementNoOverride(t *testing.T) {
	cur := snap(t)
	apple := catalogtest.Perennial()
	apple.StorageMonths = 2 // Aug–Nov matches the guide exactly
	proposed, _ := cur.With([]catalog.Ingredient{apple}, nil)
	p := BuildPreview(cur, proposed, testCases(t))
	if p.NeedsOverride || p.Delta <= 0 {
		t.Fatalf("override=%v delta=%v", p.NeedsOverride, p.Delta)
	}
}

func TestPreviewAddedAndDeletedBars(t *testing.T) {
	cur := snap(t)
	chard := catalogtest.Hardy()
	chard.Slug, chard.Name = "chard", "Chard"
	proposed, _ := cur.With([]catalog.Ingredient{chard}, []string{"banana"})
	p := BuildPreview(cur, proposed, testCases(t))
	for _, it := range p.Items {
		b := it.Bars[0]
		switch it.Slug {
		case "chard":
			if b.HadBefore || !b.HasAfter {
				t.Fatalf("chard bars = %+v", b)
			}
		case "banana":
			if !b.HadBefore || b.HasAfter {
				t.Fatalf("banana bars = %+v", b)
			}
		}
	}
}

// Review Focus 5: first import into an empty catalog.
func TestPreviewFromEmptyCatalog(t *testing.T) {
	empty, _ := catalog.NewSnapshot(0, nil)
	proposed, err := empty.With(catalogtest.SmallCatalog(), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := BuildPreview(empty, proposed, testCases(t))
	if p.NeedsOverride || p.Before.Compared != 0 || len(p.Diff.Added) != 11 || math.IsNaN(p.Delta) {
		t.Fatalf("preview = %+v", p)
	}
}

func TestNeedsOverrideBoundary(t *testing.T) {
	if NeedsOverride(0.50, 0.48) {
		t.Fatal("a drop of exactly 0.02 must not need an override")
	}
	if !NeedsOverride(0.50, 0.4799) {
		t.Fatal("a drop above 0.02 needs an override")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/changeset/ -run Preview`
Expected: FAIL to compile: `undefined: BuildPreview`.

- [ ] **Step 3: Implement the preview**

Create `internal/changeset/preview.go`:

```go
package changeset

import (
	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/season"
)

// OverrideThreshold is the largest score drop allowed without an override.
const OverrideThreshold = 0.02

// NeedsOverride reports a drop of more than OverrideThreshold. The epsilon
// keeps a drop of exactly 0.02 (in decimal) from tripping on float noise.
func NeedsOverride(before, after float64) bool {
	return before-after > OverrideThreshold+1e-9
}

type Bars struct {
	Fixture           string
	Before, After     [12]season.Status
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
		Delta: after.Score - before.Score, NeedsOverride: NeedsOverride(before.Score, after.Score),
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
```

- [ ] **Step 4: Write the case resolver and its test**

Create `internal/calibrun/cases_test.go`:

```go
package calibrun

import (
	"context"
	"testing"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/ocean"
)

func TestCases(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	fx := []calibration.Fixture{
		{ID: "lisbon", Name: "Lisbon", Lat: dbtest.Lisbon.Lat, Lng: dbtest.Lisbon.Lng, Source: "s"},
		{ID: "madrid", Name: "Madrid", Lat: dbtest.Madrid.Lat, Lng: dbtest.Madrid.Lng, Source: "s"},
		{ID: "sea", Name: "Sea", Lat: dbtest.MidAtlantic.Lat, Lng: dbtest.MidAtlantic.Lng, Source: "s"},
	}
	cases, err := Cases(ctx, fx, climate.NewService(pool), ocean.NewService(pool))
	if err != nil {
		t.Fatal(err)
	}
	if cases[0].Input.Normals == nil || len(cases[0].Input.Ecoregions) != 1 || cases[0].Input.Ecoregions[0] != 30 {
		t.Fatalf("lisbon = %+v", cases[0].Input)
	}
	if cases[1].Input.Normals == nil || len(cases[1].Input.Ecoregions) != 0 {
		t.Fatalf("madrid = %+v", cases[1].Input)
	}
	if cases[2].Input.Normals != nil {
		t.Fatalf("sea = %+v", cases[2].Input)
	}
}
```

Create `internal/calibrun/cases.go`:

```go
// Package calibrun connects calibration fixtures to live reference data and
// runs the daily drift check.
package calibrun

import (
	"context"
	"fmt"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/climate"
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/season"
)

type NormalsFinder interface {
	Normals(ctx context.Context, lat, lng float64) (climate.Lookup, error)
}

type EcoregionFinder interface {
	Nearby(ctx context.Context, lat, lng float64) ([]ocean.EcoregionID, error)
}

// Cases resolves every fixture location to an engine input (once per call).
func Cases(ctx context.Context, fixtures []calibration.Fixture, cl NormalsFinder, oc EcoregionFinder) ([]calibration.Case, error) {
	out := make([]calibration.Case, 0, len(fixtures))
	for _, f := range fixtures {
		l, err := cl.Normals(ctx, f.Lat, f.Lng)
		if err != nil {
			return nil, fmt.Errorf("fixture %s normals: %w", f.ID, err)
		}
		eco, err := oc.Nearby(ctx, f.Lat, f.Lng)
		if err != nil {
			return nil, fmt.Errorf("fixture %s ecoregions: %w", f.ID, err)
		}
		out = append(out, calibration.Case{Fixture: f, Input: season.Input{Normals: l.Normals, Ecoregions: eco}})
	}
	return out, nil
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/changeset/ ./internal/calibrun/ -count=1`
Expected: `ok` for both.

- [ ] **Step 6: Commit**

```bash
git add internal/changeset internal/calibrun
git commit -m "feat(changeset): calibration preview with per-fixture season bars

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Pending changesets and the preview service

**Files:**
- Create: `migrations/00005_changesets.sql`, `internal/store/queries/changeset.sql`, `internal/changeset/service.go`, `internal/changeset/service_test.go`

**Interfaces:**
- Consumes: `catalog.Load`, `Validate`, `BuildPreview`, `store`.
- Produces:
  - Tables `pending_changeset (id, payload, base_version, score_before, score_after, created_at, expires_at)` and `calibration_run (id, ran_at, data_version, score, compared)`
  - sqlc: `InsertPendingChangeset`, `GetPendingChangeset`, `GetPendingChangesetForUpdate`, `DeletePendingChangeset`, `DeleteExpiredChangesets`, `LockCatalogVersion`, `InsertCalibrationRun`, `LatestCalibrationRun`
  - `type changeset.Pending struct { ID int64; Changeset Changeset; BaseVersion int64; ScoreBefore, ScoreAfter float64; ExpiresAt time.Time }`
  - `type changeset.CasesFunc func(context.Context) ([]calibration.Case, error)`
  - `func changeset.NewService(pool *pgxpool.Pool, eco EcoregionChecker, cases CasesFunc) *Service`
  - `(*Service).Preview(ctx, cs Changeset) (Pending, Preview, error)`: returns `*InvalidError` when there are problems
  - `(*Service).Reopen(ctx, id int64) (Pending, Preview, error)`: rebuilds the preview for a stored changeset; returns `ErrNotFound` or `ErrStale`

- [ ] **Step 1: Write the migration and queries**

Create `migrations/00005_changesets.sql`:

```sql
-- +goose Up
CREATE TABLE pending_changeset (
  id           bigserial PRIMARY KEY,
  payload      jsonb NOT NULL,
  base_version bigint NOT NULL,
  score_before double precision NOT NULL,
  score_after  double precision NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL DEFAULT now() + interval '1 hour'
);

CREATE TABLE calibration_run (
  id           bigserial PRIMARY KEY,
  ran_at       timestamptz NOT NULL DEFAULT now(),
  data_version text NOT NULL,
  score        double precision NOT NULL,
  compared     integer NOT NULL
);

-- +goose Down
DROP TABLE calibration_run;
DROP TABLE pending_changeset;
```

Create `internal/store/queries/changeset.sql`:

```sql
-- name: InsertPendingChangeset :one
INSERT INTO pending_changeset (payload, base_version, score_before, score_after)
VALUES (@payload, @base_version, @score_before, @score_after)
RETURNING id, expires_at;

-- name: GetPendingChangeset :one
SELECT * FROM pending_changeset WHERE id = @id AND expires_at > now();

-- name: GetPendingChangesetForUpdate :one
SELECT * FROM pending_changeset WHERE id = @id AND expires_at > now() FOR UPDATE;

-- name: DeletePendingChangeset :exec
DELETE FROM pending_changeset WHERE id = @id;

-- name: DeleteExpiredChangesets :exec
DELETE FROM pending_changeset WHERE expires_at <= now();

-- name: LockCatalogVersion :one
SELECT version FROM catalog_meta WHERE id FOR UPDATE;

-- name: InsertCalibrationRun :exec
INSERT INTO calibration_run (data_version, score, compared) VALUES (@data_version, @score, @compared);

-- name: LatestCalibrationRun :one
SELECT * FROM calibration_run ORDER BY id DESC LIMIT 1;
```

Run: `./scripts/sqlc.sh generate && go build ./internal/store/`
Expected: compiles. `InsertPendingChangeset` returns a row with `ID int64` and `ExpiresAt pgtype.Timestamptz`; `PendingChangeset` has `Payload []byte`, `ScoreBefore float64`.

- [ ] **Step 2: Write the failing service tests**

Create `internal/changeset/service_test.go`:

```go
package changeset

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/ocean"
	"thymeandplace/internal/store"
)

// newTestService returns a service over a DB seeded with SmallCatalog and the
// reference fixture (ecoregions 25, 30, 204 exist).
func newTestService(t *testing.T, seed bool) (*Service, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	if seed {
		tx, _ := pool.Begin(ctx)
		if err := catalog.Write(ctx, store.New(tx), catalogtest.SmallCatalog(), nil); err != nil {
			t.Fatal(err)
		}
		_ = tx.Commit(ctx)
	}
	cases := testCases(t)
	svc := NewService(pool, ocean.NewService(pool), func(context.Context) ([]calibration.Case, error) { return cases, nil })
	return svc, pool
}

func TestServicePreviewStoresPending(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, true)
	k := catalogtest.Hardy()
	k.Name = "Curly kale"
	pending, pv, err := svc.Preview(ctx, FormChangeset(k))
	if err != nil {
		t.Fatal(err)
	}
	if pending.ID == 0 || pending.BaseVersion == 0 || time.Until(pending.ExpiresAt) < 59*time.Minute {
		t.Fatalf("pending = %+v", pending)
	}
	if len(pv.Diff.Changed) != 1 || pv.Diff.Unchanged != 10 {
		t.Fatalf("diff = %+v", pv.Diff)
	}
	again, pv2, err := svc.Reopen(ctx, pending.ID)
	if err != nil || again.ID != pending.ID || len(pv2.Diff.Changed) != 1 {
		t.Fatalf("reopen: %+v %v", again, err)
	}
}

func TestServicePreviewInvalid(t *testing.T) {
	svc, _ := newTestService(t, true)
	bad := catalogtest.Seafood()
	bad.Regions[0].EcoregionID = 12345
	_, _, err := svc.Preview(context.Background(), FormChangeset(bad))
	var inv *InvalidError
	if !errors.As(err, &inv) || inv.Problems[0].Field != "regions[0].ecoregion_id" {
		t.Fatalf("err = %v", err)
	}
}

// Review Focus 1: exporting then importing the same file changes nothing.
func TestExportImportNoPhantomChanges(t *testing.T) {
	ctx := context.Background()
	svc, pool := newTestService(t, true)
	snap, err := catalog.Load(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Export(&buf, snap, time.Now()); err != nil {
		t.Fatal(err)
	}
	cs, err := ImportChangeset(&buf)
	if err != nil {
		t.Fatal(err)
	}
	_, pv, err := svc.Preview(ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if !pv.Diff.Empty() || pv.Delta != 0 {
		t.Fatalf("phantom changes: %+v delta=%v", pv.Diff, pv.Delta)
	}
}

// Review Focus 5 at the service level.
func TestFirstImportIntoEmptyCatalog(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, false)
	var buf bytes.Buffer
	empty, _ := catalog.NewSnapshot(0, catalogtest.SmallCatalog())
	_ = Export(&buf, empty, time.Now())
	cs, _ := ImportChangeset(&buf)
	_, pv, err := svc.Preview(ctx, cs)
	if err != nil || len(pv.Diff.Added) != 11 || pv.NeedsOverride {
		t.Fatalf("pv=%+v err=%v", pv.Diff, err)
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/changeset/ -run 'Service|Export|FirstImport'`
Expected: FAIL to compile: `undefined: NewService`.

- [ ] **Step 4: Implement the service (preview side)**

Create `internal/changeset/service.go`:

```go
package changeset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/store"
)

type CasesFunc func(context.Context) ([]calibration.Case, error)

type Pending struct {
	ID                      int64
	Changeset               Changeset
	BaseVersion             int64
	ScoreBefore, ScoreAfter float64
	ExpiresAt               time.Time
}

type writeFunc func(ctx context.Context, q *store.Queries, ups []catalog.Ingredient, dels []string) error

type Service struct {
	pool  *pgxpool.Pool
	eco   EcoregionChecker
	cases CasesFunc
	write writeFunc // catalog.Write; replaced in tests to inject failures
}

func NewService(pool *pgxpool.Pool, eco EcoregionChecker, cases CasesFunc) *Service {
	return &Service{pool: pool, eco: eco, cases: cases, write: catalog.Write}
}

// Preview validates cs against the catalog as stored now, stores it for one
// hour and returns the preview. Problems come back as *InvalidError.
func (s *Service) Preview(ctx context.Context, cs Changeset) (Pending, Preview, error) {
	cur, err := catalog.Load(ctx, s.pool)
	if err != nil {
		return Pending{}, Preview{}, fmt.Errorf("load catalog: %w", err)
	}
	pv, err := s.build(ctx, cs, cur)
	if err != nil {
		return Pending{}, Preview{}, err
	}
	payload, err := json.Marshal(cs)
	if err != nil {
		return Pending{}, Preview{}, err
	}
	q := store.New(s.pool)
	_ = q.DeleteExpiredChangesets(ctx)
	row, err := q.InsertPendingChangeset(ctx, store.InsertPendingChangesetParams{
		Payload: payload, BaseVersion: cur.Version(), ScoreBefore: pv.Before.Score, ScoreAfter: pv.After.Score,
	})
	if err != nil {
		return Pending{}, Preview{}, fmt.Errorf("store preview: %w", err)
	}
	return Pending{
		ID: row.ID, Changeset: cs, BaseVersion: cur.Version(),
		ScoreBefore: pv.Before.Score, ScoreAfter: pv.After.Score, ExpiresAt: row.ExpiresAt.Time,
	}, pv, nil
}

// Reopen rebuilds the preview of a stored changeset.
func (s *Service) Reopen(ctx context.Context, id int64) (Pending, Preview, error) {
	row, err := store.New(s.pool).GetPendingChangeset(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Pending{}, Preview{}, ErrNotFound
	}
	if err != nil {
		return Pending{}, Preview{}, err
	}
	p, err := pendingFromRow(row)
	if err != nil {
		return Pending{}, Preview{}, err
	}
	cur, err := catalog.Load(ctx, s.pool)
	if err != nil {
		return Pending{}, Preview{}, err
	}
	if cur.Version() != p.BaseVersion {
		return p, Preview{}, ErrStale
	}
	pv, err := s.build(ctx, p.Changeset, cur)
	return p, pv, err
}

func (s *Service) build(ctx context.Context, cs Changeset, cur *catalog.Snapshot) (Preview, error) {
	proposed, problems, err := Validate(ctx, cs, cur, s.eco)
	if err != nil {
		return Preview{}, err
	}
	if len(problems) > 0 {
		return Preview{}, &InvalidError{Problems: problems}
	}
	cases, err := s.cases(ctx)
	if err != nil {
		return Preview{}, fmt.Errorf("calibration inputs: %w", err)
	}
	return BuildPreview(cur, proposed, cases), nil
}

func pendingFromRow(row store.PendingChangeset) (Pending, error) {
	var cs Changeset
	if err := json.Unmarshal(row.Payload, &cs); err != nil {
		return Pending{}, fmt.Errorf("decode pending changeset %d: %w", row.ID, err)
	}
	return Pending{
		ID: row.ID, Changeset: cs, BaseVersion: row.BaseVersion,
		ScoreBefore: row.ScoreBefore, ScoreAfter: row.ScoreAfter, ExpiresAt: row.ExpiresAt.Time,
	}, nil
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/changeset/ -count=1`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add migrations/00005_changesets.sql internal/store internal/changeset
git commit -m "feat(changeset): stored previews with one-hour expiry

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Apply

**Files:**
- Modify: `internal/changeset/service.go`
- Create: `internal/changeset/apply_test.go`

**Interfaces:**
- Consumes: Task 4, `catalog.Write`, `catalog.Load`, history triggers (Plan 2).
- Produces:
  - `type changeset.ApplyOptions struct { Override bool; ConfirmSlug string }`
  - `(*Service).Apply(ctx context.Context, id int64, actor string, opts ApplyOptions) error`: returns `ErrNotFound`, `ErrStale`, `ErrConfirmMismatch`, `ErrOverrideRequired` or `*InvalidError`; on any error nothing is written

- [ ] **Step 1: Write the failing apply tests**

Create `internal/changeset/apply_test.go`:

```go
package changeset

import (
	"context"
	"errors"
	"strings"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/store"
)

func loadSnap(t *testing.T, svc *Service) *catalog.Snapshot {
	t.Helper()
	s, err := catalog.Load(context.Background(), svc.pool)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestApplyWritesWithActorAndChangeID(t *testing.T) {
	ctx := context.Background()
	svc, pool := newTestService(t, true)
	k := catalogtest.Hardy()
	k.Name = "Curly kale"
	p, _, err := svc.Preview(ctx, FormChangeset(k))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := loadSnap(t, svc).Get("kale"); got.Name != "Curly kale" {
		t.Fatalf("kale = %q", got.Name)
	}
	var actor string
	var change int64
	err = pool.QueryRow(ctx, `SELECT actor, change_id FROM catalog_history WHERE table_name = 'ingredient' ORDER BY id DESC LIMIT 1`).Scan(&actor, &change)
	if err != nil || actor != "owner" || change != p.ID {
		t.Fatalf("history actor=%q change=%d err=%v", actor, change, err)
	}
	var left int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM pending_changeset WHERE id = $1`, p.ID).Scan(&left)
	if left != 0 {
		t.Fatal("pending row not deleted")
	}
}

// Review Focus 4: double apply.
func TestApplyTwice(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, true)
	k := catalogtest.Hardy()
	k.Note = "once"
	p, _, _ := svc.Preview(ctx, FormChangeset(k))
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{})
	if !errors.Is(err, ErrNotFound) || UserMessage(err) == "" {
		t.Fatalf("second apply: %v", err)
	}
}

// Review Focus 3: expired preview.
func TestApplyExpired(t *testing.T) {
	ctx := context.Background()
	svc, pool := newTestService(t, true)
	p, _, _ := svc.Preview(ctx, FormChangeset(catalogtest.Hardy()))
	if _, err := pool.Exec(ctx, `UPDATE pending_changeset SET expires_at = now() - interval '1 second' WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyStale(t *testing.T) {
	ctx := context.Background()
	svc, pool := newTestService(t, true)
	p, _, _ := svc.Preview(ctx, FormChangeset(catalogtest.Hardy()))
	if _, err := pool.Exec(ctx, `UPDATE ingredient SET note = 'someone else' WHERE slug = 'banana'`); err != nil {
		t.Fatal(err)
	}
	err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{})
	if !errors.Is(err, ErrStale) || UserMessage(err) != "The catalog changed since this preview. Preview again." {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := svc.Reopen(ctx, p.ID); !errors.Is(err, ErrStale) {
		t.Fatalf("reopen stale: %v", err)
	}
}

func TestApplyOverride(t *testing.T) {
	ctx := context.Background()
	svc, pool := newTestService(t, true)
	tomato := catalogtest.Annual()
	tomato.Pairings = []string{"basil"}
	tomato.MonthsToHarvest = catalogtest.P(1)
	p, pv, _ := svc.Preview(ctx, FormChangeset(tomato))
	if !pv.NeedsOverride {
		t.Fatal("expected override to be needed")
	}
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{}); !errors.Is(err, ErrOverrideRequired) {
		t.Fatalf("without override: %v", err)
	}
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{Override: true}); err != nil {
		t.Fatalf("with override: %v", err)
	}
	var override bool
	err := pool.QueryRow(ctx, `SELECT (after->'_meta'->>'override')::boolean FROM catalog_history
		WHERE change_id = $1 AND table_name = 'ingredient'`, p.ID).Scan(&override)
	if err != nil || !override {
		t.Fatalf("override not recorded: %v %v", override, err)
	}
}

func TestApplyDeleteNeedsTypedSlug(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, true)
	p, pv, _ := svc.Preview(ctx, DeleteChangeset("basil"))
	if len(pv.Diff.Deleted) != 1 || len(pv.Diff.Changed) != 1 { // tomato loses its pairing
		t.Fatalf("diff = %+v", pv.Diff)
	}
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{ConfirmSlug: "basi"}); !errors.Is(err, ErrConfirmMismatch) {
		t.Fatalf("mismatch: %v", err)
	}
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{ConfirmSlug: "basil"}); err != nil {
		t.Fatalf("confirmed: %v", err)
	}
	if _, ok := loadSnap(t, svc).Get("basil"); ok {
		t.Fatal("basil not deleted")
	}
}

func TestApplyIsAtomic(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, true)
	k := catalogtest.Hardy()
	k.Name = "Half-written kale"
	b := catalogtest.YearRound()
	b.Name = "Half-written banana"
	p, _, _ := svc.Preview(ctx, Changeset{Source: SourceImport, Upserts: []catalog.Ingredient{k, b}})

	svc.write = func(ctx context.Context, q *store.Queries, ups []catalog.Ingredient, dels []string) error {
		if err := catalog.Write(ctx, q, ups[:1], nil); err != nil {
			return err
		}
		return errors.New("boom after first write")
	}
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{}); err == nil {
		t.Fatal("want failure")
	}
	s := loadSnap(t, svc)
	if g, _ := s.Get("kale"); g.Name != "Kale" {
		t.Fatalf("partial write leaked: kale = %q", g.Name)
	}
	if _, _, err := svc.Reopen(ctx, p.ID); err != nil {
		t.Fatalf("pending row should survive a failed apply: %v", err)
	}
}

func TestImportUpsertOnlyAndListReplacement(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, true)
	file := `{"format":"thymeandplace-catalog/v1","ingredients":[
	  {"slug":"tomato","name":"Tomato","category":"vegetable","kind":"annual","frost":"tender",
	   "grow_temp_c":[16,30],"months_to_harvest":3,"storage_months":0,
	   "tips":{"choose":"Heavy for its size, glossy, fragrant at the stem.","store":"Stem-side down at room temperature."},
	   "pairings":[],"note":""}]}`
	cs, err := ImportChangeset(strings.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	p, pv, err := svc.Preview(ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Diff.Deleted) != 0 || len(pv.Diff.Changed) != 1 {
		t.Fatalf("diff = %+v", pv.Diff)
	}
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	s := loadSnap(t, svc)
	if s.Len() != 11 {
		t.Fatalf("import deleted items: len=%d", s.Len())
	}
	if tm, _ := s.Get("tomato"); len(tm.Pairings) != 0 {
		t.Fatalf("pairings not replaced: %v", tm.Pairings)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/changeset/ -run Apply`
Expected: FAIL to compile: `svc.Apply undefined`.

- [ ] **Step 3: Implement Apply**

Append to `internal/changeset/service.go` (add `"reflect"` and `"strconv"` to the imports):

```go
type ApplyOptions struct {
	Override    bool   // "Apply despite lower calibration score"
	ConfirmSlug string // typed slug, required for delete changesets
}

// Apply writes a previewed changeset in one transaction. On any error nothing
// is written and the pending row is kept (except when it no longer exists).
func (s *Service) Apply(ctx context.Context, id int64, actor string, opts ApplyOptions) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := store.New(tx)

	row, err := q.GetPendingChangesetForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	version, err := q.LockCatalogVersion(ctx)
	if err != nil {
		return err
	}
	if version != row.BaseVersion {
		return ErrStale
	}
	p, err := pendingFromRow(row)
	if err != nil {
		return err
	}
	cs := p.Changeset
	if cs.Source == SourceDelete && (len(cs.Deletes) != 1 || opts.ConfirmSlug != cs.Deletes[0]) {
		return ErrConfirmMismatch
	}

	// catalog_meta is locked, so no other writer can commit until we finish;
	// a fresh read sees exactly the previewed base version.
	cur, err := catalog.Load(ctx, s.pool)
	if err != nil {
		return err
	}
	if cur.Version() != version {
		return ErrStale
	}
	if _, problems, err := Validate(ctx, cs, cur, s.eco); err != nil {
		return err
	} else if len(problems) > 0 {
		return &InvalidError{Problems: problems}
	}

	override := NeedsOverride(p.ScoreBefore, p.ScoreAfter)
	if override && !opts.Override {
		return ErrOverrideRequired
	}
	meta := ""
	if override {
		b, _ := json.Marshal(map[string]any{"override": true, "score_before": p.ScoreBefore, "score_after": p.ScoreAfter})
		meta = string(b)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.actor', $1, true), set_config('app.change_id', $2, true), set_config('app.meta', $3, true)`,
		actor, strconv.FormatInt(id, 10), meta); err != nil {
		return err
	}
	if err := s.write(ctx, q, changedUpserts(cur, cs.Upserts), cs.Deletes); err != nil {
		return fmt.Errorf("apply changeset %d: %w", id, err)
	}
	if err := q.DeletePendingChangeset(ctx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// changedUpserts drops upserts identical to what is stored, so unchanged
// import rows produce no history.
func changedUpserts(cur *catalog.Snapshot, ups []catalog.Ingredient) []catalog.Ingredient {
	var out []catalog.Ingredient
	for _, u := range ups {
		n := catalog.Normalize(u)
		if old, ok := cur.Get(n.Slug); ok && reflect.DeepEqual(old, n) {
			continue
		}
		out = append(out, n)
	}
	return out
}
```

- [ ] **Step 4: Run all changeset tests**

Run: `go test ./internal/changeset/ -count=1`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/changeset
git commit -m "feat(changeset): atomic audited apply with stale, expiry, override and confirm checks

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Daily calibration drift check

**Files:**
- Create: `internal/calibrun/drift.go`, `internal/calibrun/drift_test.go`

**Interfaces:**
- Consumes: `calibration.Score`, `store.InsertCalibrationRun`, `store.LatestCalibrationRun`, `changeset.OverrideThreshold`.
- Produces:
  - `type calibrun.Drift struct { Pool *pgxpool.Pool; Snapshot func() *catalog.Snapshot; Cases func(context.Context) ([]calibration.Case, error); DataVersion func() string; Log *slog.Logger }`
  - `(*Drift).RunOnce(ctx) (calibration.Report, error)`
  - `(*Drift).Loop(ctx context.Context, every time.Duration)`: runs once immediately, then every `every`, until ctx ends; errors are logged at ERROR

- [ ] **Step 1: Write the failing test**

Create `internal/calibrun/drift_test.go`:

```go
package calibrun

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/climate/climatetest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/season"
)

func TestDriftRecordsAndWarns(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	fx, err := calibration.LoadFixtures(os.DirFS("../../testdata/calibration"))
	if err != nil {
		t.Fatal(err)
	}
	n := climatetest.Temperate()
	cases := []calibration.Case{{Fixture: fx[0], Input: season.Input{Normals: &n}}}

	good, _ := catalog.NewSnapshot(1, catalogtest.SmallCatalog())
	worse := func() *catalog.Snapshot {
		tomato := catalogtest.Annual()
		tomato.Pairings = []string{"basil"}
		tomato.MonthsToHarvest = catalogtest.P(1)
		s, _ := good.With([]catalog.Ingredient{tomato}, nil)
		return s
	}()

	var logs bytes.Buffer
	cur := good
	d := &Drift{
		Pool:        pool,
		Snapshot:    func() *catalog.Snapshot { return cur },
		Cases:       func(context.Context) ([]calibration.Case, error) { return cases, nil },
		DataVersion: func() string { return "abc123" },
		Log:         slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Fatal("first run must not warn")
	}
	cur = worse
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "calibration score dropped") {
		t.Fatalf("no drift warning in:\n%s", logs.String())
	}
	var runs int
	var dv string
	_ = pool.QueryRow(ctx, `SELECT count(*), max(data_version) FROM calibration_run`).Scan(&runs, &dv)
	if runs != 2 || dv != "abc123" {
		t.Fatalf("runs=%d dv=%q", runs, dv)
	}
}

func TestDriftNeedsSnapshot(t *testing.T) {
	d := &Drift{Pool: dbtest.New(t), Snapshot: func() *catalog.Snapshot { return nil }}
	if _, err := d.RunOnce(context.Background()); err == nil {
		t.Fatal("want error without a snapshot")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/calibrun/ -run Drift`
Expected: FAIL to compile: `undefined: Drift`.

- [ ] **Step 3: Implement the drift job**

Create `internal/calibrun/drift.go`:

```go
package calibrun

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/changeset"
	"thymeandplace/internal/store"
)

// Drift scores the live snapshot and records it, warning on drops that
// come from reference-data swaps rather than catalog edits.
type Drift struct {
	Pool        *pgxpool.Pool
	Snapshot    func() *catalog.Snapshot
	Cases       func(context.Context) ([]calibration.Case, error)
	DataVersion func() string
	Log         *slog.Logger
}

func (d *Drift) RunOnce(ctx context.Context) (calibration.Report, error) {
	snap := d.Snapshot()
	if snap == nil {
		return calibration.Report{}, errors.New("drift check: no catalog snapshot loaded")
	}
	cases, err := d.Cases(ctx)
	if err != nil {
		return calibration.Report{}, err
	}
	rep := calibration.Score(snap, cases)
	q := store.New(d.Pool)
	prev, err := q.LatestCalibrationRun(ctx)
	hasPrev := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return rep, err
	}
	dv := d.DataVersion()
	if err := q.InsertCalibrationRun(ctx, store.InsertCalibrationRunParams{
		DataVersion: dv, Score: rep.Score, Compared: int32(rep.Compared),
	}); err != nil {
		return rep, err
	}
	if hasPrev && changeset.NeedsOverride(prev.Score, rep.Score) {
		d.Log.Warn("calibration score dropped", "previous", prev.Score, "current", rep.Score,
			"previous_data_version", prev.DataVersion, "data_version", dv)
	} else {
		d.Log.Info("calibration drift check", "score", rep.Score, "compared", rep.Compared, "data_version", dv)
	}
	return rep, nil
}

func (d *Drift) Loop(ctx context.Context, every time.Duration) {
	run := func() {
		if _, err := d.RunOnce(ctx); err != nil && ctx.Err() == nil {
			d.Log.Error("calibration drift check failed", "err", err)
		}
	}
	run()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
```

- [ ] **Step 4: Run all tests**

Run: `go vet ./... && go test ./... -count=1`
Expected: every package `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/calibrun
git commit -m "feat(calibrun): daily calibration drift check with WARN on drops

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Plan completion check

- [ ] `./scripts/sqlc.sh generate && git diff --exit-code internal/store`
- [ ] `go vet ./... && go test ./... -count=1` passes.
- [ ] Every §13.3 changeset bullet has a test: upsert-only import, list replacement, unknown format, 5 MB limit, atomic apply, stale rejection, preview score and bars against the test catalog, override flag required and recorded.
