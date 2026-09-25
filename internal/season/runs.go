package season

import (
	"sort"
	"strings"
	"time"
)

// run is a maximal stretch of consecutive months (wrapping Dec→Jan).
type run struct {
	Start int // 0-based month
	Len   int
}

// runs finds maximal runs in set. When every month is set, it returns a
// single 12-month run starting at anchor, so the caller controls where a
// full-year window "begins".
func runs(set [12]bool, anchor int) []run {
	first := -1
	for i := 0; i < 12; i++ {
		if !set[(anchor+i)%12] {
			first = (anchor + i) % 12
			break
		}
	}
	if first < 0 {
		return []run{{anchor, 12}}
	}
	var out []run
	cur := run{Start: -1}
	for i := 1; i <= 12; i++ {
		m := (first + i) % 12
		if set[m] {
			if cur.Start < 0 {
				cur = run{Start: m}
			}
			cur.Len++
			continue
		}
		if cur.Start >= 0 {
			out = append(out, cur)
			cur = run{Start: -1}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// peakOffsets returns the offsets within a window of length n that are peak:
// windows of 1–2 months are all peak; otherwise the middle month (odd n) or
// the middle two (even n).
func peakOffsets(n int) []int {
	switch {
	case n <= 2:
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	case n%2 == 1:
		return []int{n / 2}
	default:
		return []int{n/2 - 1, n / 2}
	}
}

// harvestStatuses marks each harvest run InSeason with Peak in its middle.
func harvestStatuses(harvest [12]bool, anchor int) [12]Status {
	var months [12]Status
	for _, r := range runs(harvest, anchor) {
		if r.Len == 0 {
			continue
		}
		for i := 0; i < r.Len; i++ {
			months[(r.Start+i)%12] = InSeason
		}
		for _, p := range peakOffsets(r.Len) {
			months[(r.Start+p)%12] = Peak
		}
	}
	return months
}

// applyStorage marks the storage months after each harvest run FromStorage,
// never downgrading a harvest month.
func applyStorage(months *[12]Status, harvest [12]bool, anchor, storage int) {
	if storage <= 0 {
		return
	}
	for _, r := range runs(harvest, anchor) {
		if r.Len == 0 || r.Len >= 12 {
			continue
		}
		end := r.Start + r.Len - 1
		for k := 1; k <= storage; k++ {
			m := (end + k) % 12
			if months[m] < FromStorage {
				months[m] = FromStorage
			}
		}
	}
}

// FormatMonths renders a month set as "May–Sep, Nov".
func FormatMonths(set [12]bool) string {
	count := 0
	for _, v := range set {
		if v {
			count++
		}
	}
	switch count {
	case 0:
		return ""
	case 12:
		return "all year"
	}
	var parts []string
	for _, r := range runs(set, 0) {
		start := monthAbbr(r.Start)
		if r.Len == 1 {
			parts = append(parts, start)
			continue
		}
		parts = append(parts, start+"–"+monthAbbr((r.Start+r.Len-1)%12))
	}
	return strings.Join(parts, ", ")
}

func monthAbbr(m int) string { return time.Month(m + 1).String()[:3] }
