package web

import (
	"html"
	"regexp"
	"strings"
	"testing"

	"thymeandplace/internal/ingest"
)

var tagRE = regexp.MustCompile(`<[^>]*>`)

// The About page must carry every credit line each dataset declares, and
// make no licensing claim that no recorded review supports.
func TestAboutCarriesDatasetAttributions(t *testing.T) {
	hs := newHarness(t)
	text := strings.Join(strings.Fields(html.UnescapeString(tagRE.ReplaceAllString(rawBody(t, hs, "/about/data"), ""))), " ")
	for _, ds := range []ingest.Dataset{ingest.NewPlaces(), ingest.NewOcean(""), ingest.NewClimate("")} {
		lines := ds.Attribution()
		if len(lines) == 0 {
			t.Errorf("%s declares no attribution", ds.Name())
		}
		for _, line := range lines {
			if !strings.Contains(text, line) {
				t.Errorf("/about/data is missing %s attribution %q:\n%s", ds.Name(), line, text)
			}
		}
	}
	if strings.Contains(text, "used under its data licence") {
		t.Error("/about/data still claims a MEOW data licence that nothing records")
	}
}
