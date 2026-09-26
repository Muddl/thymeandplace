package admin

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/ocean"
)

// kindFields lists the kind-specific inputs each kind shows.
var kindFields = map[catalog.Kind][]string{
	catalog.KindAnnual:         {"frost", "grow_temp_min_c", "grow_temp_max_c", "months_to_harvest", "storage_months"},
	catalog.KindPerennialFruit: {"frost", "chill_months_min", "warmup_temp_c", "months_bloom_to_harvest", "harvest_span_months", "storage_months"},
	catalog.KindYearRound:      {},
	catalog.KindWildMushroom:   {"fruiting_temp_min_c", "fruiting_temp_max_c", "min_precip_mm", "storage_months"},
	catalog.KindSeafood:        {"regions"},
}

func parseFloat(v url.Values, field string, ps *[]catalog.Problem, slug string) *float64 {
	s := strings.TrimSpace(v.Get(field))
	if s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		*ps = append(*ps, catalog.Problem{Slug: slug, Field: field, Message: "must be a number"})
		return nil
	}
	return &f
}

func parseInt(v url.Values, field string, ps *[]catalog.Problem, slug string) *int {
	s := strings.TrimSpace(v.Get(field))
	if s == "" {
		return nil
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		*ps = append(*ps, catalog.Problem{Slug: slug, Field: field, Message: "must be a whole number"})
		return nil
	}
	return &i
}

func parsePairings(text string) []string {
	var out []string
	for _, p := range strings.Split(text, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

var regionLine = regexp.MustCompile(`^\s*(\d+)\s*:\s*([0-9,\s]+?)\s*(?:\|\s*peak\s*([0-9,\s]*))?\s*$`)

func parseMonths(s string) ([]int, error) {
	var out []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		m, err := strconv.Atoi(p)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func parseRegions(text string) ([]catalog.SeafoodRegion, []catalog.Problem) {
	var rs []catalog.SeafoodRegion
	var ps []catalog.Problem
	i := 0
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		field := fmt.Sprintf("regions[%d].months", i)
		i++
		m := regionLine.FindStringSubmatch(line)
		if m == nil {
			ps = append(ps, regionSyntaxProblem(field, line))
			continue
		}
		id, err := strconv.Atoi(m[1])
		if err != nil || id < 1 || id > math.MaxInt32 {
			ps = append(ps, regionSyntaxProblem(field, line))
			continue
		}
		months, err1 := parseMonths(m[2])
		peak, err2 := parseMonths(m[3])
		if err1 != nil || err2 != nil {
			ps = append(ps, catalog.Problem{Field: field, Message: "months must be numbers 1–12"})
			continue
		}
		rs = append(rs, catalog.SeafoodRegion{EcoregionID: ocean.EcoregionID(id), Months: months, PeakMonths: peak})
	}
	return rs, ps
}

// regionSyntaxProblem reports a region line that doesn't parse as a
// "25: 1,2,3 | peak 2" line, including an ecoregion ID that is missing,
// non-numeric, or out of int32 range.
func regionSyntaxProblem(field, line string) catalog.Problem {
	return catalog.Problem{Field: field, Message: fmt.Sprintf("%q should look like \"25: 1,2,3 | peak 2\"", strings.TrimSpace(line))}
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ",")
}

func formatRegions(rs []catalog.SeafoodRegion) string {
	lines := make([]string, len(rs))
	for i, r := range rs {
		lines[i] = fmt.Sprintf("%d: %s", r.EcoregionID, joinInts(r.Months))
		if len(r.PeakMonths) > 0 {
			lines[i] += " | peak " + joinInts(r.PeakMonths)
		}
	}
	return strings.Join(lines, "\n")
}

// parseIngredientForm reads every field that was submitted. Inputs for other
// kinds are not in the form (HTMX swaps them out), so they stay nil.
func parseIngredientForm(v url.Values) (catalog.Ingredient, []catalog.Problem) {
	slug := strings.TrimSpace(v.Get("slug"))
	var ps []catalog.Problem
	ing := catalog.Ingredient{
		Slug: slug, Name: strings.TrimSpace(v.Get("name")),
		Category: catalog.Category(v.Get("category")), Kind: catalog.Kind(v.Get("kind")),
		Frost:     catalog.Frost(v.Get("frost")),
		TipChoose: strings.TrimSpace(v.Get("tip_choose")), TipStore: strings.TrimSpace(v.Get("tip_store")),
		Note: strings.TrimSpace(v.Get("note")), Pairings: parsePairings(v.Get("pairings")),
	}
	ing.GrowTempMinC = parseFloat(v, "grow_temp_min_c", &ps, slug)
	ing.GrowTempMaxC = parseFloat(v, "grow_temp_max_c", &ps, slug)
	ing.MonthsToHarvest = parseInt(v, "months_to_harvest", &ps, slug)
	if sm := parseInt(v, "storage_months", &ps, slug); sm != nil {
		ing.StorageMonths = *sm
	}
	ing.ChillMonthsMin = parseInt(v, "chill_months_min", &ps, slug)
	ing.WarmupTempC = parseFloat(v, "warmup_temp_c", &ps, slug)
	ing.MonthsBloomToHarvest = parseInt(v, "months_bloom_to_harvest", &ps, slug)
	ing.HarvestSpanMonths = parseInt(v, "harvest_span_months", &ps, slug)
	ing.FruitingTempMinC = parseFloat(v, "fruiting_temp_min_c", &ps, slug)
	ing.FruitingTempMaxC = parseFloat(v, "fruiting_temp_max_c", &ps, slug)
	ing.MinPrecipMM = parseFloat(v, "min_precip_mm", &ps, slug)
	rs, rps := parseRegions(v.Get("regions"))
	for i := range rps {
		rps[i].Slug = slug
	}
	ing.Regions, ps = rs, append(ps, rps...)
	return ing, ps
}

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

// formValues is the inverse of parseIngredientForm, for prefilling the form.
func formValues(ing catalog.Ingredient) map[string]string {
	return map[string]string{
		"slug": ing.Slug, "name": ing.Name, "category": string(ing.Category), "kind": string(ing.Kind),
		"frost": string(ing.Frost), "grow_temp_min_c": fstr(ing.GrowTempMinC), "grow_temp_max_c": fstr(ing.GrowTempMaxC),
		"months_to_harvest": istr(ing.MonthsToHarvest), "storage_months": strconv.Itoa(ing.StorageMonths),
		"chill_months_min": istr(ing.ChillMonthsMin), "warmup_temp_c": fstr(ing.WarmupTempC),
		"months_bloom_to_harvest": istr(ing.MonthsBloomToHarvest), "harvest_span_months": istr(ing.HarvestSpanMonths),
		"fruiting_temp_min_c": fstr(ing.FruitingTempMinC), "fruiting_temp_max_c": fstr(ing.FruitingTempMaxC),
		"min_precip_mm": fstr(ing.MinPrecipMM), "tip_choose": ing.TipChoose, "tip_store": ing.TipStore,
		"note": ing.Note, "pairings": strings.Join(ing.Pairings, ", "), "regions": formatRegions(ing.Regions),
	}
}
