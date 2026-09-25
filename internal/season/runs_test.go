package season

import (
	"reflect"
	"testing"
)

func set(months ...int) [12]bool {
	var s [12]bool
	for _, m := range months {
		s[m-1] = true
	}
	return s
}

func TestRuns(t *testing.T) {
	cases := []struct {
		name   string
		in     [12]bool
		anchor int
		want   []run
	}{
		{"none", set(), 0, nil},
		{"single", set(5), 0, []run{{4, 1}}},
		{"wraps dec-jan", set(11, 12, 1, 2), 0, []run{{10, 4}}},
		{"two runs", set(2, 3, 7), 0, []run{{1, 2}, {6, 1}}},
		{"all from anchor", set(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12), 7, []run{{7, 12}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runs(c.in, c.anchor)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("runs = %v, want %v", got, c.want)
			}
		})
	}
}

func TestPeakOffsets(t *testing.T) {
	cases := map[int][]int{1: {0}, 2: {0, 1}, 3: {1}, 4: {1, 2}, 5: {2}, 6: {2, 3}, 12: {5, 6}}
	for n, want := range cases {
		if got := peakOffsets(n); !reflect.DeepEqual(got, want) {
			t.Errorf("peakOffsets(%d) = %v, want %v", n, got, want)
		}
	}
}

func TestFormatMonths(t *testing.T) {
	cases := []struct {
		in   [12]bool
		want string
	}{
		{set(), ""},
		{set(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12), "all year"},
		{set(5, 6, 7, 8, 9), "May–Sep"},
		{set(5), "May"},
		{set(11, 12, 1, 2, 5), "May, Nov–Feb"},
	}
	for _, c := range cases {
		if got := FormatMonths(c.in); got != c.want {
			t.Errorf("FormatMonths = %q, want %q", got, c.want)
		}
	}
}
