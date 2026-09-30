package web

import "testing"

func TestSeasonRetryURL(t *testing.T) {
	for _, tc := range []struct {
		name        string
		lat, lng    float64
		place, date string
		want        string
	}{
		{"all parts", 38.72, -9.14, "1", "2026-08-15", "/season?lat=38.72&lng=-9.14&place=1&date=2026-08-15"},
		{"no place, no date", 38.72, -9.14, "", "", "/season?lat=38.72&lng=-9.14"},
		{"junk place dropped", 38.72, -9.14, "abc", "", "/season?lat=38.72&lng=-9.14"},
		{"out-of-range place dropped", 38.72, -9.14, "99999999999", "", "/season?lat=38.72&lng=-9.14"},
		{"script place dropped", 38.72, -9.14, "javascript:alert(1)", "", "/season?lat=38.72&lng=-9.14"},
		{"exact near a cell boundary", 38.49996, -9.0, "", "", "/season?lat=38.49996&lng=-9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := seasonRetryURL(tc.lat, tc.lng, tc.place, tc.date); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRetryPath(t *testing.T) {
	for in, want := range map[string]string{
		"/season":             "/season",
		"/ingredient/tomato":  "/ingredient/tomato",
		"/ingredient/a b":     "/ingredient/a%20b",
		"/ingredient/a/b":     "/ingredient/a%2Fb",
		"/places":             "/",
		"/":                   "/",
		"/seasonjunk":         "/",
		"javascript:alert(1)": "/",
	} {
		if got := retryPath(in); got != want {
			t.Errorf("retryPath(%q) = %q, want %q", in, got, want)
		}
	}
}
